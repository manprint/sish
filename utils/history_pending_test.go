package utils

import (
	"net"
	"testing"
	"time"

	"github.com/antoniomika/syncmap"
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
