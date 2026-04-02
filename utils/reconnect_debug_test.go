package utils

import (
	"encoding/base64"
	"net"
	"net/url"
	"testing"

	"github.com/antoniomika/syncmap"
	"github.com/spf13/viper"
	"github.com/vulcand/oxy/roundrobin"
)

func TestGetOpenHostPurgesStaleHTTPHolder(t *testing.T) {
	state := NewState()
	sshConn := &SSHConnection{
		ConnectionID: "test02",
		Listeners:    syncmap.New[string, net.Listener](),
		Messages:     make(chan string, 2),
		Close:        make(chan bool),
	}

	lb, err := roundrobin.New(nil)
	if err != nil {
		t.Fatalf("roundrobin.New: %v", err)
	}
	staleListener := "/tmp/non-existent-listener.sock"
	err = lb.UpsertServer(&url.URL{Scheme: "http", Host: base64.StdEncoding.EncodeToString([]byte(staleListener))})
	if err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	state.HTTPListeners.Store("xiaomi-sdufs.tuns.example.com", &HTTPHolder{
		HTTPUrl:        &url.URL{Host: "xiaomi-sdufs.tuns.example.com"},
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancer:       lb,
	})

	viper.Set("domain", "tuns.example.com")
	viper.Set("bind-random-subdomains", false)
	viper.Set("force-requested-subdomains", true)
	viper.Set("http-load-balancer", false)
	viper.Set("append-user-to-subdomain", false)

	hostURL, _ := GetOpenHost("xiaomi-sdufs", state, sshConn)
	if hostURL == nil {
		t.Fatalf("expected host assignment after stale purge")
	}
	if got := state.Lifecycle.DebugStaleHolderPurgedHTTPTotal.Load(); got != 1 {
		t.Fatalf("DebugStaleHolderPurgedHTTPTotal=%d, want 1", got)
	}
}

func TestGetOpenHostTracksBindConflictMetric(t *testing.T) {
	state := NewState()
	sshConn := &SSHConnection{
		ConnectionID: "test02",
		Listeners:    syncmap.New[string, net.Listener](),
		Messages:     make(chan string, 2),
		Close:        make(chan bool),
	}

	lb, err := roundrobin.New(nil)
	if err != nil {
		t.Fatalf("roundrobin.New: %v", err)
	}
	activeListener := "/tmp/listener-active.sock"
	state.Listeners.Store(activeListener, &fakeListener{addr: activeListener})
	err = lb.UpsertServer(&url.URL{Scheme: "http", Host: base64.StdEncoding.EncodeToString([]byte(activeListener))})
	if err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}
	state.HTTPListeners.Store("xiaomi-sdufs.tuns.example.com", &HTTPHolder{
		HTTPUrl:        &url.URL{Host: "xiaomi-sdufs.tuns.example.com"},
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancer:       lb,
	})

	viper.Set("domain", "tuns.example.com")
	viper.Set("bind-random-subdomains", false)
	viper.Set("force-requested-subdomains", true)
	viper.Set("http-load-balancer", false)
	viper.Set("bind-random-subdomains-length", 8)
	viper.Set("append-user-to-subdomain", false)

	hostURL, _ := GetOpenHost("xiaomi-sdufs", state, sshConn)
	if hostURL != nil {
		t.Fatalf("expected nil host assignment when requested subdomain is forced and busy")
	}
	if got := state.Lifecycle.DebugBindConflictHTTPTotal.Load(); got != 1 {
		t.Fatalf("DebugBindConflictHTTPTotal=%d, want 1", got)
	}
}
