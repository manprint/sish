package utils

import (
	"encoding/base64"
	"net"
	"net/url"
	"sync"
	"testing"

	"github.com/antoniomika/syncmap"
	"github.com/vulcand/oxy/roundrobin"
)

func mustBalancer(t *testing.T, sockets ...string) *roundrobin.RoundRobin {
	t.Helper()

	balancer, err := roundrobin.New(nil)
	if err != nil {
		t.Fatalf("roundrobin.New: %v", err)
	}

	for _, socket := range sockets {
		err := balancer.UpsertServer(&url.URL{
			Scheme: "http",
			Host:   base64.StdEncoding.EncodeToString([]byte(socket)),
		})
		if err != nil {
			t.Fatalf("UpsertServer: %v", err)
		}
	}

	return balancer
}

func newForwardIndexTestConn(sockets ...string) *SSHConnection {
	conn := &SSHConnection{
		BandwidthProfileLock: &sync.RWMutex{},
		Listeners:            syncmap.New[string, net.Listener](),
		ForwardCleanups:      syncmap.New[string, func()](),
		VisitorForwarders:    syncmap.New[string, bool](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		Exec:                 make(chan bool),
		Session:              make(chan bool),
		Messages:             make(chan string, 1),
		SetupLock:            &sync.Mutex{},
	}

	for _, socket := range sockets {
		conn.Listeners.Store(socket, &fakeListener{addr: socket})
	}

	return conn
}

// The index replaces a full re-scan of every holder per connection (B22). It
// must produce exactly what the direct resolution produced, for every forward
// type.
func TestForwardIndexMatchesDirectResolution(t *testing.T) {
	state := NewState()

	httpSocket := "/tmp/http.sock"
	tcpSocket := "/tmp/tcp.sock"
	sniSocket := "/tmp/sni.sock"
	aliasSocket := "/tmp/alias.sock"

	httpURL, err := url.Parse("http://demo.example.com/base")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	httpConns := syncmap.New[string, *SSHConnection]()
	httpConns.Store(httpSocket, nil)
	state.HTTPListeners.Store("demo", &HTTPHolder{
		HTTPUrl:        httpURL,
		SSHConnections: httpConns,
		Balancer:       mustBalancer(t, httpSocket),
	})

	tcpBalancers := syncmap.New[string, *roundrobin.RoundRobin]()
	tcpBalancers.Store("", mustBalancer(t, tcpSocket))
	state.TCPListeners.Store(":9000", &TCPHolder{
		TCPHost:        ":9000",
		Listener:       &fakeListener{addr: ":9000"},
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancers:      tcpBalancers,
	})

	sniBalancers := syncmap.New[string, *roundrobin.RoundRobin]()
	sniBalancers.Store("sni.example.com", mustBalancer(t, sniSocket))
	state.TCPListeners.Store(":9443", &TCPHolder{
		TCPHost:        ":9443",
		Listener:       &fakeListener{addr: ":9443"},
		SNIProxy:       true,
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancers:      sniBalancers,
	})

	state.AliasListeners.Store("alias.example.com:22", &AliasHolder{
		AliasHost:      "alias.example.com:22",
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancer:       mustBalancer(t, aliasSocket),
	})

	cases := []struct {
		name    string
		sockets []string
		want    string
	}{
		{"http", []string{httpSocket}, "demo.example.com/base"},
		{"tcp", []string{tcpSocket}, ":9000"},
		{"sni", []string{sniSocket}, ":9443-sni.example.com"},
		{"alias", []string{aliasSocket}, "alias.example.com:22"},
		{"multiple", []string{httpSocket, tcpSocket, aliasSocket}, ":9000, alias.example.com:22, demo.example.com/base"},
		{"unknown socket", []string{"/tmp/nothing.sock"}, ""},
		{"no listeners", nil, ""},
	}

	index := buildForwardIndex(state)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := newForwardIndexTestConn(tc.sockets...)

			if got := resolveConnectionForwardersIndexed(conn, index); got != tc.want {
				t.Fatalf("indexed=%q, want %q", got, tc.want)
			}

			if got := resolveConnectionForwarders(conn, state); got != tc.want {
				t.Fatalf("direct=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestForwardIndexHandlesNilStateAndConnection(t *testing.T) {
	if index := buildForwardIndex(nil); len(index) != 0 {
		t.Fatalf("buildForwardIndex(nil)=%v, want empty", index)
	}

	if got := resolveConnectionForwardersIndexed(nil, forwardIndex{}); got != "" {
		t.Fatalf("resolveConnectionForwardersIndexed(nil)=%q, want empty", got)
	}

	if got := connectionListeners(nil); got != nil {
		t.Fatalf("connectionListeners(nil)=%v, want nil", got)
	}
}

// A visitor connection reports its alias label rather than its sockets.
func TestResolveConnectionForwardersPrefersVisitorLabel(t *testing.T) {
	state := NewState()
	conn := newForwardIndexTestConn("/tmp/visitor.sock")
	conn.VisitorConnection.Store(true)
	conn.VisitorForwarders.Store("alias.example.com:22", true)

	if got := resolveConnectionForwardersIndexed(conn, buildForwardIndex(state)); got != "alias.example.com:22" {
		t.Fatalf("indexed=%q, want the visitor label", got)
	}

	if got := resolveConnectionForwarders(conn, state); got != "alias.example.com:22" {
		t.Fatalf("direct=%q, want the visitor label", got)
	}
}

// De-duplication matters: a forward served by several sockets of the same
// connection must appear once.
func TestForwardIndexDeduplicatesForwards(t *testing.T) {
	state := NewState()

	socketA := "/tmp/dup-a.sock"
	socketB := "/tmp/dup-b.sock"

	httpURL, err := url.Parse("http://dup.example.com")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	httpConns := syncmap.New[string, *SSHConnection]()
	httpConns.Store(socketA, nil)
	httpConns.Store(socketB, nil)
	state.HTTPListeners.Store("dup", &HTTPHolder{
		HTTPUrl:        httpURL,
		SSHConnections: httpConns,
		Balancer:       mustBalancer(t, socketA, socketB),
	})

	conn := newForwardIndexTestConn(socketA, socketB)

	if got := resolveConnectionForwardersIndexed(conn, buildForwardIndex(state)); got != "dup.example.com" {
		t.Fatalf("forwards=%q, want %q", got, "dup.example.com")
	}
}
