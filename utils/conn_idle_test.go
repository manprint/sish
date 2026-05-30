package utils

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// deadlineCountConn is a net.Conn stub that counts SetDeadline calls and treats
// Read/Write as instantly successful.
type deadlineCountConn struct {
	net.Conn
	setDeadlineCalls atomic.Int32
}

func (c *deadlineCountConn) SetDeadline(time.Time) error { c.setDeadlineCalls.Add(1); return nil }
func (c *deadlineCountConn) Read(p []byte) (int, error)  { return len(p), nil }
func (c *deadlineCountConn) Write(p []byte) (int, error) { return len(p), nil }

// TestIdleTimeoutConnAmortizesSetDeadline verifies many rapid reads within the
// step window trigger only a single SetDeadline syscall (O6).
func TestIdleTimeoutConnAmortizesSetDeadline(t *testing.T) {
	viper.Set("idle-connection-timeout", 4*time.Second) // step = 1s
	defer viper.Set("idle-connection-timeout", 0)

	c := &deadlineCountConn{}
	itc := NewIdleTimeoutConn(c)

	buf := make([]byte, 8)
	for i := 0; i < 100; i++ {
		if _, err := itc.Read(buf); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}

	if got := c.setDeadlineCalls.Load(); got != 1 {
		t.Fatalf("expected 1 SetDeadline call within the step window, got %d", got)
	}
}

// TestIdleTimeoutConnZeroTimeoutNoDeadline verifies a non-positive timeout sets
// no deadline at all.
func TestIdleTimeoutConnZeroTimeoutNoDeadline(t *testing.T) {
	viper.Set("idle-connection-timeout", time.Duration(0))

	c := &deadlineCountConn{}
	itc := NewIdleTimeoutConn(c)

	buf := make([]byte, 8)
	if _, err := itc.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := itc.Write(buf); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := c.setDeadlineCalls.Load(); got != 0 {
		t.Fatalf("expected 0 SetDeadline calls for zero timeout, got %d", got)
	}
}
