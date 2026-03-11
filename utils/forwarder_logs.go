package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	forwardersLogFilePartSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	forwardersLogANSISanitizer     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	forwardersLogLock              sync.Mutex
	forwardersLogWriters           = map[string]*lumberjack.Logger{}
)

// ForwardersLogEnabled indicates whether forwarder-specific logging is enabled.
func ForwardersLogEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(viper.GetString("forwarders-log")), "enable")
}

func sanitizeForwardersLogPart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}

	value = strings.ReplaceAll(value, ":", "_")
	value = forwardersLogFilePartSanitizer.ReplaceAllString(value, "_")
	value = strings.Trim(value, "._-")
	if value == "" {
		return "unknown"
	}

	return value
}

// StripANSISequences removes ANSI escape codes so logs remain readable in plain text views.
func StripANSISequences(value string) string {
	if strings.IndexByte(value, 0x1b) == -1 {
		return value
	}

	return forwardersLogANSISanitizer.ReplaceAllString(value, "")
}

// BuildHTTPForwardersLogKey returns the file key format id-domain.
func BuildHTTPForwardersLogKey(connectionID string, domain string) string {
	return fmt.Sprintf("%s-%s", sanitizeForwardersLogPart(connectionID), sanitizeForwardersLogPart(domain))
}

// BuildTCPForwardersLogKey returns the file key format id-port.
func BuildTCPForwardersLogKey(connectionID string, port int) string {
	return fmt.Sprintf("%s-%d", sanitizeForwardersLogPart(connectionID), port)
}

// BuildAliasForwardersLogKey returns the file key format id-alias_port.
func BuildAliasForwardersLogKey(connectionID string, alias string, port uint32) string {
	return fmt.Sprintf("%s-%s_%d", sanitizeForwardersLogPart(connectionID), sanitizeForwardersLogPart(alias), port)
}

func getForwardersLogWriter(fileKey string) (*lumberjack.Logger, error) {
	writer, ok := forwardersLogWriters[fileKey]
	if ok {
		return writer, nil
	}

	logDir := strings.TrimSpace(viper.GetString("forwarders-log-dir"))
	if logDir == "" {
		return nil, fmt.Errorf("forwarders-log-dir is empty")
	}

	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, err
	}

	writer = &lumberjack.Logger{
		Filename:   filepath.Join(logDir, fileKey),
		MaxSize:    viper.GetInt("forwarders-log-max-size"),
		MaxBackups: viper.GetInt("forwarders-log-max-backups"),
		MaxAge:     viper.GetInt("forwarders-log-max-age"),
		Compress:   viper.GetBool("forwarders-log-compress"),
	}

	forwardersLogWriters[fileKey] = writer
	return writer, nil
}

// WriteForwardersLogLine writes a line to the forwarder-specific log file.
func WriteForwardersLogLine(fileKey string, message string) {
	if !ForwardersLogEnabled() {
		return
	}

	fileKey = sanitizeForwardersLogPart(fileKey)
	message = strings.TrimSpace(StripANSISequences(message))
	if message == "" {
		return
	}

	forwardersLogLock.Lock()
	defer forwardersLogLock.Unlock()

	writer, err := getForwardersLogWriter(fileKey)
	if err != nil {
		return
	}

	timeFmt := viper.GetString("forwarders-log-time-format")
	if strings.TrimSpace(timeFmt) == "" {
		timeFmt = viper.GetString("time-format")
	}

	line := fmt.Sprintf("%s | %s\n", time.Now().Format(timeFmt), message)
	_, _ = writer.Write([]byte(line))
}

// ParseAliasHostPort splits alias key host:port.
func ParseAliasHostPort(alias string) (string, uint32, bool) {
	parts := strings.Split(alias, ":")
	if len(parts) != 2 {
		return "", 0, false
	}

	port64, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 32)
	if err != nil {
		return "", 0, false
	}

	return strings.TrimSpace(parts[0]), uint32(port64), true
}
