package utils

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/time/rate"
)

// tcpPair returns a connected pair of loopback TCP connections.
func tcpPair(tb testing.TB) (net.Conn, net.Conn) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	accErr := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			accErr <- err
			return
		}
		accepted <- c
	}()

	dialed, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		tb.Fatalf("dial: %v", err)
	}

	select {
	case c := <-accepted:
		return dialed, c
	case err := <-accErr:
		tb.Fatalf("accept: %v", err)
		return nil, nil
	}
}

// benchCopyThroughput measures one-directional throughput through CopyBoth.
// Topology mirrors production: data flows upstreamClient -> upstreamServer(reader)
// -> CopyBoth(copyToWriter) -> visitorServer(writer) -> visitorClient(read+measure).
//
// mode 0: plain CopyBoth (no profile wrapping)         -- SNI / state.go path
// mode 1: static stats profile (counting, nil limiter) -- CopyBoth(.., profile)
// mode 2: dynamic getter via SSHConnection.GetBandwidthProfile (RLock per read)
//
//	-- production tunnel path (requests.go:525)
func benchCopyThroughput(b *testing.B, mode int) {
	const chunk = 64 * 1024
	payload := make([]byte, chunk)

	visitorClient, visitorServer := tcpPair(b)
	upstreamServer, upstreamClient := tcpPair(b)

	switch mode {
	case 0:
		go CopyBoth(visitorServer, upstreamServer)
	case 1:
		go CopyBoth(visitorServer, upstreamServer, NewConnectionStatsProfile())
	case 2:
		sc := &SSHConnection{
			BandwidthProfileLock: &sync.RWMutex{},
			UserBandwidthProfile: NewConnectionStatsProfile(),
		}
		go CopyBothWithBandwidthProfileGetter(visitorServer, upstreamServer, sc.GetBandwidthProfile)
	}

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := upstreamClient.Write(payload); err != nil {
				return
			}
		}
	}()

	buf := make([]byte, chunk)
	b.SetBytes(chunk)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := io.ReadFull(visitorClient, buf); err != nil {
			b.Fatalf("read: %v", err)
		}
	}
	b.StopTimer()

	close(stop)
	// Only close the client ends; CopyBoth owns and closes the server ends
	// (visitorServer/upstreamServer) once the copies error out, so closing
	// them here would double-close and emit benign log noise.
	visitorClient.Close()
	upstreamClient.Close()
}

func BenchmarkCopyBoth_NoProfile(b *testing.B)     { benchCopyThroughput(b, 0) }
func BenchmarkCopyBoth_StaticProfile(b *testing.B) { benchCopyThroughput(b, 1) }
func BenchmarkCopyBoth_DynamicGetter(b *testing.B) { benchCopyThroughput(b, 2) }

// --- CPU-bound microbench: isolates the read-wrapper chain + io.Copy buffer
// size from real network syscalls, so O1 (buffer) and O2 (per-read RLock) show
// a clean signal that loopback TCP throughput would otherwise mask. ---

// infReader never EOFs and implements neither io.WriterTo nor anything that
// would let io.Copy bypass its internal buffer.
type infReader struct{}

func (infReader) Read(p []byte) (int, error) { return len(p), nil }

// nullWriter discards and deliberately does NOT implement io.ReaderFrom, so
// io.Copy uses its buffered loop exactly like the production copy path.
type nullWriter struct{}

func (nullWriter) Write(p []byte) (int, error) { return len(p), nil }

// benchReadChain copies a fixed amount through the same wrapper chain that
// copyBothInternal builds for copyToWriter, with the default io.Copy buffer.
//
// mode 0: bare source (no wrapping)                    -- baseline
// mode 1: countingReader only (static stats)
// mode 2: countingReader + rateLimitedReader(nil)      -- static profile path
// mode 3: same as 2 but limiterGetter/counterGetter resolve via an
//
//	SSHConnection.GetBandwidthProfile (RLock per read)  -- production tunnel path
func benchReadChain(b *testing.B, mode int) {
	const total = 16 << 20 // 16 MiB per op

	sc := &SSHConnection{
		BandwidthProfileLock: &sync.RWMutex{},
		UserBandwidthProfile: NewConnectionStatsProfile(),
	}
	getProfile := sc.GetBandwidthProfile

	build := func() io.Reader {
		var src io.Reader = infReader{}
		switch mode {
		case 0:
			return src
		case 1:
			p := getProfile()
			return &countingReader{reader: src, counter: &p.DataOutBytes}
		case 2:
			p := getProfile()
			src = &countingReader{reader: src, counter: &p.DataOutBytes}
			return &rateLimitedReader{reader: src, limiter: p.UploadLimiter}
		default: // 3
			src = &countingReader{reader: src, counterGetter: func() *atomic.Int64 {
				p := getProfile()
				if p == nil {
					return nil
				}
				return &p.DataOutBytes
			}}
			return &rateLimitedReader{reader: src, limiterGetter: func() *rate.Limiter {
				p := getProfile()
				if p == nil {
					return nil
				}
				return p.UploadLimiter
			}}
		}
	}

	dst := nullWriter{}
	b.SetBytes(total)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := io.CopyN(dst, build(), total); err != nil {
			b.Fatalf("copy: %v", err)
		}
	}
}

func BenchmarkReadChain_Bare(b *testing.B)     { benchReadChain(b, 0) }
func BenchmarkReadChain_Counting(b *testing.B) { benchReadChain(b, 1) }
func BenchmarkReadChain_Static(b *testing.B)   { benchReadChain(b, 2) }
func BenchmarkReadChain_Dynamic(b *testing.B)  { benchReadChain(b, 3) }
