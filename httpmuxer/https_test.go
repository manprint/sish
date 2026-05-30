package httpmuxer

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/antoniomika/sish/utils"
	"github.com/antoniomika/syncmap"
	"github.com/jpillora/ipfilter"
	"github.com/vulcand/oxy/roundrobin"
)

type testAddr string

func (a testAddr) Network() string { return "tcp" }
func (a testAddr) String() string  { return string(a) }

type stubConn struct {
	remote testAddr
	local  testAddr
	closed bool
}

func (c *stubConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *stubConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *stubConn) Close() error                     { c.closed = true; return nil }
func (c *stubConn) LocalAddr() net.Addr              { return c.local }
func (c *stubConn) RemoteAddr() net.Addr             { return c.remote }
func (c *stubConn) SetDeadline(time.Time) error      { return nil }
func (c *stubConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stubConn) SetWriteDeadline(time.Time) error { return nil }

type addrOverrideConn struct {
	net.Conn
	remote testAddr
	local  testAddr
}

func (c *addrOverrideConn) LocalAddr() net.Addr  { return c.local }
func (c *addrOverrideConn) RemoteAddr() net.Addr { return c.remote }

type queueListener struct {
	conns []net.Conn
}

func (l *queueListener) Accept() (net.Conn, error) {
	if len(l.conns) == 0 {
		return nil, net.ErrClosed
	}

	conn := l.conns[0]
	l.conns = l.conns[1:]
	return conn, nil
}

func (l *queueListener) Close() error {
	for _, conn := range l.conns {
		_ = conn.Close()
	}
	l.conns = nil
	return nil
}

func (l *queueListener) Addr() net.Addr { return testAddr("127.0.0.1:443") }

func TestProxyListenerAcceptSkipsRejectedConnection(t *testing.T) {
	blocked := &stubConn{
		remote: testAddr("not-a-host-port"),
		local:  testAddr("127.0.0.1:443"),
	}

	server, client := net.Pipe()
	go func() {
		_, _ = client.Write([]byte("hello"))
		_ = client.Close()
	}()

	listener := &queueListener{
		conns: []net.Conn{
			blocked,
			&addrOverrideConn{
				Conn:   server,
				remote: testAddr("198.51.100.10:12345"),
				local:  testAddr("127.0.0.1:443"),
			},
		},
	}

	proxy := &proxyListener{
		Listener: listener,
		Holder: &utils.TCPHolder{
			Balancers:      syncmap.New[string, *roundrobin.RoundRobin](),
			SSHConnections: syncmap.New[string, *utils.SSHConnection](),
		},
		State: utils.NewState(),
	}
	proxy.State.IPFilter = ipfilter.New(ipfilter.Options{})

	conn, err := proxy.Accept()
	if err != nil {
		t.Fatalf("Accept returned error: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	if !blocked.closed {
		t.Fatal("expected rejected connection to be closed before continuing")
	}
	if got := conn.RemoteAddr().String(); got != "198.51.100.10:12345" {
		t.Fatalf("RemoteAddr=%q, want %q", got, "198.51.100.10:12345")
	}
}
