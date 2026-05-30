package utils

import (
	"bytes"
	"io"
	"testing"

	"golang.org/x/time/rate"
)

// TestNewLimiterBurstFloor verifies the burst is floored to at least the copy
// buffer size so small bandwidth limits cannot make WaitN reject reads (B1).
func TestNewLimiterBurstFloor(t *testing.T) {
	l := newLimiter(1000, 1.0) // 1000 B/s -> raw burst 1000, must be floored
	if l == nil {
		t.Fatal("expected non-nil limiter")
	}
	if l.Burst() < copyBufferSize {
		t.Fatalf("burst %d below floor %d", l.Burst(), copyBufferSize)
	}
}

// TestWaitForTokensHonoursReadLargerThanBurst proves the split-loop honours a
// read larger than the limiter burst instead of returning an error (the old
// WaitN(n) with n > burst returned an error immediately, killing the conn).
// High rate keeps the test fast; burst is deliberately smaller than the read.
func TestWaitForTokensHonoursReadLargerThanBurst(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(10*1024*1024), 8*1024) // 10MB/s, 8KB burst
	if err := waitForTokens(limiter, 64*1024); err != nil {
		t.Fatalf("waitForTokens rejected a read larger than burst: %v", err)
	}
}

// TestRateLimitedReaderLargeReadNoError ensures the reader does not surface a
// limiter error for a read bigger than the burst.
func TestRateLimitedReaderLargeReadNoError(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(10*1024*1024), 8*1024)
	r := &rateLimitedReader{
		reader:  bytes.NewReader(make([]byte, 64*1024)),
		limiter: limiter,
	}
	buf := make([]byte, 64*1024)
	n, err := r.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("unexpected error: %v", err)
	}
	if n == 0 {
		t.Fatal("expected data to be read")
	}
}
