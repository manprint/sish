package utils

import (
	"testing"
	"time"
)

// TestAcceptBackoffProgression verifies the exponential progression, the cap, and
// that Reset returns to the initial delay (B57).
func TestAcceptBackoffProgression(t *testing.T) {
	var b AcceptBackoff

	want := []time.Duration{
		5 * time.Millisecond,
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		80 * time.Millisecond,
		160 * time.Millisecond,
		320 * time.Millisecond,
		640 * time.Millisecond,
		1 * time.Second, // capped (1280ms -> 1s)
		1 * time.Second, // stays capped
	}

	for i, exp := range want {
		if got := b.Pause(); got != exp {
			t.Fatalf("Pause #%d = %s, want %s", i+1, got, exp)
		}
	}

	b.Reset()
	if got := b.Pause(); got != acceptBackoffMin {
		t.Fatalf("after Reset, Pause = %s, want %s", got, acceptBackoffMin)
	}
}
