package utils

import (
	"encoding/base64"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/vulcand/oxy/roundrobin"
)

func TestBuildConnectionHistoryEntryShowsVisitorPendingDisplay(t *testing.T) {
	conn := &SSHConnection{
		ConnectionID:         "rand-pend1234",
		ConnectionIDProvided: false,
		ConnectedAt:          time.Unix(100, 0),
		Listeners:            syncmap.New[string, net.Listener](),
		VisitorForwarders:    syncmap.New[string, bool](),
	}

	entry := buildConnectionHistoryEntry(conn, NewState(), time.Unix(130, 0))
	if entry.ID != "visitor-pending-pend1234" {
		t.Fatalf("history ID=%q, want visitor-pending-pend1234", entry.ID)
	}
	if entry.Forwarder != "VIS-pending" {
		t.Fatalf("history Forwarder=%q, want VIS-pending", entry.Forwarder)
	}
}

func TestBuildConnectionHistoryEntryKeepsResolvedVisitorForwarder(t *testing.T) {
	conn := &SSHConnection{
		ConnectionID:         "visitor-abcd1234",
		ConnectionIDProvided: false,
		ConnectedAt:          time.Unix(100, 0),
		Listeners:            syncmap.New[string, net.Listener](),
		VisitorForwarders:    syncmap.New[string, bool](),
	}
	conn.MarkVisitorForwarder("VIS-myalias:8999")

	entry := buildConnectionHistoryEntry(conn, NewState(), time.Unix(130, 0))
	if entry.ID != "visitor-abcd1234" {
		t.Fatalf("history ID=%q, want visitor-abcd1234", entry.ID)
	}
	if entry.Forwarder != "VIS-myalias:8999" {
		t.Fatalf("history Forwarder=%q, want VIS-myalias:8999", entry.Forwarder)
	}
}

func TestBuildConnectionHistoryEntryResolvesHTTPForwarder(t *testing.T) {
	state := NewState()
	conn := &SSHConnection{
		ConnectionID:      "xiaomi-superdufs",
		ConnectionIDProvided: true,
		ConnectedAt:       time.Unix(100, 0),
		Listeners:         syncmap.New[string, net.Listener](),
		VisitorForwarders: syncmap.New[string, bool](),
	}
	conn.Listeners.Store("/tmp/http.sock", &fakeListener{addr: "/tmp/http.sock"})

	lb, err := roundrobin.New(nil)
	if err != nil {
		t.Fatalf("roundrobin.New: %v", err)
	}
	if err := lb.UpsertServer(&url.URL{Scheme: "http", Host: base64.StdEncoding.EncodeToString([]byte("/tmp/http.sock"))}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	hostURL, err := url.Parse("http://app.example.com")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	httpConns := syncmap.New[string, *SSHConnection]()
	httpConns.Store("/tmp/http.sock", conn)
	state.HTTPListeners.Store("app.example.com", &HTTPHolder{
		HTTPUrl:        hostURL,
		SSHConnections: httpConns,
		Balancer:       lb,
	})

	entry := buildConnectionHistoryEntry(conn, state, time.Unix(130, 0))
	if entry.Forwarder != "app.example.com" {
		t.Fatalf("history Forwarder=%q, want app.example.com", entry.Forwarder)
	}
}

func TestBuildConnectionHistoryEntryResolvesTCPForwarder(t *testing.T) {
	state := NewState()
	conn := &SSHConnection{
		ConnectionID:      "tcp-owner",
		ConnectionIDProvided: true,
		ConnectedAt:       time.Unix(100, 0),
		Listeners:         syncmap.New[string, net.Listener](),
		VisitorForwarders: syncmap.New[string, bool](),
	}
	conn.Listeners.Store("/tmp/tcp.sock", &fakeListener{addr: "/tmp/tcp.sock"})

	lb, err := roundrobin.New(nil)
	if err != nil {
		t.Fatalf("roundrobin.New: %v", err)
	}
	if err := lb.UpsertServer(&url.URL{Scheme: "http", Host: base64.StdEncoding.EncodeToString([]byte("/tmp/tcp.sock"))}); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	balancers := syncmap.New[string, *roundrobin.RoundRobin]()
	balancers.Store("", lb)
	state.TCPListeners.Store(":9001", &TCPHolder{
		TCPHost:        ":9001",
		Listener:       &fakeListener{addr: ":9001"},
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancers:      balancers,
	})

	entry := buildConnectionHistoryEntry(conn, state, time.Unix(130, 0))
	if entry.Forwarder != ":9001" {
		t.Fatalf("history Forwarder=%q, want :9001", entry.Forwarder)
	}
}
