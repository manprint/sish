package utils

import (
	"bytes"
	"encoding/csv"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
	"github.com/vulcand/oxy/roundrobin"
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

// WebClient represents a primitive web console client. It maintains
// references that allow us to communicate and track a client connection.
type WebClient struct {
	Conn    *websocket.Conn
	Console *WebConsole
	Send    chan []byte
	Route   string
}

// WebConsole represents the data structure that stores web console client information.
type WebConsole struct {
	Clients     *syncmap.Map[string, []*WebClient]
	RouteTokens *syncmap.Map[string, string]
	History     []ConnectionHistory
	HistoryLock *sync.RWMutex
	State       *State
}

// ConnectionHistory contains immutable connection lifecycle information.
type ConnectionHistory struct {
	ID         string
	RemoteAddr string
	Username   string
	StartedAt  time.Time
	EndedAt    time.Time
	Duration   time.Duration
}

// NewWebConsole sets up the WebConsole.
func NewWebConsole() *WebConsole {
	return &WebConsole{
		Clients:     syncmap.New[string, []*WebClient](),
		RouteTokens: syncmap.New[string, string](),
		History:     []ConnectionHistory{},
		HistoryLock: &sync.RWMutex{},
	}
}

// HandleRequest handles an incoming web request, handles auth, and then routes it.
func (c *WebConsole) HandleRequest(proxyUrl string, hostIsRoot bool, g *gin.Context) {
	userAuthed := false
	userIsAdmin := false
	if (viper.GetBool("admin-console") && viper.GetString("admin-console-token") != "") && (g.Request.URL.Query().Get("x-authorization") == viper.GetString("admin-console-token") || g.Request.Header.Get("x-authorization") == viper.GetString("admin-console-token")) {
		userIsAdmin = true
		userAuthed = true
	}

	tokenInterface, ok := c.RouteTokens.Load(proxyUrl)
	if ok {
		routeToken := tokenInterface
		if routeToken == "" {
			ok = false
		}

		if viper.GetBool("service-console") && ok && (g.Request.URL.Query().Get("x-authorization") == routeToken || g.Request.Header.Get("x-authorization") == routeToken) {
			userAuthed = true
		}
	}

	if strings.HasPrefix(g.Request.URL.Path, "/_sish/console/ws") && userAuthed {
		c.HandleWebSocket(proxyUrl, g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/history/download") && userIsAdmin {
		c.HandleHistoryDownload(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/history/clear") && userIsAdmin {
		if g.Request.Method != http.MethodPost {
			err := g.AbortWithError(http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}

		c.HandleHistoryClear(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/history") && userIsAdmin {
		c.HandleHistory(g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/history") && userIsAdmin {
		c.HandleHistoryTemplate(g)
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
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/console") && userAuthed {
		c.HandleTemplate(proxyUrl, hostIsRoot, userIsAdmin, g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/disconnectclient/") && userIsAdmin {
		c.HandleDisconnectClient(proxyUrl, g)
		return
	} else if strings.HasPrefix(g.Request.URL.Path, "/_sish/api/disconnectroute/") && userIsAdmin {
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

	parts := strings.SplitN(credentials, ":", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("admin-consolle-editkeys-credentials format is invalid"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return false
	}

	username, password, ok := g.Request.BasicAuth()
	if !ok || username != parts[0] || password != parts[1] {
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

	parts := strings.SplitN(credentials, ":", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		err := g.AbortWithError(http.StatusForbidden, fmt.Errorf("admin-consolle-editusers-credentials format is invalid"))
		if err != nil {
			log.Println("Aborting with error", err)
		}

		return false
	}

	username, password, ok := g.Request.BasicAuth()
	if !ok || username != parts[0] || password != parts[1] {
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
	g.HTML(http.StatusOK, "editkeys", nil)
}

// HandleEditUsersTemplate renders the editusers page.
func (c *WebConsole) HandleEditUsersTemplate(g *gin.Context) {
	g.HTML(http.StatusOK, "editusers", nil)
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

	relToBase, err := filepath.Rel(baseDir, resolvedAbs)
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
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("yaml content is empty")
	}

	parsedUsers := map[string]any{}
	if err := yaml.Unmarshal([]byte(content), &parsedUsers); err != nil {
		return fmt.Errorf("invalid yaml: %w", err)
	}

	return nil
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
		"status":        true,
		"files":         files,
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

	mode := os.FileMode(0600)
	if stat, statErr := os.Stat(filePath); statErr == nil {
		mode = stat.Mode().Perm()
	}

	if err := os.WriteFile(filePath, []byte(payload.Content), mode); err != nil {
		err := g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

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

	mode := os.FileMode(0600)
	if stat, statErr := os.Stat(filePath); statErr == nil {
		mode = stat.Mode().Perm()
	}

	if err := os.WriteFile(filePath, []byte(payload.Content), mode); err != nil {
		err := g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
	})
}

// HandleHistoryTemplate handles rendering the history template.
func (c *WebConsole) HandleHistoryTemplate(g *gin.Context) {
	g.HTML(http.StatusOK, "history", nil)
}

// HandleHistory returns in-memory connection history rows.
func (c *WebConsole) HandleHistory(g *gin.Context) {
	const defaultPageSize = 10

	data := map[string]any{
		"status": true,
	}

	rows := []map[string]any{}
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

		return strings.Contains(strings.ToLower(entry.ID), search) ||
			strings.Contains(strings.ToLower(entry.RemoteAddr), search) ||
			strings.Contains(strings.ToLower(entry.Username), search) ||
			strings.Contains(started, search) ||
			strings.Contains(ended, search)
	}

	c.HistoryLock.RLock()
	filtered := make([]ConnectionHistory, 0, len(c.History))
	for i := len(c.History) - 1; i >= 0; i-- {
		entry := c.History[i]
		if !matchesHistory(entry) {
			continue
		}

		filtered = append(filtered, entry)
	}

	total := len(filtered)
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
		rows = append(rows, map[string]any{
			"id":         entry.ID,
			"remoteAddr": entry.RemoteAddr,
			"username":   entry.Username,
			"started":    entry.StartedAt.Format(viper.GetString("time-format")),
			"ended":      entry.EndedAt.Format(viper.GetString("time-format")),
			"duration":   formatDurationDDHHMMSS(entry.Duration),
		})
	}
	c.HistoryLock.RUnlock()

	data["history"] = rows
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}

	if totalPages > 0 && page > totalPages {
		page = totalPages
	}

	data["page"] = page
	data["pageSize"] = pageSize
	data["total"] = total
	data["totalPages"] = totalPages
	data["search"] = search
	g.JSON(http.StatusOK, data)
}

// HandleHistoryClear removes all in-memory history entries.
func (c *WebConsole) HandleHistoryClear(g *gin.Context) {
	c.HistoryLock.Lock()
	c.History = []ConnectionHistory{}
	c.HistoryLock.Unlock()

	g.JSON(http.StatusOK, map[string]any{
		"status": true,
	})
}

// HandleHistoryDownload downloads in-memory history entries as CSV.
func (c *WebConsole) HandleHistoryDownload(g *gin.Context) {
	buffer := &bytes.Buffer{}
	writer := csv.NewWriter(buffer)

	err := writer.Write([]string{"ID", "Client Remote Address", "Username", "Started", "Ended", "Duration"})
	if err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	c.HistoryLock.RLock()
	for i := len(c.History) - 1; i >= 0; i-- {
		entry := c.History[i]
		err = writer.Write([]string{
			entry.ID,
			entry.RemoteAddr,
			entry.Username,
			entry.StartedAt.Format(viper.GetString("time-format")),
			entry.EndedAt.Format(viper.GetString("time-format")),
			formatDurationDDHHMMSS(entry.Duration),
		})
		if err != nil {
			c.HistoryLock.RUnlock()
			err = g.AbortWithError(http.StatusInternalServerError, err)
			if err != nil {
				log.Println("Aborting with error", err)
			}
			return
		}
	}
	c.HistoryLock.RUnlock()

	writer.Flush()
	if err := writer.Error(); err != nil {
		err = g.AbortWithError(http.StatusInternalServerError, err)
		if err != nil {
			log.Println("Aborting with error", err)
		}
		return
	}

	filename := fmt.Sprintf("history-%s-%s.csv", time.Now().Format("20060201"), time.Now().Format("1504"))
	g.Header("Content-Description", "File Transfer")
	g.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	g.Data(http.StatusOK, "text/csv", buffer.Bytes())
}
// AddHistoryEntry appends a connection lifecycle record to in-memory history.
func (c *WebConsole) AddHistoryEntry(entry ConnectionHistory) {
	c.HistoryLock.Lock()
	defer c.HistoryLock.Unlock()

	c.History = append(c.History, entry)
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

// HandleTemplate handles rendering the console templates.
func (c *WebConsole) HandleTemplate(proxyUrl string, hostIsRoot bool, userIsAdmin bool, g *gin.Context) {
	if hostIsRoot && userIsAdmin {
		g.HTML(http.StatusOK, "routes", nil)
		return
	}

	if c.RouteExists(proxyUrl) {
		g.HTML(http.StatusOK, "console", nil)
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
		Send:    make(chan []byte),
		Route:   proxyUrl,
	}

	c.AddClient(proxyUrl, client)

	go client.Handle()
}

// HandleDisconnectClient handles the disconnection request for a SSH client.
func (c *WebConsole) HandleDisconnectClient(proxyUrl string, g *gin.Context) {
	client := strings.TrimPrefix(g.Request.URL.Path, "/_sish/api/disconnectclient/")

	c.State.SSHConnections.Range(func(clientName string, holderConn *SSHConnection) bool {
		if clientName == client {
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
	route := strings.Split(strings.TrimPrefix(g.Request.URL.Path, "/_sish/api/disconnectroute/"), "/")
	encRouteName := route[1]

	decRouteName, err := base64.StdEncoding.DecodeString(encRouteName)
	if err != nil {
		log.Println("Error decoding route name:", err)
		err := g.AbortWithError(http.StatusInternalServerError, err)

		if err != nil {
			log.Println("Error aborting with error:", err)
		}
		return
	}

	routeName := string(decRouteName)

	listenerTmp, ok := c.State.Listeners.Load(routeName)
	if ok {
		listener, ok := listenerTmp.(*ListenerHolder)

		if ok {
			err := listener.Close()
			if err != nil {
				log.Println("Error closing listener:", err)
			}
		}
	}

	data := map[string]any{
		"status": true,
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

	clients := map[string]map[string]any{}
	c.State.SSHConnections.Range(func(clientName string, sshConn *SSHConnection) bool {
		listeners := []string{}
		routeListeners := map[string]map[string]any{}

		sshConn.Listeners.Range(func(name string, val net.Listener) bool {
			if name != "" {
				listeners = append(listeners, name)
			}

			return true
		})

		tcpAliases := map[string]any{}
		c.State.AliasListeners.Range(func(tcpAlias string, aliasHolder *AliasHolder) bool {
			for _, v := range listeners {
				for _, server := range aliasHolder.Balancer.Servers() {
					serverAddr, err := base64.StdEncoding.DecodeString(server.Host)
					if err != nil {
						log.Println("Error decoding server host:", err)
						continue
					}

					aliasAddress := string(serverAddr)

					if v == aliasAddress {
						tcpAliases[tcpAlias] = aliasAddress
					}
				}
			}

			return true
		})

		listenerParts := map[string]any{}
		c.State.TCPListeners.Range(func(tcpAlias string, aliasHolder *TCPHolder) bool {
			for _, v := range listeners {
				aliasHolder.Balancers.Range(func(ikey string, balancer *roundrobin.RoundRobin) bool {
					newAlias := tcpAlias
					if aliasHolder.SNIProxy {
						newAlias = fmt.Sprintf("%s-%s", tcpAlias, ikey)
					}

					for _, server := range balancer.Servers() {
						serverAddr, err := base64.StdEncoding.DecodeString(server.Host)
						if err != nil {
							log.Println("Error decoding server host:", err)
							continue
						}

						aliasAddress := string(serverAddr)

						if v == aliasAddress {
							listenerParts[newAlias] = aliasAddress
						}
					}

					return true
				})
			}

			return true
		})

		httpListeners := map[string]any{}
		c.State.HTTPListeners.Range(func(key string, httpHolder *HTTPHolder) bool {
			listenerHandlers := []string{}
			httpHolder.SSHConnections.Range(func(httpAddr string, val *SSHConnection) bool {
				for _, v := range listeners {
					if v == httpAddr {
						listenerHandlers = append(listenerHandlers, httpAddr)
					}
				}
				return true
			})

			if len(listenerHandlers) > 0 {
				var userPass string
				password, _ := httpHolder.HTTPUrl.User.Password()
				if httpHolder.HTTPUrl.User.Username() != "" || password != "" {
					userPass = fmt.Sprintf("%s:%s@", httpHolder.HTTPUrl.User.Username(), password)
				}

				httpListeners[fmt.Sprintf("%s%s%s", userPass, httpHolder.HTTPUrl.Hostname(), httpHolder.HTTPUrl.Path)] = listenerHandlers
			}

			return true
		})

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

		clients[clientName] = map[string]any{
			"id":                sshConn.ConnectionID,
			"remoteAddr":        sshConn.SSHConn.RemoteAddr().String(),
			"user":              sshConn.SSHConn.User(),
			"version":           string(sshConn.SSHConn.ClientVersion()),
			"session":           sshConn.SSHConn.SessionID(),
			"connectedAt":       sshConn.ConnectedAt.UTC().Format(time.RFC3339),
			"connectedAtPretty": sshConn.ConnectedAt.Format(viper.GetString("time-format")),
			"connectionNote":    sshConn.ConnectionNote,
			"pubKey":            pubKey,
			"pubKeyFingerprint": pubKeyFingerprint,
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
	c.Clients.LoadOrStore(route, []*WebClient{})
	c.RouteTokens.Store(route, token)
}

// RemoveRoute removes a route token from the console.
func (c *WebConsole) RemoveRoute(route string) {
	clients, ok := c.Clients.Load(route)

	if !ok {
		return
	}

	for _, client := range clients {
		err := client.Conn.Close()
		if err != nil {
			log.Println("Error closing websocket connection:", err)
		}
	}

	c.Clients.Delete(route)
	c.RouteTokens.Delete(route)
}

// AddClient adds a client to the console route.
func (c *WebConsole) AddClient(route string, w *WebClient) {
	clients, ok := c.Clients.Load(route)

	if !ok {
		return
	}

	clients = append(clients, w)

	c.Clients.Store(route, clients)
}

// RemoveClient removes a client from the console route.
func (c *WebConsole) RemoveClient(route string, w *WebClient) {
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
		clients[toRemove] = clients[len(clients)-1]
		c.Clients.Store(route, clients[:len(clients)-1])
	}
}

// BroadcastRoute sends a message to all clients on a route.
func (c *WebConsole) BroadcastRoute(route string, message []byte) {
	clients, ok := c.Clients.Load(route)

	if !ok {
		return
	}

	for _, client := range clients {
		client.Send <- message
	}
}

// Handle is the only place socket reads and writes happen.
func (c *WebClient) Handle() {
	defer func() {
		err := c.Conn.Close()
		if err != nil {
			log.Println("Error closing websocket connection:", err)
		}

		c.Console.RemoveClient(c.Route, c)
	}()

	for message := range c.Send {
		w, err := c.Conn.NextWriter(websocket.TextMessage)
		if err != nil {
			return
		}

		_, err = w.Write(message)
		if err != nil {
			return
		}

		if err := w.Close(); err != nil {
			return
		}
	}

	err := c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
	if err != nil {
		log.Println("Error writing to websocket:", err)
	}
}
