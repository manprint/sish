package utils

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jpillora/ipfilter"
)

// OriginIPAuditEntry contains per-IP SSH ingress authentication statistics.
type OriginIPAuditEntry struct {
	IP                string           `json:"ip"`
	Country           string           `json:"country"`
	IngressEvidence   string           `json:"ingressEvidence"`
	Attempts          int64            `json:"attempts"`
	Success           int64            `json:"success"`
	Rejected          int64            `json:"rejected"`
	LastRejectReason  string           `json:"lastRejectReason"`
	RejectReasonsText string           `json:"rejectReasonsText"`
	RejectReasons     map[string]int64 `json:"rejectReasons"`
	LastSeen          string           `json:"lastSeen"`
	Forwarders        []string         `json:"forwarders"`
}

type originIPAuditCounter struct {
	Attempts         int64
	Success          int64
	Rejected         int64
	IngressHits      map[string]int64
	LastRejectReason string
	RejectReasons    map[string]int64
	LastSeen         time.Time
	Country          string
}

var (
	originIPAuditLock sync.RWMutex
	originIPAuditData = map[string]*originIPAuditCounter{}
)

func normalizeRemoteIP(remoteAddr string) string {
	trimmed := strings.TrimSpace(remoteAddr)
	if trimmed == "" {
		return "unknown"
	}

	host, _, err := net.SplitHostPort(trimmed)
	if err == nil {
		trimmed = host
	}

	trimmed = strings.TrimPrefix(trimmed, "[")
	trimmed = strings.TrimSuffix(trimmed, "]")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return "unknown"
	}

	return trimmed
}

func getOrInitOriginIPAuditCounter(ip string) *originIPAuditCounter {
	counter, ok := originIPAuditData[ip]
	if ok {
		return counter
	}

	counter = &originIPAuditCounter{
		Country:       resolveCountryFromIP(ip),
		IngressHits:   map[string]int64{},
		RejectReasons: map[string]int64{},
	}
	originIPAuditData[ip] = counter
	return counter
}

func resolveCountryFromIP(ip string) string {
	ip = strings.TrimSpace(strings.ToLower(ip))
	if ip == "" || ip == "unknown" {
		return "Unknown"
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "Unknown"
	}

	if parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsLinkLocalUnicast() || parsed.IsLinkLocalMulticast() {
		return "Local"
	}

	country := strings.ToUpper(strings.TrimSpace(ipfilter.NetIPToCountry(parsed)))
	if country == "" || country == "ZZ" {
		return "Unknown"
	}

	return country
}

// RecordOriginIPAttempt increments attempt count for an incoming SSH connection.
func RecordOriginIPAttempt(remoteAddr string, ingress string, localPort string) {
	ip := normalizeRemoteIP(remoteAddr)
	originIPAuditLock.Lock()
	defer originIPAuditLock.Unlock()

	counter := getOrInitOriginIPAuditCounter(ip)
	counter.Attempts++
	counter.IngressHits[buildIngressKey(ingress, localPort)]++
	counter.LastSeen = time.Now()
}

// RecordOriginIPSuccess increments success count for an authenticated SSH connection.
func RecordOriginIPSuccess(remoteAddr string) {
	ip := normalizeRemoteIP(remoteAddr)
	originIPAuditLock.Lock()
	defer originIPAuditLock.Unlock()

	counter := getOrInitOriginIPAuditCounter(ip)
	counter.Success++
	counter.LastSeen = time.Now()
}

// RecordOriginIPReject increments rejection counters and stores rejection reason.
func RecordOriginIPReject(remoteAddr string, reason string) {
	ip := normalizeRemoteIP(remoteAddr)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Unknown reason"
	}

	originIPAuditLock.Lock()
	defer originIPAuditLock.Unlock()

	counter := getOrInitOriginIPAuditCounter(ip)
	counter.Rejected++
	counter.LastRejectReason = reason
	counter.RejectReasons[reason]++
	counter.LastSeen = time.Now()
}

// GetOriginIPAuditSnapshot returns a stable, sorted snapshot of Origin IP audit entries.
func GetOriginIPAuditSnapshot(timeFmt string) []OriginIPAuditEntry {
	originIPAuditLock.RLock()
	defer originIPAuditLock.RUnlock()

	rows := make([]OriginIPAuditEntry, 0, len(originIPAuditData))
	for ip, data := range originIPAuditData {
		reasons := map[string]int64{}
		for k, v := range data.RejectReasons {
			reasons[k] = v
		}

		country := strings.TrimSpace(data.Country)
		if country == "" || strings.EqualFold(country, "unknown") {
			country = resolveCountryFromIP(ip)
		}

		lastSeen := "never"
		if !data.LastSeen.IsZero() {
			lastSeen = data.LastSeen.Format(timeFmt)
		}

		rows = append(rows, OriginIPAuditEntry{
			IP:                ip,
			Country:           country,
			IngressEvidence:   buildIngressEvidence(data.IngressHits),
			Attempts:          data.Attempts,
			Success:           data.Success,
			Rejected:          data.Rejected,
			LastRejectReason:  data.LastRejectReason,
			RejectReasonsText: buildRejectReasonsText(reasons),
			RejectReasons:     reasons,
			LastSeen:          lastSeen,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Attempts == rows[j].Attempts {
			return rows[i].IP < rows[j].IP
		}

		return rows[i].Attempts > rows[j].Attempts
	})

	return rows
}

func buildRejectReasonsText(reasons map[string]int64) string {
	if len(reasons) == 0 {
		return "None"
	}

	parts := make([]string, 0, len(reasons))
	for reason, count := range reasons {
		parts = append(parts, reason+" ("+strconv.FormatInt(count, 10)+")")
	}

	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

func buildIngressKey(ingress string, localPort string) string {
	normalizedIngress := strings.ToLower(strings.TrimSpace(ingress))
	switch normalizedIngress {
	case "https":
		normalizedIngress = "multiplexer"
	case "ssh":
		normalizedIngress = "ssh"
	default:
		normalizedIngress = "unknown"
	}

	port := strings.TrimSpace(localPort)
	if port == "" {
		port = "unknown"
	}

	return normalizedIngress + ":" + port
}

func buildIngressEvidence(ingressHits map[string]int64) string {
	if len(ingressHits) == 0 {
		return "Unknown"
	}

	multiplexerPorts := map[string]struct{}{}
	sshPorts := map[string]struct{}{}

	for key, count := range ingressHits {
		if count <= 0 {
			continue
		}

		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}

		ingress := parts[0]
		port := parts[1]
		switch ingress {
		case "multiplexer":
			multiplexerPorts[port] = struct{}{}
		case "ssh":
			sshPorts[port] = struct{}{}
		}
	}

	multiplexerList := sortedKeys(multiplexerPorts)
	sshList := sortedKeys(sshPorts)

	multiplexerLabel := formatPortsLabel(multiplexerList)
	sshLabel := formatPortsLabel(sshList)

	switch {
	case len(multiplexerList) > 0 && len(sshList) > 0:
		return fmt.Sprintf("Both (Multiplexer %s | SSH %s)", multiplexerLabel, sshLabel)
	case len(multiplexerList) > 0:
		return "Multiplexer " + multiplexerLabel
	case len(sshList) > 0:
		return "SSH standard " + sshLabel
	default:
		return "Unknown"
	}
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func formatPortsLabel(ports []string) string {
	if len(ports) == 0 {
		return "(:unknown)"
	}

	for i, port := range ports {
		ports[i] = ":" + port
	}
	return "(" + strings.Join(ports, ", ") + ")"
}
