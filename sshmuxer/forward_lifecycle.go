package sshmuxer

import (
	"log"
	"os"
	"strings"
	"sync"

	"github.com/antoniomika/sish/utils"
	"github.com/logrusorgru/aurora"
)

// forwardLifecycle coordinates registration and cleanup for a single forward.
// Cleanup is idempotent and intended to run through cleanupOnce.
type forwardLifecycle struct {
	state          *utils.State
	sshConn        *utils.SSHConnection
	listenerKey    string
	listenerHolder *utils.ListenerHolder
	cleanupOnce    *sync.Once
	onCleanup      func()

	// forwardingType and forwardingName describe the forward that was actually
	// established (set by setForwarding once the listener type is wired up).
	// They stay empty for setups that never completed, so teardown of a forward
	// that never started is not logged.
	forwardingType string
	forwardingName string
}

func newForwardLifecycle(state *utils.State, sshConn *utils.SSHConnection, listenerKey string, listenerHolder *utils.ListenerHolder, cleanupOnce *sync.Once) *forwardLifecycle {
	return &forwardLifecycle{
		state:          state,
		sshConn:        sshConn,
		listenerKey:    listenerKey,
		listenerHolder: listenerHolder,
		cleanupOnce:    cleanupOnce,
		onCleanup:      func() {},
	}
}

func (f *forwardLifecycle) register() {
	if f == nil || f.sshConn == nil || strings.TrimSpace(f.listenerKey) == "" || f.cleanupOnce == nil {
		return
	}
	f.sshConn.RegisterForwardCleanup(f.listenerKey, func() {
		f.cleanupOnce.Do(f.cleanup)
	})
}

// setForwarding records the established forward so its teardown can be logged,
// mirroring the "forwarding started" logs emitted by the listener handlers.
func (f *forwardLifecycle) setForwarding(forwardingType string, forwardingName string) {
	if f == nil {
		return
	}
	f.forwardingType = forwardingType
	f.forwardingName = forwardingName
}

func (f *forwardLifecycle) setOnCleanup(fn func()) {
	if f == nil {
		return
	}
	if fn == nil {
		f.onCleanup = func() {}
		return
	}
	f.onCleanup = fn
}

func (f *forwardLifecycle) cleanup() {
	if f == nil {
		return
	}

	if f.listenerHolder != nil {
		if err := f.listenerHolder.Close(); err != nil {
			log.Println("Error closing listener:", err)
			if f.state != nil {
				f.state.IncrementForwardCleanupErrorCause("listener_close")
			}
		}
	}

	if f.state != nil && strings.TrimSpace(f.listenerKey) != "" {
		f.state.Listeners.Delete(f.listenerKey)
	}

	if f.sshConn != nil && strings.TrimSpace(f.listenerKey) != "" {
		f.sshConn.Listeners.Delete(f.listenerKey)
		f.sshConn.UnregisterForwardCleanup(f.listenerKey)
	}

	if strings.TrimSpace(f.listenerKey) != "" {
		if err := os.Remove(f.listenerKey); err != nil {
			// unix listeners created with net.Listen("unix", ...) can already unlink
			// the path on Close(), so ENOENT here is expected and not an error.
			if !os.IsNotExist(err) {
				log.Println("Error removing unix socket:", err)
				if f.state != nil {
					f.state.IncrementForwardCleanupErrorCause("socket_remove")
				}
			}
		}
	}

	if f.onCleanup != nil {
		f.onCleanup()
	}

	if f.forwardingType != "" {
		clientAddr := "unknown"
		if f.sshConn != nil && f.sshConn.SSHConn != nil {
			clientAddr = f.sshConn.SSHConn.RemoteAddr().String()
		}

		log.Printf("%s forwarding stopped: %s -> %s for client: %s\n", aurora.BgBlue(f.forwardingType), f.forwardingName, f.listenerKey, clientAddr)
	}

	if f.state != nil {
		f.state.IncrementForwardCleanup()
	}
}
