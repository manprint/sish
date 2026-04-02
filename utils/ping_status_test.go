package utils

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/spf13/viper"
)

func TestGetPingStatusRowsEmpty(t *testing.T) {
	console, _ := testConsoleState()
	viper.Set("ping-client", true)
	defer viper.Set("ping-client", false)

	rows := console.getPingStatusRows()
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows with no SSH connections, got %d", len(rows))
	}
}

func TestGetPingStatusRowsSkipsNoListeners(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	defer viper.Set("ping-client", false)

	conn := &SSHConnection{
		ConnectionID:         "test-id",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	state.SSHConnections.Store("127.0.0.1:50000", conn)

	rows := console.getPingStatusRows()
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows when connection has no listeners, got %d", len(rows))
	}
}

func TestGetPingStatusRowsSkipsEmptyID(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	defer viper.Set("ping-client", false)

	conn := &SSHConnection{
		ConnectionID:         "",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("listener1", &fakeListener{addr: "listener1"})
	state.SSHConnections.Store("127.0.0.1:50001", conn)

	rows := console.getPingStatusRows()
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows when connection has empty ID, got %d", len(rows))
	}
}

func TestGetPingStatusRowsPendingStatus(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	viper.Set("time-format", time.RFC3339)
	defer func() {
		viper.Set("ping-client", false)
		viper.Set("time-format", "")
	}()

	conn := &SSHConnection{
		ConnectionID:         "client-a",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("listener1", &fakeListener{addr: "listener1"})
	state.SSHConnections.Store("127.0.0.1:50002", conn)

	rows := console.getPingStatusRows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Status != "pending" {
		t.Fatalf("expected status 'pending', got '%s'", rows[0].Status)
	}
	if rows[0].PingSent != 0 {
		t.Fatalf("expected pingSent=0, got %d", rows[0].PingSent)
	}
	if rows[0].PingFail != 0 {
		t.Fatalf("expected pingFail=0, got %d", rows[0].PingFail)
	}
	if rows[0].LastPing != "" {
		t.Fatalf("expected empty lastPing, got '%s'", rows[0].LastPing)
	}
	if rows[0].LastPingOk != "" {
		t.Fatalf("expected empty lastPingOk, got '%s'", rows[0].LastPingOk)
	}
}

func TestGetPingStatusRowsOkStatus(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	viper.Set("time-format", time.RFC3339)
	defer func() {
		viper.Set("ping-client", false)
		viper.Set("time-format", "")
	}()

	conn := &SSHConnection{
		ConnectionID:         "client-b",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("listener1", &fakeListener{addr: "listener1"})
	now := time.Now()
	conn.PingSentTotal.Store(10)
	conn.LastPingAtNs.Store(now.UnixNano())
	conn.LastPingOkAtNs.Store(now.UnixNano())
	state.SSHConnections.Store("127.0.0.1:50003", conn)

	rows := console.getPingStatusRows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Status != "ok" {
		t.Fatalf("expected status 'ok', got '%s'", rows[0].Status)
	}
	if rows[0].PingSent != 10 {
		t.Fatalf("expected pingSent=10, got %d", rows[0].PingSent)
	}
	if rows[0].LastPing == "" {
		t.Fatalf("expected non-empty lastPing")
	}
	if rows[0].LastPingOk == "" {
		t.Fatalf("expected non-empty lastPingOk")
	}
}

func TestGetPingStatusRowsDegradedStatus(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	viper.Set("time-format", time.RFC3339)
	defer func() {
		viper.Set("ping-client", false)
		viper.Set("time-format", "")
	}()

	conn := &SSHConnection{
		ConnectionID:         "client-c",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("listener1", &fakeListener{addr: "listener1"})
	now := time.Now()
	conn.PingSentTotal.Store(20)
	conn.PingFailTotal.Store(2)
	conn.LastPingAtNs.Store(now.UnixNano())
	conn.LastPingOkAtNs.Store(now.Add(-10 * time.Second).UnixNano())
	state.SSHConnections.Store("127.0.0.1:50004", conn)

	rows := console.getPingStatusRows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Status != "degraded" {
		t.Fatalf("expected status 'degraded', got '%s'", rows[0].Status)
	}
}

func TestGetPingStatusRowsFailingStatus(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	viper.Set("time-format", time.RFC3339)
	defer func() {
		viper.Set("ping-client", false)
		viper.Set("time-format", "")
	}()

	conn := &SSHConnection{
		ConnectionID:         "client-d",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("listener1", &fakeListener{addr: "listener1"})
	conn.PingSentTotal.Store(5)
	conn.PingFailTotal.Store(5)
	conn.LastPingAtNs.Store(time.Now().UnixNano())
	// LastPingOkAtNs stays 0 -> never had a successful ping
	state.SSHConnections.Store("127.0.0.1:50005", conn)

	rows := console.getPingStatusRows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Status != "failing" {
		t.Fatalf("expected status 'failing', got '%s'", rows[0].Status)
	}
}

func TestGetPingStatusRowsDisabledStatus(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", false)
	viper.Set("time-format", time.RFC3339)
	defer viper.Set("time-format", "")

	conn := &SSHConnection{
		ConnectionID:         "client-e",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("listener1", &fakeListener{addr: "listener1"})
	state.SSHConnections.Store("127.0.0.1:50006", conn)

	rows := console.getPingStatusRows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Status != "disabled" {
		t.Fatalf("expected status 'disabled', got '%s'", rows[0].Status)
	}
}

func TestGetPingStatusRowsSortedByID(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	viper.Set("time-format", time.RFC3339)
	defer func() {
		viper.Set("ping-client", false)
		viper.Set("time-format", "")
	}()

	for _, id := range []string{"zulu", "alpha", "mike"} {
		conn := &SSHConnection{
			ConnectionID:         id,
			Listeners:            syncmap.New[string, net.Listener](),
			Closed:               &sync.Once{},
			Close:                make(chan bool),
			BandwidthProfileLock: &sync.RWMutex{},
		}
		conn.Listeners.Store("l-"+id, &fakeListener{addr: "l-" + id})
		state.SSHConnections.Store("127.0.0.1:"+id, conn)
	}

	rows := console.getPingStatusRows()
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].ID != "alpha" || rows[1].ID != "mike" || rows[2].ID != "zulu" {
		t.Fatalf("rows not sorted: %s, %s, %s", rows[0].ID, rows[1].ID, rows[2].ID)
	}
}

func TestGetPingStatusRowsAtomicCounters(t *testing.T) {
	conn := &SSHConnection{
		ConnectionID:         "counter-test",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}

	conn.PingSentTotal.Add(1)
	conn.PingSentTotal.Add(1)
	conn.PingSentTotal.Add(1)
	conn.PingFailTotal.Add(1)

	if conn.PingSentTotal.Load() != 3 {
		t.Fatalf("expected PingSentTotal=3, got %d", conn.PingSentTotal.Load())
	}
	if conn.PingFailTotal.Load() != 1 {
		t.Fatalf("expected PingFailTotal=1, got %d", conn.PingFailTotal.Load())
	}

	now := time.Now()
	conn.LastPingAtNs.Store(now.UnixNano())
	conn.LastPingOkAtNs.Store(now.UnixNano())

	if conn.LastPingAtNs.Load() != now.UnixNano() {
		t.Fatalf("LastPingAtNs mismatch")
	}
	if conn.LastPingOkAtNs.Load() != now.UnixNano() {
		t.Fatalf("LastPingOkAtNs mismatch")
	}
}

func TestResolveForwardNamesNilInputs(t *testing.T) {
	forwards := resolveForwardNames(nil, nil)
	if forwards != nil {
		t.Fatalf("expected nil for nil inputs, got %v", forwards)
	}
}

func TestResolveForwardNamesNoListeners(t *testing.T) {
	_, state := testConsoleState()
	conn := &SSHConnection{
		Listeners: syncmap.New[string, net.Listener](),
	}
	forwards := resolveForwardNames(conn, state)
	if len(forwards) != 0 {
		t.Fatalf("expected 0 forwards, got %d", len(forwards))
	}
}
