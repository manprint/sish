package utils

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func scrapeInternalMetrics(t *testing.T, console *WebConsole) string {
	t.Helper()

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	g, _ := gin.CreateTestContext(recorder)
	g.Request = httptest.NewRequest(http.MethodGet, "/_sish/api/internal/metrics", nil)

	console.HandleInternalMetrics(g)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", recorder.Code)
	}

	return recorder.Body.String()
}

func metricLine(t *testing.T, body string, prefix string) string {
	t.Helper()

	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line)
		}
	}

	t.Fatalf("metric %q missing from export:\n%s", prefix, body)
	return ""
}

// sish_dirty_forward_total is a gauge of forwards that are dirty right now. It
// used to be derived from the cumulative dirty_forwards_stable_* counters, so it
// could only ever grow and disagreed with the same-named value rendered by the
// HTML page (B23).
func TestInternalMetricsDirtyGaugeIsLive(t *testing.T) {
	console, state := testConsoleState()

	state.Listeners.Store("dirty-1", &ListenerHolder{
		ListenAddr: "dirty-1",
		Listener:   &fakeListener{addr: "dirty-1"},
		SSHConn:    nil,
	})

	// First scrape: the issue is still transient (stability threshold is 2).
	if got := metricLine(t, scrapeInternalMetrics(t, console), "sish_dirty_forward_total{type=\"total\"}"); got != "sish_dirty_forward_total{type=\"total\"} 0" {
		t.Fatalf("first scrape: %q", got)
	}

	// Second scrape: the issue is stable and must be reported.
	body := scrapeInternalMetrics(t, console)
	if got := metricLine(t, body, "sish_dirty_forward_total{type=\"total\"}"); got != "sish_dirty_forward_total{type=\"total\"} 1" {
		t.Fatalf("second scrape: %q", got)
	}
	if got := metricLine(t, body, "sish_dirty_forward_total{type=\"listener\"}"); got != "sish_dirty_forward_total{type=\"listener\"} 1" {
		t.Fatalf("second scrape listener: %q", got)
	}
	if got := metricLine(t, body, "sish_lifecycle_counter{name=\"dirty_forwards_stable_total\"}"); got != "sish_lifecycle_counter{name=\"dirty_forwards_stable_total\"} 1" {
		t.Fatalf("stable counter: %q", got)
	}

	// The forward is cleaned up: the gauge must drop back to zero while the
	// cumulative counter keeps its value.
	state.Listeners.Delete("dirty-1")

	body = scrapeInternalMetrics(t, console)
	if got := metricLine(t, body, "sish_dirty_forward_total{type=\"total\"}"); got != "sish_dirty_forward_total{type=\"total\"} 0" {
		t.Fatalf("after cleanup gauge: %q", got)
	}
	if got := metricLine(t, body, "sish_lifecycle_counter{name=\"dirty_forwards_stable_total\"}"); got != "sish_lifecycle_counter{name=\"dirty_forwards_stable_total\"} 1" {
		t.Fatalf("after cleanup counter: %q", got)
	}
}

// A Prometheus scrape must not advance the rate window the internal status page
// maintains; both endpoints used to fight over the same previous sample and
// truncated each other's interval (B24).
func TestLifecycleRatesSnapshotDoesNotAdvanceWindow(t *testing.T) {
	console, _ := testConsoleState()

	base := map[string]uint64{"forward_create_total": 10}
	t0 := time.Unix(1000, 0)
	if _, rates, _ := console.updateInternalStatusState(t0, base, map[string]int{}); rates["forward_create_total"] != 0 {
		t.Fatalf("first sample rate=%f, want 0", rates["forward_create_total"])
	}

	next := map[string]uint64{"forward_create_total": 20}

	// Two scrapes four seconds in: both see the same 10 events over 4s.
	for i := 0; i < 2; i++ {
		rates := console.lifecycleRatesSnapshot(t0.Add(4*time.Second), next)
		if rates["forward_create_total"] != 2.5 {
			t.Fatalf("scrape %d rate=%f, want 2.5", i, rates["forward_create_total"])
		}
	}

	console.InternalState.Lock.Lock()
	prevAt := console.InternalState.PrevAt
	prevValue := console.InternalState.PrevLifecycle["forward_create_total"]
	console.InternalState.Lock.Unlock()

	if !prevAt.Equal(t0) {
		t.Fatalf("PrevAt=%v, want %v", prevAt, t0)
	}
	if prevValue != 10 {
		t.Fatalf("PrevLifecycle=%d, want 10", prevValue)
	}

	// The page still measures against its own last sample, not against a scrape.
	_, rates, _ := console.updateInternalStatusState(t0.Add(4*time.Second), next, map[string]int{})
	if rates["forward_create_total"] != 2.5 {
		t.Fatalf("page rate=%f, want 2.5", rates["forward_create_total"])
	}
}

func TestLifecycleRatesSnapshotWithoutPreviousSample(t *testing.T) {
	console, _ := testConsoleState()

	rates := console.lifecycleRatesSnapshot(time.Unix(1000, 0), map[string]uint64{"forward_create_total": 5})
	if len(rates) != 0 {
		t.Fatalf("rates=%v, want empty before the first sample", rates)
	}
}

// dirty_forwards_stable_* are cumulative counters: an issue must be counted once
// when it crosses the stability threshold, not once per poll (B25).
func TestFilterStableDirtyForwardsCountsEachIssueOnce(t *testing.T) {
	console, state := testConsoleState()

	rows := []internalForwardIssue{
		{Type: "listener", Name: "l1", Issue: "orphan"},
		{Type: "http", Name: "h1", Issue: "no backends"},
	}

	if got := console.filterStableDirtyForwards(rows); len(got) != 0 {
		t.Fatalf("first pass returned %d rows, want 0", len(got))
	}
	if got := lifecycleMetricsMap(state)["dirty_forwards_stable_total"]; got != 0 {
		t.Fatalf("stable counter after first pass=%d, want 0", got)
	}

	if got := console.filterStableDirtyForwards(rows); len(got) != 2 {
		t.Fatalf("second pass returned %d rows, want 2", len(got))
	}

	metrics := lifecycleMetricsMap(state)
	if metrics["dirty_forwards_stable_total"] != 2 {
		t.Fatalf("stable counter=%d, want 2", metrics["dirty_forwards_stable_total"])
	}
	if metrics["dirty_forwards_stable_listener_total"] != 1 || metrics["dirty_forwards_stable_http_total"] != 1 {
		t.Fatalf("per-type counters=%+v", metrics)
	}

	// The same issues persisting must not inflate the counters on later polls.
	for i := 0; i < 5; i++ {
		if got := console.filterStableDirtyForwards(rows); len(got) != 2 {
			t.Fatalf("poll %d returned %d rows, want 2", i, len(got))
		}
	}

	if got := lifecycleMetricsMap(state)["dirty_forwards_stable_total"]; got != 2 {
		t.Fatalf("stable counter after repeated polls=%d, want 2", got)
	}

	// A new issue is counted when it becomes stable, once.
	extended := append(append([]internalForwardIssue(nil), rows...), internalForwardIssue{Type: "tcp", Name: "t1", Issue: "no backends"})
	_ = console.filterStableDirtyForwards(extended)
	_ = console.filterStableDirtyForwards(extended)

	metrics = lifecycleMetricsMap(state)
	if metrics["dirty_forwards_stable_total"] != 3 {
		t.Fatalf("stable counter after new issue=%d, want 3", metrics["dirty_forwards_stable_total"])
	}
	if metrics["dirty_forwards_stable_tcp_total"] != 1 {
		t.Fatalf("tcp stable counter=%d, want 1", metrics["dirty_forwards_stable_tcp_total"])
	}
}
