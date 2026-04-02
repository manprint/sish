package utils

import "testing"

func TestLifecycleMetricsCountersAndDirtyRecording(t *testing.T) {
	state := NewState()

	state.IncrementForwardCreate()
	state.IncrementForwardCreate()
	state.IncrementForwardCleanup()
	state.IncrementForwardCleanupError()
	state.IncrementForceConnectTakeovers(3)

	state.RecordStableDirtyForwardTypes([]internalForwardIssue{
		{Type: "listener"},
		{Type: "http"},
		{Type: "tcp"},
		{Type: "alias"},
		{Type: "unknown"},
	})

	if got := state.Lifecycle.ForwardCreateTotal.Load(); got != 2 {
		t.Fatalf("ForwardCreateTotal = %d, want 2", got)
	}
	if got := state.Lifecycle.ForwardCleanupTotal.Load(); got != 1 {
		t.Fatalf("ForwardCleanupTotal = %d, want 1", got)
	}
	if got := state.Lifecycle.ForwardCleanupErrorsTotal.Load(); got != 1 {
		t.Fatalf("ForwardCleanupErrorsTotal = %d, want 1", got)
	}
	if got := state.Lifecycle.ForceConnectTakeoversTotal.Load(); got != 3 {
		t.Fatalf("ForceConnectTakeoversTotal = %d, want 3", got)
	}
	if got := state.Lifecycle.DirtyForwardsStableTotal.Load(); got != 5 {
		t.Fatalf("DirtyForwardsStableTotal = %d, want 5", got)
	}
	if got := state.Lifecycle.DirtyForwardsListenerTotal.Load(); got != 1 {
		t.Fatalf("DirtyForwardsListenerTotal = %d, want 1", got)
	}
	if got := state.Lifecycle.DirtyForwardsHTTPTotal.Load(); got != 1 {
		t.Fatalf("DirtyForwardsHTTPTotal = %d, want 1", got)
	}
	if got := state.Lifecycle.DirtyForwardsTCPTotal.Load(); got != 1 {
		t.Fatalf("DirtyForwardsTCPTotal = %d, want 1", got)
	}
	if got := state.Lifecycle.DirtyForwardsAliasTotal.Load(); got != 1 {
		t.Fatalf("DirtyForwardsAliasTotal = %d, want 1", got)
	}
}

func TestLifecycleMetricsRows(t *testing.T) {
	state := NewState()
	state.IncrementForwardCreate()
	state.IncrementForwardCleanup()
	state.IncrementForwardCleanupErrorCause("listener_close")
	state.IncrementForwardCleanupErrorCause("socket_remove")
	state.IncrementForwardCleanupError()
	state.IncrementForceConnectTakeovers(2)
	state.RecordStableDirtyForwardTypes([]internalForwardIssue{{Type: "listener"}, {Type: "tcp"}})

	rows := lifecycleMetricsKVRows(state)
	if len(rows) != 25 {
		t.Fatalf("rows len = %d, want 25", len(rows))
	}

	values := map[string]string{}
	for _, row := range rows {
		values[row.Key] = row.Value
	}

	if values["forward_create_total"] != "1" {
		t.Fatalf("forward_create_total = %s", values["forward_create_total"])
	}
	if values["forward_cleanup_total"] != "1" {
		t.Fatalf("forward_cleanup_total = %s", values["forward_cleanup_total"])
	}
	if values["forward_cleanup_errors_total"] != "3" {
		t.Fatalf("forward_cleanup_errors_total = %s", values["forward_cleanup_errors_total"])
	}
	if values["forward_cleanup_listener_close_errors_total"] != "1" {
		t.Fatalf("forward_cleanup_listener_close_errors_total = %s", values["forward_cleanup_listener_close_errors_total"])
	}
	if values["forward_cleanup_socket_remove_errors_total"] != "1" {
		t.Fatalf("forward_cleanup_socket_remove_errors_total = %s", values["forward_cleanup_socket_remove_errors_total"])
	}
	if values["forward_cleanup_unknown_errors_total"] != "1" {
		t.Fatalf("forward_cleanup_unknown_errors_total = %s", values["forward_cleanup_unknown_errors_total"])
	}
	if values["force_connect_takeovers_total"] != "2" {
		t.Fatalf("force_connect_takeovers_total = %s", values["force_connect_takeovers_total"])
	}
	if values["dirty_forwards_stable_total"] != "2" {
		t.Fatalf("dirty_forwards_stable_total = %s", values["dirty_forwards_stable_total"])
	}
	if values["debug_bind_conflict_total"] != "0" {
		t.Fatalf("debug_bind_conflict_total = %s", values["debug_bind_conflict_total"])
	}
	if values["debug_stale_holder_purged_total"] != "0" {
		t.Fatalf("debug_stale_holder_purged_total = %s", values["debug_stale_holder_purged_total"])
	}
	if values["visitor_alias_connections_total"] != "0" {
		t.Fatalf("visitor_alias_connections_total = %s", values["visitor_alias_connections_total"])
	}
}
