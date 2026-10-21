package utils

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

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
	if err := waitForTokens(context.Background(), limiter, 64*1024); err != nil {
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

// TestWaitForTokensHonoursContextCancel proves the B41 plumbing: a throttled
// wait returns promptly when its context is cancelled (connection teardown)
// instead of waiting out the configured bandwidth on a dead connection.
func TestWaitForTokensHonoursContextCancel(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(1), 1) // 1 B/s: WaitN would block ~forever
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if err := waitForTokens(ctx, limiter, 1024*1024); err == nil {
		t.Fatal("expected a cancellation error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitForTokens did not return promptly on cancel: %v", elapsed)
	}
}

// TestRateLimitedReaderContextCancel ensures the reader surfaces the context
// error (and returns promptly) when its copy context is cancelled mid-throttle.
func TestRateLimitedReaderContextCancel(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(1), 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := &rateLimitedReader{
		ctx:     ctx,
		reader:  bytes.NewReader(make([]byte, 1024*1024)),
		limiter: limiter,
	}
	buf := make([]byte, 1024*1024)

	start := time.Now()
	n, err := r.Read(buf)
	if n == 0 {
		t.Fatal("expected data to be read before the throttle")
	}
	if err == nil {
		t.Fatal("expected a context error from the throttled read")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("rateLimitedReader did not return promptly on cancel: %v", elapsed)
	}
}
