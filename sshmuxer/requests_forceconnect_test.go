package sshmuxer

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antoniomika/sish/utils"
)

func TestWithForceConnectTargetLockSerializesSameTarget(t *testing.T) {
	started := make(chan struct{}, 2)
	finished := make(chan struct{}, 2)
	entered := 0
	var mu sync.Mutex
	// Record violations via an atomic instead of calling t.Fatalf from the
	// spawned goroutine (go vet: Fatalf from a non-test goroutine does not
	// reliably fail the test). The main goroutine asserts on it (B7).
	var concurrentViolation atomic.Bool

	run := func() {
		withForceConnectTargetLock(utils.TCPListener, "example", 8080, func() {
			mu.Lock()
			entered++
			if entered > 1 {
				concurrentViolation.Store(true)
			}
			mu.Unlock()

			started <- struct{}{}
			time.Sleep(80 * time.Millisecond)

			mu.Lock()
			entered--
			mu.Unlock()
			finished <- struct{}{}
		})
	}

	go run()
	go run()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("first execution did not start")
	}

	select {
	case <-started:
		t.Fatalf("second execution should not start before first finishes")
	case <-time.After(40 * time.Millisecond):
	}

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatalf("first execution did not finish")
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("second execution did not start after first finished")
	}

	if concurrentViolation.Load() {
		t.Fatalf("critical section entered concurrently for same target")
	}
}

func TestWithForceConnectTargetLockDifferentTargetsCanRunConcurrently(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})

	run := func(addr string, port uint32) {
		withForceConnectTargetLock(utils.TCPListener, addr, port, func() {
			started <- struct{}{}
			<-release
		})
	}

	go run("target-a", 8080)
	go run("target-b", 8080)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("first goroutine did not start")
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("second goroutine should run concurrently on different target key")
	}

	close(release)
}

// TestForceConnectTargetLockStressNoConcurrentEntry hammers acquire/release on
// one target from many goroutines, exercising the refCount delete/recreate path
// that previously could yield two live locks for the same key (B6). Run -race.
func TestForceConnectTargetLockStressNoConcurrentEntry(t *testing.T) {
	var inside atomic.Int32
	var violation atomic.Bool
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				release := acquireForceConnectTargetLock(utils.TCPListener, "stress", 9000)
				if inside.Add(1) != 1 {
					violation.Store(true)
				}
				inside.Add(-1)
				release()
			}
		}()
	}

	wg.Wait()

	if violation.Load() {
		t.Fatal("two goroutines held the same target lock concurrently")
	}
}

// TestForceConnectTargetLockReleaseIdempotent ensures release can be called more
// than once without a double-unlock panic.
func TestForceConnectTargetLockReleaseIdempotent(t *testing.T) {
	release := acquireForceConnectTargetLock(utils.AliasListener, "idem", 1)
	release()
	release()
}
