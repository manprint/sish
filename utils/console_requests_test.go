package utils

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func newHistoryConsole(t *testing.T, history, wsMessages int) *WebConsole {
	t.Helper()

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("service-console-history", history)
	viper.Set("service-console-websocket-messages", wsMessages)

	console := NewWebConsole()
	console.AddRoute("route", "token")

	return console
}

func historyIDs(t *testing.T, messages [][]byte) []uint64 {
	t.Helper()

	var ids []uint64
	for _, message := range messages {
		var decoded map[string]any
		if err := json.Unmarshal(message, &decoded); err != nil {
			t.Fatalf("history holds invalid JSON %q: %s", message, err)
		}

		if id, ok := decoded["id"].(float64); ok {
			ids = append(ids, uint64(id))
		}
	}

	return ids
}

func TestBroadcastRouteAssignsIncreasingIDs(t *testing.T) {
	console := newHistoryConsole(t, 25, 200)

	first := console.BroadcastRoute("route", map[string]any{})
	second := console.BroadcastRoute("route", map[string]any{})

	if first == 0 || second <= first {
		t.Fatalf("expected increasing non-zero ids, got %d then %d", first, second)
	}

	if id := console.BroadcastRoute("missing", map[string]any{}); id != 0 {
		t.Fatalf("expected id 0 for a route without a console, got %d", id)
	}
}

func TestRequestHistoryKeepsMostRecent(t *testing.T) {
	console := newHistoryConsole(t, 3, 200)

	var ids []uint64
	for range 5 {
		ids = append(ids, console.BroadcastRoute("route", map[string]any{}))
	}

	got := historyIDs(t, console.RequestHistory("route"))
	want := ids[2:]

	if len(got) != len(want) {
		t.Fatalf("expected %d requests in history, got %d", len(want), len(got))
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected history %v, got %v", want, got)
		}
	}
}

func TestRequestHistoryDisabled(t *testing.T) {
	console := newHistoryConsole(t, 0, 200)

	id := console.BroadcastRoute("route", map[string]any{})
	console.BroadcastEvent("route", id, map[string]any{"type": "ws-message"})

	if history := console.RequestHistory("route"); len(history) != 0 {
		t.Fatalf("expected no history, got %d messages", len(history))
	}
}

func TestRequestHistoryKeepsEventsAfterTheirRequest(t *testing.T) {
	console := newHistoryConsole(t, 25, 2)

	ws := console.BroadcastRoute("route", map[string]any{"websocket": true})
	other := console.BroadcastRoute("route", map[string]any{})

	for i := range 3 {
		console.BroadcastEvent("route", ws, map[string]any{"type": "ws-message", "seq": i + 1})
	}

	history := console.RequestHistory("route")
	if len(history) != 4 {
		t.Fatalf("expected 2 requests and 2 events, got %d messages", len(history))
	}

	var event map[string]any
	if err := json.Unmarshal(history[1], &event); err != nil {
		t.Fatal(err)
	}

	if uint64(event["requestId"].(float64)) != ws || event["seq"].(float64) != 2 {
		t.Fatalf("expected the oldest kept event of request %d to be seq 2, got %v", ws, event)
	}

	if ids := historyIDs(t, history); len(ids) != 2 || ids[1] != other {
		t.Fatalf("expected request %d after the events, got %v", other, ids)
	}
}

func TestRequestHistoryIsBoundedInBytes(t *testing.T) {
	console := newHistoryConsole(t, 1000, 200)

	body := strings.Repeat("x", 1<<20)
	for range 20 {
		console.BroadcastRoute("route", map[string]any{"responseBody": body})
	}

	total := 0
	for _, message := range console.RequestHistory("route") {
		total += len(message)
	}

	if total > consoleHistoryMaxBytes {
		t.Fatalf("expected history to stay under %d bytes, got %d", consoleHistoryMaxBytes, total)
	}

	if len(console.RequestHistory("route")) == 0 {
		t.Fatal("expected the most recent requests to be kept")
	}
}

func TestRemoveRouteForgetsRequestHistory(t *testing.T) {
	console := newHistoryConsole(t, 25, 200)

	console.BroadcastRoute("route", map[string]any{})
	console.RemoveRoute("route")

	if history := console.RequestHistory("route"); len(history) != 0 {
		t.Fatalf("expected history to be forgotten with the route, got %d messages", len(history))
	}

	// A request that finishes after the route closed must not bring it back.
	console.addRequestHistory("route", 1, []byte(`{"id":1}`))

	if history := console.RequestHistory("route"); len(history) != 0 {
		t.Fatalf("expected no history for a removed route, got %d messages", len(history))
	}
}

func TestRequestHistoryOnZeroValueConsole(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("service-console-history", 25)

	console := &WebConsole{}

	if history := console.RequestHistory("route"); len(history) != 0 {
		t.Fatalf("expected empty history, got %d messages", len(history))
	}
}
