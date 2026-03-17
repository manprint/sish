package utils

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
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

	activeForwards := c.getActiveForwardRows()
	dirtyForwards := c.getDirtyForwardRows()
	stateCounts, stateDetails := c.buildInternalState(dirtyForwards)

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
		"meta": map[string]any{
			"generatedAt":  time.Now().Format(viper.GetString("time-format")),
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
		"stateCounts":     stateCounts,
		"stateDetails":    stateDetails,
		"runtimeCounters": map[string]any{"heapObjects": mem.HeapObjects, "numGC": mem.NumGC, "lastGCTimeUnixNs": mem.LastGC},
		"activeForwards":  activeForwards,
		"dirtyForwards":   dirtyForwards,
	})
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
	rows := []internalForwardIssue{}

	c.State.Listeners.Range(func(listenerName string, rawListener net.Listener) bool {
		holder, ok := rawListener.(*ListenerHolder)
		if !ok {
			return true
		}

		if holder.SSHConn == nil {
			rows = append(rows, internalForwardIssue{
				Type:  "listener",
				Name:  listenerName,
				Issue: "listener holder has no owning ssh connection",
			})
			return true
		}

		if holder.SSHConn.SSHConn == nil || holder.SSHConn.SSHConn.RemoteAddr() == nil {
			rows = append(rows, internalForwardIssue{
				Type:  "listener",
				Name:  listenerName,
				Issue: "owning ssh connection has no remote address",
			})
			return true
		}

		remoteAddr := strings.TrimSpace(holder.SSHConn.SSHConn.RemoteAddr().String())
		if _, exists := c.State.SSHConnections.Load(remoteAddr); !exists {
			rows = append(rows, internalForwardIssue{
				Type:  "listener",
				Name:  listenerName,
				Issue: "owning ssh connection is missing from active connection map",
			})
			return true
		}

		if _, exists := holder.SSHConn.Listeners.Load(listenerName); !exists {
			rows = append(rows, internalForwardIssue{
				Type:  "listener",
				Name:  listenerName,
				Issue: "listener is not linked in owning ssh connection listener map",
			})
		}

		return true
	})

	c.State.HTTPListeners.Range(func(name string, httpHolder *HTTPHolder) bool {
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
		return true
	})

	c.State.TCPListeners.Range(func(name string, tcpHolder *TCPHolder) bool {
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
		return true
	})

	c.State.AliasListeners.Range(func(name string, aliasHolder *AliasHolder) bool {
		if len(aliasHolder.Balancer.Servers()) == 0 {
			rows = append(rows, internalForwardIssue{
				Type:  "alias",
				Name:  name,
				Issue: "alias forward has no active backends",
			})
		}
		return true
	})

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Type == rows[j].Type {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Type < rows[j].Type
	})

	return rows
}
