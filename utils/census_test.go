package utils

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/spf13/viper"
)

func withCensusCache(t *testing.T, cache censusCache) {
	t.Helper()

	censusCacheLock.Lock()
	previous := censusCacheHolder
	censusCacheHolder = cache
	censusCacheLock.Unlock()

	t.Cleanup(func() {
		censusCacheLock.Lock()
		censusCacheHolder = previous
		censusCacheLock.Unlock()
	})
}

// IsIDCensed runs on the bind path and, with strict enforcement on, once per
// second for every active connection. It used to deep-copy the whole cache and
// linear-scan it; it is now a map lookup (B21). Behaviour must be unchanged.
func TestIsIDCensed(t *testing.T) {
	withCensusCache(t, censusCache{
		IDs: []string{"alpha", "beta"},
		IDSources: map[string]CensusIDSource{
			"alpha": {URL: true},
			"beta":  {Files: []string{"local.yml"}},
		},
		IDNotes: map[string]string{"alpha": "note"},
	})

	for _, id := range []string{"alpha", "beta", "  alpha  ", "\tbeta\n"} {
		if !IsIDCensed(id) {
			t.Fatalf("IsIDCensed(%q)=false, want true", id)
		}
	}

	for _, id := range []string{"gamma", "", "   ", "ALPHA"} {
		if IsIDCensed(id) {
			t.Fatalf("IsIDCensed(%q)=true, want false", id)
		}
	}
}

func TestIsIDCensedIsSafeUnderConcurrentRefresh(t *testing.T) {
	withCensusCache(t, censusCache{
		IDs:       []string{"alpha"},
		IDSources: map[string]CensusIDSource{"alpha": {URL: true}},
		IDNotes:   map[string]string{},
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}

			censusCacheLock.Lock()
			censusCacheHolder.IDSources = map[string]CensusIDSource{"alpha": {URL: i%2 == 0}}
			censusCacheHolder.IDs = []string{"alpha"}
			censusCacheLock.Unlock()
		}
	}()

	for i := 0; i < 5000; i++ {
		if !IsIDCensed("alpha") {
			close(stop)
			wg.Wait()
			t.Fatal("IsIDCensed lost a present ID during refresh")
		}
		_ = GetIDSource("alpha")
		_ = GetCensusCacheSnapshot()
	}

	close(stop)
	wg.Wait()
}

func TestGetIDSourceReturnsCopy(t *testing.T) {
	withCensusCache(t, censusCache{
		IDs:       []string{"alpha"},
		IDSources: map[string]CensusIDSource{"alpha": {URL: true, Files: []string{"a.yml"}}},
		IDNotes:   map[string]string{},
	})

	source := GetIDSource("alpha")
	if !source.URL || !reflect.DeepEqual(source.Files, []string{"a.yml"}) {
		t.Fatalf("GetIDSource=%+v", source)
	}

	source.Files[0] = "tampered"

	if again := GetIDSource("alpha"); again.Files[0] != "a.yml" {
		t.Fatalf("census cache mutated through returned slice: %q", again.Files[0])
	}

	if missing := GetIDSource("nope"); missing.URL || len(missing.Files) != 0 {
		t.Fatalf("GetIDSource(missing)=%+v, want zero value", missing)
	}
}

func TestGetCensusCacheSnapshotIsDeepCopy(t *testing.T) {
	withCensusCache(t, censusCache{
		IDs:       []string{"alpha"},
		IDSources: map[string]CensusIDSource{"alpha": {URL: true, Files: []string{"a.yml"}}},
		IDNotes:   map[string]string{"alpha": "note"},
		URLFiles:  []string{"a.yml"},
	})

	snapshot := GetCensusCacheSnapshot()
	snapshot.IDs[0] = "tampered"
	snapshot.IDSources["alpha"].Files[0] = "tampered"
	snapshot.IDNotes["alpha"] = "tampered"
	snapshot.URLFiles[0] = "tampered"
	snapshot.IDSources["injected"] = CensusIDSource{}

	fresh := GetCensusCacheSnapshot()
	if fresh.IDs[0] != "alpha" || fresh.IDNotes["alpha"] != "note" || fresh.URLFiles[0] != "a.yml" {
		t.Fatalf("snapshot mutation leaked into the cache: %+v", fresh)
	}
	if fresh.IDSources["alpha"].Files[0] != "a.yml" {
		t.Fatalf("source files mutation leaked: %+v", fresh.IDSources["alpha"])
	}
	if _, ok := fresh.IDSources["injected"]; ok {
		t.Fatal("snapshot map insertion leaked into the cache")
	}
}

func TestNormalizeCensusIDs(t *testing.T) {
	got := normalizeCensusIDs([]censusEntry{
		{ID: "  beta "},
		{ID: "alpha"},
		{ID: "beta"},
		{ID: ""},
		{ID: "   "},
	})

	if !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("normalizeCensusIDs=%v", got)
	}
}

func TestParseCensusBody(t *testing.T) {
	ids, err := parseCensusBody([]byte("- id: b\n  note: second\n- id: a\n"))
	if err != nil {
		t.Fatalf("parseCensusBody: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"a", "b"}) {
		t.Fatalf("ids=%v", ids)
	}

	if _, err := parseCensusBody([]byte("not: a list")); err == nil {
		t.Fatal("parseCensusBody accepted a non-list document")
	}

	if err := ValidateCensusYAML([]byte("- id: a\n")); err != nil {
		t.Fatalf("ValidateCensusYAML: %v", err)
	}

	if err := ValidateCensusYAML([]byte("{{")); err == nil {
		t.Fatal("ValidateCensusYAML accepted invalid yaml")
	}
}

func setViperForTest(t *testing.T, key string, value any) {
	t.Helper()

	previous := viper.Get(key)
	viper.Set(key, value)
	t.Cleanup(func() { viper.Set(key, previous) })
}

func TestRefreshCensusCacheFromDirectory(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "one.yaml"), []byte("- id: alpha\n  note: first\n- id: beta\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "two.yml"), []byte("- id: beta\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.txt"), []byte("- id: gamma\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	withCensusCache(t, censusCache{IDs: []string{}, IDSources: map[string]CensusIDSource{}, IDNotes: map[string]string{}})
	setViperForTest(t, "census-enabled", true)
	setViperForTest(t, "strict-id-censed-url", false)
	setViperForTest(t, "strict-id-censed-files", true)
	setViperForTest(t, "census-url", "")
	setViperForTest(t, "census-directory", dir)

	if err := RefreshCensusCache(); err != nil {
		t.Fatalf("RefreshCensusCache: %v", err)
	}

	snapshot := GetCensusCacheSnapshot()
	if !reflect.DeepEqual(snapshot.IDs, []string{"alpha", "beta"}) {
		t.Fatalf("IDs=%v", snapshot.IDs)
	}
	if snapshot.IDNotes["alpha"] != "first" {
		t.Fatalf("notes=%v", snapshot.IDNotes)
	}
	if !reflect.DeepEqual(snapshot.IDSources["beta"].Files, []string{"one.yaml", "two.yml"}) {
		t.Fatalf("beta sources=%+v", snapshot.IDSources["beta"])
	}
	if IsIDCensed("gamma") {
		t.Fatal("a non-yaml file was loaded")
	}
	if !IsIDCensed("alpha") || !IsIDCensed("beta") {
		t.Fatal("directory IDs are not censed")
	}
}

func TestRefreshCensusCacheClearsWhenDisabled(t *testing.T) {
	withCensusCache(t, censusCache{
		IDs:       []string{"alpha"},
		IDSources: map[string]CensusIDSource{"alpha": {URL: true}},
		IDNotes:   map[string]string{},
	})
	setViperForTest(t, "census-enabled", false)

	if err := RefreshCensusCache(); err != nil {
		t.Fatalf("RefreshCensusCache: %v", err)
	}

	if IsIDCensed("alpha") {
		t.Fatal("cache was not cleared when census is disabled")
	}
}

func TestRefreshCensusCacheRecordsErrorWhenBothStrictModesOff(t *testing.T) {
	withCensusCache(t, censusCache{IDs: []string{}, IDSources: map[string]CensusIDSource{}, IDNotes: map[string]string{}})
	setViperForTest(t, "census-enabled", true)
	setViperForTest(t, "strict-id-censed-url", false)
	setViperForTest(t, "strict-id-censed-files", false)

	if err := RefreshCensusCache(); err != nil {
		t.Fatalf("RefreshCensusCache: %v", err)
	}

	if got := GetCensusCacheSnapshot().LastError; got == "" {
		t.Fatal("expected LastError to explain why the cache is empty")
	}
}

func TestIsStrictIDCensedEnabled(t *testing.T) {
	setViperForTest(t, "census-enabled", false)
	setViperForTest(t, "strict-id-censed", true)
	setViperForTest(t, "strict-id-censed-url", true)
	setViperForTest(t, "strict-id-censed-files", true)

	if IsStrictIDCensedEnabled() {
		t.Fatal("strict enforcement must be off when census is disabled")
	}

	viper.Set("census-enabled", true)
	viper.Set("strict-id-censed", false)
	viper.Set("strict-id-censed-url", false)
	viper.Set("strict-id-censed-files", false)

	if IsStrictIDCensedEnabled() {
		t.Fatal("strict enforcement must be off when no strict mode is set")
	}

	viper.Set("strict-id-censed-files", true)

	if !IsStrictIDCensedEnabled() {
		t.Fatal("strict enforcement must be on with strict-id-censed-files")
	}
}
