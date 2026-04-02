package sshmuxer

import (
	"errors"
	"net"
	"os"
	"sync"
	"testing"

	"github.com/antoniomika/sish/utils"
	"github.com/antoniomika/syncmap"
)

type testListener struct {
	addr   string
	closed bool
	mu     sync.Mutex
}

func (l *testListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *testListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	return nil
}
func (l *testListener) Addr() net.Addr { return testAddr(l.addr) }

type errListener struct {
	addr string
}

func (l *errListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *errListener) Close() error              { return errors.New("close failed") }
func (l *errListener) Addr() net.Addr            { return testAddr(l.addr) }

type testAddr string

func (a testAddr) Network() string { return "unix" }
func (a testAddr) String() string  { return string(a) }

func TestForwardLifecycleCleanupIsIdempotent(t *testing.T) {
	state := utils.NewState()
	state.Console.State = state

	sshConn := &utils.SSHConnection{
		Listeners:       syncmap.New[string, net.Listener](),
		ForwardCleanups: syncmap.New[string, func()](),
	}

	key := "/tmp/sish-test-forward-lifecycle-idempotent.sock"
	base := &testListener{addr: key}
	holder := &utils.ListenerHolder{ListenAddr: key, Listener: base, SSHConn: sshConn}

	state.Listeners.Store(key, holder)
	sshConn.Listeners.Store(key, holder)

	cleanupOnce := &sync.Once{}
	lifecycle := newForwardLifecycle(state, sshConn, key, holder, cleanupOnce)
	lifecycle.setOnCleanup(func() {})
	lifecycle.register()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cleanupOnce.Do(lifecycle.cleanup)
		}()
	}
	wg.Wait()

	if _, ok := state.Listeners.Load(key); ok {
		t.Fatalf("listener key still present in state after cleanup")
	}
	if _, ok := sshConn.Listeners.Load(key); ok {
		t.Fatalf("listener key still present in sshConn listeners after cleanup")
	}
	if got := state.Lifecycle.ForwardCleanupTotal.Load(); got != 1 {
		t.Fatalf("ForwardCleanupTotal=%d, want 1", got)
	}
}

func TestForwardLifecycleCleanupErrorMetric(t *testing.T) {
	state := utils.NewState()
	state.Console.State = state
	sshConn := &utils.SSHConnection{
		Listeners:       syncmap.New[string, net.Listener](),
		ForwardCleanups: syncmap.New[string, func()](),
	}

	key := "/tmp/sish-test-forward-lifecycle-error.sock"
	holder := &utils.ListenerHolder{ListenAddr: key, Listener: &errListener{addr: key}, SSHConn: sshConn}
	cleanupOnce := &sync.Once{}
	lifecycle := newForwardLifecycle(state, sshConn, key, holder, cleanupOnce)
	lifecycle.cleanup()

	if got := state.Lifecycle.ForwardCleanupErrorsTotal.Load(); got == 0 {
		t.Fatalf("expected cleanup errors metric increment, got %d", got)
	}
}

func TestForwardLifecycleRegisterAndRunForwardCleanup(t *testing.T) {
	state := utils.NewState()
	state.Console.State = state
	sshConn := &utils.SSHConnection{
		Listeners:       syncmap.New[string, net.Listener](),
		ForwardCleanups: syncmap.New[string, func()](),
	}
	key := "/tmp/sish-test-forward-lifecycle-register.sock"
	holder := &utils.ListenerHolder{ListenAddr: key, Listener: &testListener{addr: key}, SSHConn: sshConn}
	cleanupOnce := &sync.Once{}
	lifecycle := newForwardLifecycle(state, sshConn, key, holder, cleanupOnce)
	lifecycle.register()

	if !sshConn.RunForwardCleanup(key) {
		t.Fatalf("expected RunForwardCleanup to execute registered cleanup")
	}
	if sshConn.RunForwardCleanup(key) {
		t.Fatalf("RunForwardCleanup should be one-shot")
	}
}

func TestForwardLifecycleCleanupRemovesSocketPathBestEffort(t *testing.T) {
	state := utils.NewState()
	state.Console.State = state
	sshConn := &utils.SSHConnection{
		Listeners:       syncmap.New[string, net.Listener](),
		ForwardCleanups: syncmap.New[string, func()](),
	}
	key := "relative-socket-path-not-existing"
	holder := &utils.ListenerHolder{ListenAddr: key, Listener: &testListener{addr: key}, SSHConn: sshConn}
	lifecycle := newForwardLifecycle(state, sshConn, key, holder, &sync.Once{})
	lifecycle.cleanup()

	if got := state.Lifecycle.ForwardCleanupTotal.Load(); got != 1 {
		t.Fatalf("ForwardCleanupTotal=%d, want 1", got)
	}
	// ENOENT on socket remove is expected and must not count as cleanup error.
	if got := state.Lifecycle.ForwardCleanupErrorsTotal.Load(); got != 0 {
		t.Fatalf("expected no cleanup errors on missing socket path, got %d", got)
	}
}

func TestForwardLifecycleOnCleanupRuns(t *testing.T) {
	state := utils.NewState()
	state.Console.State = state
	sshConn := &utils.SSHConnection{
		Listeners:       syncmap.New[string, net.Listener](),
		ForwardCleanups: syncmap.New[string, func()](),
	}
	key := "/tmp/sish-test-forward-lifecycle-hook.sock"
	hookCalled := false
	lifecycle := newForwardLifecycle(state, sshConn, key, &utils.ListenerHolder{
		ListenAddr: key,
		Listener:   &testListener{addr: key},
		SSHConn:    sshConn,
	}, &sync.Once{})
	lifecycle.setOnCleanup(func() {
		hookCalled = true
	})
	lifecycle.cleanup()
	if !hookCalled {
		t.Fatalf("expected cleanup hook to be called")
	}
}

func TestForwardLifecycleKeyValidationNoop(t *testing.T) {
	sshConn := &utils.SSHConnection{
		ForwardCleanups: syncmap.New[string, func()](),
	}
	sshConn.RegisterForwardCleanup("   ", func() {})
	count := 0
	sshConn.ForwardCleanups.Range(func(_ string, _ func()) bool {
		count++
		return true
	})
	if count != 0 {
		t.Fatalf("expected no registrations for blank key, got %d", count)
	}

	if sshConn.RunForwardCleanup("   ") {
		t.Fatalf("blank key cleanup should not run")
	}
}

func TestForwardLifecycleConcurrentLight(t *testing.T) {
	if os.Getenv("SISH_ENABLE_LIGHT_CONCURRENCY_TESTS") == "0" {
		t.Skip("light concurrency tests disabled")
	}

	state := utils.NewState()
	state.Console.State = state

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "/tmp/sish-light-lifecycle-" + string(rune('a'+i)) + ".sock"
			sshConn := &utils.SSHConnection{
				Listeners:       syncmap.New[string, net.Listener](),
				ForwardCleanups: syncmap.New[string, func()](),
			}
			holder := &utils.ListenerHolder{
				ListenAddr: key,
				Listener:   &testListener{addr: key},
				SSHConn:    sshConn,
			}
			state.Listeners.Store(key, holder)
			sshConn.Listeners.Store(key, holder)

			cleanupOnce := &sync.Once{}
			lifecycle := newForwardLifecycle(state, sshConn, key, holder, cleanupOnce)
			lifecycle.register()
			lifecycle.setOnCleanup(func() {})

			for j := 0; j < 5; j++ {
				cleanupOnce.Do(lifecycle.cleanup)
			}
		}(i)
	}
	wg.Wait()

	if got := state.Lifecycle.ForwardCleanupTotal.Load(); got != 8 {
		t.Fatalf("ForwardCleanupTotal=%d, want 8", got)
	}
}
