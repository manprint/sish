package utils

import (
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/antoniomika/syncmap"
	"golang.org/x/crypto/ssh"
)

// fakeAddr is defined in internal_status_test.go — reuse it here.

// fakeSSHConn implements ssh.Conn for testing. Only RemoteAddr and User are
// used by the code under test; all other methods panic if called.
type fakeSSHConn struct {
	ssh.ConnMetadata
	remote net.Addr
	user   string
}

func (f *fakeSSHConn) RemoteAddr() net.Addr                                   { return f.remote }
func (f *fakeSSHConn) User() string                                           { return f.user }
func (f *fakeSSHConn) LocalAddr() net.Addr                                    { return fakeAddr("127.0.0.1:2222") }
func (f *fakeSSHConn) SessionID() []byte                                      { return nil }
func (f *fakeSSHConn) ClientVersion() []byte                                  { return nil }
func (f *fakeSSHConn) ServerVersion() []byte                                  { return nil }
func (f *fakeSSHConn) SendRequest(string, bool, []byte) (bool, []byte, error) { return false, nil, nil }
func (f *fakeSSHConn) OpenChannel(string, []byte) (ssh.Channel, <-chan *ssh.Request, error) {
	return nil, nil, nil
}
func (f *fakeSSHConn) Close() error { return nil }
func (f *fakeSSHConn) Wait() error  { return nil }

func newTestSSHConnection(remoteAddr string, visitorForwarders ...string) *SSHConnection {
	conn := &SSHConnection{
		Listeners:            syncmap.New[string, net.Listener](),
		VisitorForwarders:    syncmap.New[string, bool](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		Exec:                 make(chan bool),
		Messages:             make(chan string, 1),
		Session:              make(chan bool),
		SetupLock:            &sync.Mutex{},
		ForwardCleanups:      syncmap.New[string, func()](),
		BandwidthProfileLock: &sync.RWMutex{},
		ConnectedAt:          time.Now(),
		SSHConn: &ssh.ServerConn{
			Conn: &fakeSSHConn{
				remote: fakeAddr(remoteAddr),
				user:   "testuser",
			},
		},
	}
	for _, fw := range visitorForwarders {
		conn.MarkVisitorForwarder(fw)
	}
	return conn
}

func TestResolveActiveForwardersByIP_NoConnections(t *testing.T) {
	state := NewState()
	wc := &WebConsole{State: state}
	result := wc.resolveActiveForwardersByIP()
	if len(result) != 0 {
		t.Fatalf("expected empty map, got %d entries", len(result))
	}
}

func TestResolveActiveForwardersByIP_NilState(t *testing.T) {
	wc := &WebConsole{State: nil}
	result := wc.resolveActiveForwardersByIP()
	if len(result) != 0 {
		t.Fatalf("expected empty map for nil state, got %d entries", len(result))
	}
}

func TestResolveActiveForwardersByIP_SingleConnectionSingleForwarder(t *testing.T) {
	state := NewState()
	conn := newTestSSHConnection("10.0.0.1:54321", "subdomain.example.com")
	state.SSHConnections.Store("conn1", conn)

	wc := &WebConsole{State: state}
	result := wc.resolveActiveForwardersByIP()

	fwds, ok := result["10.0.0.1"]
	if !ok {
		t.Fatalf("expected entry for 10.0.0.1")
	}
	if len(fwds) != 1 || fwds[0] != "subdomain.example.com" {
		t.Fatalf("unexpected forwarders: %v", fwds)
	}
}

func TestResolveActiveForwardersByIP_MultipleForwardersSameIP(t *testing.T) {
	state := NewState()
	conn1 := newTestSSHConnection("10.0.0.2:54321", "alpha.example.com")
	conn2 := newTestSSHConnection("10.0.0.2:54322", "beta.example.com")
	state.SSHConnections.Store("conn1", conn1)
	state.SSHConnections.Store("conn2", conn2)

	wc := &WebConsole{State: state}
	result := wc.resolveActiveForwardersByIP()

	fwds, ok := result["10.0.0.2"]
	if !ok {
		t.Fatalf("expected entry for 10.0.0.2")
	}
	sort.Strings(fwds)
	if len(fwds) != 2 || fwds[0] != "alpha.example.com" || fwds[1] != "beta.example.com" {
		t.Fatalf("unexpected forwarders: %v", fwds)
	}
}

func TestResolveActiveForwardersByIP_DifferentIPs(t *testing.T) {
	state := NewState()
	conn1 := newTestSSHConnection("10.0.0.3:54321", "one.example.com")
	conn2 := newTestSSHConnection("10.0.0.4:54322", "two.example.com")
	state.SSHConnections.Store("conn1", conn1)
	state.SSHConnections.Store("conn2", conn2)

	wc := &WebConsole{State: state}
	result := wc.resolveActiveForwardersByIP()

	if len(result) != 2 {
		t.Fatalf("expected 2 IPs, got %d", len(result))
	}
	if fwds := result["10.0.0.3"]; len(fwds) != 1 || fwds[0] != "one.example.com" {
		t.Fatalf("10.0.0.3 forwarders = %v", fwds)
	}
	if fwds := result["10.0.0.4"]; len(fwds) != 1 || fwds[0] != "two.example.com" {
		t.Fatalf("10.0.0.4 forwarders = %v", fwds)
	}
}

func TestResolveActiveForwardersByIP_DeduplicatesForwarders(t *testing.T) {
	state := NewState()
	conn1 := newTestSSHConnection("10.0.0.5:54321", "dup.example.com")
	conn2 := newTestSSHConnection("10.0.0.5:54322", "dup.example.com")
	state.SSHConnections.Store("conn1", conn1)
	state.SSHConnections.Store("conn2", conn2)

	wc := &WebConsole{State: state}
	result := wc.resolveActiveForwardersByIP()

	fwds := result["10.0.0.5"]
	if len(fwds) != 1 || fwds[0] != "dup.example.com" {
		t.Fatalf("expected deduplicated forwarders, got %v", fwds)
	}
}

func TestResolveActiveForwardersByIP_SkipsNoForwarder(t *testing.T) {
	state := NewState()
	// Connection with no visitor forwarders and no listeners -> no forwarder resolved
	conn := newTestSSHConnection("10.0.0.6:54321")
	state.SSHConnections.Store("conn1", conn)

	wc := &WebConsole{State: state}
	result := wc.resolveActiveForwardersByIP()

	if len(result) != 0 {
		t.Fatalf("expected empty map for conn with no forwarders, got %v", result)
	}
}

func TestResolveActiveForwardersByIP_SkipsNilSSHConn(t *testing.T) {
	state := NewState()
	conn := &SSHConnection{
		Listeners:         syncmap.New[string, net.Listener](),
		VisitorForwarders: syncmap.New[string, bool](),
	}
	// SSHConn is nil
	state.SSHConnections.Store("conn1", conn)

	wc := &WebConsole{State: state}
	result := wc.resolveActiveForwardersByIP()

	if len(result) != 0 {
		t.Fatalf("expected empty map for nil SSHConn, got %v", result)
	}
}

func TestResolveActiveForwardersByIP_MultipleForwardersOnSingleConnection(t *testing.T) {
	state := NewState()
	conn := newTestSSHConnection("10.0.0.7:54321", "alpha.example.com", "beta.example.com")
	state.SSHConnections.Store("conn1", conn)

	wc := &WebConsole{State: state}
	result := wc.resolveActiveForwardersByIP()

	fwds := result["10.0.0.7"]
	sort.Strings(fwds)
	if len(fwds) != 2 || fwds[0] != "alpha.example.com" || fwds[1] != "beta.example.com" {
		t.Fatalf("unexpected forwarders: %v", fwds)
	}
}

func TestAuditEntryForwardersField(t *testing.T) {
	resetOriginAuditForTest()

	RecordOriginIPAttempt("10.0.0.8:50100", "ssh", "2222")
	RecordOriginIPSuccess("10.0.0.8:50100")
	RecordOriginIPAttempt("10.0.0.9:50200", "ssh", "2222")
	RecordOriginIPReject("10.0.0.9:50200", "auth failed")

	rows := GetOriginIPAuditSnapshot(time.RFC3339)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	// Verify the Forwarders field is nil/empty by default from the snapshot
	for _, row := range rows {
		if len(row.Forwarders) != 0 {
			t.Fatalf("expected empty Forwarders in snapshot for ip=%s, got %v", row.IP, row.Forwarders)
		}
	}
}

func TestAuditForwardersOnlyForSuccessRows(t *testing.T) {
	resetOriginAuditForTest()

	// IP with success
	RecordOriginIPAttempt("10.0.0.10:50100", "ssh", "2222")
	RecordOriginIPSuccess("10.0.0.10:50100")

	// IP with only rejects
	RecordOriginIPAttempt("10.0.0.11:50200", "ssh", "2222")
	RecordOriginIPReject("10.0.0.11:50200", "denied")

	rows := GetOriginIPAuditSnapshot(time.RFC3339)

	// Simulate what HandleAudit does: enrich only success > 0 rows
	ipForwarders := map[string][]string{
		"10.0.0.10": {"test.example.com"},
		"10.0.0.11": {"other.example.com"}, // should NOT be assigned
	}
	for i := range rows {
		if rows[i].Success > 0 {
			if fwds, ok := ipForwarders[rows[i].IP]; ok {
				rows[i].Forwarders = fwds
			}
		}
	}

	for _, row := range rows {
		if row.IP == "10.0.0.10" {
			if len(row.Forwarders) != 1 || row.Forwarders[0] != "test.example.com" {
				t.Fatalf("expected forwarder for success IP, got %v", row.Forwarders)
			}
		} else if row.IP == "10.0.0.11" {
			if len(row.Forwarders) != 0 {
				t.Fatalf("expected no forwarder for rejected-only IP, got %v", row.Forwarders)
			}
		}
	}
}
