package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

type trackingListener struct {
	addr       string
	closeCount int
}

func (l *trackingListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *trackingListener) Close() error {
	l.closeCount++
	return nil
}
func (l *trackingListener) Addr() net.Addr { return fakeAddr(l.addr) }

func TestBroadcastRouteDoesNotBlockOnSlowClient(t *testing.T) {
	console := NewWebConsole()
	console.AddRoute("route", "token")

	slow := &WebClient{
		Console: console,
		Send:    make(chan []byte, 1),
		Done:    make(chan struct{}),
		Route:   "route",
	}
	slow.Send <- []byte("busy")
	console.AddClient("route", slow)

	done := make(chan struct{})
	go func() {
		console.BroadcastRoute("route", map[string]any{"next": true})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("BroadcastRoute blocked on a slow client")
	}

	// A page that falls behind keeps its connection and is told how many
	// messages it missed (upstream 86d6b30); before, it was closed and
	// dropped. A page that stops reading altogether is still closed by the
	// write deadline in Handle.
	if slow.closed.Load() {
		t.Fatal("expected slow websocket client to stay open")
	}
	if got := slow.dropped.Load(); got != 1 {
		t.Fatalf("expected 1 dropped message, got %d", got)
	}
	if notice := string(slow.droppedNotice()); notice != `{"type":"dropped","count":1}` {
		t.Fatalf("unexpected dropped notice %q", notice)
	}
	if clients, ok := console.Clients.Load("route"); !ok || len(clients) != 1 {
		t.Fatalf("expected slow client to stay registered, got %d clients", len(clients))
	}
}

func TestResolveManagedFileRejectsSymlinkEscape(t *testing.T) {
	viper.Reset()
	baseDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.yml")
	if err := os.WriteFile(outsideFile, []byte("users:\n  - name: outside\n    password: \"secret\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	linkDir := filepath.Join(baseDir, "escape")
	if err := os.Symlink(outsideDir, linkDir); err != nil {
		t.Fatal(err)
	}

	viper.Set("auth-users-directory", baseDir)
	if _, _, err := resolveManagedFile("escape/secret.yml", "auth-users-directory"); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestHandleDisconnectRouteAcceptsJSONArrayPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	console, state := testConsoleState()

	listenerA := &trackingListener{addr: "listener-a"}
	listenerB := &trackingListener{addr: "listener-b"}
	state.Listeners.Store("listener-a", &ListenerHolder{Listener: listenerA, ListenAddr: "listener-a"})
	state.Listeners.Store("listener-b", &ListenerHolder{Listener: listenerB, ListenAddr: "listener-b"})

	body, err := json.Marshal(map[string]any{
		"routeData": []string{"listener-a", "listener-b"},
	})
	if err != nil {
		t.Fatal(err)
	}

	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/_sish/api/disconnectroute", bytes.NewReader(body))
	console.HandleDisconnectRoute("", ctx)

	if writer.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", writer.Code, http.StatusOK)
	}
	if listenerA.closeCount != 1 || listenerB.closeCount != 1 {
		t.Fatalf("expected both listeners to be closed once, got %d and %d", listenerA.closeCount, listenerB.closeCount)
	}

	response := map[string]any{}
	if err := json.Unmarshal(writer.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if got := int(response["closed"].(float64)); got != 2 {
		t.Fatalf("closed=%d, want 2", got)
	}
}

func TestHandleEditUsersFileWriteReloadsUsersImmediately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viper.Reset()
	resetAuthUsersStateForTest()

	dir := t.TempDir()
	usersFile := filepath.Join(dir, "users.yml")
	if err := os.WriteFile(usersFile, []byte("users:\n  - name: alpha\n    password: \"old-pass\"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	viper.Set("auth-users-enabled", true)
	viper.Set("auth-users-directory", dir)
	loadAuthUsers()

	if !checkAuthUserPassword("alpha", []byte("old-pass")) {
		t.Fatal("expected initial password to authenticate")
	}

	payload, err := json.Marshal(map[string]string{
		"file":    "users.yml",
		"content": "users:\n  - name: alpha\n    password: \"new-pass\"\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/_sish/api/editusers/write", bytes.NewReader(payload))

	console := NewWebConsole()
	console.HandleEditUsersFileWrite(ctx)

	if writer.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", writer.Code, http.StatusOK)
	}
	if !checkAuthUserPassword("alpha", []byte("new-pass")) {
		t.Fatal("expected updated password to be loaded immediately after save")
	}
	if checkAuthUserPassword("alpha", []byte("old-pass")) {
		t.Fatal("expected old password to stop working after save")
	}
}

func TestHandleHistoryClampsOutOfRangePage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viper.Reset()
	viper.Set("history-enabled", true)
	viper.Set("time-format", time.RFC3339)

	console := NewWebConsole()
	for i := 0; i < 11; i++ {
		started := time.Unix(int64(i+1), 0).UTC()
		ended := started.Add(time.Second)
		console.AddHistoryEntry(ConnectionHistory{
			ID:        fmt.Sprintf("id-%02d", i),
			StartedAt: started,
			EndedAt:   ended,
			Duration:  ended.Sub(started),
		})
	}

	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/_sish/api/history?page=99", nil)
	console.HandleHistory(ctx)

	if writer.Code != http.StatusOK {
		t.Fatalf("unexpected status code: got %d want %d", writer.Code, http.StatusOK)
	}

	response := struct {
		Page       int              `json:"page"`
		TotalPages int              `json:"totalPages"`
		History    []map[string]any `json:"history"`
	}{}
	if err := json.Unmarshal(writer.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}

	if response.TotalPages != 2 {
		t.Fatalf("totalPages=%d, want 2", response.TotalPages)
	}
	if response.Page != 2 {
		t.Fatalf("page=%d, want 2", response.Page)
	}
	if len(response.History) != 1 {
		t.Fatalf("expected last page to contain 1 row, got %d", len(response.History))
	}
}

func TestAddHistoryEntryIsBounded(t *testing.T) {
	oldMaxHistoryEntries := maxHistoryEntries
	maxHistoryEntries = 2
	defer func() {
		maxHistoryEntries = oldMaxHistoryEntries
	}()

	console := NewWebConsole()
	console.AddHistoryEntry(ConnectionHistory{ID: "one"})
	console.AddHistoryEntry(ConnectionHistory{ID: "two"})
	console.AddHistoryEntry(ConnectionHistory{ID: "three"})

	history := console.historySnapshot()
	if len(history) != 2 {
		t.Fatalf("history len=%d, want 2", len(history))
	}
	if history[0].ID != "two" || history[1].ID != "three" {
		t.Fatalf("unexpected bounded history contents: %+v", history)
	}
}
