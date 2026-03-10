package utils

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh"
	"golang.org/x/time/rate"
)

type UserBandwidthProfile struct {
	UploadBytesPerSecond   int64
	DownloadBytesPerSecond int64
	BurstFactor            float64
	UploadLimiter          *rate.Limiter
	DownloadLimiter        *rate.Limiter
}

func newLimiter(bytesPerSecond int64, burstFactor float64) *rate.Limiter {
	if bytesPerSecond <= 0 {
		return nil
	}

	if burstFactor <= 0 {
		burstFactor = 1.0
	}

	burstBytes := int(math.Max(1, float64(bytesPerSecond)*burstFactor))
	return rate.NewLimiter(rate.Limit(float64(bytesPerSecond)), burstBytes)
}

func NewUserBandwidthProfile(uploadBytesPerSecond int64, downloadBytesPerSecond int64, burstFactor float64) *UserBandwidthProfile {
	if uploadBytesPerSecond <= 0 && downloadBytesPerSecond <= 0 {
		return nil
	}

	if burstFactor <= 0 {
		burstFactor = 1.0
	}

	return &UserBandwidthProfile{
		UploadBytesPerSecond:   uploadBytesPerSecond,
		DownloadBytesPerSecond: downloadBytesPerSecond,
		BurstFactor:            burstFactor,
		UploadLimiter:          newLimiter(uploadBytesPerSecond, burstFactor),
		DownloadLimiter:        newLimiter(downloadBytesPerSecond, burstFactor),
	}
}

func UserBandwidthProfileFromPermissions(permissions *ssh.Permissions) *UserBandwidthProfile {
	if permissions == nil || permissions.Extensions == nil {
		return nil
	}

	extensions := permissions.Extensions
	upload := int64(0)
	download := int64(0)
	burst := 1.0

	if uploadValue, ok := extensions[authUserBandwidthUploadExtKey]; ok && strings.TrimSpace(uploadValue) != "" {
		parsed, err := strconv.ParseInt(strings.TrimSpace(uploadValue), 10, 64)
		if err == nil && parsed > 0 {
			upload = parsed
		}
	}

	if downloadValue, ok := extensions[authUserBandwidthDownloadExtKey]; ok && strings.TrimSpace(downloadValue) != "" {
		parsed, err := strconv.ParseInt(strings.TrimSpace(downloadValue), 10, 64)
		if err == nil && parsed > 0 {
			download = parsed
		}
	}

	if burstValue, ok := extensions[authUserBandwidthBurstExtKey]; ok && strings.TrimSpace(burstValue) != "" {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(burstValue), 64)
		if err == nil && parsed > 0 {
			burst = parsed
		}
	}

	return NewUserBandwidthProfile(upload, download, burst)
}

type rateLimitedReader struct {
	reader  io.Reader
	limiter *rate.Limiter
}

func (r *rateLimitedReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 && r.limiter != nil {
		if waitErr := r.limiter.WaitN(context.Background(), n); waitErr != nil {
			return n, waitErr
		}
	}

	return n, err
}

// SSHConnection handles state for a SSHConnection. It wraps an ssh.ServerConn
// and allows us to pass other state around the application.
type SSHConnection struct {
	SSHConn                *ssh.ServerConn
	ConnectionID           string
	ConnectionIDProvided   bool
	UserBandwidthProfile   *UserBandwidthProfile
	ConnectedAt            time.Time
	ConnectionNote         string
	Listeners              *syncmap.Map[string, net.Listener]
	Closed                 *sync.Once
	Close                  chan bool
	Exec                   chan bool
	Messages               chan string
	ProxyProto             byte
	HostHeader             string
	StripPath              bool
	SNIProxy               bool
	TCPAddress             string
	TCPAlias               bool
	LocalForward           bool
	TCPAliasesAllowedUsers []string
	AutoClose              bool
	ForceHTTPS             bool
	ForceConnect           bool
	Session                chan bool
	CleanupHandler         bool
	SetupLock              *sync.Mutex
	Deadline               *time.Time
	ExecMode               bool
}

// SendMessage sends a console message to the connection. If block is true, it
// will block until the message is sent. If it is false, it will try to send the
// message 5 times, waiting 100ms each time.
func (s *SSHConnection) SendMessage(message string, block bool) {
	if block {
		s.Messages <- message
		return
	}

	for i := 0; i < 5; {
		select {
		case <-s.Close:
			return
		case s.Messages <- message:
			return
		default:
			time.Sleep(100 * time.Millisecond)
			i++
		}
	}
}

// ListenerCount returns the number of current active listeners on this connection.
func (s *SSHConnection) ListenerCount() int {
	if s.LocalForward {
		return -1
	}

	count := 0

	s.Listeners.Range(func(key string, value net.Listener) bool {
		count++
		return true
	})

	return count
}

// CleanUp closes all allocated resources for a SSH session and cleans them up.
func (s *SSHConnection) CleanUp(state *State) {
	s.Closed.Do(func() {
		endedAt := time.Now()

		if state != nil && state.Console != nil {
			startedAt := s.ConnectedAt
			if startedAt.IsZero() {
				startedAt = endedAt
			}

			connectionID := s.ConnectionID
			if strings.TrimSpace(connectionID) == "" {
				connectionID = fmt.Sprintf("rand-%s", strings.ToLower(RandStringBytesMaskImprSrc(8)))
			}

			remoteAddr := ""
			username := ""
			if s.SSHConn != nil {
				if s.SSHConn.RemoteAddr() != nil {
					remoteAddr = s.SSHConn.RemoteAddr().String()
				}
				username = s.SSHConn.User()
			}

			state.Console.AddHistoryEntry(ConnectionHistory{
				ID:         connectionID,
				RemoteAddr: remoteAddr,
				Username:   username,
				StartedAt:  startedAt,
				EndedAt:    endedAt,
				Duration:   endedAt.Sub(startedAt),
			})
		}

		close(s.Close)

		err := s.SSHConn.Close()
		if err != nil {
			log.Println("Error closing SSH connection:", err)
		}

		state.SSHConnections.Delete(s.SSHConn.RemoteAddr().String())
		log.Println("Closed SSH connection for:", s.SSHConn.RemoteAddr().String(), "user:", s.SSHConn.User())
	})
}

// TeeConn represents a simple net.Conn interface for SNI Processing.
type TeeConn struct {
	Conn     net.Conn
	Buffer   *bufio.Reader
	Unbuffer bool
}

// Read implements a reader ontop of the TeeReader.
func (conn *TeeConn) Read(p []byte) (int, error) {
	if conn.Unbuffer && conn.Buffer.Buffered() > 0 {
		return conn.Buffer.Read(p)
	}
	return conn.Conn.Read(p)
}

// Write is a shim function to fit net.Conn.
func (conn *TeeConn) Write(p []byte) (int, error) {
	return conn.Conn.Write(p)
}

// Close is a shim function to fit net.Conn.
func (conn *TeeConn) Close() error {
	return conn.Conn.Close()
}

// LocalAddr is a shim function to fit net.Conn.
func (conn *TeeConn) LocalAddr() net.Addr { return conn.Conn.LocalAddr() }

// RemoteAddr is a shim function to fit net.Conn.
func (conn *TeeConn) RemoteAddr() net.Addr { return conn.Conn.RemoteAddr() }

// SetDeadline is a shim function to fit net.Conn.
func (conn *TeeConn) SetDeadline(t time.Time) error { return conn.Conn.SetDeadline(t) }

// SetReadDeadline is a shim function to fit net.Conn.
func (conn *TeeConn) SetReadDeadline(t time.Time) error { return conn.Conn.SetReadDeadline(t) }

// SetWriteDeadline is a shim function to fit net.Conn.
func (conn *TeeConn) SetWriteDeadline(t time.Time) error { return conn.Conn.SetWriteDeadline(t) }

func NewTeeConn(conn net.Conn) *TeeConn {
	teeConn := &TeeConn{
		Conn:   conn,
		Buffer: bufio.NewReaderSize(conn, 65535),
	}

	return teeConn
}

// PeekTLSHello peeks the TLS Connection Hello to proxy based on SNI.
func PeekTLSHello(conn net.Conn) (*tls.ClientHelloInfo, *TeeConn, error) {
	var tlsHello *tls.ClientHelloInfo

	tlsConfig := &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			tlsHello = hello
			return nil, nil
		},
	}

	teeConn := NewTeeConn(conn)

	header, err := teeConn.Buffer.Peek(5)
	if err != nil {
		return tlsHello, teeConn, err
	}

	if header[0] != 0x16 {
		return tlsHello, teeConn, err
	}

	helloBytes, err := teeConn.Buffer.Peek(len(header) + (int(header[3])<<8 | int(header[4])))
	if err != nil {
		return tlsHello, teeConn, err
	}

	err = tls.Server(bufConn{reader: bytes.NewReader(helloBytes)}, tlsConfig).Handshake()

	teeConn.Unbuffer = true

	return tlsHello, teeConn, err
}

type bufConn struct {
	reader io.Reader
	net.Conn
}

func (b bufConn) Read(p []byte) (int, error) { return b.reader.Read(p) }
func (bufConn) Write(p []byte) (int, error)  { return 0, io.EOF }

// IdleTimeoutConn handles the connection with a context deadline.
// code adapted from https://qiita.com/kwi/items/b38d6273624ad3f6ae79
type IdleTimeoutConn struct {
	Conn net.Conn
}

// Read is needed to implement the reader part.
func (i IdleTimeoutConn) Read(buf []byte) (int, error) {
	err := i.Conn.SetDeadline(time.Now().Add(viper.GetDuration("idle-connection-timeout")))
	if err != nil {
		return 0, err
	}

	return i.Conn.Read(buf)
}

// Write is needed to implement the writer part.
func (i IdleTimeoutConn) Write(buf []byte) (int, error) {
	err := i.Conn.SetDeadline(time.Now().Add(viper.GetDuration("idle-connection-timeout")))
	if err != nil {
		return 0, err
	}

	return i.Conn.Write(buf)
}

// CopyBoth copies betwen a reader and writer and will cleanup each.
func CopyBoth(writer net.Conn, reader io.ReadWriteCloser, bandwidthProfile ...*UserBandwidthProfile) {
	closeBoth := func() {
		err := reader.Close()
		if err != nil {
			log.Println("Error closing reader:", err)
		}

		err = writer.Close()
		if err != nil {
			log.Println("Error closing writer:", err)
		}
	}

	var tcon io.ReadWriter
	var profile *UserBandwidthProfile
	if len(bandwidthProfile) > 0 {
		profile = bandwidthProfile[0]
	}

	if viper.GetBool("idle-connection") {
		tcon = IdleTimeoutConn{
			Conn: writer,
		}
	} else {
		tcon = writer
	}

	copyToReader := func() {
		source := io.Reader(tcon)
		if profile != nil && profile.DownloadLimiter != nil {
			source = &rateLimitedReader{reader: source, limiter: profile.DownloadLimiter}
		}

		_, err := io.Copy(reader, source)
		if err != nil && viper.GetBool("debug") {
			log.Println("Error copying to reader:", err)
		}

		closeBoth()
	}

	copyToWriter := func() {
		source := io.Reader(reader)
		if profile != nil && profile.UploadLimiter != nil {
			source = &rateLimitedReader{reader: source, limiter: profile.UploadLimiter}
		}

		_, err := io.Copy(tcon, source)
		if err != nil && viper.GetBool("debug") {
			log.Println("Error copying to writer:", err)
		}

		closeBoth()
	}

	go copyToReader()
	copyToWriter()
}
