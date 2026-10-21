package utils

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/antoniomika/multilistener"
)

// TestIsListenerClosedErr covers the sentinel contract the B57 accept loops rely
// on: they exit ONLY when IsListenerClosedErr is true, so it must recognise both
// net.ErrClosed and multilistener.ErrClosed (directly and wrapped) and must NOT
// match an unrelated transient error (which would otherwise end the loop and skip
// the deferred cleanup / tear a forward down).
func TestIsListenerClosedErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"net.ErrClosed", net.ErrClosed, true},
		{"wrapped net.ErrClosed", fmt.Errorf("accept tcp: %w", net.ErrClosed), true},
		{"multilistener.ErrClosed", multilistener.ErrClosed, true},
		{"wrapped multilistener.ErrClosed", fmt.Errorf("mux: %w", multilistener.ErrClosed), true},
		{"io.EOF (transient)", io.EOF, false},
		{"generic error", errors.New("temporary failure"), false},
	}

	for _, tc := range cases {
		if got := IsListenerClosedErr(tc.err); got != tc.want {
			t.Errorf("IsListenerClosedErr(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestListenerHolderAcceptAfterClose verifies that a real unix listener wrapped
// in a ListenerHolder (as the per-forward loop in requests.go uses) reports a
// closed error that IsListenerClosedErr recognises, so the accept loop exits and
// its deferred cleanup runs instead of spinning.
func TestListenerHolderAcceptAfterClose(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "lh.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(sock) })

	holder := &ListenerHolder{Listener: ln, ListenAddr: sock}

	if err := holder.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, acceptErr := holder.Accept()
	if acceptErr == nil {
		t.Fatal("Accept after Close returned nil error")
	}
	if !IsListenerClosedErr(acceptErr) {
		t.Fatalf("Accept after Close err = %v, IsListenerClosedErr = false; the B57 loop would not exit", acceptErr)
	}
}
