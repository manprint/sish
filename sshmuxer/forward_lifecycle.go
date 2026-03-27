package sshmuxer

import (
	"log"
	"os"
	"strings"
	"sync"

	"github.com/antoniomika/sish/utils"
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
			log.Println("Error removing unix socket:", err)
			if f.state != nil {
				f.state.IncrementForwardCleanupErrorCause("socket_remove")
			}
		}
	}

	if f.onCleanup != nil {
		f.onCleanup()
	}

	if f.state != nil {
		f.state.IncrementForwardCleanup()
	}
}
