package utils

import (
	"testing"
	"time"

	"github.com/spf13/viper"
)

func resetForwardersLogStateForTest() {
	forwardersLogLock.Lock()
	defer forwardersLogLock.Unlock()

	for key, entry := range forwardersLogWriters {
		if entry != nil && entry.logger != nil {
			_ = entry.logger.Close()
		}
		delete(forwardersLogWriters, key)
	}
	forwardersLogLastCleanup = time.Time{}
}

func TestStripANSISequencesRemovesColorCodes(t *testing.T) {
	in := "2026/03/11 - 18:17:17 | host |\x1b[97;42m 200 \x1b[0m|\x1b[97;44m GET \x1b[0m /"
	out := StripANSISequences(in)

	expected := "2026/03/11 - 18:17:17 | host | 200 | GET  /"
	if out != expected {
		t.Fatalf("unexpected sanitized output: got %q want %q", out, expected)
	}
}

func TestStripANSISequencesWithoutEscapeReturnsInput(t *testing.T) {
	in := "plain monochrome line"
	out := StripANSISequences(in)

	if out != in {
		t.Fatalf("expected unchanged input, got %q", out)
	}
}

func TestWriteForwardersLogLineCleansIdleWriters(t *testing.T) {
	viper.Reset()
	resetForwardersLogStateForTest()
	oldForwardersLogWriterMaxIdle := forwardersLogWriterMaxIdle
	oldForwardersLogCleanupInterval := forwardersLogCleanupInterval
	defer func() {
		forwardersLogWriterMaxIdle = oldForwardersLogWriterMaxIdle
		forwardersLogCleanupInterval = oldForwardersLogCleanupInterval
		resetForwardersLogStateForTest()
	}()

	viper.Set("forwarders-log", "enable")
	viper.Set("forwarders-log-dir", t.TempDir())
	viper.Set("forwarders-log-max-size", 100)
	viper.Set("forwarders-log-max-backups", 1)
	viper.Set("forwarders-log-max-age", 1)
	viper.Set("forwarders-log-compress", false)
	viper.Set("time-format", time.RFC3339)
	viper.Set("forwarders-log-time-format", time.RFC3339)
	forwardersLogWriterMaxIdle = 10 * time.Millisecond
	forwardersLogCleanupInterval = 0

	WriteForwardersLogLine("alpha", "first line")

	forwardersLogLock.RLock()
	alpha := forwardersLogWriters["alpha"]
	forwardersLogLock.RUnlock()
	if alpha == nil {
		t.Fatal("expected alpha forwarder writer to be created")
	}

	alpha.lock.Lock()
	alpha.lastUsed = time.Now().Add(-time.Second)
	alpha.lock.Unlock()

	WriteForwardersLogLine("beta", "second line")

	forwardersLogLock.RLock()
	_, alphaExists := forwardersLogWriters["alpha"]
	_, betaExists := forwardersLogWriters["beta"]
	forwardersLogLock.RUnlock()

	if alphaExists {
		t.Fatal("expected stale alpha writer to be cleaned up")
	}
	if !betaExists {
		t.Fatal("expected beta writer to remain active")
	}
}
