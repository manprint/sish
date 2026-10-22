package sshmuxer

import (
	"net"
	"testing"
	"time"

	"github.com/antoniomika/sish/utils"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh"
)

// With ping-client on and the interval equal to the timeout (the 5s/5s
// defaults), the connection deadline used to expire exactly when the first
// ping went out, so every tunnel was closed after one interval with
// "Error retrieving keepalive response: i/o timeout". A client that answers
// the pings must stay connected.
func TestPingClientKeepsRespondingClientConnected(t *testing.T) {
	const interval = 200 * time.Millisecond

	server := newHandleTestServerWithConfig(t, func() {
		viper.Set("ping-client", true)
		viper.Set("ping-client-interval", interval)
		viper.Set("ping-client-timeout", interval)
	})

	netConn, err := net.Dial("tcp", server.Addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	clientConfig := &ssh.ClientConfig{
		User:            "tester",
		Auth:            []ssh.AuthMethod{ssh.Password("")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	conn, chans, reqs, err := ssh.NewClientConn(netConn, server.Addr, clientConfig)
	if err != nil {
		_ = netConn.Close()
		t.Fatalf("ssh handshake: %v", err)
	}

	client := ssh.NewClient(conn, chans, reqs)
	t.Cleanup(func() { _ = client.Close() })

	// The session keeps sish from treating the connection as unused.
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	closed := make(chan error, 1)
	go func() { closed <- client.Wait() }()

	select {
	case err := <-closed:
		t.Fatalf("expected the connection to survive several ping cycles, closed after less than %s: %v", 6*interval, err)
	case <-time.After(6 * interval):
	}

	// Let the server finish with the connection before the next test
	// reconfigures viper (which also turns ping-client off again).
	_ = session.Close()
	_ = client.Close()

	deadline := time.Now().Add(5 * time.Second)
	for sshConnectionCount(server) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("expected the server to drop the closed connection")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sshConnectionCount(server *handleTestServer) int {
	count := 0
	server.State.SSHConnections.Range(func(_ string, _ *utils.SSHConnection) bool {
		count++
		return true
	})
	return count
}
