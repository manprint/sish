package utils

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRefreshCensusCachePreservesURLIDsOnFailure guards B35: a transient
// census-url failure must not drop the last-good URL-sourced IDs, otherwise the
// strict enforcer (which runs every second) would disconnect every URL-censed
// client on a brief outage.
func TestRefreshCensusCachePreservesURLIDsOnFailure(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("- id: alpha\n  note: first\n- id: beta\n"))
	}))
	defer srv.Close()

	withCensusCache(t, censusCache{IDs: []string{}, IDSources: map[string]CensusIDSource{}, IDNotes: map[string]string{}})
	setViperForTest(t, "census-enabled", true)
	setViperForTest(t, "strict-id-censed-url", true)
	setViperForTest(t, "strict-id-censed-files", false)
	setViperForTest(t, "census-url", srv.URL)
	setViperForTest(t, "census-directory", "")

	if err := RefreshCensusCache(); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if !IsIDCensed("alpha") || !IsIDCensed("beta") {
		t.Fatal("expected alpha and beta censed after a good refresh")
	}

	fail = true
	if err := RefreshCensusCache(); err != nil {
		t.Fatalf("B35: refresh that preserved last-good data should not error: %v", err)
	}
	if !IsIDCensed("alpha") || !IsIDCensed("beta") {
		t.Fatal("B35: URL-censed IDs were dropped on a transient census-url failure")
	}

	if snap := GetCensusCacheSnapshot(); snap.LastError == "" {
		t.Fatal("expected LastError to record the census-url failure")
	}
}

// TestDownloadCensusBodyRejectsOversizedBody guards B37: the census-url read is
// bounded so a hostile/misconfigured endpoint cannot exhaust memory.
func TestDownloadCensusBodyRejectsOversizedBody(t *testing.T) {
	chunk := strings.Repeat("a", 1<<20) // 1 MiB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < (maxCensusBodySize/len(chunk))+2; i++ {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	if _, err := downloadCensusBody(srv.URL); err == nil {
		t.Fatal("B37: expected an error for an oversized census body")
	}
}
