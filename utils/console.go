package utils

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
	"github.com/vulcand/oxy/roundrobin"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

// upgrader is the default WS upgrader that we use for webconsole clients.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var insertAPIKeyLock sync.Mutex
var insertAPIUserLock sync.Mutex

// WebClient represents a primitive web console client. It maintains
// references that allow us to communicate and track a client connection.
type WebClient struct {
	Conn    *websocket.Conn
	Console *WebConsole
	Send    chan []byte
	Done    chan struct{}
	Route   string
	closed  atomic.Bool
	closeMu sync.Once
}

// WebConsole represents the data structure that stores web console client information.
type WebConsole struct {
	Clients        *syncmap.Map[string, []*WebClient]
	ClientsLock    *sync.RWMutex
	RouteTokens    *syncmap.Map[string, string]
	History        []ConnectionHistory
	HistoryLock    *sync.RWMutex
	DirtyState     *dirtyForwardState
	InternalState  *internalStatusState
	ClosedPingRows []pingStatusRow
	ClosedPingLock *sync.RWMutex
	State          *State
}

const defaultWebClientSendQueueSize = 32

// webClientWriteTimeout bounds a single websocket write to a console viewer.
const webClientWriteTimeout = 10 * time.Second

var maxHistoryEntries = 10000

type dirtyForwardState struct {
	Lock      *sync.Mutex
	SeenCount map[string]int
}

type internalStatusState struct {
	Lock            *sync.Mutex
	PrevAt          time.Time
	PrevLifecycle   map[string]uint64
	History         []internalMetricsSample
	MaxHistoryItems int
}

// ConnectionHistory contains immutable connection lifecycle information.
type ConnectionHistory struct {
	ID           string
	RemoteAddr   string
	Username     string
	StartedAt    time.Time
	EndedAt      time.Time
	Duration     time.Duration
	DataInBytes  int64
	DataOutBytes int64
	Ingress      string
	IngressPort  string
	Forwarder    string
}

type auditBandwidthSnapshot struct {
	TotalUploadBytes   int64 `json:"totalUploadBytes"`
	TotalDownloadBytes int64 `json:"totalDownloadBytes"`
}

type consoleInfoRow struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type clientInfoField struct {
	Key          string
	DefaultValue string
	Extract      func(*SSHConnection) string
}

type configInfoField struct {
	Key          string
	DefaultValue string
	Extract      func(authUser, bool, string) string
}

var sishClientInfoFields = []clientInfoField{
	{Key: "id", DefaultValue: "Not Defined", Extract: func(conn *SSHConnection) string { return strings.TrimSpace(conn.GetConnectionID()) }},
	{Key: "id-provided", DefaultValue: "false", Extract: func(conn *SSHConnection) string { return strconv.FormatBool(conn.IsConnectionIDProvided()) }},
	{Key: "visitor", DefaultValue: "false", Extract: func(conn *SSHConnection) string { return strconv.FormatBool(conn.VisitorConnection.Load()) }},
	{Key: "visitor-forwarder", DefaultValue: "Not Defined", Extract: func(conn *SSHConnection) string { return strings.TrimSpace(conn.VisitorForwarderLabel()) }},
	{Key: "force-connect", DefaultValue: "false", Extract: func(conn *SSHConnection) string { return strconv.FormatBool(conn.GetForceConnect()) }},
	{Key: "force-https", DefaultValue: "false", Extract: func(conn *SSHConnection) string { return strconv.FormatBool(conn.GetForceHTTPS()) }},
	{Key: "proxy-protocol", DefaultValue: "0", Extract: func(conn *SSHConnection) string { return strconv.Itoa(int(conn.GetProxyProto())) }},
	{Key: "host-header", DefaultValue: "Not Defined", Extract: func(conn *SSHConnection) string { return strings.TrimSpace(conn.GetHostHeader()) }},
	{Key: "strip-path", DefaultValue: strconv.FormatBool(viper.GetBool("strip-http-path")), Extract: func(conn *SSHConnection) string { return strconv.FormatBool(conn.GetStripPath()) }},
	{Key: "sni-proxy", DefaultValue: "false", Extract: func(conn *SSHConnection) string { return strconv.FormatBool(conn.GetSNIProxy()) }},
	{Key: "tcp-address", DefaultValue: "Not Defined", Extract: func(conn *SSHConnection) string { return strings.TrimSpace(conn.GetTCPAddress()) }},
	{Key: "tcp-alias", DefaultValue: "false", Extract: func(conn *SSHConnection) string { return strconv.FormatBool(conn.GetTCPAlias()) }},
	{Key: "local-forward", DefaultValue: "false", Extract: func(conn *SSHConnection) string { return strconv.FormatBool(conn.GetLocalForward()) }},
	{Key: "auto-close", DefaultValue: "false", Extract: func(conn *SSHConnection) string { return strconv.FormatBool(conn.GetAutoClose()) }},
	{Key: "tcp-aliases-allowed-users", DefaultValue: "Not Defined", Extract: func(conn *SSHConnection) string { return strings.Join(conn.GetTCPAliasesAllowedUsers(), ",") }},
	{Key: "deadline", DefaultValue: "Not Defined", Extract: func(conn *SSHConnection) string {
		deadline := conn.GetDeadline()
		if deadline == nil {
			return ""
		}

		return deadline.UTC().Format(time.RFC3339)
	}},
	{Key: "exec-mode", DefaultValue: "false", Extract: func(conn *SSHConnection) string { return strconv.FormatBool(conn.GetExecMode()) }},
}

var sishConfigInfoFields = []configInfoField{
	{Key: "name", DefaultValue: "Not Defined", Extract: func(cfg authUser, hasCfg bool, fallbackUsername string) string {
		if hasCfg {
			return strings.TrimSpace(cfg.Name)
		}

		return strings.TrimSpace(fallbackUsername)
	}},
	{Key: "password", DefaultValue: "Not Defined", Extract: func(cfg authUser, hasCfg bool, _ string) string {
		if !hasCfg || strings.TrimSpace(cfg.Password) == "" {
			return ""
		}

		return "REDACTED"
	}},
	{Key: "pubkey", DefaultValue: "Not Defined", Extract: func(cfg authUser, hasCfg bool, _ string) string {
		if !hasCfg || strings.TrimSpace(cfg.PubKey) == "" {
			return ""
		}

		return "REDACTED"
	}},
	{Key: "bandwidth-upload", DefaultValue: "Not Defined", Extract: func(cfg authUser, hasCfg bool, _ string) string {
		if !hasCfg {
			return ""
		}

		return strings.TrimSpace(cfg.BandwidthUpload)
	}},
	{Key: "bandwidth-download", DefaultValue: "Not Defined", Extract: func(cfg authUser, hasCfg bool, _ string) string {
		if !hasCfg {
			return ""
		}

		return strings.TrimSpace(cfg.BandwidthDownload)
	}},
	{Key: "bandwidth-burst", DefaultValue: "1.0", Extract: func(cfg authUser, hasCfg bool, _ string) string {
		if !hasCfg {
			return ""
		}

		return strings.TrimSpace(cfg.BandwidthBurst)
	}},
	{Key: "allowed-forwarder", DefaultValue: "Not Defined", Extract: func(cfg authUser, hasCfg bool, _ string) string {
		if !hasCfg {
			return ""
		}

		return strings.TrimSpace(cfg.AllowedForwarder)
	}},
}

var sishIngressInfoFields = []clientInfoField{
	{Key: "type", DefaultValue: "Unknown", Extract: func(conn *SSHConnection) string {
		return ingressLabel(conn.Ingress)
	}},
	{Key: "port", DefaultValue: "Unknown", Extract: func(conn *SSHConnection) string {
		return strings.TrimSpace(conn.IngressPort)
	}},
}

func withDefaultValue(value string, defaultValue string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultValue
	}

	return trimmed
}

func ingressLabel(ingress string) string {
	switch ingress {
	case "ssh":
		return "SSH"
	case "https":
		return "Multiplexer"
	default:
		return "Unknown"
	}
}

func visitorPendingDisplayID(connectionID string) string {
	trimmed := strings.TrimSpace(connectionID)
	if strings.HasPrefix(trimmed, "rand-") {
		suffix := strings.TrimPrefix(trimmed, "rand-")
		if suffix != "" {
			return "visitor-pending-" + suffix
		}
	}
	return "visitor-pending"
}

func shouldShowVisitorPending(conn *SSHConnection, listeners []string, forwarder string) bool {
	if conn == nil {
		return false
	}
	if conn.VisitorConnection.Load() {
		return false
	}
	if conn.IsConnectionIDProvided() {
		return false
	}
	if len(listeners) != 0 {
		return false
	}
	if strings.TrimSpace(forwarder) != "" {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(conn.GetConnectionID()), "rand-")
}

func displayConnectionIdentityAndForwarder(conn *SSHConnection, connectionID string, listeners []string, forwarder string) (string, string) {
	if conn == nil {
		return strings.TrimSpace(connectionID), strings.TrimSpace(forwarder)
	}

	displayID := strings.TrimSpace(connectionID)
	if displayID == "" {
		displayID = strings.TrimSpace(conn.GetConnectionID())
	}
	displayForwarder := strings.TrimSpace(forwarder)

	if shouldShowVisitorPending(conn, listeners, displayForwarder) {
		displayID = visitorPendingDisplayID(displayID)
		displayForwarder = "VIS-pending"
	}

	return displayID, displayForwarder
}

func finalizeActiveForwardList(forwards []string, pendingForwarder string) []string {
	unique := []string{}
	seen := map[string]struct{}{}

	add := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		if _, ok := seen[trimmed]; ok {
			return
		}
		seen[trimmed] = struct{}{}
		unique = append(unique, trimmed)
	}

	for _, forward := range forwards {
		add(forward)
	}
	add(pendingForwarder)

	sort.Strings(unique)
	return unique
}

func buildSishClientInfoRows(conn *SSHConnection) []consoleInfoRow {
	rows := make([]consoleInfoRow, 0, len(sishClientInfoFields))
	for _, field := range sishClientInfoFields {
		value := field.Extract(conn)
		rows = append(rows, consoleInfoRow{Key: field.Key, Value: withDefaultValue(value, field.DefaultValue)})
	}

	return rows
}

func buildSishConfigInfoRows(username string) []consoleInfoRow {
	cfg, hasCfg := getAuthUserRawConfig(username)
	rows := make([]consoleInfoRow, 0, len(sishConfigInfoFields))
	for _, field := range sishConfigInfoFields {
		value := field.Extract(cfg, hasCfg, username)
		rows = append(rows, consoleInfoRow{Key: field.Key, Value: withDefaultValue(value, field.DefaultValue)})
	}

	return rows
}

func buildSishIngressInfoRows(conn *SSHConnection) []consoleInfoRow {
	rows := make([]consoleInfoRow, 0, len(sishIngressInfoFields))
	for _, field := range sishIngressInfoFields {
		value := field.Extract(conn)
		rows = append(rows, consoleInfoRow{Key: field.Key, Value: withDefaultValue(value, field.DefaultValue)})
	}

	return rows
}

// NewWebConsole sets up the WebConsole.
func NewWebConsole() *WebConsole {
	return &WebConsole{
		Clients:        syncmap.New[string, []*WebClient](),
		ClientsLock:    &sync.RWMutex{},
		RouteTokens:    syncmap.New[string, string](),
		History:        []ConnectionHistory{},
		HistoryLock:    &sync.RWMutex{},
		ClosedPingRows: []pingStatusRow{},
		ClosedPingLock: &sync.RWMutex{},
		DirtyState: &dirtyForwardState{
			Lock:      &sync.Mutex{},
			SeenCount: map[string]int{},
		},
		InternalState: &internalStatusState{
			Lock:            &sync.Mutex{},
			PrevLifecycle:   map[string]uint64{},
			History:         []internalMetricsSample{},
			MaxHistoryItems: 60,
		},
	}
}

func parsePublicKeyLine(line string) ssh.PublicKey {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return nil
	}

	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(trimmed))
	if err != nil {
		return nil
	}

	return key
}

func listPublicKeysInDirectory(baseDir string) ([]ssh.PublicKey, error) {
	keys := []ssh.PublicKey{}

	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		return keys, nil
	}

	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		for _, line := range strings.Split(string(content), "\n") {
			key := parsePublicKeyLine(line)
			if key != nil {
				keys = append(keys, key)
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return keys, nil
}

func appendAPIKeyBlock(existing []byte, keyLine string, timestamp string, comment string) []byte {
	comment = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(comment, "\r", " "), "\n", " "))

	content := strings.TrimRight(string(existing), "\n")
	if content != "" {
		content += "\n\n"
	}

	content += fmt.Sprintf("# Inserted by api in date: %s\n", timestamp)
	if comment != "" {
		content += fmt.Sprintf("# %s\n", comment)
	}
	content += keyLine + "\n\n"

	return []byte(content)
}

func validateAuthUsersStructuredYAML(content string) error {
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("yaml content is empty")
	}

	parsed := &authUsersFile{}
	if err := yaml.Unmarshal([]byte(content), parsed); err != nil {
		return fmt.Errorf("invalid yaml: %w", err)
	}

	if len(parsed.Users) == 0 {
		return fmt.Errorf("yaml must contain at least one user")
	}

	for _, u := range parsed.Users {
		if strings.TrimSpace(u.Name) == "" {
			return fmt.Errorf("user name cannot be empty")
		}

		hasPassword := strings.TrimSpace(u.Password) != ""
		hasPubKey := strings.TrimSpace(u.PubKey) != ""

		if !hasPassword && !hasPubKey {
			return fmt.Errorf("user %s must have at least one credential: password or pubkey", strings.TrimSpace(u.Name))
		}

		if hasPubKey {
			if _, err := parseAuthorizedPubKeyString(u.PubKey); err != nil {
				return fmt.Errorf("invalid pubkey for user %s: %w", strings.TrimSpace(u.Name), err)
			}
		}

		if _, _, err := parseAuthUserBandwidthConfig(u); err != nil {
			return fmt.Errorf("invalid bandwidth config for user %s: %w", strings.TrimSpace(u.Name), err)
		}

		if _, _, err := parseAuthUserAllowedForwarderConfig(u.AllowedForwarder); err != nil {
			return fmt.Errorf("invalid allowed-forwarder config for user %s: %w", strings.TrimSpace(u.Name), err)
		}
	}

	return nil
}

func listAuthUsersInDirectory(baseDir string) ([]authUser, error) {
	users := []authUser{}

	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		return users, nil
	}

	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !isYAMLFile(path) {
			return nil
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		if strings.TrimSpace(string(content)) == "" {
			return nil
		}

		parsed := &authUsersFile{}
		if unmarshalErr := yaml.Unmarshal(content, parsed); unmarshalErr != nil {
			return fmt.Errorf("invalid yaml in %s: %w", path, unmarshalErr)
		}

		for _, u := range parsed.Users {
			if strings.TrimSpace(u.Name) == "" {
				continue
			}

			users = append(users, authUser{
				Name:     strings.TrimSpace(u.Name),
				Password: u.Password,
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return users, nil
}

type apiUserParams struct {
	Username          string
	Password          string
	Timestamp         string
	Comment           string
	PubKey            string
	BandwidthUpload   string
	BandwidthDownload string
	BandwidthBurst    string
	AllowedForwarder  string
}

func appendAPIUserBlock(existing []byte, params apiUserParams) []byte {
	comment := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(params.Comment, "\r", " "), "\n", " "))

	trimmed := strings.TrimRight(string(existing), "\n")

	if trimmed == "" {
		trimmed = "users:"
	}

	if !strings.HasPrefix(strings.TrimSpace(trimmed), "users:") {
		trimmed = "users:\n\n" + trimmed
	}

	if !strings.HasSuffix(trimmed, "\n") {
		trimmed += "\n"
	}

	trimmed += fmt.Sprintf("\n# Inserted by api in date: %s\n", params.Timestamp)
	if comment != "" {
		trimmed += fmt.Sprintf("# %s\n", comment)
	}
	trimmed += fmt.Sprintf("  - name: %s\n", params.Username)
	if params.Password != "" {
		trimmed += fmt.Sprintf("    password: %q\n", params.Password)
	}
	if params.PubKey != "" {
		trimmed += fmt.Sprintf("    pubkey: %q\n", params.PubKey)
	}
	if params.BandwidthUpload != "" {
		trimmed += fmt.Sprintf("    bandwidth-upload: %q\n", params.BandwidthUpload)
	}
	if params.BandwidthDownload != "" {
		trimmed += fmt.Sprintf("    bandwidth-download: %q\n", params.BandwidthDownload)
	}
	if params.BandwidthBurst != "" {
		trimmed += fmt.Sprintf("    bandwidth-burst: %q\n", params.BandwidthBurst)
	}
	if params.AllowedForwarder != "" {
		trimmed += fmt.Sprintf("    allowed-forwarder: %q\n", params.AllowedForwarder)
	}

	return []byte(trimmed + "\n")
}

// HandleInsertKeyAPI inserts a public key into fromapi.key under authentication-keys-directory.
func (c *WebConsole) HandleInsertKeyAPI(g *gin.Context) {
	if g.Request.Method != http.MethodPost {
		err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	if !c.CheckEditKeysBasicAuth(g) {
		return
	}

	body, err := io.ReadAll(g.Request.Body)
	if err != nil {
		err = g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	keyLine := strings.TrimSpace(string(body))
	if keyLine == "" {
		err = g.AbortWithError(http.StatusBadRequest, fmt.Errorf("public key payload is empty"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	parsedKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(keyLine))
	if err != nil {
		err = g.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid public key"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	keysDir := strings.TrimSpace(viper.GetString("authentication-keys-directory"))
	if keysDir == "" {
		err = g.AbortWithError(http.StatusBadRequest, fmt.Errorf("authentication-keys-directory is empty"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	baseDir, err := filepath.Abs(keysDir)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	comment := strings.TrimSpace(g.Request.Header.Get("x-api-comment"))
	if comment == "" {
		comment = strings.TrimSpace(g.Request.URL.Query().Get("comment"))
	}

	insertAPIKeyLock.Lock()
	defer insertAPIKeyLock.Unlock()

	existingKeys, err := listPublicKeysInDirectory(baseDir)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	for _, existing := range existingKeys {
		if bytes.Equal(existing.Marshal(), parsedKey.Marshal()) {
			g.JSON(http.StatusOK, map[string]any{
				"status":   true,
				"inserted": false,
				"message":  "key already present",
				"file":     "fromapi.key",
			})
			return
		}
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	fromAPIPath := filepath.Join(baseDir, "fromapi.key")
	existingContent, readErr := os.ReadFile(fromAPIPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		err = g.AbortWithError(http.StatusInternalServerError, readErr)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	mode := fileModeOrDefault(fromAPIPath, 0600)

	newContent := appendAPIKeyBlock(existingContent, keyLine, time.Now().Format("2006-01-02-15-04-05"), comment)
	if err := writeFileAtomic(fromAPIPath, newContent, mode); err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	loadKeys()

	g.JSON(http.StatusOK, map[string]any{
		"status":   true,
		"inserted": true,
		"message":  "key inserted",
		"file":     "fromapi.key",
	})
}

// HandleInsertUserAPI inserts a user into fromapi.yml under auth-users-directory.
func (c *WebConsole) HandleInsertUserAPI(g *gin.Context) {
	if g.Request.Method != http.MethodPost {
		g.JSON(http.StatusMethodNotAllowed, map[string]any{"status": false, "message": "method not allowed"})
		g.Abort()
		return
	}

	if !c.CheckEditUsersBasicAuth(g) {
		return
	}

	if !viper.GetBool("auth-users-enabled") {
		g.JSON(http.StatusBadRequest, map[string]any{"status": false, "message": "auth-users-enabled is false"})
		g.Abort()
		return
	}

	var username, password, comment, pubkey, bandwidthUpload, bandwidthDownload, bandwidthBurst, allowedForwarder string

	bodyBytes, readErr := io.ReadAll(g.Request.Body)
	if readErr != nil {
		g.JSON(http.StatusBadRequest, map[string]any{"status": false, "message": "read body error: " + readErr.Error()})
		g.Abort()
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(g.Request.Header.Get("Content-Type")))
	if strings.Contains(contentType, "application/json") {
		var payload struct {
			Name              string `json:"name"`
			Password          string `json:"password"`
			Comment           string `json:"comment"`
			PubKey            string `json:"pubkey"`
			BandwidthUpload   string `json:"bandwidth-upload"`
			BandwidthDownload string `json:"bandwidth-download"`
			BandwidthBurst    string `json:"bandwidth-burst"`
			AllowedForwarder  string `json:"allowed-forwarder"`
		}
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			g.JSON(http.StatusBadRequest, map[string]any{"status": false, "message": "json decode error: " + err.Error()})
			g.Abort()
			return
		}
		username = strings.TrimSpace(payload.Name)
		password = strings.TrimSpace(payload.Password)
		comment = strings.TrimSpace(payload.Comment)
		pubkey = strings.TrimSpace(payload.PubKey)
		bandwidthUpload = strings.TrimSpace(payload.BandwidthUpload)
		bandwidthDownload = strings.TrimSpace(payload.BandwidthDownload)
		bandwidthBurst = strings.TrimSpace(payload.BandwidthBurst)
		allowedForwarder = strings.TrimSpace(payload.AllowedForwarder)
	} else {
		// Restore body for ParseForm
		g.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		if err := g.Request.ParseForm(); err != nil {
			g.JSON(http.StatusBadRequest, map[string]any{"status": false, "message": "parse form error: " + err.Error()})
			g.Abort()
			return
		}
		username = strings.TrimSpace(g.Request.FormValue("name"))
		password = strings.TrimSpace(g.Request.FormValue("password"))
		pubkey = strings.TrimSpace(g.Request.FormValue("pubkey"))
		bandwidthUpload = strings.TrimSpace(g.Request.FormValue("bandwidth-upload"))
		bandwidthDownload = strings.TrimSpace(g.Request.FormValue("bandwidth-download"))
		bandwidthBurst = strings.TrimSpace(g.Request.FormValue("bandwidth-burst"))
		allowedForwarder = strings.TrimSpace(g.Request.FormValue("allowed-forwarder"))
		comment = strings.TrimSpace(g.Request.FormValue("comment"))
		if comment == "" {
			comment = strings.TrimSpace(g.Request.URL.Query().Get("comment"))
		}
	}

	if headerComment := strings.TrimSpace(g.Request.Header.Get("x-api-comment")); headerComment != "" {
		comment = headerComment
	}

	if username == "" {
		g.JSON(http.StatusBadRequest, map[string]any{"status": false, "message": "name is required"})
		g.Abort()
		return
	}

	if password == "" && pubkey == "" {
		g.JSON(http.StatusBadRequest, map[string]any{"status": false, "message": "at least one of password or pubkey is required"})
		g.Abort()
		return
	}

	usersDir := strings.TrimSpace(viper.GetString("auth-users-directory"))
	if usersDir == "" {
		g.JSON(http.StatusBadRequest, map[string]any{"status": false, "message": "auth-users-directory is empty"})
		g.Abort()
		return
	}

	baseDir, err := filepath.Abs(usersDir)
	if err != nil {
		g.JSON(http.StatusInternalServerError, map[string]any{"status": false, "message": err.Error()})
		g.Abort()
		return
	}

	insertAPIUserLock.Lock()
	defer insertAPIUserLock.Unlock()

	existingUsers, err := listAuthUsersInDirectory(baseDir)
	if err != nil {
		g.JSON(http.StatusInternalServerError, map[string]any{"status": false, "message": err.Error()})
		g.Abort()
		return
	}

	for _, existing := range existingUsers {
		if existing.Name == username {
			g.JSON(http.StatusOK, map[string]any{
				"status":   true,
				"inserted": false,
				"message":  "user already present",
				"file":     "fromapi.yml",
			})
			return
		}
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		g.JSON(http.StatusInternalServerError, map[string]any{"status": false, "message": err.Error()})
		g.Abort()
		return
	}

	fromAPIPath := filepath.Join(baseDir, "fromapi.yml")
	existingContent, readErr := os.ReadFile(fromAPIPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		g.JSON(http.StatusInternalServerError, map[string]any{"status": false, "message": readErr.Error()})
		g.Abort()
		return
	}

	mode := fileModeOrDefault(fromAPIPath, 0600)

	newContent := appendAPIUserBlock(existingContent, apiUserParams{
		Username:          username,
		Password:          password,
		Timestamp:         time.Now().Format("2006-01-02-15-04-05"),
		Comment:           comment,
		PubKey:            pubkey,
		BandwidthUpload:   bandwidthUpload,
		BandwidthDownload: bandwidthDownload,
		BandwidthBurst:    bandwidthBurst,
		AllowedForwarder:  allowedForwarder,
	})
	if err := validateAuthUsersStructuredYAML(string(newContent)); err != nil {
		g.JSON(http.StatusBadRequest, map[string]any{"status": false, "message": "validation error: " + err.Error()})
		g.Abort()
		return
	}

	if err := writeFileAtomic(fromAPIPath, newContent, mode); err != nil {
		g.JSON(http.StatusInternalServerError, map[string]any{"status": false, "message": err.Error()})
		g.Abort()
		return
	}

	loadAuthUsers()

	g.JSON(http.StatusOK, map[string]any{
		"status":   true,
		"inserted": true,
		"message":  "user inserted",
		"file":     "fromapi.yml",
	})
}

type censusForwardRow struct {
	ID                        string   `json:"id"`
	Listeners                 int      `json:"listeners"`
	RemoteAddr                string   `json:"remoteAddr"`
	Forwards                  []string `json:"forwards"`
	DataInBytes               int64    `json:"dataInBytes"`
	DataOutBytes              int64    `json:"dataOutBytes"`
	BandwidthProfileVersion   int64    `json:"bandwidthProfileVersion"`
	BandwidthProfileUpdatedAt string   `json:"bandwidthProfileUpdatedAt"`
}

// forwardIndex maps a forward's backend socket address to the forward names
// served by it.
//
// Resolving forwards used to mean re-scanning every HTTP, TCP and alias holder
// (and every balancer server inside them) once per SSH connection. The console
// polls this once a second and calls it for every connection, so the cost was
// quadratic in the number of forwards; getActiveForwardRows even did the whole
// scan twice per connection. Building the reverse index once per request makes
// each connection a handful of map lookups (B22).
type forwardIndex map[string][]string

// buildForwardIndex walks the listener maps once and returns socket -> forwards.
func buildForwardIndex(state *State) forwardIndex {
	index := forwardIndex{}
	if state == nil {
		return index
	}

	add := func(socket string, forward string) {
		if socket == "" || forward == "" {
			return
		}
		index[socket] = append(index[socket], forward)
	}

	decodeServers := func(balancer *roundrobin.RoundRobin, forward string) {
		if balancer == nil {
			return
		}
		for _, server := range balancer.Servers() {
			serverAddr, err := base64.StdEncoding.DecodeString(server.Host)
			if err != nil {
				continue
			}
			add(string(serverAddr), forward)
		}
	}

	state.HTTPListeners.Range(func(_ string, httpHolder *HTTPHolder) bool {
		if httpHolder == nil || httpHolder.HTTPUrl == nil {
			return true
		}

		var userPass string
		password, _ := httpHolder.HTTPUrl.User.Password()
		if httpHolder.HTTPUrl.User.Username() != "" || password != "" {
			userPass = fmt.Sprintf("%s:%s@", httpHolder.HTTPUrl.User.Username(), password)
		}
		forward := fmt.Sprintf("%s%s%s", userPass, httpHolder.HTTPUrl.Hostname(), httpHolder.HTTPUrl.Path)

		httpHolder.SSHConnections.Range(func(httpAddr string, _ *SSHConnection) bool {
			add(httpAddr, forward)
			return true
		})

		return true
	})

	state.TCPListeners.Range(func(tcpAddr string, tcpHolder *TCPHolder) bool {
		if tcpHolder == nil {
			return true
		}

		tcpHolder.Balancers.Range(func(ikey string, balancer *roundrobin.RoundRobin) bool {
			forward := tcpAddr
			if tcpHolder.SNIProxy {
				forward = fmt.Sprintf("%s-%s", tcpAddr, ikey)
			}
			decodeServers(balancer, forward)
			return true
		})

		return true
	})

	state.AliasListeners.Range(func(tcpAlias string, aliasHolder *AliasHolder) bool {
		if aliasHolder == nil {
			return true
		}
		decodeServers(aliasHolder.Balancer, tcpAlias)
		return true
	})

	return index
}

// forwardsForListeners returns the forward names served by the given listener
// sockets, de-duplicated and sorted.
func (index forwardIndex) forwardsForListeners(listeners []string) []string {
	forwards := []string{}
	for _, listener := range listeners {
		forwards = append(forwards, index[listener]...)
	}

	return finalizeActiveForwardList(forwards, "")
}

// connectionListeners returns the non-empty listener socket names of a connection.
func connectionListeners(s *SSHConnection) []string {
	if s == nil || s.Listeners == nil {
		return nil
	}

	var listeners []string
	s.Listeners.Range(func(name string, _ net.Listener) bool {
		if strings.TrimSpace(name) != "" {
			listeners = append(listeners, name)
		}
		return true
	})

	return listeners
}

// resolveConnectionForwardersIndexed is resolveConnectionForwarders against a
// pre-built index, for callers that resolve many connections in one pass.
func resolveConnectionForwardersIndexed(s *SSHConnection, index forwardIndex) string {
	if s != nil && s.VisitorConnection.Load() {
		if visitor := strings.TrimSpace(s.VisitorForwarderLabel()); visitor != "" {
			return visitor
		}
	}

	listeners := connectionListeners(s)
	if len(listeners) == 0 {
		return ""
	}

	return strings.Join(index.forwardsForListeners(listeners), ", ")
}

// resolveConnectionForwarders returns a comma-separated string of all active
// forward targets for the given connection, resolved from the global listener maps.
func resolveConnectionForwarders(s *SSHConnection, state *State) string {
	if s != nil && s.VisitorConnection.Load() {
		if visitor := strings.TrimSpace(s.VisitorForwarderLabel()); visitor != "" {
			return visitor
		}
	}

	if len(connectionListeners(s)) == 0 {
		return ""
	}

	return resolveConnectionForwardersIndexed(s, buildForwardIndex(state))
}

func (c *WebConsole) getActiveForwardRows() []censusForwardRow {
	rows := []censusForwardRow{}
	index := buildForwardIndex(c.State)

	c.State.SSHConnections.Range(func(_ string, sshConn *SSHConnection) bool {
		listeners := connectionListeners(sshConn)
		listenerCount := len(listeners)

		if listenerCount == 0 {
			return true
		}

		id := strings.TrimSpace(sshConn.GetConnectionID())
		if id == "" {
			return true
		}

		dataInBytes := int64(0)
		dataOutBytes := int64(0)
		profile := sshConn.GetBandwidthProfile()
		if profile != nil {
			dataInBytes = profile.DataInBytes.Load()
			dataOutBytes = profile.DataOutBytes.Load()
		}
		profileVersion, profileUpdatedAt := sshConn.GetBandwidthProfileMeta()
		profileUpdatedAtLabel := "never"
		if !profileUpdatedAt.IsZero() {
			profileUpdatedAtLabel = profileUpdatedAt.Format(viper.GetString("time-format"))
		}

		resolvedForwarder := strings.TrimSpace(resolveConnectionForwardersIndexed(sshConn, index))
		displayID, displayForwarder := displayConnectionIdentityAndForwarder(sshConn, sshConn.GetConnectionID(), listeners, resolvedForwarder)
		id = displayID

		forwards := index.forwardsForListeners(listeners)

		pendingForwarder := ""
		if displayForwarder == "VIS-pending" {
			pendingForwarder = displayForwarder
		}
		forwards = finalizeActiveForwardList(forwards, pendingForwarder)

		rows = append(rows, censusForwardRow{
			ID:                        id,
			Listeners:                 listenerCount,
			RemoteAddr:                sshConn.SSHConn.RemoteAddr().String(),
			Forwards:                  forwards,
			DataInBytes:               dataInBytes,
			DataOutBytes:              dataOutBytes,
			BandwidthProfileVersion:   profileVersion,
			BandwidthProfileUpdatedAt: profileUpdatedAtLabel,
		})

		return true
	})

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].ID < rows[j].ID
	})

	return rows
}

// HandleCensusTemplate renders the census page.
func (c *WebConsole) HandleCensusTemplate(g *gin.Context) {
	if !viper.GetBool("census-enabled") {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("census-enabled is false"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return
	}

	g.HTML(http.StatusOK, "census", c.templateData(true, true))
}

// HandleCensusRefresh forces a census refresh from census-url.
func (c *WebConsole) HandleCensusRefresh(g *gin.Context) {
	if !viper.GetBool("census-enabled") {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("census-enabled is false"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return
	}

	if err := RefreshCensusCache(); err != nil {
		err := g.AbortWithError(http.StatusBadGateway, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
	})
}

// HandleCensusSource returns the remote census content and YAML validity.
func (c *WebConsole) HandleCensusSource(g *gin.Context) {
	if !viper.GetBool("census-enabled") {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("census-enabled is false"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return
	}

	censusURL, body, ids, err := FetchCensusSource()
	if err != nil {
		content := ""
		if body != nil {
			content = string(body)
		}

		g.JSON(http.StatusOK, map[string]any{
			"status":        false,
			"censusUrl":     censusURL,
			"validYaml":     false,
			"message":       err.Error(),
			"content":       content,
			"parsedIDs":     []string{},
			"parsedIDCount": 0,
		})
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status":        true,
		"censusUrl":     censusURL,
		"validYaml":     true,
		"message":       "YAML is valid",
		"content":       string(body),
		"parsedIDs":     ids,
		"parsedIDCount": len(ids),
	})
}

// HandleCensus returns census analysis sections for active forward IDs.
func (c *WebConsole) HandleCensus(g *gin.Context) {
	if !viper.GetBool("census-enabled") {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("census-enabled is false"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return
	}

	snapshot := GetCensusCacheSnapshot()
	activeRows := c.getActiveForwardRows()

	activeByID := map[string]censusForwardRow{}
	for _, row := range activeRows {
		activeByID[row.ID] = row
	}

	censusSet := map[string]struct{}{}
	for _, id := range snapshot.IDs {
		if strings.TrimSpace(id) == "" {
			continue
		}

		censusSet[id] = struct{}{}
	}

	type censusRowWithSource struct {
		ID         string         `json:"id"`
		Listeners  int            `json:"listeners,omitempty"`
		RemoteAddr string         `json:"remoteAddr,omitempty"`
		Source     CensusIDSource `json:"source"`
		Note       string         `json:"note,omitempty"`
		Forward    string         `json:"forward,omitempty"`
	}

	proxyCensed := []censusRowWithSource{}
	proxyUncensed := []censusRowWithSource{}
	censedNotForwarded := []censusRowWithSource{}

	for _, row := range activeRows {
		forward := ""
		if len(row.Forwards) > 0 {
			forward = row.Forwards[0]
		}

		if _, ok := censusSet[row.ID]; ok {
			src := snapshot.IDSources[row.ID]
			proxyCensed = append(proxyCensed, censusRowWithSource{
				ID: row.ID, Listeners: row.Listeners, RemoteAddr: row.RemoteAddr, Source: src, Note: snapshot.IDNotes[row.ID], Forward: forward,
			})
		} else {
			proxyUncensed = append(proxyUncensed, censusRowWithSource{
				ID: row.ID, Listeners: row.Listeners, RemoteAddr: row.RemoteAddr, Forward: forward,
			})
		}
	}

	for _, id := range snapshot.IDs {
		if _, ok := activeByID[id]; !ok {
			src := snapshot.IDSources[id]
			censedNotForwarded = append(censedNotForwarded, censusRowWithSource{
				ID: id, Source: src, Note: snapshot.IDNotes[id],
			})
		}
	}

	refreshEvery := viper.GetDuration("census-refresh-time")
	if refreshEvery <= 0 {
		refreshEvery = 2 * time.Minute
	}

	lastRefreshPretty := "never"
	if !snapshot.LastRefresh.IsZero() {
		lastRefreshPretty = snapshot.LastRefresh.Format(viper.GetString("time-format"))
	}

	urlEnabled := viper.GetBool("strict-id-censed-url")
	filesEnabled := viper.GetBool("strict-id-censed-files")
	censusDir := strings.TrimSpace(viper.GetString("census-directory"))

	g.JSON(http.StatusOK, map[string]any{
		"status":              true,
		"proxyCensed":         proxyCensed,
		"proxyUncensed":       proxyUncensed,
		"censedNotForwarded":  censedNotForwarded,
		"censusUrl":           viper.GetString("census-url"),
		"censusDirectory":     censusDir,
		"censusFiles":         snapshot.URLFiles,
		"urlEnabled":          urlEnabled,
		"filesEnabled":        filesEnabled,
		"lastRefreshPretty":   lastRefreshPretty,
		"lastError":           snapshot.LastError,
		"refreshEverySeconds": int(refreshEvery.Seconds()),
		"autoRefreshActive":   true,
	})
}

// HandleRequest handles an incoming web request, handles auth, and then routes it.
func (c *WebConsole) HandleRequest(proxyUrl string, hostIsRoot bool, g *gin.Context) {
	userAuthed := false
	userIsAdmin := false
	adminToken := viper.GetString("admin-console-token")
	requestToken := g.Request.URL.Query().Get("x-authorization")
	if requestToken == "" {
		requestToken = g.Request.Header.Get("x-authorization")
	}

	if viper.GetBool("admin-console") && adminToken != "" && secureCompare(requestToken, adminToken) {
		userIsAdmin = true
		userAuthed = true
	}

	tokenInterface, ok := c.RouteTokens.Load(proxyUrl)
	if ok {
		routeToken := tokenInterface
		if routeToken == "" {
			ok = false
		}

		if viper.GetBool("service-console") && ok && secureCompare(requestToken, routeToken) {
			userAuthed = true
		}
	}

	if strings.HasPrefix(g.Request.URL.Path, "/_sish/console/ws") && userAuthed {
		c.HandleWebSocket(proxyUrl, g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/history/download") && userIsAdmin && viper.GetBool("history-enabled") {
		c.HandleHistoryDownload(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/history/clear") && userIsAdmin && viper.GetBool("history-enabled") {
		if g.Request.Method != http.MethodPost {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}

		c.HandleHistoryClear(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/history") && userIsAdmin && viper.GetBool("history-enabled") {
		c.HandleHistory(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/history") && userIsAdmin && viper.GetBool("history-enabled") {
		c.HandleHistoryTemplate(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/audit") && hostIsRoot && userIsAdmin {
		c.HandleAuditTemplate(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/audit") && hostIsRoot && userIsAdmin {
		if g.Request.Method != http.MethodGet {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}

		c.HandleAudit(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/logs") && hostIsRoot && userIsAdmin && ForwardersLogEnabled() {
		c.HandleLogsTemplate(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/logs/files") && hostIsRoot && userIsAdmin && ForwardersLogEnabled() {
		if g.Request.Method != http.MethodGet {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}

		c.HandleLogsFiles(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/logs/file") && hostIsRoot && userIsAdmin && ForwardersLogEnabled() {
		if g.Request.Method != http.MethodGet {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}

		c.HandleLogsFile(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/logs/download") && hostIsRoot && userIsAdmin && ForwardersLogEnabled() {
		if g.Request.Method != http.MethodGet {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}

		c.HandleLogsDownload(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/editkeys") && hostIsRoot && userIsAdmin {
		if !c.CheckEditKeysBasicAuth(g) {
			return
		}

		c.HandleEditKeysTemplate(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/editkeys/files") && hostIsRoot && userIsAdmin {
		if !c.CheckEditKeysBasicAuth(g) {
			return
		}

		c.HandleEditKeysFiles(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/editkeys/file") && hostIsRoot && userIsAdmin {
		if !c.CheckEditKeysBasicAuth(g) {
			return
		}

		if g.Request.Method == http.MethodGet {
			c.HandleEditKeysFileRead(g)
			return
		}

		if g.Request.Method == http.MethodPost {
			c.HandleEditKeysFileWrite(g)
			return
		}

		err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/editusers") && hostIsRoot && userIsAdmin {
		if !c.CheckEditUsersBasicAuth(g) {
			return
		}

		c.HandleEditUsersTemplate(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/editusers/files") && hostIsRoot && userIsAdmin {
		if !c.CheckEditUsersBasicAuth(g) {
			return
		}

		c.HandleEditUsersFiles(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/editusers/validate") && hostIsRoot && userIsAdmin {
		if !c.CheckEditUsersBasicAuth(g) {
			return
		}

		if g.Request.Method != http.MethodPost {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}

		c.HandleEditUsersValidate(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/editusers/file") && hostIsRoot && userIsAdmin {
		if !c.CheckEditUsersBasicAuth(g) {
			return
		}

		if g.Request.Method == http.MethodGet {
			c.HandleEditUsersFileRead(g)
			return
		}

		if g.Request.Method == http.MethodPost {
			c.HandleEditUsersFileWrite(g)
			return
		}

		err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/editheaders") && hostIsRoot && userIsAdmin {
		if !c.CheckEditHeadersBasicAuth(g) {
			return
		}

		c.HandleEditHeadersTemplate(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/editheaders/files") && hostIsRoot && userIsAdmin {
		if !c.CheckEditHeadersBasicAuth(g) {
			return
		}

		c.HandleEditHeadersFiles(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/editheaders/validate") && hostIsRoot && userIsAdmin {
		if !c.CheckEditHeadersBasicAuth(g) {
			return
		}

		if g.Request.Method != http.MethodPost {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}

		c.HandleEditHeadersValidate(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/editheaders/file") && hostIsRoot && userIsAdmin {
		if !c.CheckEditHeadersBasicAuth(g) {
			return
		}

		if g.Request.Method == http.MethodGet {
			c.HandleEditHeadersFileRead(g)
			return
		}

		if g.Request.Method == http.MethodPost {
			c.HandleEditHeadersFileWrite(g)
			return
		}

		err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/editcensus") && hostIsRoot && userIsAdmin {
		if !c.CheckEditCensusBasicAuth(g) {
			return
		}

		c.HandleEditCensusTemplate(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/editcensus/files") && hostIsRoot && userIsAdmin {
		if !c.CheckEditCensusBasicAuth(g) {
			return
		}

		c.HandleEditCensusFiles(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/editcensus/validate") && hostIsRoot && userIsAdmin {
		if !c.CheckEditCensusBasicAuth(g) {
			return
		}

		if g.Request.Method != http.MethodPost {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}

		c.HandleEditCensusValidate(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/editcensus/file") && hostIsRoot && userIsAdmin {
		if !c.CheckEditCensusBasicAuth(g) {
			return
		}

		if g.Request.Method == http.MethodGet {
			c.HandleEditCensusFileRead(g)
			return
		}

		if g.Request.Method == http.MethodPost {
			c.HandleEditCensusFileWrite(g)
			return
		}

		err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/census") && hostIsRoot && userIsAdmin {
		c.HandleCensusTemplate(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/census/refresh") && hostIsRoot && userIsAdmin {
		if g.Request.Method != http.MethodPost {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}

		c.HandleCensusRefresh(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/census/source") && hostIsRoot && userIsAdmin {
		if g.Request.Method != http.MethodGet {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}

		c.HandleCensusSource(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/census") && hostIsRoot && userIsAdmin {
		c.HandleCensus(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/internal") && hostIsRoot && userIsAdmin && viper.GetBool("show-internal-state") {
		c.HandleInternalTemplate(g)
		return
	} else if g.Request.URL.Path == "/_sish/api/internal/ping-status" && hostIsRoot && userIsAdmin && viper.GetBool("show-internal-state") {
		if g.Request.Method != http.MethodGet {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}
		c.HandleInternalPingStatus(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/internal/metrics") && hostIsRoot && userIsAdmin && viper.GetBool("show-internal-state") {
		if g.Request.Method != http.MethodGet {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}
		c.HandleInternalMetrics(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/internal") && hostIsRoot && userIsAdmin && viper.GetBool("show-internal-state") {
		c.HandleInternal(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/console") && userAuthed {
		c.HandleTemplate(proxyUrl, hostIsRoot, userIsAdmin, g)
		return
	} else if (g.Request.URL.Path == "/_sish/api/disconnectclient" || strings.HasPrefix(g.Request.URL.Path, "/_sish/api/disconnectclient/")) && userIsAdmin {
		c.HandleDisconnectClient(proxyUrl, g)
		return
	} else if (g.Request.URL.Path == "/_sish/api/disconnectroute" || strings.HasPrefix(g.Request.URL.Path, "/_sish/api/disconnectroute/")) && userIsAdmin {
		c.HandleDisconnectRoute(proxyUrl, g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/clients") && hostIsRoot && userIsAdmin {
		c.HandleClients(proxyUrl, g)
		return
	}

	if strings.HasPrefix(g.Request.URL.Path, "/_sish/") {
		status := http.StatusUnauthorized
		if userAuthed {
			status = http.StatusNotFound
		}

		err := g.AbortWithError(status, fmt.Errorf("cannot access console route: %s", g.Request.URL.Path))
		if err != nil {
			log.Println("Aborting with error", err)
		}
	}
}

// secureCompare reports whether two secrets are equal without leaking their
// contents through comparison timing. Plain == on tokens and passwords is
// byte-by-byte with early exit (B19).
func secureCompare(a string, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// secureCredentialsMatch checks a basic-auth username/password pair in constant
// time. Both halves are always compared so a wrong username costs the same as a
// wrong password.
func secureCredentialsMatch(gotUser, gotPassword, wantUser, wantPassword string) bool {
	userOK := secureCompare(gotUser, wantUser)
	passwordOK := secureCompare(gotPassword, wantPassword)
	return userOK && passwordOK
}

func parseConsoleCredentials(raw string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(raw), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}

	username := strings.TrimSpace(parts[0])
	password := strings.TrimSpace(parts[1])
	if username == "" || password == "" {
		return "", "", false
	}

	return username, password, true
}

func (c *WebConsole) templateData(hostIsRoot bool, userIsAdmin bool) map[string]any {
	canAccessAdminConsoleFeatures := hostIsRoot && userIsAdmin

	_, _, hasEditKeysCredentials := parseConsoleCredentials(viper.GetString("admin-consolle-editkeys-credentials"))
	_, _, hasEditUsersCredentials := parseConsoleCredentials(viper.GetString("admin-consolle-editusers-credentials"))
	_, _, hasEditHeadersCredentials := parseConsoleCredentials(viper.GetString("admin-consolle-editheaders-credentials"))
	_, _, hasEditCensusCredentials := parseConsoleCredentials(viper.GetString("admin-consolle-editcensus-credentials"))

	return map[string]any{
		"ShowHistory":     canAccessAdminConsoleFeatures && viper.GetBool("history-enabled"),
		"ShowCensus":      canAccessAdminConsoleFeatures && viper.GetBool("census-enabled"),
		"ShowInternal":    canAccessAdminConsoleFeatures && viper.GetBool("show-internal-state"),
		"ShowAudit":       canAccessAdminConsoleFeatures,
		"ShowLogs":        canAccessAdminConsoleFeatures && ForwardersLogEnabled(),
		"ShowEditKeys":    canAccessAdminConsoleFeatures && hasEditKeysCredentials,
		"ShowEditUsers":   canAccessAdminConsoleFeatures && hasEditUsersCredentials,
		"ShowEditHeaders": canAccessAdminConsoleFeatures && hasEditHeadersCredentials,
		"ShowEditCensus":  canAccessAdminConsoleFeatures && hasEditCensusCredentials && viper.GetBool("census-enabled") && viper.GetBool("strict-id-censed-files") && strings.TrimSpace(viper.GetString("census-directory")) != "",
	}
}

// CheckEditKeysBasicAuth validates extra basic auth required for editkeys routes.
func (c *WebConsole) CheckEditKeysBasicAuth(g *gin.Context) bool {
	credentials := strings.TrimSpace(viper.GetString("admin-consolle-editkeys-credentials"))
	if credentials == "" {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("admin-consolle-editkeys-credentials is not configured"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return false
	}

	expectedUser, expectedPassword, ok := parseConsoleCredentials(credentials)
	if !ok {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("admin-consolle-editkeys-credentials format is invalid"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return false
	}

	username, password, ok := g.Request.BasicAuth()
	if !ok || !secureCredentialsMatch(username, password, expectedUser, expectedPassword) {
		g.Header("WWW-Authenticate", "Basic realm=\"sish-editkeys\"")
		status := http.StatusUnauthorized
		g.AbortWithStatus(status)
		if viper.GetBool("debug") {
			log.Println("Aborting with status", status)
		}

		return false
	}

	return true
}

// CheckEditUsersBasicAuth validates extra basic auth required for editusers routes.
func (c *WebConsole) CheckEditUsersBasicAuth(g *gin.Context) bool {
	credentials := strings.TrimSpace(viper.GetString("admin-consolle-editusers-credentials"))
	if credentials == "" {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("admin-consolle-editusers-credentials is not configured"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return false
	}

	expectedUser, expectedPassword, ok := parseConsoleCredentials(credentials)
	if !ok {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("admin-consolle-editusers-credentials format is invalid"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return false
	}

	username, password, ok := g.Request.BasicAuth()
	if !ok || !secureCredentialsMatch(username, password, expectedUser, expectedPassword) {
		g.Header("WWW-Authenticate", "Basic realm=\"sish-editusers\"")
		status := http.StatusUnauthorized
		g.AbortWithStatus(status)
		if viper.GetBool("debug") {
			log.Println("Aborting with status", status)
		}

		return false
	}

	return true
}

// HandleEditKeysTemplate renders the editkeys page.
func (c *WebConsole) HandleEditKeysTemplate(g *gin.Context) {
	g.HTML(http.StatusOK, "editkeys", c.templateData(true, true))
}

// HandleEditUsersTemplate renders the editusers page.
func (c *WebConsole) HandleEditUsersTemplate(g *gin.Context) {
	g.HTML(http.StatusOK, "editusers", c.templateData(true, true))
}

// CheckEditHeadersBasicAuth validates extra basic auth required for editheaders routes.
func (c *WebConsole) CheckEditHeadersBasicAuth(g *gin.Context) bool {
	credentials := strings.TrimSpace(viper.GetString("admin-consolle-editheaders-credentials"))
	if credentials == "" {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("admin-consolle-editheaders-credentials is not configured"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return false
	}

	expectedUser, expectedPassword, ok := parseConsoleCredentials(credentials)
	if !ok {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("admin-consolle-editheaders-credentials format is invalid"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return false
	}

	username, password, ok := g.Request.BasicAuth()
	if !ok || !secureCredentialsMatch(username, password, expectedUser, expectedPassword) {
		g.Header("WWW-Authenticate", "Basic realm=\"sish-editheaders\"")
		status := http.StatusUnauthorized
		g.AbortWithStatus(status)
		if viper.GetBool("debug") {
			log.Println("Aborting with status", status)
		}

		return false
	}

	return true
}

// HandleEditHeadersTemplate renders the editheaders page.
func (c *WebConsole) HandleEditHeadersTemplate(g *gin.Context) {
	g.HTML(http.StatusOK, "editheaders", c.templateData(true, true))
}

// HandleEditHeadersFiles returns the list of YAML files under headers-setting-directory.
func (c *WebConsole) HandleEditHeadersFiles(g *gin.Context) {
	headersDir := strings.TrimSpace(viper.GetString("headers-setting-directory"))
	if headersDir == "" {
		err := g.AbortWithError(http.StatusBadRequest, fmt.Errorf("headers-setting-directory is empty"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	baseDir, err := filepath.Abs(headersDir)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	files, err := listManagedFiles(baseDir, true)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status":           true,
		"files":            files,
		"headersDirectory": baseDir,
	})
}

// HandleEditHeadersFileRead returns file content for read-only view or edit.
func (c *WebConsole) HandleEditHeadersFileRead(g *gin.Context) {
	requested := g.Query("file")
	baseDir, filePath, err := c.resolveEditHeadersFile(requested)
	if err != nil {
		err = g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	rel, err := filepath.Rel(baseDir, filePath)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status":  true,
		"file":    filepath.ToSlash(rel),
		"content": string(content),
	})
}

// HandleEditHeadersValidate validates YAML content for headers settings files.
func (c *WebConsole) HandleEditHeadersValidate(g *gin.Context) {
	payload := struct {
		Content string `json:"content"`
	}{}

	decoder := json.NewDecoder(g.Request.Body)
	if err := decoder.Decode(&payload); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	if err := ValidateHeaderSettingsConfig([]byte(payload.Content)); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
		"valid":  true,
	})
}

// HandleEditHeadersFileWrite validates and saves updated headers settings YAML file content.
func (c *WebConsole) HandleEditHeadersFileWrite(g *gin.Context) {
	payload := struct {
		File    string `json:"file"`
		Content string `json:"content"`
	}{}

	decoder := json.NewDecoder(g.Request.Body)
	if err := decoder.Decode(&payload); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	if err := ValidateHeaderSettingsConfig([]byte(payload.Content)); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	_, filePath, err := c.resolveEditHeadersFile(payload.File)
	if err != nil {
		err = g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	mode := fileModeOrDefault(filePath, 0600)

	if err := writeFileAtomic(filePath, []byte(payload.Content), mode); err != nil {
		err := g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	loadHeaderSettingsConfig()

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
	})
}

// CheckEditCensusBasicAuth validates extra basic auth required for editcensus routes.
func (c *WebConsole) CheckEditCensusBasicAuth(g *gin.Context) bool {
	credentials := strings.TrimSpace(viper.GetString("admin-consolle-editcensus-credentials"))
	if credentials == "" {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("admin-consolle-editcensus-credentials is not configured"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return false
	}

	expectedUser, expectedPassword, ok := parseConsoleCredentials(credentials)
	if !ok {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("admin-consolle-editcensus-credentials format is invalid"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return false
	}

	username, password, ok := g.Request.BasicAuth()
	if !ok || !secureCredentialsMatch(username, password, expectedUser, expectedPassword) {
		g.Header("WWW-Authenticate", "Basic realm=\"sish-editcensus\"")
		status := http.StatusUnauthorized
		g.AbortWithStatus(status)
		if viper.GetBool("debug") {
			log.Println("Aborting with status", status)
		}

		return false
	}

	return true
}

// HandleEditCensusTemplate renders the editcensus page.
func (c *WebConsole) HandleEditCensusTemplate(g *gin.Context) {
	g.HTML(http.StatusOK, "editcensus", c.templateData(true, true))
}

// HandleEditCensusFiles returns the list of YAML files under census-directory.
func (c *WebConsole) HandleEditCensusFiles(g *gin.Context) {
	censusDir := strings.TrimSpace(viper.GetString("census-directory"))
	if censusDir == "" {
		err := g.AbortWithError(http.StatusBadRequest, fmt.Errorf("census-directory is empty"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	baseDir, err := filepath.Abs(censusDir)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	files, err := listManagedFiles(baseDir, true)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status":          true,
		"files":           files,
		"censusDirectory": baseDir,
	})
}

// HandleEditCensusFileRead returns file content for read-only view or edit.
func (c *WebConsole) HandleEditCensusFileRead(g *gin.Context) {
	requested := g.Query("file")
	baseDir, filePath, err := c.resolveEditCensusFile(requested)
	if err != nil {
		err = g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	rel, err := filepath.Rel(baseDir, filePath)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status":  true,
		"file":    filepath.ToSlash(rel),
		"content": string(content),
	})
}

// HandleEditCensusValidate validates YAML content for census files.
func (c *WebConsole) HandleEditCensusValidate(g *gin.Context) {
	payload := struct {
		Content string `json:"content"`
	}{}

	decoder := json.NewDecoder(g.Request.Body)
	if err := decoder.Decode(&payload); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	if err := ValidateCensusYAML([]byte(payload.Content)); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
		"valid":  true,
	})
}

// HandleEditCensusFileWrite validates and saves updated census YAML file content.
func (c *WebConsole) HandleEditCensusFileWrite(g *gin.Context) {
	payload := struct {
		File    string `json:"file"`
		Content string `json:"content"`
	}{}

	decoder := json.NewDecoder(g.Request.Body)
	if err := decoder.Decode(&payload); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	if err := ValidateCensusYAML([]byte(payload.Content)); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	_, filePath, err := c.resolveEditCensusFile(payload.File)
	if err != nil {
		err = g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	mode := fileModeOrDefault(filePath, 0600)

	if err := writeFileAtomic(filePath, []byte(payload.Content), mode); err != nil {
		err := g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	// Trigger census cache refresh after saving a local census file.
	_ = RefreshCensusCache()

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
	})
}

func (c *WebConsole) collectAuditBandwidthSnapshot() auditBandwidthSnapshot {
	snapshot := auditBandwidthSnapshot{}

	for _, entry := range c.historySnapshot() {
		snapshot.TotalUploadBytes += entry.DataInBytes
		snapshot.TotalDownloadBytes += entry.DataOutBytes
	}

	c.State.SSHConnections.Range(func(_ string, sshConn *SSHConnection) bool {
		profile := sshConn.GetBandwidthProfile()
		if profile == nil {
			return true
		}

		snapshot.TotalUploadBytes += profile.DataInBytes.Load()
		snapshot.TotalDownloadBytes += profile.DataOutBytes.Load()
		return true
	})

	return snapshot
}

// HandleAuditTemplate renders the audit page.
func (c *WebConsole) HandleAuditTemplate(g *gin.Context) {
	g.HTML(http.StatusOK, "audit", c.templateData(true, true))
}

// HandleAudit returns an audit snapshot used by the audit page refresh button.
func (c *WebConsole) HandleAudit(g *gin.Context) {
	timeFormat := viper.GetString("time-format")
	originRows := GetOriginIPAuditSnapshot(timeFormat)

	for i := range originRows {
		originRows[i].LastRejectReason = withDefaultValue(originRows[i].LastRejectReason, "None")
		originRows[i].Country = withDefaultValue(originRows[i].Country, "Unknown")
		originRows[i].RejectReasonsText = withDefaultValue(originRows[i].RejectReasonsText, "None")
	}

	ipForwarders := c.resolveActiveForwardersByIP()
	for i := range originRows {
		if originRows[i].Success > 0 {
			if fwds, ok := ipForwarders[originRows[i].IP]; ok {
				originRows[i].Forwarders = fwds
			}
		}
	}

	bandwidth := c.collectAuditBandwidthSnapshot()

	g.JSON(http.StatusOK, map[string]any{
		"status":      true,
		"generatedAt": time.Now().Format(timeFormat),
		"bandwidth": map[string]any{
			"totalUploadBytes":   bandwidth.TotalUploadBytes,
			"totalDownloadBytes": bandwidth.TotalDownloadBytes,
			"totalUploadMB":      formatBytesToMB1Decimal(bandwidth.TotalUploadBytes),
			"totalDownloadMB":    formatBytesToMB1Decimal(bandwidth.TotalDownloadBytes),
		},
		"originIPStats": originRows,
	})
}

// resolveActiveForwardersByIP builds a map of normalized IP -> sorted unique forwarders
// from all currently active SSH connections.
func (c *WebConsole) resolveActiveForwardersByIP() map[string][]string {
	result := map[string][]string{}
	if c.State == nil {
		return result
	}

	ipSet := map[string]map[string]struct{}{}

	c.State.SSHConnections.Range(func(_ string, sshConn *SSHConnection) bool {
		if sshConn == nil || sshConn.SSHConn == nil {
			return true
		}

		remoteAddr := ""
		if sshConn.SSHConn.RemoteAddr() != nil {
			remoteAddr = sshConn.SSHConn.RemoteAddr().String()
		}
		ip := normalizeRemoteIP(remoteAddr)
		if ip == "unknown" {
			return true
		}

		fwd := strings.TrimSpace(resolveConnectionForwarders(sshConn, c.State))
		if fwd == "" {
			return true
		}

		if _, ok := ipSet[ip]; !ok {
			ipSet[ip] = map[string]struct{}{}
		}
		for _, part := range strings.Split(fwd, ", ") {
			part = strings.TrimSpace(part)
			if part != "" {
				ipSet[ip][part] = struct{}{}
			}
		}

		return true
	})

	for ip, fwdSet := range ipSet {
		fwds := make([]string, 0, len(fwdSet))
		for f := range fwdSet {
			fwds = append(fwds, f)
		}
		sort.Strings(fwds)
		result[ip] = fwds
	}

	return result
}

func fileModeOrDefault(filePath string, defaultMode os.FileMode) os.FileMode {
	mode := defaultMode
	if stat, err := os.Stat(filePath); err == nil {
		mode = stat.Mode().Perm()
	}

	return mode
}

func writeFileAtomic(filePath string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(filePath)
	base := filepath.Base(filePath)

	tmpFile, err := os.CreateTemp(dir, base+".tmp-*")
	if err != nil {
		return err
	}

	tmpName := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmpFile.Write(content); err != nil {
		cleanup()
		return err
	}

	if err := tmpFile.Chmod(mode); err != nil {
		cleanup()
		return err
	}

	if err := tmpFile.Sync(); err != nil {
		cleanup()
		return err
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	if err := os.Rename(tmpName, filePath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	return nil
}

func (c *WebConsole) historySnapshot() []ConnectionHistory {
	if c == nil {
		return nil
	}

	c.HistoryLock.RLock()
	defer c.HistoryLock.RUnlock()

	snapshot := make([]ConnectionHistory, len(c.History))
	copy(snapshot, c.History)
	return snapshot
}

func resolveManagedFile(requested string, directoryKey string) (string, string, error) {
	managedDir := strings.TrimSpace(viper.GetString(directoryKey))
	if managedDir == "" {
		return "", "", fmt.Errorf("%s is empty", directoryKey)
	}

	baseDir, err := filepath.Abs(managedDir)
	if err != nil {
		return "", "", err
	}

	realBaseDir, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", "", err
		}

		realBaseDir = baseDir
	}

	rel := filepath.Clean(strings.TrimSpace(requested))
	if rel == "." || rel == "" {
		return "", "", fmt.Errorf("invalid file path")
	}

	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") || strings.Contains(rel, string(filepath.Separator)+"..") {
		return "", "", fmt.Errorf("invalid file path")
	}

	resolved := filepath.Join(baseDir, rel)
	resolvedAbs, err := filepath.Abs(resolved)
	if err != nil {
		return "", "", err
	}

	realResolvedPath := resolvedAbs
	if _, err := os.Stat(resolvedAbs); err == nil {
		realResolvedPath, err = filepath.EvalSymlinks(resolvedAbs)
		if err != nil {
			return "", "", err
		}
	} else {
		if !os.IsNotExist(err) {
			return "", "", err
		}

		realParent, err := filepath.EvalSymlinks(filepath.Dir(resolvedAbs))
		if err != nil {
			return "", "", err
		}

		realResolvedPath = filepath.Join(realParent, filepath.Base(resolvedAbs))
	}

	relToBase, err := filepath.Rel(realBaseDir, realResolvedPath)
	if err != nil {
		return "", "", err
	}

	if strings.HasPrefix(relToBase, "..") || filepath.IsAbs(relToBase) {
		return "", "", fmt.Errorf("invalid file path")
	}

	return baseDir, resolvedAbs, nil
}

func (c *WebConsole) resolveEditKeysFile(requested string) (string, string, error) {
	return resolveManagedFile(requested, "authentication-keys-directory")
}

func (c *WebConsole) resolveEditUsersFile(requested string) (string, string, error) {
	return resolveManagedFile(requested, "auth-users-directory")
}

func (c *WebConsole) resolveEditHeadersFile(requested string) (string, string, error) {
	return resolveManagedFile(requested, "headers-setting-directory")
}

func (c *WebConsole) resolveEditCensusFile(requested string) (string, string, error) {
	return resolveManagedFile(requested, "census-directory")
}

func (c *WebConsole) resolveForwardersLogFile(requested string) (string, string, error) {
	return resolveManagedFile(requested, "forwarders-log-dir")
}

func tailFileLines(filePath string, lines int) (string, error) {
	if lines <= 0 {
		lines = 100
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 0, 128*1024)
	scanner.Buffer(buffer, 1024*1024)

	if lines > 5000 {
		lines = 5000
	}

	ring := make([]string, lines)
	count := 0
	idx := 0

	for scanner.Scan() {
		ring[idx] = scanner.Text()
		idx = (idx + 1) % lines
		if count < lines {
			count++
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	if count == 0 {
		return "", nil
	}

	out := make([]string, 0, count)
	start := (idx - count + lines) % lines
	for i := 0; i < count; i++ {
		pos := (start + i) % lines
		out = append(out, ring[pos])
	}

	return strings.Join(out, "\n"), nil
}

func listManagedFiles(baseDir string, onlyYAML bool) ([]string, error) {
	files := []string{}

	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		if onlyYAML && !isYAMLFile(path) {
			return nil
		}

		rel, relErr := filepath.Rel(baseDir, path)
		if relErr != nil {
			return relErr
		}

		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

func validateAuthUsersYAML(content string) error {
	return validateAuthUsersStructuredYAML(content)
}

// HandleEditKeysFiles returns the list of files under authentication-keys-directory.
func (c *WebConsole) HandleEditKeysFiles(g *gin.Context) {
	keysDir := strings.TrimSpace(viper.GetString("authentication-keys-directory"))
	if keysDir == "" {
		err := g.AbortWithError(http.StatusBadRequest, fmt.Errorf("authentication-keys-directory is empty"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	baseDir, err := filepath.Abs(keysDir)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	files, err := listManagedFiles(baseDir, false)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status":        true,
		"files":         files,
		"keysDirectory": baseDir,
	})
}

// HandleEditUsersFiles returns the list of YAML files under auth-users-directory.
func (c *WebConsole) HandleEditUsersFiles(g *gin.Context) {
	usersDir := strings.TrimSpace(viper.GetString("auth-users-directory"))
	if usersDir == "" {
		err := g.AbortWithError(http.StatusBadRequest, fmt.Errorf("auth-users-directory is empty"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	baseDir, err := filepath.Abs(usersDir)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	files, err := listManagedFiles(baseDir, true)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status":         true,
		"files":          files,
		"usersDirectory": baseDir,
	})
}

// HandleEditKeysFileRead returns file content for read-only view or edit.
func (c *WebConsole) HandleEditKeysFileRead(g *gin.Context) {
	requested := g.Query("file")
	baseDir, filePath, err := c.resolveEditKeysFile(requested)
	if err != nil {
		err = g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	rel, err := filepath.Rel(baseDir, filePath)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status":  true,
		"file":    filepath.ToSlash(rel),
		"content": string(content),
	})
}

// HandleEditKeysFileWrite saves updated file content.
func (c *WebConsole) HandleEditKeysFileWrite(g *gin.Context) {
	payload := struct {
		File    string `json:"file"`
		Content string `json:"content"`
	}{}

	decoder := json.NewDecoder(g.Request.Body)
	if err := decoder.Decode(&payload); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	_, filePath, err := c.resolveEditKeysFile(payload.File)
	if err != nil {
		err = g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	mode := fileModeOrDefault(filePath, 0600)

	if err := writeFileAtomic(filePath, []byte(payload.Content), mode); err != nil {
		err := g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	loadKeys()

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
	})
}

// HandleEditUsersFileRead returns file content for read-only view or edit.
func (c *WebConsole) HandleEditUsersFileRead(g *gin.Context) {
	requested := g.Query("file")
	baseDir, filePath, err := c.resolveEditUsersFile(requested)
	if err != nil {
		err = g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	rel, err := filepath.Rel(baseDir, filePath)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status":  true,
		"file":    filepath.ToSlash(rel),
		"content": string(content),
	})
}

// HandleEditUsersValidate validates YAML content for auth-users files.
func (c *WebConsole) HandleEditUsersValidate(g *gin.Context) {
	payload := struct {
		Content string `json:"content"`
	}{}

	decoder := json.NewDecoder(g.Request.Body)
	if err := decoder.Decode(&payload); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	if err := validateAuthUsersYAML(payload.Content); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
		"valid":  true,
	})
}

// HandleEditUsersFileWrite validates and saves updated auth-users YAML file content.
func (c *WebConsole) HandleEditUsersFileWrite(g *gin.Context) {
	payload := struct {
		File    string `json:"file"`
		Content string `json:"content"`
	}{}

	decoder := json.NewDecoder(g.Request.Body)
	if err := decoder.Decode(&payload); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	if err := validateAuthUsersYAML(payload.Content); err != nil {
		err := g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	_, filePath, err := c.resolveEditUsersFile(payload.File)
	if err != nil {
		err = g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	mode := fileModeOrDefault(filePath, 0600)

	if err := writeFileAtomic(filePath, []byte(payload.Content), mode); err != nil {
		err := g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	loadAuthUsers()

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
	})
}

// HandleLogsTemplate renders the logs page.
func (c *WebConsole) HandleLogsTemplate(g *gin.Context) {
	g.HTML(http.StatusOK, "logs", c.templateData(true, true))
}

// HandleLogsFiles returns the list of forwarder log files.
func (c *WebConsole) HandleLogsFiles(g *gin.Context) {
	logsDir := strings.TrimSpace(viper.GetString("forwarders-log-dir"))
	if logsDir == "" {
		err := g.AbortWithError(http.StatusBadRequest, fmt.Errorf("forwarders-log-dir is empty"))
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	baseDir, err := filepath.Abs(logsDir)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	files, err := listManagedFiles(baseDir, false)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status":  true,
		"files":   files,
		"logsDir": baseDir,
	})
}

// HandleLogsFile returns tail content for a selected forwarder log file.
func (c *WebConsole) HandleLogsFile(g *gin.Context) {
	requested := g.Query("file")
	baseDir, filePath, err := c.resolveForwardersLogFile(requested)
	if err != nil {
		err = g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	lines := 100
	if linesRaw := strings.TrimSpace(g.Query("lines")); linesRaw != "" {
		if parsed, parseErr := strconv.Atoi(linesRaw); parseErr == nil {
			lines = parsed
		}
	}

	// B51: clamp before reading. The old code tailed the file with the raw
	// (unvalidated) line count and only clamped afterwards, so ?lines=999999999
	// or a negative value reached tailFileLines.
	if lines <= 0 {
		lines = 100
	}

	if lines > 5000 {
		lines = 5000
	}

	content, err := tailFileLines(filePath, lines)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	content = StripANSISequences(content)

	rel, err := filepath.Rel(baseDir, filePath)
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status":  true,
		"file":    filepath.ToSlash(rel),
		"lines":   lines,
		"content": content,
	})
}

// HandleLogsDownload streams the full selected forwarder log file.
func (c *WebConsole) HandleLogsDownload(g *gin.Context) {
	requested := g.Query("file")
	_, filePath, err := c.resolveForwardersLogFile(requested)
	if err != nil {
		err = g.AbortWithError(http.StatusBadRequest, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(filePath)))
	g.File(filePath)
}

// HandleHistoryTemplate handles rendering the history template.
func (c *WebConsole) HandleHistoryTemplate(g *gin.Context) {
	if !viper.GetBool("history-enabled") {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("history-enabled is false"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return
	}

	g.HTML(http.StatusOK, "history", c.templateData(true, true))
}

// HandleHistory returns in-memory connection history rows.
func (c *WebConsole) HandleHistory(g *gin.Context) {
	if !viper.GetBool("history-enabled") {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("history-enabled is false"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return
	}

	const defaultPageSize = 10

	data := map[string]any{
		"status": true,
	}

	rows := []map[string]any{}
	history := c.historySnapshot()
	search := strings.ToLower(strings.TrimSpace(g.Query("q")))
	page := 1
	if pageParam := g.Query("page"); pageParam != "" {
		if p, err := strconv.Atoi(pageParam); err == nil && p > 0 {
			page = p
		}
	}

	pageSize := defaultPageSize
	if pageSizeParam := g.Query("pageSize"); pageSizeParam != "" {
		if ps, err := strconv.Atoi(pageSizeParam); err == nil && ps > 0 {
			pageSize = ps
		}
	}

	if pageSize > defaultPageSize {
		pageSize = defaultPageSize
	}

	matchesHistory := func(entry ConnectionHistory) bool {
		if search == "" {
			return true
		}

		started := strings.ToLower(entry.StartedAt.Format(viper.GetString("time-format")))
		ended := strings.ToLower(entry.EndedAt.Format(viper.GetString("time-format")))
		ingressType := strings.ToLower(ingressLabel(entry.Ingress))

		return strings.Contains(strings.ToLower(entry.ID), search) ||
			strings.Contains(strings.ToLower(entry.RemoteAddr), search) ||
			strings.Contains(strings.ToLower(entry.Username), search) ||
			strings.Contains(started, search) ||
			strings.Contains(ended, search) ||
			strings.Contains(ingressType, search) ||
			strings.Contains(strings.ToLower(entry.Forwarder), search)
	}

	filtered := make([]ConnectionHistory, 0, len(history))
	for i := len(history) - 1; i >= 0; i-- {
		entry := history[i]
		if !matchesHistory(entry) {
			continue
		}

		filtered = append(filtered, entry)
	}

	total := len(filtered)
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}

	if totalPages > 0 && page > totalPages {
		page = totalPages
	}

	start := (page - 1) * pageSize
	end := start + pageSize

	if start > total {
		start = total
	}

	if end > total {
		end = total
	}

	for i := start; i < end; i++ {
		entry := filtered[i]
		transfer := fmt.Sprintf("IN %s MB / OUT %s MB", formatBytesToMB1Decimal(entry.DataInBytes), formatBytesToMB1Decimal(entry.DataOutBytes))
		ingressPort := strings.TrimSpace(entry.IngressPort)
		if ingressPort == "" {
			ingressPort = "Unknown"
		}
		rows = append(rows, map[string]any{
			"id":         entry.ID,
			"remoteAddr": entry.RemoteAddr,
			"username":   entry.Username,
			"forwarder":  entry.Forwarder,
			"ingress":    fmt.Sprintf("%s (:%s)", ingressLabel(entry.Ingress), ingressPort),
			"started":    entry.StartedAt.Format(viper.GetString("time-format")),
			"ended":      entry.EndedAt.Format(viper.GetString("time-format")),
			"duration":   formatDurationDDHHMMSS(entry.Duration),
			"transfer":   transfer,
		})
	}

	data["history"] = rows

	data["page"] = page
	data["pageSize"] = pageSize
	data["total"] = total
	data["totalPages"] = totalPages
	data["search"] = search
	g.JSON(http.StatusOK, data)
}

// HandleHistoryClear removes all in-memory history entries.
func (c *WebConsole) HandleHistoryClear(g *gin.Context) {
	if !viper.GetBool("history-enabled") {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("history-enabled is false"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return
	}

	c.HistoryLock.Lock()
	c.History = []ConnectionHistory{}
	c.HistoryLock.Unlock()

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
	})
}

// HandleHistoryDownload downloads in-memory history entries as CSV.
func (c *WebConsole) HandleHistoryDownload(g *gin.Context) {
	if !viper.GetBool("history-enabled") {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("history-enabled is false"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return
	}

	buffer := &bytes.Buffer{}
	writer := csv.NewWriter(buffer)

	err := writer.Write([]string{"ID", "Client Remote Address", "Username", "Forwarder", "Ingress", "Started", "Ended", "Duration", "Transfer"})
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	history := c.historySnapshot()
	for i := len(history) - 1; i >= 0; i-- {
		entry := history[i]
		transfer := fmt.Sprintf("IN %s MB / OUT %s MB", formatBytesToMB1Decimal(entry.DataInBytes), formatBytesToMB1Decimal(entry.DataOutBytes))
		csvIngressPort := strings.TrimSpace(entry.IngressPort)
		if csvIngressPort == "" {
			csvIngressPort = "Unknown"
		}
		err = writer.Write([]string{
			entry.ID,
			entry.RemoteAddr,
			entry.Username,
			entry.Forwarder,
			fmt.Sprintf("%s (:%s)", ingressLabel(entry.Ingress), csvIngressPort),
			entry.StartedAt.Format(viper.GetString("time-format")),
			entry.EndedAt.Format(viper.GetString("time-format")),
			formatDurationDDHHMMSS(entry.Duration),
			transfer,
		})
		if err != nil {
			err = g.AbortWithError(http.StatusInternalServerError, err)
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	// B52: the date layout was "20060201" (year-day-month); the Go reference
	// date for year-month-day is "20060102".
	filename := fmt.Sprintf("history-%s-%s.csv", time.Now().Format("20060102"), time.Now().Format("1504"))
	g.Header("Content-Description", "File Transfer")
	g.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	g.Data(http.StatusOK, "text/csv", buffer.Bytes())
}

// AddHistoryEntry appends a connection lifecycle record to in-memory history.
func (c *WebConsole) AddHistoryEntry(entry ConnectionHistory) {
	if c == nil {
		return
	}

	c.HistoryLock.Lock()
	defer c.HistoryLock.Unlock()

	c.History = append(c.History, entry)
	if maxHistoryEntries > 0 && len(c.History) > maxHistoryEntries {
		trimmed := make([]ConnectionHistory, maxHistoryEntries)
		copy(trimmed, c.History[len(c.History)-maxHistoryEntries:])
		c.History = trimmed
	}
}

func formatDurationDDHHMMSS(duration time.Duration) string {
	totalSeconds := int(duration.Seconds())
	if totalSeconds < 0 {
		totalSeconds = 0
	}

	days := totalSeconds / 86400
	hours := (totalSeconds % 86400) / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60

	pad := func(value int) string {
		if value < 10 {
			return "0" + strconv.Itoa(value)
		}

		return strconv.Itoa(value)
	}

	return fmt.Sprintf("%s:%s:%s:%s", pad(days), pad(hours), pad(minutes), pad(seconds))
}

func formatBytesToMB1Decimal(bytes int64) string {
	if bytes < 0 {
		bytes = 0
	}

	mb := float64(bytes) / (1024 * 1024)
	return fmt.Sprintf("%.1f", mb)
}

func parseDisconnectClientTarget(g *gin.Context) (string, error) {
	if g.Request.Method == http.MethodPost {
		payload := struct {
			RemoteAddr string `json:"remoteAddr"`
		}{}

		if err := json.NewDecoder(g.Request.Body).Decode(&payload); err != nil {
			return "", err
		}

		target := strings.TrimSpace(payload.RemoteAddr)
		if target == "" {
			return "", fmt.Errorf("remoteAddr is required")
		}

		return target, nil
	}

	target := strings.TrimSpace(strings.TrimPrefix(g.Request.URL.Path, "/_sish/api/disconnectclient/"))
	if target == "" {
		return "", fmt.Errorf("remoteAddr is required")
	}

	return target, nil
}

func decodeDisconnectRouteTargets(payload []byte) ([]string, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("routeData is required")
	}

	var many []string
	if err := json.Unmarshal(trimmed, &many); err == nil {
		targets := []string{}
		seen := map[string]struct{}{}
		for _, value := range many {
			target := strings.TrimSpace(value)
			if target == "" {
				continue
			}
			if _, ok := seen[target]; ok {
				continue
			}
			seen[target] = struct{}{}
			targets = append(targets, target)
		}

		if len(targets) == 0 {
			return nil, fmt.Errorf("routeData is required")
		}

		return targets, nil
	}

	var single string
	if err := json.Unmarshal(trimmed, &single); err == nil {
		target := strings.TrimSpace(single)
		if target == "" {
			return nil, fmt.Errorf("routeData is required")
		}

		return []string{target}, nil
	}

	target := strings.TrimSpace(string(trimmed))
	if target == "" {
		return nil, fmt.Errorf("routeData is required")
	}

	return []string{target}, nil
}

func parseDisconnectRouteTargets(g *gin.Context) ([]string, error) {
	if g.Request.Method == http.MethodPost {
		payload := struct {
			RouteData json.RawMessage `json:"routeData"`
		}{}

		if err := json.NewDecoder(g.Request.Body).Decode(&payload); err != nil {
			return nil, err
		}

		return decodeDisconnectRouteTargets(payload.RouteData)
	}

	path := strings.TrimPrefix(g.Request.URL.Path, "/_sish/api/disconnectroute/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("routeData is required")
	}

	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, err
		}
	}

	return decodeDisconnectRouteTargets(decoded)
}

// HandleTemplate handles rendering the console templates.
func (c *WebConsole) HandleTemplate(proxyUrl string, hostIsRoot bool, userIsAdmin bool, g *gin.Context) {
	if hostIsRoot && userIsAdmin {
		g.HTML(http.StatusOK, "routes", c.templateData(hostIsRoot, userIsAdmin))
		return
	}

	if c.RouteExists(proxyUrl) {
		g.HTML(http.StatusOK, "console", c.templateData(hostIsRoot, userIsAdmin))
		return
	}

	err := g.AbortWithError(http.StatusNotFound, fmt.Errorf("cannot find connection for host: %s", proxyUrl))
	if err != nil {
		log.Println("Aborting with error", err)
	}
}

// HandleWebSocket handles the websocket route.
func (c *WebConsole) HandleWebSocket(proxyUrl string, g *gin.Context) {
	conn, err := upgrader.Upgrade(g.Writer, g.Request, nil)
	if err != nil {
		log.Println(err)
		return
	}

	client := &WebClient{
		Conn:    conn,
		Console: c,
		Send:    make(chan []byte, defaultWebClientSendQueueSize),
		Done:    make(chan struct{}),
		Route:   proxyUrl,
	}

	c.AddClient(proxyUrl, client)

	go client.ReadPump()
	go client.Handle()
}

// HandleDisconnectClient handles the disconnection request for a SSH client.
func (c *WebConsole) HandleDisconnectClient(proxyUrl string, g *gin.Context) {
	client, err := parseDisconnectClientTarget(g)
	if err != nil {
		abortErr := g.AbortWithError(http.StatusBadRequest, err)
		if abortErr != nil {
			log.Println("Aborting with error", abortErr)
		}
		return
	}

	c.State.SSHConnections.Range(func(clientName string, holderConn *SSHConnection) bool {
		if clientName == client {
			holderConn.SetCloseInfo("server", "admin disconnect")
			holderConn.CleanUp(c.State)

			return false
		}

		return true
	})

	data := map[string]any{
		"status": true,
	}

	g.JSON(http.StatusOK, data)
}

// HandleDisconnectRoute handles the disconnection request for a forwarded route.
func (c *WebConsole) HandleDisconnectRoute(proxyUrl string, g *gin.Context) {
	targets, err := parseDisconnectRouteTargets(g)
	if err != nil {
		abortErr := g.AbortWithError(http.StatusBadRequest, err)

		if abortErr != nil {
			log.Println("Error aborting with error:", abortErr)
		}
		return
	}

	closed := 0
	for _, routeName := range targets {
		listenerTmp, ok := c.State.Listeners.Load(routeName)
		if !ok {
			continue
		}

		listener, ok := listenerTmp.(*ListenerHolder)
		if !ok {
			continue
		}

		if err := listener.Close(); err != nil {
			log.Println("Error closing listener:", err)
			continue
		}

		closed++
	}

	data := map[string]any{
		"status": true,
		"closed": closed,
	}

	g.JSON(http.StatusOK, data)
}

// HandleClients handles returning all connected SSH clients. This will
// also go through all of the forwarded connections for the SSH client and
// return them.
func (c *WebConsole) HandleClients(proxyUrl string, g *gin.Context) {
	data := map[string]any{
		"status": true,
	}

	censusSet := map[string]struct{}{}
	if viper.GetBool("census-enabled") {
		snapshot := GetCensusCacheSnapshot()
		for _, id := range snapshot.IDs {
			trimmed := strings.TrimSpace(id)
			if trimmed == "" {
				continue
			}

			censusSet[trimmed] = struct{}{}
		}
	}

	// Build the per-forward reverse indexes once. Re-deriving them inside the
	// per-connection loop made this handler quadratic in the number of forwards,
	// on an endpoint the console polls every second (B22).
	aliasIndex := map[string]map[string]any{}
	c.State.AliasListeners.Range(func(tcpAlias string, aliasHolder *AliasHolder) bool {
		if aliasHolder == nil {
			return true
		}
		for _, server := range aliasHolder.Balancer.Servers() {
			serverAddr, err := base64.StdEncoding.DecodeString(server.Host)
			if err != nil {
				log.Println("Error decoding server host:", err)
				continue
			}

			aliasAddress := string(serverAddr)
			if aliasIndex[aliasAddress] == nil {
				aliasIndex[aliasAddress] = map[string]any{}
			}
			aliasIndex[aliasAddress][tcpAlias] = aliasAddress
		}
		return true
	})

	tcpIndex := map[string]map[string]any{}
	c.State.TCPListeners.Range(func(tcpAlias string, tcpHolder *TCPHolder) bool {
		if tcpHolder == nil {
			return true
		}
		tcpHolder.Balancers.Range(func(ikey string, balancer *roundrobin.RoundRobin) bool {
			newAlias := tcpAlias
			if tcpHolder.SNIProxy {
				newAlias = fmt.Sprintf("%s-%s", tcpAlias, ikey)
			}

			for _, server := range balancer.Servers() {
				serverAddr, err := base64.StdEncoding.DecodeString(server.Host)
				if err != nil {
					log.Println("Error decoding server host:", err)
					continue
				}

				aliasAddress := string(serverAddr)
				if tcpIndex[aliasAddress] == nil {
					tcpIndex[aliasAddress] = map[string]any{}
				}
				tcpIndex[aliasAddress][newAlias] = aliasAddress
			}

			return true
		})
		return true
	})

	httpIndex := map[string]string{}
	c.State.HTTPListeners.Range(func(key string, httpHolder *HTTPHolder) bool {
		if httpHolder == nil || httpHolder.HTTPUrl == nil {
			return true
		}

		var userPass string
		password, _ := httpHolder.HTTPUrl.User.Password()
		if httpHolder.HTTPUrl.User.Username() != "" || password != "" {
			userPass = fmt.Sprintf("%s:%s@", httpHolder.HTTPUrl.User.Username(), password)
		}
		name := fmt.Sprintf("%s%s%s", userPass, httpHolder.HTTPUrl.Hostname(), httpHolder.HTTPUrl.Path)

		httpHolder.SSHConnections.Range(func(httpAddr string, _ *SSHConnection) bool {
			httpIndex[httpAddr] = name
			return true
		})

		return true
	})

	forwardIdx := buildForwardIndex(c.State)

	clients := map[string]map[string]any{}
	c.State.SSHConnections.Range(func(clientName string, sshConn *SSHConnection) bool {
		listeners := connectionListeners(sshConn)
		routeListeners := map[string]map[string]any{}

		tcpAliases := map[string]any{}
		listenerParts := map[string]any{}
		httpListeners := map[string]any{}

		for _, listener := range listeners {
			for alias, addr := range aliasIndex[listener] {
				tcpAliases[alias] = addr
			}

			for alias, addr := range tcpIndex[listener] {
				listenerParts[alias] = addr
			}

			if name, ok := httpIndex[listener]; ok {
				existing, _ := httpListeners[name].([]string)
				httpListeners[name] = append(existing, listener)
			}
		}

		routeListeners["tcpAliases"] = tcpAliases
		routeListeners["listeners"] = listenerParts
		routeListeners["httpListeners"] = httpListeners

		pubKey := ""
		pubKeyFingerprint := ""
		if sshConn.SSHConn.Permissions != nil {
			if _, ok := sshConn.SSHConn.Permissions.Extensions["pubKey"]; ok {
				pubKey = sshConn.SSHConn.Permissions.Extensions["pubKey"]
				pubKeyFingerprint = sshConn.SSHConn.Permissions.Extensions["pubKeyFingerprint"]
			}
		}

		connectionID := strings.TrimSpace(sshConn.GetConnectionID())
		_, isCensused := censusSet[connectionID]

		dataInBytes := int64(0)
		dataOutBytes := int64(0)
		profile := sshConn.GetBandwidthProfile()
		if profile != nil {
			dataInBytes = profile.DataInBytes.Load()
			dataOutBytes = profile.DataOutBytes.Load()
		}

		forwarder := resolveConnectionForwardersIndexed(sshConn, forwardIdx)
		displayID, displayForwarder := displayConnectionIdentityAndForwarder(sshConn, sshConn.GetConnectionID(), listeners, forwarder)

		clients[clientName] = map[string]any{
			"id":                displayID,
			"isCensused":        isCensused,
			"remoteAddr":        sshConn.SSHConn.RemoteAddr().String(),
			"user":              sshConn.SSHConn.User(),
			"version":           string(sshConn.SSHConn.ClientVersion()),
			"session":           sshConn.SSHConn.SessionID(),
			"connectedAt":       sshConn.ConnectedAt.UTC().Format(time.RFC3339),
			"connectedAtPretty": sshConn.ConnectedAt.Format(viper.GetString("time-format")),
			"connectionNote":    sshConn.GetConnectionNote(),
			"pubKey":            pubKey,
			"pubKeyFingerprint": pubKeyFingerprint,
			"dataInBytes":       dataInBytes,
			"dataOutBytes":      dataOutBytes,
			"forwarder":         displayForwarder,
			"clientInfo":        buildSishClientInfoRows(sshConn),
			"configInfo":        buildSishConfigInfoRows(sshConn.SSHConn.User()),
			"ingressInfo":       buildSishIngressInfoRows(sshConn),
			"listeners":         listeners,
			"routeListeners":    routeListeners,
		}

		return true
	})

	data["clients"] = clients

	g.JSON(http.StatusOK, data)
}

// RouteToken returns the route token for a specific route.
func (c *WebConsole) RouteToken(route string) (string, bool) {
	token, ok := c.RouteTokens.Load(route)
	routeToken := ""

	if ok {
		routeToken = token
	}

	return routeToken, ok
}

// RouteExists check if a route token exists.
func (c *WebConsole) RouteExists(route string) bool {
	_, ok := c.RouteToken(route)
	return ok
}

// AddRoute adds a route token to the console.
func (c *WebConsole) AddRoute(route string, token string) {
	c.ClientsLock.Lock()
	defer c.ClientsLock.Unlock()

	if _, ok := c.Clients.Load(route); !ok {
		c.Clients.Store(route, []*WebClient{})
	}

	c.RouteTokens.Store(route, token)
}

// RemoveRoute removes a route token from the console.
func (c *WebConsole) RemoveRoute(route string) {
	c.ClientsLock.Lock()
	clients, ok := c.Clients.Load(route)
	if ok {
		copied := make([]*WebClient, len(clients))
		copy(copied, clients)
		clients = copied
	}
	c.Clients.Delete(route)
	c.ClientsLock.Unlock()

	c.RouteTokens.Delete(route)

	if !ok {
		return
	}

	for _, client := range clients {
		client.Close()
	}
}

// AddClient adds a client to the console route.
func (c *WebConsole) AddClient(route string, w *WebClient) {
	c.ClientsLock.Lock()
	defer c.ClientsLock.Unlock()

	clients, ok := c.Clients.Load(route)

	if !ok {
		w.Close()
		return
	}

	updated := make([]*WebClient, len(clients), len(clients)+1)
	copy(updated, clients)
	updated = append(updated, w)

	c.Clients.Store(route, updated)
}

// RemoveClient removes a client from the console route.
func (c *WebConsole) RemoveClient(route string, w *WebClient) {
	c.ClientsLock.Lock()
	defer c.ClientsLock.Unlock()

	clients, ok := c.Clients.Load(route)

	if !ok {
		return
	}

	found := false
	toRemove := 0
	for i, client := range clients {
		if client == w {
			found = true
			toRemove = i
			break
		}
	}

	if found {
		updated := make([]*WebClient, 0, len(clients)-1)
		updated = append(updated, clients[:toRemove]...)
		updated = append(updated, clients[toRemove+1:]...)
		c.Clients.Store(route, updated)
	}
}

// BroadcastRoute sends a message to all clients on a route.
func (c *WebConsole) BroadcastRoute(route string, message []byte) {
	c.ClientsLock.RLock()
	clients, ok := c.Clients.Load(route)
	if ok {
		copied := make([]*WebClient, len(clients))
		copy(copied, clients)
		clients = copied
	}
	c.ClientsLock.RUnlock()

	if !ok {
		return
	}

	toDrop := []*WebClient{}
	for _, client := range clients {
		if client == nil || client.closed.Load() {
			toDrop = append(toDrop, client)
			continue
		}

		select {
		case client.Send <- message:
		default:
			toDrop = append(toDrop, client)
		}
	}

	for _, client := range toDrop {
		if client == nil {
			continue
		}

		client.Close()
		c.RemoveClient(route, client)
	}
}

// Handle is the only place socket reads and writes happen.
func (c *WebClient) Handle() {
	defer func() {
		c.Console.RemoveClient(c.Route, c)
		c.Close()
	}()

	for {
		select {
		case <-c.Done:
			return
		case message := <-c.Send:
			// Without a write deadline a wedged viewer parks this goroutine and
			// its socket indefinitely, since a half-open TCP connection never
			// errors on its own.
			if err := c.Conn.SetWriteDeadline(time.Now().Add(webClientWriteTimeout)); err != nil {
				return
			}

			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}

			_, err = w.Write(message)
			if err != nil {
				_ = w.Close()
				return
			}

			if err := w.Close(); err != nil {
				return
			}
		}
	}
}

func (c *WebClient) ReadPump() {
	defer c.Close()

	for {
		if _, _, err := c.Conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (c *WebClient) Close() {
	if c == nil {
		return
	}

	c.closeMu.Do(func() {
		c.closed.Store(true)
		if c.Done != nil {
			close(c.Done)
		}

		if c.Conn != nil {
			if err := c.Conn.Close(); err != nil {
				log.Println("Error closing websocket connection:", err)
			}
		}
	})
}
