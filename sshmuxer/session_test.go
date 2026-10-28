package sshmuxer

import (
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestSessionChannels covers the "session" channel handling: one session per
// connection, shell/exec in any order, and replies to every request that
// wants one. The subtests share one server because they leave session
// goroutines that read viper after the test ends; a second server per test
// would rewrite viper under them (and trip -race). This file sorts last in the
// package for the same reason.
func TestSessionChannels(t *testing.T) {
	_, addr := newHandleTestServer(t)

	logBuf := &syncBuffer{}
	log.SetOutput(logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	closed := 0

	// dial connects a client and returns it with a func that closes it and
	// waits for the server's "Closed SSH connection" line, so subtests don't
	// overlap and the connection is fully torn down before the next one.
	dial := func(t *testing.T) (*ssh.Client, func()) {
		t.Helper()

		client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
			User:            "tester",
			Auth:            []ssh.AuthMethod{ssh.Password("")},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		})
		if err != nil {
			t.Fatalf("ssh dial: %v", err)
		}

		return client, func() {
			_ = client.Close()
			closed++

			deadline := time.Now().Add(5 * time.Second)
			for strings.Count(logBuf.String(), "Closed SSH connection for") < closed {
				if time.Now().After(deadline) {
					t.Fatal("server never finished closing the connection")
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}

	// A connection gets one session. Opening another, as a client multiplexing
	// over a shared connection does, is rejected and leaves the first one
	// working.
	t.Run("second session rejected", func(t *testing.T) {
		client, closeClient := dial(t)
		defer closeClient()

		first, err := client.NewSession()
		if err != nil {
			t.Fatalf("first session: %v", err)
		}
		if err := first.Shell(); err != nil {
			t.Fatalf("first shell: %v", err)
		}

		if _, err := client.NewSession(); err == nil {
			t.Fatal("expected the second session to be rejected")
		}

		ok, err := first.SendRequest("exec", true, ssh.Marshal(struct{ Command string }{""}))
		if err != nil || !ok {
			t.Fatalf("first session stopped working: ok=%v err=%v", ok, err)
		}
	})

	// Sending both shell and exec on one session doesn't take the server down.
	t.Run("shell then exec", func(t *testing.T) {
		client, closeClient := dial(t)
		defer closeClient()

		session, err := client.NewSession()
		if err != nil {
			t.Fatal(err)
		}

		if _, err := session.SendRequest("shell", true, nil); err != nil {
			t.Fatalf("shell: %v", err)
		}
		_, _ = session.SendRequest("exec", false, ssh.Marshal(struct{ Command string }{"auto-close=true"}))
		_, _ = session.SendRequest("shell", false, nil)

		if _, err := client.NewSession(); err == nil {
			t.Fatal("expected the second session to be rejected")
		}
	})

	// Session requests that want a reply get one, so clients that wait for it,
	// like x/crypto/ssh, don't hang.
	t.Run("requests replied", func(t *testing.T) {
		client, closeClient := dial(t)
		defer closeClient()

		session, err := client.NewSession()
		if err != nil {
			t.Fatal(err)
		}

		done := make(chan error, 1)
		go func() {
			if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
				done <- fmt.Errorf("pty: %w", err)
				return
			}
			if err := session.Setenv("FOO", "bar"); err != nil {
				done <- fmt.Errorf("env: %w", err)
				return
			}
			done <- session.Start("strip-path=false")
		}()

		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a session request never got a reply")
		}
	})
}
