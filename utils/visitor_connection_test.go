package utils

import (
	"net"
	"testing"

	"github.com/antoniomika/syncmap"
)

func TestVisitorForwarderLabelSortedAndDeduped(t *testing.T) {
	conn := &SSHConnection{
		VisitorForwarders: syncmap.New[string, bool](),
	}

	conn.MarkVisitorForwarder("VIS-myalias:8999")
	conn.MarkVisitorForwarder("VIS-zz:7777")
	conn.MarkVisitorForwarder("VIS-myalias:8999")

	got := conn.VisitorForwarderLabel()
	want := "VIS-myalias:8999, VIS-zz:7777"
	if got != want {
		t.Fatalf("VisitorForwarderLabel = %q, want %q", got, want)
	}
}

func TestResolveConnectionForwardersUsesVisitorLabel(t *testing.T) {
	state := NewState()
	conn := &SSHConnection{
		VisitorForwarders: syncmap.New[string, bool](),
		Listeners:         syncmap.New[string, net.Listener](),
	}
	conn.MarkVisitorForwarder("VIS-myalias:8999")

	got := resolveConnectionForwarders(conn, state)
	if got != "VIS-myalias:8999" {
		t.Fatalf("resolveConnectionForwarders = %q, want VIS-myalias:8999", got)
	}
}

func TestShouldShowVisitorPending(t *testing.T) {
	conn := &SSHConnection{
		ConnectionID:      "rand-abcd1234",
		ConnectionIDProvided: false,
		VisitorForwarders: syncmap.New[string, bool](),
	}
	if !shouldShowVisitorPending(conn, nil, "") {
		t.Fatalf("expected visitor pending true")
	}

	conn.MarkVisitorForwarder("VIS-myalias:8999")
	if shouldShowVisitorPending(conn, nil, "") {
		t.Fatalf("expected visitor pending false for marked visitor")
	}
}

func TestDisplayConnectionIdentityAndForwarderPending(t *testing.T) {
	conn := &SSHConnection{
		ConnectionID:        "rand-abcd1234",
		ConnectionIDProvided: false,
		VisitorForwarders:   syncmap.New[string, bool](),
	}

	id, forwarder := displayConnectionIdentityAndForwarder(conn, conn.ConnectionID, nil, "")
	if id != "visitor-pending-abcd1234" {
		t.Fatalf("display id = %q, want visitor-pending-abcd1234", id)
	}
	if forwarder != "VIS-pending" {
		t.Fatalf("display forwarder = %q, want VIS-pending", forwarder)
	}
}

func TestDisplayConnectionIdentityAndForwarderKeepsResolvedVisitor(t *testing.T) {
	conn := &SSHConnection{
		ConnectionID:        "visitor-1234abcd",
		ConnectionIDProvided: false,
		VisitorForwarders:   syncmap.New[string, bool](),
	}
	conn.MarkVisitorForwarder("VIS-myalias:8999")

	id, forwarder := displayConnectionIdentityAndForwarder(conn, conn.ConnectionID, nil, "VIS-myalias:8999")
	if id != "visitor-1234abcd" {
		t.Fatalf("display id = %q, want visitor-1234abcd", id)
	}
	if forwarder != "VIS-myalias:8999" {
		t.Fatalf("display forwarder = %q, want VIS-myalias:8999", forwarder)
	}
}
