package utils

import (
	"testing"

	"github.com/spf13/viper"
)

// TestGetRandomPortInRangeMalformed covers the B32 edge cases that used to panic
// (mathrand.Intn on an empty or zero/negative argument) or recurse unboundedly.
func TestGetRandomPortInRangeMalformed(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"only comma":       ",",
		"malformed triple": "1-2-3",
		"non numeric":      "abc",
		"non numeric hi":   "1000-xyz",
	}

	for name, portRange := range cases {
		t.Run(name, func(t *testing.T) {
			if got := GetRandomPortInRange("localhost", portRange); got != 0 {
				t.Fatalf("GetRandomPortInRange(%q) = %d, want 0", portRange, got)
			}
		})
	}
}

// TestGetRandomPortInRangeValid verifies a normal range returns a bindable port
// inside the range.
func TestGetRandomPortInRangeValid(t *testing.T) {
	const lo, hi = 49200, 49250
	got := GetRandomPortInRange("localhost", "49200-49250")
	if got == 0 {
		t.Fatal("GetRandomPortInRange returned 0 for a valid range")
	}
	if got < lo || got > hi {
		t.Fatalf("GetRandomPortInRange = %d, want within [%d,%d] (inclusive)", got, lo, hi)
	}
}

// TestGetRandomPortInRangeReversed verifies a reversed range is normalised
// instead of underflowing hi-lo and panicking (B32).
func TestGetRandomPortInRangeReversed(t *testing.T) {
	const lo, hi = 49260, 49310
	got := GetRandomPortInRange("localhost", "49310-49260")
	if got == 0 {
		t.Fatal("GetRandomPortInRange returned 0 for a reversed range")
	}
	if got < lo || got > hi {
		t.Fatalf("GetRandomPortInRange = %d, want within [%d,%d]", got, lo, hi)
	}
}

// TestGetRandomPortInRangeSinglePort exercises the single-port (hi == lo) path,
// which used to hit mathrand.Intn(0).
func TestGetRandomPortInRangeSinglePort(t *testing.T) {
	// A single-port range either binds that exact port or, if busy, returns 0
	// after the bounded retries — never panics.
	got := GetRandomPortInRange("localhost", "49321-49321")
	if got != 0 && got != 49321 {
		t.Fatalf("GetRandomPortInRange single-port = %d, want 0 or 49321", got)
	}
}

// TestLoadKeysMissingDirectoryNoPanic guards B30: a missing
// authentication-keys-directory calls the WalkDir callback with d == nil, which
// used to panic on d.IsDir() both at startup and from the key watcher.
func TestLoadKeysMissingDirectoryNoPanic(t *testing.T) {
	prev := viper.Get("authentication-keys-directory")
	t.Cleanup(func() { viper.Set("authentication-keys-directory", prev) })

	viper.Set("authentication-keys-directory", t.TempDir()+"/does-not-exist")

	// Must not panic.
	loadKeys()
}
