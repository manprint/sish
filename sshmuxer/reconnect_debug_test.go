package sshmuxer

import (
	"net"
	"sync"
	"testing"

	"github.com/antoniomika/sish/utils"
	"github.com/antoniomika/syncmap"
)

func TestForceDisconnectNoopAndTargetReleaseTimeoutMetrics(t *testing.T) {
	state := utils.NewState()
	state.Console.State = state

	current := &utils.SSHConnection{
		ConnectionID:    "test02",
		Listeners:       syncmap.New[string, net.Listener](),
		ForwardCleanups: syncmap.New[string, func()](),
		Messages:        make(chan string, 2),
		Close:           make(chan bool),
		Closed:          &sync.Once{},
		SSHConn:         nil,
	}

	target := &channelForwardMsg{Addr: "xiaomi-sdufs", Rport: 80}
	disconnected := forceDisconnectTargetConnections(utils.HTTPListener, target, current, state)
	if disconnected != 0 {
		t.Fatalf("disconnected=%d, want 0", disconnected)
	}
	if got := state.Lifecycle.DebugForceDisconnectNoopTotal.Load(); got != 1 {
		t.Fatalf("DebugForceDisconnectNoopTotal=%d, want 1", got)
	}

	waitForTargetRelease(utils.HTTPListener, target, current, state)
	if got := state.Lifecycle.DebugTargetReleaseTimeoutTotal.Load(); got != 0 {
		t.Fatalf("DebugTargetReleaseTimeoutTotal=%d, want 0", got)
	}
}
