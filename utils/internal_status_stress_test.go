package utils

import (
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/vulcand/oxy/roundrobin"
)

func requireStressTestsEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("SISH_ENABLE_STRESS_TESTS") != "1" {
		t.Skip("stress test disabled by default (set SISH_ENABLE_STRESS_TESTS=1)")
	}
}

func TestStressDirtySnapshotConcurrentMutations(t *testing.T) {
	requireStressTestsEnabled(t)
	console, state := testConsoleState()

	stop := make(chan struct{})
	var wg sync.WaitGroup

	worker := func(id int) {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}

			key := fmt.Sprintf("stress-%d-%d", id, i)
			ln := &ListenerHolder{
				ListenAddr: key,
				Listener:   &fakeListener{addr: key},
			}
			state.Listeners.Store(key, ln)

			u, _ := url.Parse("http://example.com")
			httpLB, _ := roundrobin.New(nil)
			state.HTTPListeners.Store(key, &HTTPHolder{
				HTTPUrl:        u,
				SSHConnections: syncmap.New[string, *SSHConnection](),
				Balancer:       httpLB,
			})

			tcpLB, _ := roundrobin.New(nil)
			tcpBalancers := syncmap.New[string, *roundrobin.RoundRobin]()
			tcpBalancers.Store("", tcpLB)
			state.TCPListeners.Store(key, &TCPHolder{
				TCPHost:        key,
				Listener:       &fakeListener{addr: key},
				SSHConnections: syncmap.New[string, *SSHConnection](),
				Balancers:      tcpBalancers,
			})

			aliasLB, _ := roundrobin.New(nil)
			state.AliasListeners.Store(key, &AliasHolder{
				AliasHost:      key,
				SSHConnections: syncmap.New[string, *SSHConnection](),
				Balancer:       aliasLB,
			})

			if i%2 == 0 {
				state.Listeners.Delete(key)
				state.HTTPListeners.Delete(key)
				state.TCPListeners.Delete(key)
				state.AliasListeners.Delete(key)
			}

			i++
		}
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go worker(i)
	}

	for i := 0; i < 500; i++ {
		_ = console.getDirtyForwardRows()
	}

	close(stop)
	wg.Wait()
}

func TestStressLifecycleMetricsUnderDirtySampling(t *testing.T) {
	requireStressTestsEnabled(t)
	console, state := testConsoleState()
	deadline := time.Now().Add(300 * time.Millisecond)

	for i := 0; time.Now().Before(deadline); i++ {
		key := fmt.Sprintf("metric-%d", i)
		state.Listeners.Store(key, &ListenerHolder{
			ListenAddr: key,
			Listener:   &fakeListener{addr: key},
		})

		_ = console.getDirtyForwardRows()
		if i%3 == 0 {
			state.Listeners.Delete(key)
		}
	}

	if state.Lifecycle.DirtyForwardsStableTotal.Load() == 0 {
		t.Fatalf("expected dirty stable metric increments under sustained sampling")
	}
}
