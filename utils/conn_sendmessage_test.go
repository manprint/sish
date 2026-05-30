package utils

import (
	"testing"
	"time"
)

// TestSendMessageBlockingReturnsAfterClose verifies the blocking branch of
// SendMessage no longer deadlocks once s.Close has been closed (B2).
func TestSendMessageBlockingReturnsAfterClose(t *testing.T) {
	s := &SSHConnection{
		Messages: make(chan string),
		Close:    make(chan bool),
	}
	close(s.Close)

	done := make(chan struct{})
	go func() {
		s.SendMessage("hi", true)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SendMessage(block=true) blocked after Close was closed")
	}
}

// TestSendMessageBlockingDelivers verifies the blocking branch still delivers
// to a live reader.
func TestSendMessageBlockingDelivers(t *testing.T) {
	s := &SSHConnection{
		Messages: make(chan string),
		Close:    make(chan bool),
	}

	got := make(chan string, 1)
	go func() { got <- <-s.Messages }()

	s.SendMessage("hi", true)

	select {
	case m := <-got:
		if m != "hi" {
			t.Fatalf("got %q, want %q", m, "hi")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("message was not delivered to live reader")
	}
}
