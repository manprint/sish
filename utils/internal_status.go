package utils

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"github.com/vulcand/oxy/roundrobin"
)

type internalKVRow struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type internalForwardIssue struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Issue string `json:"issue"`
}

type internalHealthAlert struct {
	Level   string `json:"level"`
	Name    string `json:"name"`
	Message string `json:"message"`
	Value   string `json:"value"`
}

type internalHealthSnapshot struct {
	Status string                `json:"status"`
	Alerts []internalHealthAlert `json:"alerts"`
}

type internalMetricsSample struct {
	GeneratedAt      string            `json:"generatedAt"`
	LifecycleMetrics map[string]uint64 `json:"lifecycleMetrics"`
	DirtyMetrics     map[string]int    `json:"dirtyMetrics"`
}

type dirtySnapshot struct {
	Listeners    []snapshotListener
	HTTP         []snapshotHTTP
	TCP          []snapshotTCP
	Alias        []snapshotAlias
	ActiveSSHSet map[string]struct{}
}

type snapshotListener struct {
	Name   string
	Holder *ListenerHolder
}

type snapshotHTTP struct {
	Name   string
	Holder *HTTPHolder
}

type snapshotTCP struct {
	Name   string
	Holder *TCPHolder
}

type snapshotAlias struct {
	Name   string
	Holder *AliasHolder
}

type pingStatusRow struct {
	ID         string   `json:"id"`
	RemoteAddr string   `json:"remoteAddr"`
	Forwards   []string `json:"forwards"`
	PingSent   uint64   `json:"pingSent"`
	PingFail   uint64   `json:"pingFail"`
	LastPing   string   `json:"lastPing"`
	LastPingOk string   `json:"lastPingOk"`
	Status     string   `json:"status"`
	DeadlineMs int64    `json:"deadlineMs"`
	ClosedAt   string   `json:"closedAt"`
}

const dirtyForwardStableThreshold = 2

// AppVersion is the application version shown in internal status.
var AppVersion = "dev"

// HandleInternalTemplate renders the internal status page.
func (c *WebConsole) HandleInternalTemplate(g *gin.Context) {
	g.HTML(http.StatusOK, "internal", c.templateData(true, true))
}

// HandleInternal returns runtime/internal state details for admin troubleshooting.
func (c *WebConsole) HandleInternal(g *gin.Context) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	now := time.Now()
	activeForwards := c.getActiveForwardRows()
	dirtyForwards := c.getDirtyForwardRows()
	dirtySummary := summarizeDirtyForwards(dirtyForwards)
	lifecycleMap := lifecycleMetricsMap(c.State)
	lifecycleRates, lifecycleRateMap, lifecycleHistory := c.updateInternalStatusState(now, lifecycleMap, dirtySummary)
	health := buildInternalHealth(lifecycleMap, dirtySummary, lifecycleRateMap)
	stateCounts, stateDetails := c.buildInternalState(dirtyForwards)

	pingEnabled := viper.GetBool("ping-client")
	pingStatus := []pingStatusRow{}
	if pingEnabled {
		pingStatus = c.getPingStatusRows()
	}

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
		"meta": map[string]any{
			"generatedAt":  now.Format(viper.GetString("time-format")),
			"appVersion":   AppVersion,
			"goVersion":    runtime.Version(),
			"gomaxprocs":   runtime.GOMAXPROCS(0),
			"numCPU":       runtime.NumCPU(),
			"numGoroutine": runtime.NumGoroutine(),
		},
		"startupFlags":   append([]string(nil), os.Args[1:]...),
		"effectiveFlags": collectEffectiveFlags(),
		"memory": map[string]any{
			"allocBytes":      mem.Alloc,
			"totalAllocBytes": mem.TotalAlloc,
			"sysBytes":        mem.Sys,
			"heapAllocBytes":  mem.HeapAlloc,
			"heapSysBytes":    mem.HeapSys,
			"stackInuseBytes": mem.StackInuse,
			"stackSysBytes":   mem.StackSys,
		},
		"stateCounts":      stateCounts,
		"stateDetails":     stateDetails,
		"runtimeCounters":  map[string]any{"heapObjects": mem.HeapObjects, "numGC": mem.NumGC, "lastGCTimeUnixNs": mem.LastGC},
		"activeForwards":   activeForwards,
		"dirtyForwards":    dirtyForwards,
		"dirtyMetrics":     dirtyMetricsKVRows(dirtySummary),
		"lifecycleMetrics": lifecycleMetricsKVRows(c.State),
		"debugMetrics":     debugMetricsKVRows(c.State),
		"lifecycleRates":   lifecycleRates,
		"lifecycleHistory": lifecycleHistory,
		"health":           health,
		"pingEnabled":      pingEnabled,
		"pingStatus":       pingStatus,
	})
}

// HandleInternalPingStatus returns only ping status data (lightweight endpoint for auto-refresh).
func (c *WebConsole) HandleInternalPingStatus(g *gin.Context) {
	pingEnabled := viper.GetBool("ping-client")
	pingStatus := []pingStatusRow{}
	if pingEnabled {
		pingStatus = c.getPingStatusRows()
	}

	g.JSON(http.StatusOK, map[string]any{
		"pingEnabled": pingEnabled,
		"pingStatus":  pingStatus,
	})
}

func (c *WebConsole) HandleInternalMetrics(g *gin.Context) {
	now := time.Now()
	lifecycleMap := lifecycleMetricsMap(c.State)
	dirtySummary := dirtySummaryFromLifecycleMetrics(lifecycleMap)
	_, lifecycleRateMap, _ := c.updateInternalStatusState(now, lifecycleMap, dirtySummary)
	health := buildInternalHealth(lifecycleMap, dirtySummary, lifecycleRateMap)

	g.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	g.String(http.StatusOK, internalMetricsPrometheusText(lifecycleMap, dirtySummary, lifecycleRateMap, health))
}

func (c *WebConsole) getPingStatusRows() []pingStatusRow {
	rows := []pingStatusRow{}
	timeFmt := viper.GetString("time-format")
	pingEnabled := viper.GetBool("ping-client")

	c.State.SSHConnections.Range(func(_ string, sshConn *SSHConnection) bool {
		if sshConn == nil {
			return true
		}

		var listeners []string
		listenerCount := 0
		sshConn.Listeners.Range(func(name string, _ net.Listener) bool {
			if strings.TrimSpace(name) != "" {
				listenerCount++
				listeners = append(listeners, name)
			}
			return true
		})

		if listenerCount == 0 {
			return true
		}

		id := strings.TrimSpace(sshConn.ConnectionID)
		if id == "" {
			return true
		}

		resolvedForwarder := strings.TrimSpace(resolveConnectionForwarders(sshConn, c.State))
		displayID, _ := displayConnectionIdentityAndForwarder(sshConn, sshConn.ConnectionID, listeners, resolvedForwarder)

		forwards := resolveForwardNames(sshConn, c.State)

		remoteAddr := ""
		if sshConn.SSHConn != nil && sshConn.SSHConn.RemoteAddr() != nil {
			remoteAddr = sshConn.SSHConn.RemoteAddr().String()
		}

		pingSent := sshConn.PingSentTotal.Load()
		pingFail := sshConn.PingFailTotal.Load()
		lastPingOkNs := sshConn.LastPingOkAtNs.Load()
		lastPingFailNs := sshConn.LastPingFailAtNs.Load()

		lastPing := ""
		if ns := sshConn.LastPingAtNs.Load(); ns > 0 {
			lastPing = time.Unix(0, ns).Format(timeFmt)
		}

		lastPingOk := ""
		if lastPingOkNs > 0 {
			lastPingOk = time.Unix(0, lastPingOkNs).Format(timeFmt)
		}

		status := "disabled"
		if pingEnabled {
			if pingSent == 0 {
				status = "pending"
			} else if lastPingOkNs > 0 && lastPingFailNs > lastPingOkNs {
				status = "unresponsive"
			} else if lastPingOkNs == 0 && pingFail > 0 {
				status = "failing"
			} else {
				status = "ok"
			}
		}

		deadlineMs := int64(0)
		if ns := sshConn.PingDeadlineNs.Load(); ns > 0 {
			deadlineMs = ns / int64(time.Millisecond)
		}

		rows = append(rows, pingStatusRow{
			ID:         displayID,
			RemoteAddr: remoteAddr,
			Forwards:   forwards,
			PingSent:   pingSent,
			PingFail:   pingFail,
			LastPing:   lastPing,
			LastPingOk: lastPingOk,
			Status:     status,
			DeadlineMs: deadlineMs,
		})

		return true
	})

	// Merge closed connection rows.
	c.ClosedPingLock.RLock()
	closedCopy := make([]pingStatusRow, len(c.ClosedPingRows))
	copy(closedCopy, c.ClosedPingRows)
	c.ClosedPingLock.RUnlock()
	rows = append(rows, closedCopy...)

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].ID < rows[j].ID
	})

	return rows
}

const maxClosedPingRows = 100

// AddClosedPingRow captures the final ping state of a connection at cleanup time.
func (c *WebConsole) AddClosedPingRow(sshConn *SSHConnection, state *State) {
	if c == nil || sshConn == nil || !viper.GetBool("ping-client") {
		return
	}

	timeFmt := viper.GetString("time-format")
	if sshConn.PingSentTotal.Load() == 0 {
		return
	}

	id := strings.TrimSpace(sshConn.ConnectionID)
	if id == "" {
		return
	}

	var listeners []string
	sshConn.Listeners.Range(func(name string, _ net.Listener) bool {
		if strings.TrimSpace(name) != "" {
			listeners = append(listeners, name)
		}
		return true
	})

	resolvedForwarder := strings.TrimSpace(resolveConnectionForwarders(sshConn, state))
	displayID, _ := displayConnectionIdentityAndForwarder(sshConn, sshConn.ConnectionID, listeners, resolvedForwarder)
	forwards := resolveForwardNames(sshConn, state)

	remoteAddr := ""
	if sshConn.SSHConn != nil && sshConn.SSHConn.RemoteAddr() != nil {
		remoteAddr = sshConn.SSHConn.RemoteAddr().String()
	}

	pingSent := sshConn.PingSentTotal.Load()
	pingFail := sshConn.PingFailTotal.Load()

	lastPing := ""
	if ns := sshConn.LastPingAtNs.Load(); ns > 0 {
		lastPing = time.Unix(0, ns).Format(timeFmt)
	}

	lastPingOk := ""
	if ns := sshConn.LastPingOkAtNs.Load(); ns > 0 {
		lastPingOk = time.Unix(0, ns).Format(timeFmt)
	}

	status := "closed"
	closedAt := time.Now().Format(timeFmt)

	row := pingStatusRow{
		ID:         displayID,
		RemoteAddr: remoteAddr,
		Forwards:   forwards,
		PingSent:   pingSent,
		PingFail:   pingFail,
		LastPing:   lastPing,
		LastPingOk: lastPingOk,
		Status:     status,
		DeadlineMs: 0,
		ClosedAt:   closedAt,
	}

	c.ClosedPingLock.Lock()
	c.ClosedPingRows = append(c.ClosedPingRows, row)
	if len(c.ClosedPingRows) > maxClosedPingRows {
		c.ClosedPingRows = c.ClosedPingRows[len(c.ClosedPingRows)-maxClosedPingRows:]
	}
	c.ClosedPingLock.Unlock()
}

func resolveForwardNames(sshConn *SSHConnection, state *State) []string {
	if sshConn == nil || state == nil {
		return nil
	}

	var listeners []string
	sshConn.Listeners.Range(func(name string, _ net.Listener) bool {
		if strings.TrimSpace(name) != "" {
			listeners = append(listeners, name)
		}
		return true
	})

	forwards := []string{}

	state.HTTPListeners.Range(func(_ string, httpHolder *HTTPHolder) bool {
		if httpHolder == nil {
			return true
		}
		httpHolder.SSHConnections.Range(func(httpAddr string, _ *SSHConnection) bool {
			for _, v := range listeners {
				if v == httpAddr {
					forward := httpHolder.HTTPUrl.Hostname() + httpHolder.HTTPUrl.Path
					forwards = append(forwards, forward)
				}
			}
			return true
		})
		return true
	})

	state.TCPListeners.Range(func(tcpAddr string, tcpHolder *TCPHolder) bool {
		if tcpHolder == nil {
			return true
		}
		tcpHolder.Balancers.Range(func(_ string, balancer *roundrobin.RoundRobin) bool {
			for _, server := range balancer.Servers() {
				serverAddr, err := base64.StdEncoding.DecodeString(server.Host)
				if err != nil {
					return true
				}
				for _, v := range listeners {
					if v == string(serverAddr) {
						forwards = append(forwards, tcpAddr)
					}
				}
			}
			return true
		})
		return true
	})

	state.AliasListeners.Range(func(aliasName string, aliasHolder *AliasHolder) bool {
		if aliasHolder == nil {
			return true
		}
		for _, server := range aliasHolder.Balancer.Servers() {
			serverAddr, err := base64.StdEncoding.DecodeString(server.Host)
			if err != nil {
				continue
			}
			for _, v := range listeners {
				if v == string(serverAddr) {
					forwards = append(forwards, aliasName)
				}
			}
		}
		return true
	})

	sort.Strings(forwards)
	return forwards
}

func collectEffectiveFlags() []internalKVRow {
	settings := viper.AllSettings()
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	rows := make([]internalKVRow, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, internalKVRow{
			Key:   key,
			Value: formatInternalValue(settings[key]),
		})
	}

	return rows
}

func formatInternalValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		buf, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", value)
		}

		return string(buf)
	}
}

func countSSHConnections(c *WebConsole) int {
	count := 0
	c.State.SSHConnections.Range(func(_ string, _ *SSHConnection) bool {
		count++
		return true
	})
	return count
}

func countListeners(c *WebConsole) int {
	count := 0
	c.State.Listeners.Range(func(_ string, _ net.Listener) bool {
		count++
		return true
	})
	return count
}

func countHTTPListeners(c *WebConsole) int {
	count := 0
	c.State.HTTPListeners.Range(func(_ string, _ *HTTPHolder) bool {
		count++
		return true
	})
	return count
}

func countTCPListeners(c *WebConsole) int {
	count := 0
	c.State.TCPListeners.Range(func(_ string, _ *TCPHolder) bool {
		count++
		return true
	})
	return count
}

func countAliasListeners(c *WebConsole) int {
	count := 0
	c.State.AliasListeners.Range(func(_ string, _ *AliasHolder) bool {
		count++
		return true
	})
	return count
}

func (c *WebConsole) buildInternalState(dirtyForwards []internalForwardIssue) (map[string]any, map[string]any) {
	totalListeners := 0
	forwardListeners := 0
	tcpGatewayListeners := 0
	systemListeners := 0

	forwardListenerNames := []string{}
	tcpGatewayListenerNames := []string{}
	systemListenerNames := []string{}

	c.State.Listeners.Range(func(name string, l net.Listener) bool {
		totalListeners++

		if _, ok := l.(*ListenerHolder); ok {
			forwardListeners++
			forwardListenerNames = append(forwardListenerNames, name)
			return true
		}

		if _, ok := c.State.TCPListeners.Load(name); ok {
			tcpGatewayListeners++
			tcpGatewayListenerNames = append(tcpGatewayListenerNames, name)
			return true
		}

		systemListeners++
		systemListenerNames = append(systemListenerNames, name)
		return true
	})

	dirtyListenerRows := 0
	for _, row := range dirtyForwards {
		if row.Type == "listener" {
			dirtyListenerRows++
		}
	}

	sort.Strings(forwardListenerNames)
	sort.Strings(tcpGatewayListenerNames)
	sort.Strings(systemListenerNames)

	return map[string]any{
			"sshConnections":      countSSHConnections(c),
			"listenersTotal":      totalListeners,
			"listenersForward":    forwardListeners,
			"listenersTCPGateway": tcpGatewayListeners,
			"listenersSystem":     systemListeners,
			"httpListeners":       countHTTPListeners(c),
			"tcpListeners":        countTCPListeners(c),
			"aliasListeners":      countAliasListeners(c),
			"dirtyListenerRows":   dirtyListenerRows,
		}, map[string]any{
			"forwardListenerNames":    forwardListenerNames,
			"tcpGatewayListenerNames": tcpGatewayListenerNames,
			"systemListenerNames":     systemListenerNames,
		}
}

func (c *WebConsole) getDirtyForwardRows() []internalForwardIssue {
	snapshot := c.buildDirtySnapshot()
	rows := []internalForwardIssue{}

	for _, listenerRow := range snapshot.Listeners {
		listenerName := listenerRow.Name
		holder := listenerRow.Holder

		if holder.SSHConn == nil {
			rows = append(rows, internalForwardIssue{
				Type:  "listener",
				Name:  listenerName,
				Issue: "listener holder has no owning ssh connection",
			})
			continue
		}

		if holder.SSHConn.SSHConn == nil || holder.SSHConn.SSHConn.RemoteAddr() == nil {
			rows = append(rows, internalForwardIssue{
				Type:  "listener",
				Name:  listenerName,
				Issue: "owning ssh connection has no remote address",
			})
			continue
		}

		remoteAddr := strings.TrimSpace(holder.SSHConn.SSHConn.RemoteAddr().String())
		if _, exists := snapshot.ActiveSSHSet[remoteAddr]; !exists {
			rows = append(rows, internalForwardIssue{
				Type:  "listener",
				Name:  listenerName,
				Issue: "owning ssh connection is missing from active connection map",
			})
			continue
		}

		if _, exists := holder.SSHConn.Listeners.Load(listenerName); !exists {
			rows = append(rows, internalForwardIssue{
				Type:  "listener",
				Name:  listenerName,
				Issue: "listener is not linked in owning ssh connection listener map",
			})
		}
	}

	for _, row := range snapshot.HTTP {
		name := row.Name
		httpHolder := row.Holder
		backends := 0
		httpHolder.SSHConnections.Range(func(_ string, _ *SSHConnection) bool {
			backends++
			return true
		})

		if backends == 0 || len(httpHolder.Balancer.Servers()) == 0 {
			rows = append(rows, internalForwardIssue{
				Type:  "http",
				Name:  name,
				Issue: "http forward has no active backends",
			})
		}
	}

	for _, row := range snapshot.TCP {
		name := row.Name
		tcpHolder := row.Holder
		totalServers := 0
		tcpHolder.Balancers.Range(func(_ string, balancer *roundrobin.RoundRobin) bool {
			totalServers += len(balancer.Servers())
			return true
		})

		if totalServers == 0 {
			rows = append(rows, internalForwardIssue{
				Type:  "tcp",
				Name:  name,
				Issue: "tcp forward has no active backends",
			})
		}
	}

	for _, row := range snapshot.Alias {
		name := row.Name
		aliasHolder := row.Holder
		if len(aliasHolder.Balancer.Servers()) == 0 {
			rows = append(rows, internalForwardIssue{
				Type:  "alias",
				Name:  name,
				Issue: "alias forward has no active backends",
			})
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Type == rows[j].Type {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Type < rows[j].Type
	})

	return c.filterStableDirtyForwards(rows)
}

func (c *WebConsole) buildDirtySnapshot() dirtySnapshot {
	snapshot := dirtySnapshot{
		Listeners:    []snapshotListener{},
		HTTP:         []snapshotHTTP{},
		TCP:          []snapshotTCP{},
		Alias:        []snapshotAlias{},
		ActiveSSHSet: map[string]struct{}{},
	}

	c.State.SSHConnections.Range(func(remoteAddr string, _ *SSHConnection) bool {
		snapshot.ActiveSSHSet[remoteAddr] = struct{}{}
		return true
	})

	c.State.Listeners.Range(func(listenerName string, rawListener net.Listener) bool {
		holder, ok := rawListener.(*ListenerHolder)
		if ok {
			snapshot.Listeners = append(snapshot.Listeners, snapshotListener{
				Name:   listenerName,
				Holder: holder,
			})
		}
		return true
	})

	c.State.HTTPListeners.Range(func(name string, holder *HTTPHolder) bool {
		if holder != nil {
			snapshot.HTTP = append(snapshot.HTTP, snapshotHTTP{Name: name, Holder: holder})
		}
		return true
	})

	c.State.TCPListeners.Range(func(name string, holder *TCPHolder) bool {
		if holder != nil {
			snapshot.TCP = append(snapshot.TCP, snapshotTCP{Name: name, Holder: holder})
		}
		return true
	})

	c.State.AliasListeners.Range(func(name string, holder *AliasHolder) bool {
		if holder != nil {
			snapshot.Alias = append(snapshot.Alias, snapshotAlias{Name: name, Holder: holder})
		}
		return true
	})

	return snapshot
}

func dirtyIssueKey(issue internalForwardIssue) string {
	return issue.Type + "|" + issue.Name + "|" + issue.Issue
}

func (c *WebConsole) filterStableDirtyForwards(rows []internalForwardIssue) []internalForwardIssue {
	if c == nil || c.DirtyState == nil || c.DirtyState.Lock == nil {
		return rows
	}

	c.DirtyState.Lock.Lock()
	defer c.DirtyState.Lock.Unlock()

	nextSeen := map[string]int{}
	filtered := make([]internalForwardIssue, 0, len(rows))

	for _, row := range rows {
		key := dirtyIssueKey(row)
		count := c.DirtyState.SeenCount[key] + 1
		nextSeen[key] = count

		if count >= dirtyForwardStableThreshold {
			filtered = append(filtered, row)
		}
	}

	c.DirtyState.SeenCount = nextSeen
	if c.State != nil {
		c.State.RecordStableDirtyForwardTypes(filtered)
	}
	return filtered
}

func summarizeDirtyForwards(rows []internalForwardIssue) map[string]int {
	summary := map[string]int{
		"listener": 0,
		"http":     0,
		"tcp":      0,
		"alias":    0,
	}

	for _, row := range rows {
		summary[row.Type]++
	}

	summary["total"] = len(rows)
	return summary
}

func dirtyMetricsKVRows(summary map[string]int) []internalKVRow {
	keys := []string{"total", "listener", "http", "tcp", "alias"}
	rows := make([]internalKVRow, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, internalKVRow{
			Key:   key,
			Value: strconv.Itoa(summary[key]),
		})
	}
	return rows
}

var lifecycleMetricOrder = []string{
	"forward_create_total",
	"forward_cleanup_total",
	"forward_cleanup_errors_total",
	"forward_cleanup_listener_close_errors_total",
	"forward_cleanup_socket_remove_errors_total",
	"forward_cleanup_unknown_errors_total",
	"dirty_forwards_stable_total",
	"dirty_forwards_stable_listener_total",
	"dirty_forwards_stable_http_total",
	"dirty_forwards_stable_tcp_total",
	"dirty_forwards_stable_alias_total",
	"force_connect_takeovers_total",
	"debug_bind_conflict_total",
	"debug_bind_conflict_http_total",
	"debug_bind_conflict_alias_total",
	"debug_bind_conflict_sni_total",
	"debug_bind_conflict_tcp_total",
	"debug_stale_holder_purged_total",
	"debug_stale_holder_purged_http_total",
	"debug_stale_holder_purged_alias_total",
	"debug_stale_holder_purged_sni_total",
	"debug_stale_holder_purged_tcp_total",
	"debug_force_disconnect_noop_total",
	"debug_target_release_timeout_total",
	"visitor_alias_connections_total",
}

func lifecycleMetricsMap(state *State) map[string]uint64 {
	if state == nil || state.Lifecycle == nil {
		return map[string]uint64{}
	}

	return map[string]uint64{
		"forward_create_total":                        state.Lifecycle.ForwardCreateTotal.Load(),
		"forward_cleanup_total":                       state.Lifecycle.ForwardCleanupTotal.Load(),
		"forward_cleanup_errors_total":                state.Lifecycle.ForwardCleanupErrorsTotal.Load(),
		"forward_cleanup_listener_close_errors_total": state.Lifecycle.ForwardCleanupListenerCloseErrorsTotal.Load(),
		"forward_cleanup_socket_remove_errors_total":  state.Lifecycle.ForwardCleanupSocketRemoveErrorsTotal.Load(),
		"forward_cleanup_unknown_errors_total":        state.Lifecycle.ForwardCleanupUnknownErrorsTotal.Load(),
		"dirty_forwards_stable_total":                 state.Lifecycle.DirtyForwardsStableTotal.Load(),
		"dirty_forwards_stable_listener_total":        state.Lifecycle.DirtyForwardsListenerTotal.Load(),
		"dirty_forwards_stable_http_total":            state.Lifecycle.DirtyForwardsHTTPTotal.Load(),
		"dirty_forwards_stable_tcp_total":             state.Lifecycle.DirtyForwardsTCPTotal.Load(),
		"dirty_forwards_stable_alias_total":           state.Lifecycle.DirtyForwardsAliasTotal.Load(),
		"force_connect_takeovers_total":               state.Lifecycle.ForceConnectTakeoversTotal.Load(),
		"debug_bind_conflict_total":                   state.Lifecycle.DebugBindConflictTotal.Load(),
		"debug_bind_conflict_http_total":              state.Lifecycle.DebugBindConflictHTTPTotal.Load(),
		"debug_bind_conflict_alias_total":             state.Lifecycle.DebugBindConflictAliasTotal.Load(),
		"debug_bind_conflict_sni_total":               state.Lifecycle.DebugBindConflictSNITotal.Load(),
		"debug_bind_conflict_tcp_total":               state.Lifecycle.DebugBindConflictTCPTotal.Load(),
		"debug_stale_holder_purged_total":             state.Lifecycle.DebugStaleHolderPurgedTotal.Load(),
		"debug_stale_holder_purged_http_total":        state.Lifecycle.DebugStaleHolderPurgedHTTPTotal.Load(),
		"debug_stale_holder_purged_alias_total":       state.Lifecycle.DebugStaleHolderPurgedAliasTotal.Load(),
		"debug_stale_holder_purged_sni_total":         state.Lifecycle.DebugStaleHolderPurgedSNITotal.Load(),
		"debug_stale_holder_purged_tcp_total":         state.Lifecycle.DebugStaleHolderPurgedTCPTotal.Load(),
		"debug_force_disconnect_noop_total":           state.Lifecycle.DebugForceDisconnectNoopTotal.Load(),
		"debug_target_release_timeout_total":          state.Lifecycle.DebugTargetReleaseTimeoutTotal.Load(),
		"visitor_alias_connections_total":             state.Lifecycle.VisitorAliasConnectionsTotal.Load(),
	}
}

func dirtySummaryFromLifecycleMetrics(lifecycle map[string]uint64) map[string]int {
	return map[string]int{
		"total":    int(lifecycle["dirty_forwards_stable_total"]),
		"listener": int(lifecycle["dirty_forwards_stable_listener_total"]),
		"http":     int(lifecycle["dirty_forwards_stable_http_total"]),
		"tcp":      int(lifecycle["dirty_forwards_stable_tcp_total"]),
		"alias":    int(lifecycle["dirty_forwards_stable_alias_total"]),
	}
}

func lifecycleMetricsKVRows(state *State) []internalKVRow {
	lifecycle := lifecycleMetricsMap(state)
	rows := make([]internalKVRow, 0, len(lifecycleMetricOrder))
	for _, key := range lifecycleMetricOrder {
		rows = append(rows, internalKVRow{
			Key:   key,
			Value: strconv.FormatUint(lifecycle[key], 10),
		})
	}

	return rows
}

func debugMetricsKVRows(state *State) []internalKVRow {
	lifecycle := lifecycleMetricsMap(state)
	keys := []string{
		"debug_bind_conflict_total",
		"debug_bind_conflict_http_total",
		"debug_bind_conflict_alias_total",
		"debug_bind_conflict_sni_total",
		"debug_bind_conflict_tcp_total",
		"debug_stale_holder_purged_total",
		"debug_stale_holder_purged_http_total",
		"debug_stale_holder_purged_alias_total",
		"debug_stale_holder_purged_sni_total",
		"debug_stale_holder_purged_tcp_total",
		"debug_force_disconnect_noop_total",
		"debug_target_release_timeout_total",
		"visitor_alias_connections_total",
	}
	rows := make([]internalKVRow, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, internalKVRow{
			Key:   key,
			Value: strconv.FormatUint(lifecycle[key], 10),
		})
	}
	return rows
}

func (c *WebConsole) updateInternalStatusState(now time.Time, lifecycle map[string]uint64, dirty map[string]int) ([]internalKVRow, map[string]float64, []internalMetricsSample) {
	rates := map[string]float64{}
	rows := make([]internalKVRow, 0, len(lifecycleMetricOrder))

	if c == nil || c.InternalState == nil || c.InternalState.Lock == nil {
		for _, key := range lifecycleMetricOrder {
			rows = append(rows, internalKVRow{Key: key + "_per_sec", Value: "0.000"})
		}
		return rows, rates, []internalMetricsSample{}
	}

	c.InternalState.Lock.Lock()
	defer c.InternalState.Lock.Unlock()

	deltaSeconds := 0.0
	if !c.InternalState.PrevAt.IsZero() {
		deltaSeconds = now.Sub(c.InternalState.PrevAt).Seconds()
	}

	for _, key := range lifecycleMetricOrder {
		current := lifecycle[key]
		if deltaSeconds > 0 {
			prev := c.InternalState.PrevLifecycle[key]
			if current >= prev {
				rates[key] = float64(current-prev) / deltaSeconds
			} else {
				rates[key] = 0
			}
		} else {
			rates[key] = 0
		}
		rows = append(rows, internalKVRow{
			Key:   key + "_per_sec",
			Value: fmt.Sprintf("%.3f", rates[key]),
		})
	}

	nextPrev := map[string]uint64{}
	for _, key := range lifecycleMetricOrder {
		nextPrev[key] = lifecycle[key]
	}
	c.InternalState.PrevLifecycle = nextPrev
	c.InternalState.PrevAt = now

	history := append([]internalMetricsSample(nil), c.InternalState.History...)
	history = append(history, internalMetricsSample{
		GeneratedAt:      now.Format(viper.GetString("time-format")),
		LifecycleMetrics: cloneLifecycleMetricsMap(lifecycle),
		DirtyMetrics:     cloneDirtyMetricsMap(dirty),
	})

	maxItems := c.InternalState.MaxHistoryItems
	if maxItems <= 0 {
		maxItems = 60
	}
	if len(history) > maxItems {
		history = history[len(history)-maxItems:]
	}
	c.InternalState.History = history

	return rows, rates, append([]internalMetricsSample(nil), history...)
}

func cloneLifecycleMetricsMap(input map[string]uint64) map[string]uint64 {
	out := map[string]uint64{}
	for _, key := range lifecycleMetricOrder {
		out[key] = input[key]
	}
	return out
}

func cloneDirtyMetricsMap(input map[string]int) map[string]int {
	out := map[string]int{
		"total":    0,
		"listener": 0,
		"http":     0,
		"tcp":      0,
		"alias":    0,
	}
	for key := range out {
		out[key] = input[key]
	}
	return out
}

func buildInternalHealth(lifecycle map[string]uint64, dirty map[string]int, rates map[string]float64) internalHealthSnapshot {
	alerts := []internalHealthAlert{}
	status := "ok"

	dirtyTotal := dirty["total"]
	if dirtyTotal > 0 {
		alerts = append(alerts, internalHealthAlert{
			Level:   "warning",
			Name:    "dirty_forwards_present",
			Message: "Stable dirty forwards detected",
			Value:   strconv.Itoa(dirtyTotal),
		})
		status = "warning"
	}

	cleanupErrors := lifecycle["forward_cleanup_errors_total"]
	cleanupTotal := lifecycle["forward_cleanup_total"]
	if cleanupErrors > 0 {
		alerts = append(alerts, internalHealthAlert{
			Level:   "warning",
			Name:    "cleanup_errors_total",
			Message: "Cleanup errors have been observed",
			Value:   strconv.FormatUint(cleanupErrors, 10),
		})
		status = "warning"
	}

	if cleanupTotal >= 20 && cleanupErrors*100 > cleanupTotal*5 {
		alerts = append(alerts, internalHealthAlert{
			Level:   "critical",
			Name:    "cleanup_error_ratio",
			Message: "Cleanup error ratio is above 5%",
			Value:   fmt.Sprintf("%d/%d", cleanupErrors, cleanupTotal),
		})
		status = "critical"
	}

	if rates["forward_cleanup_errors_total"] > 0.5 {
		alerts = append(alerts, internalHealthAlert{
			Level:   "critical",
			Name:    "cleanup_error_rate_high",
			Message: "Cleanup error rate is high",
			Value:   fmt.Sprintf("%.3f/s", rates["forward_cleanup_errors_total"]),
		})
		status = "critical"
	}

	return internalHealthSnapshot{
		Status: status,
		Alerts: alerts,
	}
}

func internalMetricsPrometheusText(lifecycle map[string]uint64, dirty map[string]int, rates map[string]float64, health internalHealthSnapshot) string {
	builder := strings.Builder{}
	builder.WriteString("# HELP sish_lifecycle_counter Lifecycle counters exposed by sish internal endpoint\n")
	builder.WriteString("# TYPE sish_lifecycle_counter counter\n")
	for _, key := range lifecycleMetricOrder {
		builder.WriteString(fmt.Sprintf("sish_lifecycle_counter{name=\"%s\"} %d\n", key, lifecycle[key]))
	}

	builder.WriteString("# HELP sish_dirty_forward_total Stable dirty forwards grouped by type\n")
	builder.WriteString("# TYPE sish_dirty_forward_total gauge\n")
	for _, key := range []string{"total", "listener", "http", "tcp", "alias"} {
		builder.WriteString(fmt.Sprintf("sish_dirty_forward_total{type=\"%s\"} %d\n", key, dirty[key]))
	}

	builder.WriteString("# HELP sish_lifecycle_rate_per_sec Lifecycle counter rates per second\n")
	builder.WriteString("# TYPE sish_lifecycle_rate_per_sec gauge\n")
	for _, key := range lifecycleMetricOrder {
		builder.WriteString(fmt.Sprintf("sish_lifecycle_rate_per_sec{name=\"%s\"} %.6f\n", key, rates[key]))
	}

	statusValue := 0
	switch health.Status {
	case "warning":
		statusValue = 1
	case "critical":
		statusValue = 2
	}
	builder.WriteString("# HELP sish_internal_health_status Internal health status (0=ok, 1=warning, 2=critical)\n")
	builder.WriteString("# TYPE sish_internal_health_status gauge\n")
	builder.WriteString(fmt.Sprintf("sish_internal_health_status %d\n", statusValue))

	return builder.String()
}
