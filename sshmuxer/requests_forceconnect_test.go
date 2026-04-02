package sshmuxer

import (
	"sync"
	"testing"
	"time"

	"github.com/antoniomika/sish/utils"
)

func TestWithForceConnectTargetLockSerializesSameTarget(t *testing.T) {
	started := make(chan struct{}, 2)
	finished := make(chan struct{}, 2)
	entered := 0
	var mu sync.Mutex

	run := func() {
		withForceConnectTargetLock(utils.TCPListener, "example", 8080, func() {
			mu.Lock()
			entered++
			if entered > 1 {
				mu.Unlock()
				t.Fatalf("critical section entered concurrently for same target")
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
