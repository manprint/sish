package utils

import (
	"testing"
	"time"
)

func resetOriginAuditForTest() {
	originIPAuditLock.Lock()
	defer originIPAuditLock.Unlock()
	originIPAuditData = map[string]*originIPAuditCounter{}
}

func TestOriginIPAuditIngressEvidenceMultiplexerOnly(t *testing.T) {
	resetOriginAuditForTest()

	RecordOriginIPAttempt("203.0.113.10:50100", "https", "443")
	RecordOriginIPSuccess("203.0.113.10:50100")

	rows := GetOriginIPAuditSnapshot(time.RFC3339)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	if got := rows[0].IngressEvidence; got != "Multiplexer (:443)" {
		t.Fatalf("unexpected ingress evidence: %q", got)
	}
}

func TestOriginIPAuditIngressEvidenceSSHOnly(t *testing.T) {
	resetOriginAuditForTest()

	RecordOriginIPAttempt("203.0.113.11:50101", "ssh", "2222")
	RecordOriginIPSuccess("203.0.113.11:50101")

	rows := GetOriginIPAuditSnapshot(time.RFC3339)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	if got := rows[0].IngressEvidence; got != "SSH standard (:2222)" {
		t.Fatalf("unexpected ingress evidence: %q", got)
	}
}

func TestOriginIPAuditIngressEvidenceBoth(t *testing.T) {
	resetOriginAuditForTest()

	RecordOriginIPAttempt("203.0.113.12:50102", "ssh", "2222")
	RecordOriginIPAttempt("203.0.113.12:50103", "https", "443")
	RecordOriginIPSuccess("203.0.113.12:50102")
	RecordOriginIPSuccess("203.0.113.12:50103")

	rows := GetOriginIPAuditSnapshot(time.RFC3339)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	if got := rows[0].IngressEvidence; got != "Both (Multiplexer (:443) | SSH (:2222))" {
		t.Fatalf("unexpected ingress evidence: %q", got)
	}
}

func TestOriginIPAuditEvictsOldestEntryWhenBounded(t *testing.T) {
	resetOriginAuditForTest()
	oldMaxOriginIPAuditEntries := maxOriginIPAuditEntries
	maxOriginIPAuditEntries = 2
	defer func() {
		maxOriginIPAuditEntries = oldMaxOriginIPAuditEntries
	}()

	RecordOriginIPAttempt("203.0.113.21:50001", "ssh", "2222")
	originIPAuditLock.Lock()
	originIPAuditData["203.0.113.21"].LastSeen = time.Unix(1, 0)
	originIPAuditLock.Unlock()

	RecordOriginIPAttempt("203.0.113.22:50002", "ssh", "2222")
	originIPAuditLock.Lock()
	originIPAuditData["203.0.113.22"].LastSeen = time.Unix(2, 0)
	originIPAuditLock.Unlock()

	RecordOriginIPAttempt("203.0.113.23:50003", "ssh", "2222")

	rows := GetOriginIPAuditSnapshot(time.RFC3339)
	if len(rows) != 2 {
		t.Fatalf("expected bounded snapshot size 2, got %d", len(rows))
	}

	seen := map[string]struct{}{}
	for _, row := range rows {
		seen[row.IP] = struct{}{}
	}
	if _, ok := seen["203.0.113.21"]; ok {
		t.Fatal("expected oldest origin IP entry to be evicted")
	}
	if _, ok := seen["203.0.113.22"]; !ok {
		t.Fatal("expected newer entry 203.0.113.22 to remain")
	}
	if _, ok := seen["203.0.113.23"]; !ok {
		t.Fatal("expected newest entry 203.0.113.23 to remain")
	}
}
