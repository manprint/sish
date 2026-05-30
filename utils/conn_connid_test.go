package utils

import (
	"fmt"
	"sync"
	"testing"
)

// TestConnectionIDConcurrentAccess exercises concurrent writers and readers of
// the connection ID under the race detector to confirm connIDLock removes the
// data race (B4). Run with -race.
func TestConnectionIDConcurrentAccess(t *testing.T) {
	s := &SSHConnection{ConnectionID: "rand-init"}

	var wg sync.WaitGroup
	const workers = 8
	const iters = 2000

	for i := 0; i < workers; i++ {
		wg.Add(2)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				s.SetConnectionID(fmt.Sprintf("id-%d-%d", id, j), j%2 == 0)
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				_ = s.GetConnectionID()
				_ = s.IsConnectionIDProvided()
			}
		}()
	}

	wg.Wait()

	// Sanity: after all writers stop, a final write is observable.
	s.SetConnectionID("final", true)
	if got := s.GetConnectionID(); got != "final" {
		t.Fatalf("GetConnectionID = %q, want %q", got, "final")
	}
	if !s.IsConnectionIDProvided() {
		t.Fatal("IsConnectionIDProvided = false, want true")
	}
}
