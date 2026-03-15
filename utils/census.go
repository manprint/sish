package utils

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

type censusEntry struct {
	ID string `yaml:"id"`
}

// CensusIDSource tracks where a census ID was found.
type CensusIDSource struct {
	URL   bool     `json:"url"`
	Files []string `json:"files"`
}

type censusCache struct {
	IDs         []string
	IDSources   map[string]CensusIDSource
	LastRefresh time.Time
	LastError   string
	URLFiles    []string // files read from census-directory in last refresh
}

var (
	censusCacheHolder = censusCache{IDs: []string{}, IDSources: map[string]CensusIDSource{}}
	censusCacheLock   sync.RWMutex
)

func normalizeCensusIDs(entries []censusEntry) []string {
	seen := map[string]struct{}{}
	ids := []string{}

	for _, e := range entries {
		id := strings.TrimSpace(e.ID)
		if id == "" {
			continue
		}

		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	sort.Strings(ids)
	return ids
}

func downloadCensusBody(censusURL string) ([]byte, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(censusURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("census-url returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func parseCensusBody(body []byte) ([]string, error) {
	entries := []censusEntry{}
	if err := yaml.Unmarshal(body, &entries); err != nil {
		return nil, err
	}

	return normalizeCensusIDs(entries), nil
}

// censusFileResult holds IDs found in a single census directory file.
type censusFileResult struct {
	FileName string
	IDs      []string
}

// loadCensusDirectoryFiles reads all YAML files from census-directory and returns per-file results.
func loadCensusDirectoryFiles() ([]censusFileResult, error) {
	dir := strings.TrimSpace(viper.GetString("census-directory"))
	if dir == "" {
		return nil, nil
	}

	baseDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("census-directory resolve error: %w", err)
	}

	dirEntries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("census-directory read error: %w", err)
	}

	var results []censusFileResult
	for _, entry := range dirEntries {
		if entry.IsDir() {
			continue
		}

		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		content, err := os.ReadFile(filepath.Join(baseDir, entry.Name()))
		if err != nil {
			log.Printf("census-directory: unable to read %s: %v", entry.Name(), err)
			continue
		}

		var entries []censusEntry
		if err := yaml.Unmarshal(content, &entries); err != nil {
			log.Printf("census-directory: unable to parse %s: %v", entry.Name(), err)
			continue
		}

		ids := normalizeCensusIDs(entries)
		results = append(results, censusFileResult{FileName: entry.Name(), IDs: ids})
	}

	return results, nil
}

// FetchCensusSource downloads census-url content and validates YAML structure.
func FetchCensusSource() (string, []byte, []string, error) {
	censusURL := strings.TrimSpace(viper.GetString("census-url"))
	if censusURL == "" {
		return "", nil, nil, fmt.Errorf("census-url is empty")
	}

	body, err := downloadCensusBody(censusURL)
	if err != nil {
		return censusURL, nil, nil, err
	}

	ids, err := parseCensusBody(body)
	if err != nil {
		return censusURL, body, nil, err
	}

	return censusURL, body, ids, nil
}

// ValidateCensusYAML validates that content is a valid census YAML (list of entries with id field).
func ValidateCensusYAML(content []byte) error {
	var entries []censusEntry
	if err := yaml.Unmarshal(content, &entries); err != nil {
		return fmt.Errorf("invalid yaml: %w", err)
	}
	return nil
}

// RefreshCensusCache downloads and refreshes census IDs from census-url and census-directory.
func RefreshCensusCache() error {
	if !viper.GetBool("census-enabled") {
		censusCacheLock.Lock()
		censusCacheHolder = censusCache{IDs: []string{}, IDSources: map[string]CensusIDSource{}}
		censusCacheLock.Unlock()
		return nil
	}

	urlEnabled := viper.GetBool("strict-id-censed-url")
	filesEnabled := viper.GetBool("strict-id-censed-files")
	censusURL := strings.TrimSpace(viper.GetString("census-url"))
	censusDir := strings.TrimSpace(viper.GetString("census-directory"))

	if !urlEnabled && !filesEnabled {
		censusCacheLock.Lock()
		censusCacheHolder = censusCache{IDs: []string{}, IDSources: map[string]CensusIDSource{}}
		censusCacheHolder.LastRefresh = time.Now()
		censusCacheHolder.LastError = "both strict-id-censed-url and strict-id-censed-files are disabled"
		censusCacheLock.Unlock()
		return nil
	}

	idSources := map[string]CensusIDSource{}
	var errors []string
	var dirFileNames []string

	// Load from URL
	if urlEnabled && censusURL != "" {
		body, err := downloadCensusBody(censusURL)
		if err != nil {
			errors = append(errors, fmt.Sprintf("census-url: %v", err))
		} else {
			ids, err := parseCensusBody(body)
			if err != nil {
				errors = append(errors, fmt.Sprintf("census-url parse: %v", err))
			} else {
				for _, id := range ids {
					src := idSources[id]
					src.URL = true
					idSources[id] = src
				}
			}
		}
	}

	// Load from directory files
	if filesEnabled && censusDir != "" {
		fileResults, err := loadCensusDirectoryFiles()
		if err != nil {
			errors = append(errors, err.Error())
		} else {
			for _, fr := range fileResults {
				dirFileNames = append(dirFileNames, fr.FileName)
				for _, id := range fr.IDs {
					src := idSources[id]
					src.Files = appendUnique(src.Files, fr.FileName)
					idSources[id] = src
				}
			}
		}
	}

	merged := make([]string, 0, len(idSources))
	for id := range idSources {
		merged = append(merged, id)
	}
	sort.Strings(merged)

	censusCacheLock.Lock()
	censusCacheHolder.IDs = merged
	censusCacheHolder.IDSources = idSources
	censusCacheHolder.LastRefresh = time.Now()
	censusCacheHolder.URLFiles = dirFileNames
	if len(errors) > 0 {
		censusCacheHolder.LastError = strings.Join(errors, "; ")
	} else {
		censusCacheHolder.LastError = ""
	}
	censusCacheLock.Unlock()

	if len(errors) > 0 && len(merged) == 0 {
		return fmt.Errorf("%s", strings.Join(errors, "; "))
	}

	return nil
}

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

// StartCensusRefresher starts automatic census refresh loop if enabled.
func StartCensusRefresher() {
	if !viper.GetBool("census-enabled") {
		return
	}

	_ = RefreshCensusCache()

	refreshEvery := viper.GetDuration("census-refresh-time")
	if refreshEvery <= 0 {
		refreshEvery = 2 * time.Minute
	}

	go func() {
		ticker := time.NewTicker(refreshEvery)
		defer ticker.Stop()

		for range ticker.C {
			_ = RefreshCensusCache()
		}
	}()
}

// GetCensusCacheSnapshot returns a safe snapshot of cached census data.
func GetCensusCacheSnapshot() censusCache {
	censusCacheLock.RLock()
	defer censusCacheLock.RUnlock()

	ids := make([]string, len(censusCacheHolder.IDs))
	copy(ids, censusCacheHolder.IDs)

	sources := make(map[string]CensusIDSource, len(censusCacheHolder.IDSources))
	for k, v := range censusCacheHolder.IDSources {
		files := make([]string, len(v.Files))
		copy(files, v.Files)
		sources[k] = CensusIDSource{URL: v.URL, Files: files}
	}

	urlFiles := make([]string, len(censusCacheHolder.URLFiles))
	copy(urlFiles, censusCacheHolder.URLFiles)

	return censusCache{
		IDs:         ids,
		IDSources:   sources,
		LastRefresh: censusCacheHolder.LastRefresh,
		LastError:   censusCacheHolder.LastError,
		URLFiles:    urlFiles,
	}
}

// IsStrictIDCensedEnabled returns true when census is enabled and at least one strict mode is active.
func IsStrictIDCensedEnabled() bool {
	if !viper.GetBool("census-enabled") {
		return false
	}
	return viper.GetBool("strict-id-censed") || viper.GetBool("strict-id-censed-url") || viper.GetBool("strict-id-censed-files")
}

// IsIDCensed checks whether an ID is currently present in the census cache.
func IsIDCensed(id string) bool {
	checkID := strings.TrimSpace(id)
	if checkID == "" {
		return false
	}

	snapshot := GetCensusCacheSnapshot()
	for _, censusID := range snapshot.IDs {
		if censusID == checkID {
			return true
		}
	}

	return false
}

// GetIDSource returns the source information for a given census ID.
func GetIDSource(id string) CensusIDSource {
	censusCacheLock.RLock()
	defer censusCacheLock.RUnlock()
	src, ok := censusCacheHolder.IDSources[id]
	if !ok {
		return CensusIDSource{}
	}
	files := make([]string, len(src.Files))
	copy(files, src.Files)
	return CensusIDSource{URL: src.URL, Files: files}
}
