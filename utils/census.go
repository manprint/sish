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
	ID   string `yaml:"id"`
	Note string `yaml:"note,omitempty"`
}

// CensusIDSource tracks where a census ID was found.
type CensusIDSource struct {
	URL   bool     `json:"url"`
	Files []string `json:"files"`
}

// CensusIDInfo holds source and note for a census ID.
type CensusIDInfo struct {
	Source CensusIDSource
	Note   string
}

type censusCache struct {
	IDs         []string
	IDSources   map[string]CensusIDSource
	IDNotes     map[string]string
	LastRefresh time.Time
	LastError   string
	URLFiles    []string // files read from census-directory in last refresh
}

var (
	censusCacheHolder = censusCache{IDs: []string{}, IDSources: map[string]CensusIDSource{}, IDNotes: map[string]string{}}
	censusCacheLock   sync.RWMutex

	// censusRefreshLock serialises RefreshCensusCache so the ticker, a manual
	// refresh and an editcensus save cannot interleave their slow downloads and
	// let an older, slower refresh clobber newer data (B36).
	censusRefreshLock sync.Mutex
)

// maxCensusBodySize bounds the census-url response read so a hostile or
// misconfigured endpoint cannot exhaust memory (B37). 16 MiB is far above any
// realistic ID list.
const maxCensusBodySize = 16 << 20

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

	// B37: cap the read so a huge/garbage body cannot exhaust memory. Read one
	// byte past the limit to detect overflow.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCensusBodySize+1))
	if err != nil {
		return nil, err
	}

	if int64(len(body)) > maxCensusBodySize {
		return nil, fmt.Errorf("census-url body exceeds %d bytes", maxCensusBodySize)
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

func parseCensusEntries(body []byte) ([]censusEntry, error) {
	var entries []censusEntry
	if err := yaml.Unmarshal(body, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// censusFileResult holds entries found in a single census directory file.
type censusFileResult struct {
	FileName string
	Entries  []censusEntry
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

		results = append(results, censusFileResult{FileName: entry.Name(), Entries: entries})
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
	// B36: serialise the whole refresh so overlapping callers cannot interleave
	// their downloads and have a slower one overwrite fresher data.
	censusRefreshLock.Lock()
	defer censusRefreshLock.Unlock()

	if !viper.GetBool("census-enabled") {
		censusCacheLock.Lock()
		censusCacheHolder = censusCache{IDs: []string{}, IDSources: map[string]CensusIDSource{}, IDNotes: map[string]string{}}
		censusCacheLock.Unlock()
		return nil
	}

	urlEnabled := viper.GetBool("strict-id-censed-url")
	filesEnabled := viper.GetBool("strict-id-censed-files")
	censusURL := strings.TrimSpace(viper.GetString("census-url"))
	censusDir := strings.TrimSpace(viper.GetString("census-directory"))

	if !urlEnabled && !filesEnabled {
		censusCacheLock.Lock()
		censusCacheHolder = censusCache{IDs: []string{}, IDSources: map[string]CensusIDSource{}, IDNotes: map[string]string{}}
		censusCacheHolder.LastRefresh = time.Now()
		censusCacheHolder.LastError = "both strict-id-censed-url and strict-id-censed-files are disabled"
		censusCacheLock.Unlock()
		return nil
	}

	idSources := map[string]CensusIDSource{}
	idNotes := map[string]string{}
	var errors []string
	var dirFileNames []string
	urlLoadFailed := false
	filesLoadFailed := false

	// Load from URL
	if urlEnabled && censusURL != "" {
		body, err := downloadCensusBody(censusURL)
		if err != nil {
			errors = append(errors, fmt.Sprintf("census-url: %v", err))
			urlLoadFailed = true
		} else {
			entries, err := parseCensusEntries(body)
			if err != nil {
				errors = append(errors, fmt.Sprintf("census-url parse: %v", err))
				urlLoadFailed = true
			} else {
				for _, e := range entries {
					id := strings.TrimSpace(e.ID)
					if id == "" {
						continue
					}
					src := idSources[id]
					src.URL = true
					idSources[id] = src
					if note := strings.TrimSpace(e.Note); note != "" {
						idNotes[id] = note
					}
				}
			}
		}
	}

	// Load from directory files
	if filesEnabled && censusDir != "" {
		fileResults, err := loadCensusDirectoryFiles()
		if err != nil {
			errors = append(errors, err.Error())
			filesLoadFailed = true
		} else {
			for _, fr := range fileResults {
				dirFileNames = append(dirFileNames, fr.FileName)
				for _, e := range fr.Entries {
					id := strings.TrimSpace(e.ID)
					if id == "" {
						continue
					}
					src := idSources[id]
					src.Files = appendUnique(src.Files, fr.FileName)
					idSources[id] = src
					if note := strings.TrimSpace(e.Note); note != "" {
						idNotes[id] = note
					}
				}
			}
		}
	}

	censusCacheLock.Lock()

	// B35: when a source failed to load this cycle, keep its last-good IDs so a
	// transient census-url outage (or a directory read blip) does not make the
	// strict enforcer, which runs every second, disconnect every client censed
	// only by that source. Entries are preserved per failing source; a source
	// that loaded successfully with fewer entries still prunes as intended.
	if (urlEnabled && urlLoadFailed) || (filesEnabled && filesLoadFailed) {
		for id, prevSrc := range censusCacheHolder.IDSources {
			restored := idSources[id]
			keep := false

			if urlEnabled && urlLoadFailed && prevSrc.URL {
				restored.URL = true
				keep = true
			}

			if filesEnabled && filesLoadFailed && len(prevSrc.Files) > 0 {
				for _, f := range prevSrc.Files {
					restored.Files = appendUnique(restored.Files, f)
				}
				keep = true
			}

			if keep {
				idSources[id] = restored
				if _, has := idNotes[id]; !has {
					if prevNote, ok := censusCacheHolder.IDNotes[id]; ok && prevNote != "" {
						idNotes[id] = prevNote
					}
				}
			}
		}
	}

	merged := make([]string, 0, len(idSources))
	for id := range idSources {
		merged = append(merged, id)
	}
	sort.Strings(merged)

	censusCacheHolder.IDs = merged
	censusCacheHolder.IDSources = idSources
	censusCacheHolder.IDNotes = idNotes
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

	notes := make(map[string]string, len(censusCacheHolder.IDNotes))
	for k, v := range censusCacheHolder.IDNotes {
		notes[k] = v
	}

	urlFiles := make([]string, len(censusCacheHolder.URLFiles))
	copy(urlFiles, censusCacheHolder.URLFiles)

	return censusCache{
		IDs:         ids,
		IDSources:   sources,
		IDNotes:     notes,
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
//
// This runs on the bind path and, with strict enforcement on, once per second
// for every active connection. It used to deep-copy the whole cache (every ID,
// source and note) and then linear-scan it, which on a large census turned a
// map lookup into an O(n) copy plus an O(n) scan per call (B21).
func IsIDCensed(id string) bool {
	checkID := strings.TrimSpace(id)
	if checkID == "" {
		return false
	}

	censusCacheLock.RLock()
	defer censusCacheLock.RUnlock()

	_, ok := censusCacheHolder.IDSources[checkID]
	return ok
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
