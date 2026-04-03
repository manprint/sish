package utils

import (
	"fmt"
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

func TestGetPingStatusRowsUnresponsiveStatus(t *testing.T) {
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
	// LastPingFailAtNs is MORE RECENT than LastPingOkAtNs → unresponsive
	conn.LastPingFailAtNs.Store(now.UnixNano())
	state.SSHConnections.Store("127.0.0.1:50004", conn)

	rows := console.getPingStatusRows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Status != "unresponsive" {
		t.Fatalf("expected status 'unresponsive', got '%s'", rows[0].Status)
	}
}

func TestGetPingStatusRowsOkAfterRecovery(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	viper.Set("time-format", time.RFC3339)
	defer func() {
		viper.Set("ping-client", false)
		viper.Set("time-format", "")
	}()

	conn := &SSHConnection{
		ConnectionID:         "client-recovery",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("listener1", &fakeListener{addr: "listener1"})
	now := time.Now()
	conn.PingSentTotal.Store(30)
	conn.PingFailTotal.Store(5)
	// Had failures in the past, but last OK is more recent than last fail → ok
	conn.LastPingAtNs.Store(now.UnixNano())
	conn.LastPingFailAtNs.Store(now.Add(-5 * time.Second).UnixNano())
	conn.LastPingOkAtNs.Store(now.UnixNano())
	state.SSHConnections.Store("127.0.0.1:50014", conn)

	rows := console.getPingStatusRows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Status != "ok" {
		t.Fatalf("expected status 'ok' after recovery, got '%s'", rows[0].Status)
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

func TestGetPingStatusRowsDeadlineMs(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	viper.Set("time-format", time.RFC3339)
	defer func() {
		viper.Set("ping-client", false)
		viper.Set("time-format", "")
	}()

	conn := &SSHConnection{
		ConnectionID:         "deadline-test",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("listener1", &fakeListener{addr: "listener1"})
	futureDeadline := time.Now().Add(2 * time.Minute)
	conn.PingDeadlineNs.Store(futureDeadline.UnixNano())
	conn.PingSentTotal.Store(1)
	conn.LastPingAtNs.Store(time.Now().UnixNano())
	conn.LastPingOkAtNs.Store(time.Now().UnixNano())
	state.SSHConnections.Store("127.0.0.1:60001", conn)

	rows := console.getPingStatusRows()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	expectedMs := futureDeadline.UnixNano() / int64(time.Millisecond)
	if rows[0].DeadlineMs != expectedMs {
		t.Fatalf("expected DeadlineMs=%d, got %d", expectedMs, rows[0].DeadlineMs)
	}
	if rows[0].ClosedAt != "" {
		t.Fatalf("expected empty ClosedAt for active connection, got '%s'", rows[0].ClosedAt)
	}
}

func TestAddClosedPingRow(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	viper.Set("time-format", time.RFC3339)
	defer func() {
		viper.Set("ping-client", false)
		viper.Set("time-format", "")
	}()

	conn := &SSHConnection{
		ConnectionID:         "closed-test",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("listener1", &fakeListener{addr: "listener1"})
	conn.PingSentTotal.Store(15)
	conn.PingFailTotal.Store(3)
	conn.LastPingAtNs.Store(time.Now().UnixNano())
	conn.LastPingOkAtNs.Store(time.Now().Add(-5 * time.Second).UnixNano())

	console.AddClosedPingRow(conn, state)

	console.ClosedPingLock.RLock()
	defer console.ClosedPingLock.RUnlock()

	if len(console.ClosedPingRows) != 1 {
		t.Fatalf("expected 1 closed row, got %d", len(console.ClosedPingRows))
	}
	row := console.ClosedPingRows[0]
	if row.Status != "closed" {
		t.Fatalf("expected status 'closed', got '%s'", row.Status)
	}
	if row.ClosedAt == "" {
		t.Fatalf("expected non-empty ClosedAt")
	}
	if row.PingSent != 15 {
		t.Fatalf("expected PingSent=15, got %d", row.PingSent)
	}
	if row.PingFail != 3 {
		t.Fatalf("expected PingFail=3, got %d", row.PingFail)
	}
	if row.DeadlineMs != 0 {
		t.Fatalf("expected DeadlineMs=0 for closed row, got %d", row.DeadlineMs)
	}
}

func TestClosedPingRowsMergedInGetPingStatusRows(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	viper.Set("time-format", time.RFC3339)
	defer func() {
		viper.Set("ping-client", false)
		viper.Set("time-format", "")
	}()

	// Add an active connection.
	conn := &SSHConnection{
		ConnectionID:         "active-conn",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("listener1", &fakeListener{addr: "listener1"})
	conn.PingSentTotal.Store(5)
	conn.LastPingAtNs.Store(time.Now().UnixNano())
	conn.LastPingOkAtNs.Store(time.Now().UnixNano())
	state.SSHConnections.Store("127.0.0.1:60010", conn)

	// Add a closed row directly.
	console.ClosedPingLock.Lock()
	console.ClosedPingRows = append(console.ClosedPingRows, pingStatusRow{
		ID:       "closed-conn",
		Status:   "closed",
		ClosedAt: "2026-01-01T00:00:00Z",
		PingSent: 10,
	})
	console.ClosedPingLock.Unlock()

	rows := console.getPingStatusRows()
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (1 active + 1 closed), got %d", len(rows))
	}
	// Sorted: active-conn, closed-conn
	if rows[0].ID != "active-conn" {
		t.Fatalf("expected first row 'active-conn', got '%s'", rows[0].ID)
	}
	if rows[1].ID != "closed-conn" {
		t.Fatalf("expected second row 'closed-conn', got '%s'", rows[1].ID)
	}
	if rows[1].Status != "closed" {
		t.Fatalf("expected closed row status 'closed', got '%s'", rows[1].Status)
	}
}

func TestAddClosedPingRowCapped(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	viper.Set("time-format", time.RFC3339)
	defer func() {
		viper.Set("ping-client", false)
		viper.Set("time-format", "")
	}()

	for i := 0; i < 120; i++ {
		conn := &SSHConnection{
			ConnectionID:         fmt.Sprintf("conn-%03d", i),
			Listeners:            syncmap.New[string, net.Listener](),
			Closed:               &sync.Once{},
			Close:                make(chan bool),
			BandwidthProfileLock: &sync.RWMutex{},
		}
		conn.Listeners.Store("l1", &fakeListener{addr: "l1"})
		conn.PingSentTotal.Store(1)
		console.AddClosedPingRow(conn, state)
	}

	console.ClosedPingLock.RLock()
	defer console.ClosedPingLock.RUnlock()

	if len(console.ClosedPingRows) != maxClosedPingRows {
		t.Fatalf("expected %d closed rows (capped), got %d", maxClosedPingRows, len(console.ClosedPingRows))
	}
	// Oldest entries should have been trimmed - first entry should be conn-020
	if console.ClosedPingRows[0].ID != "conn-020" {
		t.Fatalf("expected first entry 'conn-020' after cap, got '%s'", console.ClosedPingRows[0].ID)
	}
}

func TestAddClosedPingRowSkipsEmptyID(t *testing.T) {
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
	console.AddClosedPingRow(conn, state)

	console.ClosedPingLock.RLock()
	defer console.ClosedPingLock.RUnlock()

	if len(console.ClosedPingRows) != 0 {
		t.Fatalf("expected 0 closed rows for empty ID, got %d", len(console.ClosedPingRows))
	}
}

func TestAddClosedPingRowSkipsWhenPingDisabled(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", false)

	conn := &SSHConnection{
		ConnectionID:         "test-id",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("l1", &fakeListener{addr: "l1"})
	console.AddClosedPingRow(conn, state)

	console.ClosedPingLock.RLock()
	defer console.ClosedPingLock.RUnlock()

	if len(console.ClosedPingRows) != 0 {
		t.Fatalf("expected 0 closed rows when ping disabled, got %d", len(console.ClosedPingRows))
	}
}

func TestAddClosedPingRowSkipsGhostConnection(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	defer viper.Set("ping-client", false)

	conn := &SSHConnection{
		ConnectionID:         "ghost-conn",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("l1", &fakeListener{addr: "l1"})
	// PingSentTotal is 0 — ghost connection, never received a ping
	console.AddClosedPingRow(conn, state)

	console.ClosedPingLock.RLock()
	defer console.ClosedPingLock.RUnlock()

	if len(console.ClosedPingRows) != 0 {
		t.Fatalf("expected 0 closed rows for ghost connection (0 pings sent), got %d", len(console.ClosedPingRows))
	}
}

func TestRecordPingFailOpensWindow(t *testing.T) {
	conn := &SSHConnection{}
	now := time.Now().UnixNano()

	conn.RecordPingFail(now)
	windows := conn.SnapshotPingFailWindows()
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if windows[0].FromNs != now || windows[0].ToNs != now {
		t.Fatalf("expected From=To=%d, got From=%d To=%d", now, windows[0].FromNs, windows[0].ToNs)
	}
	if windows[0].FailCount != 1 {
		t.Fatalf("expected FailCount=1, got %d", windows[0].FailCount)
	}
	if windows[0].RecoveredNs != 0 {
		t.Fatalf("expected RecoveredNs=0, got %d", windows[0].RecoveredNs)
	}
}

func TestRecordPingFailExtendsOpenWindow(t *testing.T) {
	conn := &SSHConnection{}
	t1 := time.Now().UnixNano()
	t2 := t1 + int64(5*time.Second)

	conn.RecordPingFail(t1)
	conn.RecordPingFail(t2)

	windows := conn.SnapshotPingFailWindows()
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if windows[0].FromNs != t1 {
		t.Fatalf("expected From=%d, got %d", t1, windows[0].FromNs)
	}
	if windows[0].ToNs != t2 {
		t.Fatalf("expected To=%d, got %d", t2, windows[0].ToNs)
	}
	if windows[0].FailCount != 2 {
		t.Fatalf("expected FailCount=2, got %d", windows[0].FailCount)
	}
}

func TestRecordPingRecoveryClosesWindow(t *testing.T) {
	conn := &SSHConnection{}
	t1 := time.Now().UnixNano()
	t2 := t1 + int64(5*time.Second)
	t3 := t2 + int64(5*time.Second)

	conn.RecordPingFail(t1)
	conn.RecordPingFail(t2)
	conn.RecordPingRecovery(t3)

	windows := conn.SnapshotPingFailWindows()
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if windows[0].RecoveredNs != t3 {
		t.Fatalf("expected RecoveredNs=%d, got %d", t3, windows[0].RecoveredNs)
	}
}

func TestMultipleFailWindows(t *testing.T) {
	conn := &SSHConnection{}
	base := time.Now().UnixNano()

	// First window: 2 fails then recovery
	conn.RecordPingFail(base)
	conn.RecordPingFail(base + int64(5*time.Second))
	conn.RecordPingRecovery(base + int64(10*time.Second))

	// Second window: 3 fails, still open
	conn.RecordPingFail(base + int64(20*time.Second))
	conn.RecordPingFail(base + int64(25*time.Second))
	conn.RecordPingFail(base + int64(30*time.Second))

	windows := conn.SnapshotPingFailWindows()
	if len(windows) != 2 {
		t.Fatalf("expected 2 windows, got %d", len(windows))
	}

	if windows[0].FailCount != 2 {
		t.Fatalf("window 0: expected FailCount=2, got %d", windows[0].FailCount)
	}
	if windows[0].RecoveredNs == 0 {
		t.Fatal("window 0: expected RecoveredNs != 0")
	}

	if windows[1].FailCount != 3 {
		t.Fatalf("window 1: expected FailCount=3, got %d", windows[1].FailCount)
	}
	if windows[1].RecoveredNs != 0 {
		t.Fatal("window 1: expected RecoveredNs == 0 (ongoing)")
	}
}

func TestCloseOpenFailWindow(t *testing.T) {
	conn := &SSHConnection{}
	t1 := time.Now().UnixNano()
	t2 := t1 + int64(5*time.Second)
	tClose := t2 + int64(10*time.Second)

	conn.RecordPingFail(t1)
	conn.RecordPingFail(t2)
	conn.CloseOpenFailWindow(tClose)

	windows := conn.SnapshotPingFailWindows()
	if len(windows) != 1 {
		t.Fatalf("expected 1 window, got %d", len(windows))
	}
	if windows[0].ClosedNs != tClose {
		t.Fatalf("expected ClosedNs=%d, got %d", tClose, windows[0].ClosedNs)
	}
	if windows[0].RecoveredNs != 0 {
		t.Fatal("expected RecoveredNs=0 — closed, not recovered")
	}
}

func TestCloseOpenFailWindowNoopWhenNoOpenWindow(t *testing.T) {
	conn := &SSHConnection{}
	conn.CloseOpenFailWindow(time.Now().UnixNano())
	windows := conn.SnapshotPingFailWindows()
	if len(windows) != 0 {
		t.Fatalf("expected 0 windows, got %d", len(windows))
	}
}

func TestRecordPingRecoveryNoopWhenNoOpenWindow(t *testing.T) {
	conn := &SSHConnection{}
	conn.RecordPingRecovery(time.Now().UnixNano())
	windows := conn.SnapshotPingFailWindows()
	if len(windows) != 0 {
		t.Fatalf("expected 0 windows, got %d", len(windows))
	}
}

func TestSnapshotPingFailWindowsReturnsIndependentCopy(t *testing.T) {
	conn := &SSHConnection{}
	conn.RecordPingFail(time.Now().UnixNano())

	snap1 := conn.SnapshotPingFailWindows()
	snap1[0].FailCount = 999

	snap2 := conn.SnapshotPingFailWindows()
	if snap2[0].FailCount == 999 {
		t.Fatal("snapshot should be independent copy")
	}
}

func TestConvertFailWindows(t *testing.T) {
	viper.Set("time-format", time.RFC3339)
	defer viper.Set("time-format", "")

	base := time.Now()
	windows := []PingFailWindow{
		{FromNs: base.UnixNano(), ToNs: base.Add(10 * time.Second).UnixNano(), RecoveredNs: base.Add(15 * time.Second).UnixNano(), FailCount: 3},
		{FromNs: base.Add(30 * time.Second).UnixNano(), ToNs: base.Add(40 * time.Second).UnixNano(), ClosedNs: base.Add(45 * time.Second).UnixNano(), FailCount: 5},
		{FromNs: base.Add(60 * time.Second).UnixNano(), ToNs: base.Add(70 * time.Second).UnixNano(), FailCount: 2},
	}

	result := convertFailWindows(windows, time.RFC3339)
	if len(result) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(result))
	}

	// Window 0: recovered
	if result[0].RecoveredAt == "" {
		t.Fatal("window 0: expected non-empty RecoveredAt")
	}
	if result[0].ClosedAt != "" {
		t.Fatal("window 0: expected empty ClosedAt")
	}
	if result[0].FailCount != 3 {
		t.Fatalf("window 0: expected FailCount=3, got %d", result[0].FailCount)
	}

	// Window 1: closed (not recovered)
	if result[1].ClosedAt == "" {
		t.Fatal("window 1: expected non-empty ClosedAt")
	}
	if result[1].RecoveredAt != "" {
		t.Fatal("window 1: expected empty RecoveredAt")
	}
	if result[1].FailCount != 5 {
		t.Fatalf("window 1: expected FailCount=5, got %d", result[1].FailCount)
	}

	// Window 2: ongoing
	if result[2].RecoveredAt != "" {
		t.Fatalf("window 2: expected empty RecoveredAt, got '%s'", result[2].RecoveredAt)
	}
	if result[2].ClosedAt != "" {
		t.Fatalf("window 2: expected empty ClosedAt, got '%s'", result[2].ClosedAt)
	}
}

func TestConvertFailWindowsNil(t *testing.T) {
	result := convertFailWindows(nil, time.RFC3339)
	if result != nil {
		t.Fatalf("expected nil for nil input, got %v", result)
	}
}

func TestFailWindowsInPingStatusRow(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	viper.Set("time-format", time.RFC3339)
	defer func() {
		viper.Set("ping-client", false)
		viper.Set("time-format", "")
	}()

	conn := &SSHConnection{
		ConnectionID:         "fail-window-test",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("listener1", &fakeListener{addr: "listener1"})
	conn.PingSentTotal.Store(10)
	conn.PingFailTotal.Store(3)
	conn.LastPingAtNs.Store(time.Now().UnixNano())
	conn.LastPingOkAtNs.Store(time.Now().UnixNano())

	// Record a fail window
	base := time.Now().UnixNano()
	conn.RecordPingFail(base)
	conn.RecordPingFail(base + int64(5*time.Second))
	conn.RecordPingRecovery(base + int64(10*time.Second))

	state.SSHConnections.Store("fail-window-test", conn)
	rows := console.getPingStatusRows()

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if len(rows[0].FailWindows) != 1 {
		t.Fatalf("expected 1 fail window, got %d", len(rows[0].FailWindows))
	}
	if rows[0].FailWindows[0].FailCount != 2 {
		t.Fatalf("expected FailCount=2, got %d", rows[0].FailWindows[0].FailCount)
	}
	if rows[0].FailWindows[0].RecoveredAt == "" {
		t.Fatal("expected non-empty RecoveredAt")
	}
}

func TestFailWindowsInClosedPingRow(t *testing.T) {
	console, state := testConsoleState()
	viper.Set("ping-client", true)
	viper.Set("time-format", time.RFC3339)
	defer func() {
		viper.Set("ping-client", false)
		viper.Set("time-format", "")
	}()

	conn := &SSHConnection{
		ConnectionID:         "closed-fw-test",
		Listeners:            syncmap.New[string, net.Listener](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		BandwidthProfileLock: &sync.RWMutex{},
	}
	conn.Listeners.Store("l1", &fakeListener{addr: "l1"})
	conn.PingSentTotal.Store(5)
	conn.PingFailTotal.Store(2)
	conn.LastPingAtNs.Store(time.Now().UnixNano())

	base := time.Now().UnixNano()
	conn.RecordPingFail(base)
	conn.RecordPingFail(base + int64(5*time.Second))

	console.AddClosedPingRow(conn, state)

	console.ClosedPingLock.RLock()
	defer console.ClosedPingLock.RUnlock()

	if len(console.ClosedPingRows) != 1 {
		t.Fatalf("expected 1 closed row, got %d", len(console.ClosedPingRows))
	}
	if len(console.ClosedPingRows[0].FailWindows) != 1 {
		t.Fatalf("expected 1 fail window in closed row, got %d", len(console.ClosedPingRows[0].FailWindows))
	}
	if console.ClosedPingRows[0].FailWindows[0].FailCount != 2 {
		t.Fatalf("expected FailCount=2, got %d", console.ClosedPingRows[0].FailWindows[0].FailCount)
	}
	// Open fail window must be marked as closed (not recovered) — no "ongoing" for closed connections.
	if console.ClosedPingRows[0].FailWindows[0].ClosedAt == "" {
		t.Fatal("expected non-empty ClosedAt for closed connection fail window")
	}
	if console.ClosedPingRows[0].FailWindows[0].RecoveredAt != "" {
		t.Fatal("expected empty RecoveredAt — connection was closed, not recovered")
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
