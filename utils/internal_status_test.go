package utils

import (
	"net"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/antoniomika/syncmap"
	"github.com/vulcand/oxy/roundrobin"
)

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

type fakeListener struct {
	addr string
}

func (l *fakeListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *fakeListener) Close() error              { return nil }
func (l *fakeListener) Addr() net.Addr            { return fakeAddr(l.addr) }

func testConsoleState() (*WebConsole, *State) {
	state := NewState()
	state.Console.State = state
	return state.Console, state
}

func TestDirtyForwardsListenerNoOwnerStable(t *testing.T) {
	console, state := testConsoleState()
	state.Listeners.Store("l1", &ListenerHolder{
		ListenAddr: "l1",
		Listener:   &fakeListener{addr: "l1"},
		SSHConn:    nil,
	})

	first := console.getDirtyForwardRows()
	if len(first) != 0 {
		t.Fatalf("expected transient dirty row filtered on first pass, got %d", len(first))
	}

	second := console.getDirtyForwardRows()
	if len(second) != 1 {
		t.Fatalf("expected stable dirty row on second pass, got %d", len(second))
	}
	if second[0].Issue != "listener holder has no owning ssh connection" {
		t.Fatalf("unexpected issue: %s", second[0].Issue)
	}
}

func TestDirtyForwardsListenerNoRemoteAddr(t *testing.T) {
	console, state := testConsoleState()
	owner := &SSHConnection{
		SSHConn:   nil,
		Listeners: syncmap.New[string, net.Listener](),
	}
	holder := &ListenerHolder{ListenAddr: "l2", Listener: &fakeListener{addr: "l2"}, SSHConn: owner}
	state.Listeners.Store("l2", holder)

	_ = console.getDirtyForwardRows()
	rows := console.getDirtyForwardRows()
	if len(rows) != 1 {
		t.Fatalf("expected one stable dirty row, got %d", len(rows))
	}
	if rows[0].Issue != "owning ssh connection has no remote address" {
		t.Fatalf("unexpected issue: %s", rows[0].Issue)
	}
}

func TestDirtyForwardsHTTPNoBackends(t *testing.T) {
	console, state := testConsoleState()
	lb, err := roundrobin.New(nil)
	if err != nil {
		t.Fatalf("roundrobin.New: %v", err)
	}
	u, _ := url.Parse("http://demo.example.com")
	state.HTTPListeners.Store("demo", &HTTPHolder{
		HTTPUrl:        u,
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancer:       lb,
	})

	_ = console.getDirtyForwardRows()
	rows := console.getDirtyForwardRows()
	if len(rows) != 1 || rows[0].Type != "http" {
		t.Fatalf("expected stable http dirty row, got %+v", rows)
	}
}

func TestDirtyForwardsTCPNoBackends(t *testing.T) {
	console, state := testConsoleState()
	balancers := syncmap.New[string, *roundrobin.RoundRobin]()
	lb, err := roundrobin.New(nil)
	if err != nil {
		t.Fatalf("roundrobin.New: %v", err)
	}
	balancers.Store("", lb)
	state.TCPListeners.Store(":9000", &TCPHolder{
		TCPHost:        ":9000",
		Listener:       &fakeListener{addr: ":9000"},
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancers:      balancers,
	})

	_ = console.getDirtyForwardRows()
	rows := console.getDirtyForwardRows()
	if len(rows) != 1 || rows[0].Type != "tcp" {
		t.Fatalf("expected stable tcp dirty row, got %+v", rows)
	}
}

func TestDirtyForwardsAliasNoBackends(t *testing.T) {
	console, state := testConsoleState()
	lb, err := roundrobin.New(nil)
	if err != nil {
		t.Fatalf("roundrobin.New: %v", err)
	}
	state.AliasListeners.Store("alias:9001", &AliasHolder{
		AliasHost:      "alias:9001",
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancer:       lb,
	})

	_ = console.getDirtyForwardRows()
	rows := console.getDirtyForwardRows()
	if len(rows) != 1 || rows[0].Type != "alias" {
		t.Fatalf("expected stable alias dirty row, got %+v", rows)
	}
}

func TestDirtyForwardsSummaryAndMetrics(t *testing.T) {
	rows := []internalForwardIssue{
		{Type: "listener"}, {Type: "listener"}, {Type: "http"}, {Type: "tcp"}, {Type: "alias"},
	}
	summary := summarizeDirtyForwards(rows)
	if summary["total"] != 5 || summary["listener"] != 2 || summary["http"] != 1 || summary["tcp"] != 1 || summary["alias"] != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	metrics := dirtyMetricsKVRows(summary)
	if len(metrics) != 5 {
		t.Fatalf("unexpected metrics size: %d", len(metrics))
	}
	joined := []string{}
	for _, kv := range metrics {
		joined = append(joined, kv.Key+"="+kv.Value)
	}
	sort.Strings(joined)
	out := strings.Join(joined, ",")
	for _, want := range []string{"alias=1", "http=1", "listener=2", "tcp=1", "total=5"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing metric %s in %s", want, out)
		}
	}
}
