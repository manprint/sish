package utils

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/antoniomika/syncmap"
	"golang.org/x/time/rate"
)

func newHardeningTestConn() *SSHConnection {
	return &SSHConnection{
		ConnectionID:         "rand-test",
		BandwidthProfileLock: &sync.RWMutex{},
		Listeners:            syncmap.New[string, net.Listener](),
		ForwardCleanups:      syncmap.New[string, func()](),
		VisitorForwarders:    syncmap.New[string, bool](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		Exec:                 make(chan bool),
		Session:              make(chan bool),
		Messages:             make(chan string, 8),
		SetupLock:            &sync.Mutex{},
	}
}

// A client may open more than one "session" channel, or send "shell" and
// "exec" on the same session. Closing the corresponding channel unguarded
// panicked the whole process (B8).
func TestCloseSessionAndExecChannelsAreIdempotent(t *testing.T) {
	conn := newHardeningTestConn()

	conn.CloseSessionChannel()
	conn.CloseSessionChannel()
	conn.CloseExecChannel()
	conn.CloseExecChannel()

	select {
	case <-conn.Session:
	default:
		t.Fatal("Session channel was not closed")
	}

	select {
	case <-conn.Exec:
	default:
		t.Fatal("Exec channel was not closed")
	}
}

func TestCloseChannelsAreIdempotentUnderConcurrency(t *testing.T) {
	conn := newHardeningTestConn()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn.CloseSessionChannel()
			conn.CloseExecChannel()
		}()
	}
	wg.Wait()
}

// The tunnel option fields are written by the session request goroutine and read
// by the HTTP proxy, the TCP handlers and the console. Run under -race, this
// asserts the accessors actually synchronise them (B7).
func TestConnectionOptionAccessorsAreRaceFree(t *testing.T) {
	conn := newHardeningTestConn()
	deadline := time.Now().Add(time.Hour)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}

			conn.SetProxyProto(byte(i % 3))
			conn.SetHostHeader("host.example")
			conn.SetStripPath(i%2 == 0)
			conn.SetSNIProxy(i%2 == 0)
			conn.SetTCPAddress("127.0.0.1")
			conn.SetTCPAlias(i%2 == 0)
			conn.SetLocalForward(i%2 == 0)
			conn.SetTCPAliasesAllowedUsers([]string{"a", "b"})
			conn.SetAutoClose(i%2 == 0)
			conn.SetForceHTTPS(i%2 == 0)
			conn.SetForceConnect(i%2 == 0)
			conn.SetDeadline(deadline)
			conn.SetConnectionNote("note")
			conn.SetExecMode(i%2 == 0)
		}
	}()

	for i := 0; i < 5000; i++ {
		_ = conn.GetProxyProto()
		_ = conn.GetHostHeader()
		_ = conn.GetStripPath()
		_ = conn.GetSNIProxy()
		_ = conn.GetTCPAddress()
		_ = conn.GetTCPAlias()
		_ = conn.GetLocalForward()
		_ = conn.GetTCPAliasesAllowedUsers()
		_ = conn.GetAutoClose()
		_ = conn.GetForceHTTPS()
		_ = conn.GetForceConnect()
		_ = conn.GetDeadline()
		_ = conn.GetConnectionNote()
		_ = conn.GetExecMode()
	}

	// stop must be closed before waiting: the writer loop only exits on it, so
	// waiting first would deadlock the test.
	close(stop)
	wg.Wait()
}

// GetTCPAliasesAllowedUsers and GetDeadline must hand out copies, or a caller
// could mutate connection state (or observe a torn value) without the lock.
func TestConnectionOptionGettersReturnCopies(t *testing.T) {
	conn := newHardeningTestConn()

	conn.SetTCPAliasesAllowedUsers([]string{"fingerprint"})
	users := conn.GetTCPAliasesAllowedUsers()
	users[0] = "tampered"

	if got := conn.GetTCPAliasesAllowedUsers(); got[0] != "fingerprint" {
		t.Fatalf("allowed users mutated through returned slice: %q", got[0])
	}

	deadline := time.Now().Add(time.Hour)
	conn.SetDeadline(deadline)
	got := conn.GetDeadline()
	if got == nil {
		t.Fatal("GetDeadline returned nil after SetDeadline")
	}

	*got = time.Time{}
	if again := conn.GetDeadline(); again == nil || !again.Equal(deadline) {
		t.Fatal("deadline mutated through returned pointer")
	}
}

func TestGetDeadlineIsNilWhenUnset(t *testing.T) {
	if got := newHardeningTestConn().GetDeadline(); got != nil {
		t.Fatalf("GetDeadline()=%v, want nil", got)
	}
}

// TrySendMessage must never block, so it is safe to call while holding a shared
// lock such as the bind-allocation lock (B20).
func TestTrySendMessageNeverBlocks(t *testing.T) {
	conn := newHardeningTestConn()
	conn.Messages = make(chan string) // unbuffered, no reader

	done := make(chan bool, 1)
	go func() {
		done <- conn.TrySendMessage("hello")
	}()

	select {
	case delivered := <-done:
		if delivered {
			t.Fatal("TrySendMessage reported delivery with no reader")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TrySendMessage blocked")
	}
}

func TestTrySendMessageDeliversWhenBuffered(t *testing.T) {
	conn := newHardeningTestConn()

	if !conn.TrySendMessage("hello") {
		t.Fatal("TrySendMessage did not deliver into a buffered channel")
	}

	if got := <-conn.Messages; got != "hello" {
		t.Fatalf("message=%q, want %q", got, "hello")
	}
}

type eofReader struct{ done bool }

func (r *eofReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	copy(p, []byte("ab"))
	return 2, io.EOF
}

// A reader may return data and io.EOF together. The rate limiter used to
// discard that error whenever throttling failed, turning a clean end-of-stream
// into a transfer error.
func TestRateLimitedReaderPreservesUnderlyingError(t *testing.T) {
	reader := &rateLimitedReader{
		reader:  &eofReader{},
		limiter: rate.NewLimiter(rate.Limit(1), 1),
	}

	buf := make([]byte, 8)
	n, err := reader.Read(buf)
	if n != 2 {
		t.Fatalf("n=%d, want 2", n)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err=%v, want io.EOF", err)
	}
}
