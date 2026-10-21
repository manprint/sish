package utils

import (
	"testing"
	"time"
)

// TestExecWaitDeadlineStable verifies the per-connection exec-wait deadline is
// computed once and reused, so repeated forwards on the same connection share a
// single deadline instead of each restarting a 1s timeout (B63).
func TestExecWaitDeadlineStable(t *testing.T) {
	s := &SSHConnection{}

	before := time.Now()
	first := s.ExecWaitDeadline()
	after := time.Now()

	// The first call anchors the deadline at roughly now+1s.
	if first.Before(before.Add(1*time.Second)) || first.After(after.Add(1*time.Second)) {
		t.Fatalf("ExecWaitDeadline = %s, want within [%s, %s]", first,
			before.Add(1*time.Second), after.Add(1*time.Second))
	}

	// Subsequent calls must return the exact same instant.
	for i := 0; i < 3; i++ {
		if got := s.ExecWaitDeadline(); !got.Equal(first) {
			t.Fatalf("ExecWaitDeadline call #%d = %s, want stable %s", i+2, got, first)
		}
	}
}
