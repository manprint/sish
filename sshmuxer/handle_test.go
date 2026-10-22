package sshmuxer

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antoniomika/sish/utils"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh"
)

// syncBuffer is a goroutine-safe buffer for capturing log output written
// concurrently by the server goroutines under test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// viperConfigLock serialises viper configuration against the server goroutines
// that read it. viper's package-level store is a plain map, so a Set from one
// test racing a Get from a connection goroutine still running from an earlier
// test is a genuine data race (and was an intermittent -race failure).
var viperConfigLock sync.Mutex

// handleTestConns tracks the connection goroutines a test server spawned, so a
// test can wait for them before the next test reconfigures viper.
type handleTestServer struct {
	State *utils.State
	Addr  string

	conns sync.WaitGroup
}

// newHandleTestServer sets up a minimal sish SSH server driven directly through
// handleConn, without binding any of the real service listeners.
func newHandleTestServer(t *testing.T) (*utils.State, string) {
	t.Helper()

	server := newHandleTestServerWithWait(t)
	return server.State, server.Addr
}

func newHandleTestServerWithWait(t *testing.T) *handleTestServer {
	t.Helper()

	return newHandleTestServerWithConfig(t, nil)
}

// newHandleTestServerWithConfig is newHandleTestServerWithWait with overrides
// applied to the test defaults, under viperConfigLock.
func newHandleTestServerWithConfig(t *testing.T, overrides func()) *handleTestServer {
	t.Helper()

	dir, err := os.MkdirTemp("", "sish_keys")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	viperConfigLock.Lock()
	viper.Set("private-keys-directory", dir)
	viper.Set("authentication", false)
	viper.Set("domain", "localhost")
	viper.Set("bind-random-subdomains", false)
	viper.Set("cleanup-unauthed", false)
	viper.Set("ping-client", false)
	viper.Set("idle-connection", false)

	if overrides != nil {
		overrides()
	}

	utils.Setup(io.Discard)

	sshConfig := utils.GetSSHConfig()
	viperConfigLock.Unlock()

	state := utils.NewState()

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	server := &handleTestServer{State: state, Addr: listener.Addr().String()}

	// Close the listener and drain the connection goroutines before the test
	// returns; otherwise they outlive it and race the next test's viper.Set.
	t.Cleanup(func() {
		_ = listener.Close()
		server.conns.Wait()
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			server.conns.Add(1)
			go func(conn net.Conn) {
				defer server.conns.Done()
				viperConfigLock.Lock()
				defer viperConfigLock.Unlock()
				handleConn(conn, "ssh", state, sshConfig)
			}(conn)
		}
	}()

	return server
}

// dropConnAfterForwardRequest connects to addr, requests an HTTP remote
// forward for the given subdomain without waiting for the reply, and then
// yanks the TCP connection. The drop happens inside the server's 1s Exec
// fallback window (see handleRemoteForward), so the server ends up building
// the forward after the connection is already gone - the condition that a
// flaky network triggers in the wild.
func dropConnAfterForwardRequest(t *testing.T, addr, subdomain string) {
	t.Helper()

	netConn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	clientConfig := &ssh.ClientConfig{
		User:            "tester",
		Auth:            []ssh.AuthMethod{ssh.Password("")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	conn, chans, reqs, err := ssh.NewClientConn(netConn, addr, clientConfig)
	if err != nil {
		_ = netConn.Close()
		t.Fatalf("ssh handshake: %v", err)
	}

	go ssh.DiscardRequests(reqs)
	go func() {
		for nc := range chans {
			_ = nc.Reject(ssh.Prohibited, "no channels")
		}
	}()

	// Fire the forward request but never wait for its reply.
	go func() {
		_, _, _ = conn.SendRequest("tcpip-forward", true,
			ssh.Marshal(&channelForwardMsg{Addr: subdomain, Rport: 80}))
	}()

	// Give the request a moment to reach the server, then drop the connection.
	time.Sleep(30 * time.Millisecond)
	_ = netConn.Close()
}

func countHTTPListeners(state *utils.State) int {
	count := 0
	state.HTTPListeners.Range(func(_ string, _ *utils.HTTPHolder) bool {
		count++
		return true
	})
	return count
}

// TestForwardingCleanupOnDroppedConn verifies that connections dropped right
// after requesting a forward do not leave behind HTTP forwarding routes. A
// leaked route keeps its subdomain permanently reserved (and is a data race on
// the deferred cleanup handler, which -race flags), so a clean run must end
// with zero HTTP listeners registered.
func TestForwardingCleanupOnDroppedConn(t *testing.T) {
	// Silence the per-connection logging this test deliberately generates.
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)

	state, addr := newHandleTestServer(t)

	// Each unique subdomain registers an independent HTTP route, so any leak
	// is observable. Repeat enough to reliably lose the cleanup race.
	const numConns = 40
	for i := 0; i < numConns; i++ {
		dropConnAfterForwardRequest(t, addr, fmt.Sprintf("tun%d", i))
	}

	// Every dropped connection builds its forward ~1s later (the Exec
	// fallback) and should immediately tear it down again because the
	// connection is gone. Wait past that window, then require the route table
	// to drain to zero.
	time.Sleep(2500 * time.Millisecond)

	deadline := time.Now().Add(5 * time.Second)
	for {
		remaining := countHTTPListeners(state)
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d HTTP forwarding route(s) leaked: connections dropped before forwarding completed left their subdomains permanently reserved", remaining)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestNoForwardingForDroppedConn verifies that a connection dropped during
// forward setup never gets its forwarding stood up at all. The lifecycle
// manager already tears such forwards back down (see
// TestForwardingCleanupOnDroppedConn), but building them in the first place
// reserves the address for a dead session and races the cleanup handler
// assignment, so nothing must be started for a connection that is already gone.
func TestNoForwardingForDroppedConn(t *testing.T) {
	logBuf := &syncBuffer{}
	log.SetOutput(logBuf)
	defer log.SetOutput(os.Stderr)

	_, addr := newHandleTestServer(t)

	const numConns = 10
	for i := 0; i < numConns; i++ {
		dropConnAfterForwardRequest(t, addr, fmt.Sprintf("dropped%d", i))
	}

	// Wait past the 1s Exec fallback window, where the forward would be built.
	time.Sleep(2500 * time.Millisecond)

	if strings.Contains(logBuf.String(), "forwarding started") {
		t.Fatalf("forwarding was started for a connection that had already dropped:\n%s", logBuf.String())
	}
}

// TestForwardingStoppedIsLogged verifies that tearing down an established
// forward emits a "forwarding stopped" log line, mirroring the
// "forwarding started" log.
func TestForwardingStoppedIsLogged(t *testing.T) {
	logBuf := &syncBuffer{}
	log.SetOutput(logBuf)
	defer log.SetOutput(os.Stderr)

	_, addr := newHandleTestServer(t)

	netConn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	conn, chans, reqs, err := ssh.NewClientConn(netConn, addr, &ssh.ClientConfig{
		User:            "tester",
		Auth:            []ssh.AuthMethod{ssh.Password("")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		_ = netConn.Close()
		t.Fatalf("ssh handshake: %v", err)
	}

	go ssh.DiscardRequests(reqs)
	go func() {
		for nc := range chans {
			_ = nc.Reject(ssh.Prohibited, "no channels")
		}
	}()

	// Request the forward and wait for the reply: once it returns the server
	// has established the forward ("forwarding started" is logged).
	ok, _, err := conn.SendRequest("tcpip-forward", true,
		ssh.Marshal(&channelForwardMsg{Addr: "stoptest", Rport: 80}))
	if err != nil || !ok {
		t.Fatalf("forward request not accepted: ok=%v err=%v", ok, err)
	}

	// Tear the forward down by closing the connection.
	_ = netConn.Close()

	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(logBuf.String(), "forwarding stopped") {
		if time.Now().After(deadline) {
			t.Fatalf("expected a \"forwarding stopped\" log after the connection closed; got:\n%s", logBuf.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
