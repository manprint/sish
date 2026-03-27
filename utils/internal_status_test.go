package utils

import (
	"net"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/vulcand/oxy/roundrobin"
)

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

type fakeListener struct {
	addr string
}

func TestInternalUpdateStateHistoryAndRates(t *testing.T) {
	console, state := testConsoleState()
	if console.InternalState == nil {
		t.Fatalf("expected InternalState")
	}
	console.InternalState.MaxHistoryItems = 2

	l1 := map[string]uint64{
		"forward_create_total":                        1,
		"forward_cleanup_total":                       0,
		"forward_cleanup_errors_total":                0,
		"forward_cleanup_listener_close_errors_total": 0,
		"forward_cleanup_socket_remove_errors_total":  0,
		"forward_cleanup_unknown_errors_total":        0,
		"dirty_forwards_stable_total":                 0,
		"dirty_forwards_stable_listener_total":        0,
		"dirty_forwards_stable_http_total":            0,
		"dirty_forwards_stable_tcp_total":             0,
		"dirty_forwards_stable_alias_total":           0,
		"force_connect_takeovers_total":               0,
	}

	d1 := map[string]int{"total": 0, "listener": 0, "http": 0, "tcp": 0, "alias": 0}
	t1 := time.Unix(100, 0)
	_, rates1, h1 := console.updateInternalStatusState(t1, l1, d1)
	if rates1["forward_create_total"] != 0 {
		t.Fatalf("first rate should be 0, got %f", rates1["forward_create_total"])
	}
	if len(h1) != 1 {
		t.Fatalf("history len=%d, want 1", len(h1))
	}

	l2 := cloneLifecycleMetricsMap(l1)
	l2["forward_create_total"] = 5
	l2["forward_cleanup_errors_total"] = 2
	d2 := map[string]int{"total": 2, "listener": 2, "http": 0, "tcp": 0, "alias": 0}
	t2 := t1.Add(2 * time.Second)
	_, rates2, h2 := console.updateInternalStatusState(t2, l2, d2)
	if rates2["forward_create_total"] != 2 {
		t.Fatalf("forward_create_total rate=%f, want 2", rates2["forward_create_total"])
	}
	if rates2["forward_cleanup_errors_total"] != 1 {
		t.Fatalf("forward_cleanup_errors_total rate=%f, want 1", rates2["forward_cleanup_errors_total"])
	}
	if len(h2) != 2 {
		t.Fatalf("history len=%d, want 2", len(h2))
	}

	l3 := cloneLifecycleMetricsMap(l2)
	l3["forward_create_total"] = 6
	t3 := t2.Add(1 * time.Second)
	_, _, h3 := console.updateInternalStatusState(t3, l3, d2)
	if len(h3) != 2 {
		t.Fatalf("history should be ring-limited to 2, got %d", len(h3))
	}
	if h3[0].LifecycleMetrics["forward_create_total"] != 5 {
		t.Fatalf("expected oldest sample trimmed")
	}

	if state == nil {
		t.Fatalf("state should not be nil")
	}
}

func TestInternalHealthAndPrometheusExport(t *testing.T) {
	lifecycle := map[string]uint64{
		"forward_create_total":                        100,
		"forward_cleanup_total":                       100,
		"forward_cleanup_errors_total":                10,
		"forward_cleanup_listener_close_errors_total": 4,
		"forward_cleanup_socket_remove_errors_total":  3,
		"forward_cleanup_unknown_errors_total":        3,
		"dirty_forwards_stable_total":                 2,
		"dirty_forwards_stable_listener_total":        2,
		"dirty_forwards_stable_http_total":            0,
		"dirty_forwards_stable_tcp_total":             0,
		"dirty_forwards_stable_alias_total":           0,
		"force_connect_takeovers_total":               1,
	}
	dirty := map[string]int{"total": 2, "listener": 2, "http": 0, "tcp": 0, "alias": 0}
	rates := map[string]float64{
		"forward_cleanup_errors_total": 1.2,
	}

	health := buildInternalHealth(lifecycle, dirty, rates)
	if health.Status != "critical" {
		t.Fatalf("health status=%s, want critical", health.Status)
	}
	if len(health.Alerts) == 0 {
		t.Fatalf("expected alerts")
	}

	text := internalMetricsPrometheusText(lifecycle, dirty, rates, health)
	for _, want := range []string{
		"sish_lifecycle_counter{name=\"forward_cleanup_errors_total\"} 10",
		"sish_dirty_forward_total{type=\"total\"} 2",
		"sish_lifecycle_rate_per_sec{name=\"forward_cleanup_errors_total\"} 1.200000",
		"sish_internal_health_status 2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in export", want)
		}
	}
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

func TestBuildDirtySnapshot(t *testing.T) {
	console, state := testConsoleState()

	listener := &ListenerHolder{ListenAddr: "l3", Listener: &fakeListener{addr: "l3"}}
	state.Listeners.Store("l3", listener)

	httpLB, _ := roundrobin.New(nil)
	u, _ := url.Parse("http://snap.example.com")
	state.HTTPListeners.Store("snap-http", &HTTPHolder{
		HTTPUrl:        u,
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancer:       httpLB,
	})

	tcpLB, _ := roundrobin.New(nil)
	tcpBalancers := syncmap.New[string, *roundrobin.RoundRobin]()
	tcpBalancers.Store("", tcpLB)
	state.TCPListeners.Store("snap-tcp", &TCPHolder{
		TCPHost:        "snap-tcp",
		Listener:       &fakeListener{addr: "snap-tcp"},
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancers:      tcpBalancers,
	})

	aliasLB, _ := roundrobin.New(nil)
	state.AliasListeners.Store("snap-alias", &AliasHolder{
		AliasHost:      "snap-alias",
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancer:       aliasLB,
	})

	snapshot := console.buildDirtySnapshot()
	if len(snapshot.Listeners) != 1 {
		t.Fatalf("snapshot listeners = %d, want 1", len(snapshot.Listeners))
	}
	if len(snapshot.HTTP) != 1 {
		t.Fatalf("snapshot http = %d, want 1", len(snapshot.HTTP))
	}
	if len(snapshot.TCP) != 1 {
		t.Fatalf("snapshot tcp = %d, want 1", len(snapshot.TCP))
	}
	if len(snapshot.Alias) != 1 {
		t.Fatalf("snapshot alias = %d, want 1", len(snapshot.Alias))
	}
}
