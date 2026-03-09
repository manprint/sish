package utils

import (
	"fmt"
	"io"
	"net/http"
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

type censusCache struct {
	IDs         []string
	LastRefresh time.Time
	LastError   string
}

var (
	censusCacheHolder = censusCache{IDs: []string{}}
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

// RefreshCensusCache downloads and refreshes census IDs from census-url.
func RefreshCensusCache() error {
	if !viper.GetBool("census-enabled") {
		censusCacheLock.Lock()
		censusCacheHolder = censusCache{IDs: []string{}}
		censusCacheLock.Unlock()
		return nil
	}

	censusURL := strings.TrimSpace(viper.GetString("census-url"))
	if censusURL == "" {
		censusCacheLock.Lock()
		censusCacheHolder.LastError = "census-url is empty"
		censusCacheLock.Unlock()
		return fmt.Errorf("census-url is empty")
	}

	body, err := downloadCensusBody(censusURL)
	if err != nil {
		censusCacheLock.Lock()
		censusCacheHolder.LastError = err.Error()
		censusCacheLock.Unlock()
		return err
	}

	ids, err := parseCensusBody(body)
	if err != nil {
		censusCacheLock.Lock()
		censusCacheHolder.LastError = err.Error()
		censusCacheLock.Unlock()
		return err
	}

	censusCacheLock.Lock()
	censusCacheHolder.IDs = ids
	censusCacheHolder.LastRefresh = time.Now()
	censusCacheHolder.LastError = ""
	censusCacheLock.Unlock()

	return nil
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

	return censusCache{
		IDs:         ids,
		LastRefresh: censusCacheHolder.LastRefresh,
		LastError:   censusCacheHolder.LastError,
	}
}
