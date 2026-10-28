package sshmuxer

import (
	"testing"

	"golang.org/x/crypto/ssh"
)

func dialSessionTestServer(t *testing.T, addr string) *ssh.Client {
	t.Helper()

	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "tester",
		Auth:            []ssh.AuthMethod{ssh.Password("")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatalf("ssh dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return client
}

// A connection gets one session. Opening another, as a client multiplexing
// over a shared connection does, is rejected and leaves the first one working.
func TestSecondSessionRejected(t *testing.T) {
	_, addr := newHandleTestServer(t)
	client := dialSessionTestServer(t, addr)

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
}

// Sending both shell and exec on one session doesn't take the server down.
func TestShellThenExec(t *testing.T) {
	_, addr := newHandleTestServer(t)
	client := dialSessionTestServer(t, addr)

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
}
