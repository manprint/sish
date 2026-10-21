package utils

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/radovskyb/watcher"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

type headerSetting struct {
	Enabled *bool  `yaml:"enabled"`
	Value   string `yaml:"value"`
	Always  *bool  `yaml:"always"`
}

type headerScope struct {
	Headers map[string]headerSetting `yaml:"headers"`
}

type headerSettingsFile struct {
	Defaults   headerScope            `yaml:"defaults"`
	Subdomains map[string]headerScope `yaml:"subdomains"`
}

type resolvedHeaderSetting struct {
	Enabled bool
	Value   string
	Always  bool
}

var (
	headerSettingsHolder     = &headerSettingsFile{}
	headerSettingsHolderLock = sync.RWMutex{}
)

var nginxHeaderNameMap = map[string]string{
	"x_frame_options":           "X-Frame-Options",
	"x_xss_protection":          "X-XSS-Protection",
	"referrer_policy":           "Referrer-Policy",
	"strict_transport_security": "Strict-Transport-Security",
	"permissions_policy":        "Permissions-Policy",
	"content_security_policy":   "Content-Security-Policy",
	"x_content_type_options":    "X-Content-Type-Options",
}

func shouldApplyHeaderForStatus(always bool, status int) bool {
	if always {
		return true
	}

	switch status {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent, http.StatusPartialContent,
		http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusNotModified,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func normalizeHeaderKey(key string) string {
	k := strings.TrimSpace(strings.ToLower(key))
	if mapped, ok := nginxHeaderNameMap[k]; ok {
		return mapped
	}

	return http.CanonicalHeaderKey(strings.ReplaceAll(k, "_", "-"))
}

func toResolvedHeaderSetting(setting headerSetting) (resolvedHeaderSetting, bool) {
	enabled := false
	if setting.Enabled != nil {
		enabled = *setting.Enabled
	} else if strings.TrimSpace(setting.Value) != "" {
		enabled = true
	}

	resolved := resolvedHeaderSetting{
		Enabled: enabled,
		Value:   strings.TrimSpace(setting.Value),
		Always:  setting.Always != nil && *setting.Always,
	}

	if !resolved.Enabled {
		return resolved, true
	}

	if resolved.Value == "" {
		return resolved, false
	}

	return resolved, true
}

func loadHeaderSettingsConfig() {
	dir := strings.TrimSpace(viper.GetString("headers-setting-directory"))
	if dir == "" {
		headerSettingsHolderLock.Lock()
		headerSettingsHolder = &headerSettingsFile{}
		headerSettingsHolderLock.Unlock()
		return
	}

	baseDir, err := filepath.Abs(dir)
	if err != nil {
		log.Println("unable to resolve headers-setting-directory:", err)
		return
	}

	configCandidates := []string{
		filepath.Join(baseDir, "config.yaml"),
		filepath.Join(baseDir, "config.yml"),
		filepath.Join(baseDir, "config.headers.yaml"),
		filepath.Join(baseDir, "config.headers.yml"),
	}

	var configPath string
	for _, candidate := range configCandidates {
		if _, statErr := os.Stat(candidate); statErr == nil {
			configPath = candidate
			break
		}
	}

	if configPath == "" {
		log.Println("headers settings config not found; expected config.yaml, config.yml, config.headers.yaml, or config.headers.yml inside", baseDir)
		headerSettingsHolderLock.Lock()
		headerSettingsHolder = &headerSettingsFile{}
		headerSettingsHolderLock.Unlock()
		return
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		log.Println("unable to read headers settings file:", err)
		return
	}

	parsed := &headerSettingsFile{}
	if err := yaml.Unmarshal(content, parsed); err != nil {
		log.Println("unable to parse headers settings file:", err)
		return
	}

	if parsed.Subdomains == nil {
		parsed.Subdomains = map[string]headerScope{}
	}

	headerSettingsHolderLock.Lock()
	headerSettingsHolder = parsed
	headerSettingsHolderLock.Unlock()
}

// WatchHeadersSettings loads and watches headers-setting-directory for runtime changes.
func WatchHeadersSettings() {
	if !viper.GetBool("headers-managed") {
		headerSettingsHolderLock.Lock()
		headerSettingsHolder = &headerSettingsFile{}
		headerSettingsHolderLock.Unlock()
		return
	}

	dir := strings.TrimSpace(viper.GetString("headers-setting-directory"))
	if dir == "" {
		return
	}

	loadHeaderSettingsConfig()

	w := watcher.New()
	w.SetMaxEvents(1)

	if err := w.AddRecursive(dir); err != nil {
		log.Println("unable to watch headers-setting-directory:", err)
		return
	}

	go func() {
		w.Wait()

		for {
			select {
			case _, ok := <-w.Event:
				if !ok {
					return
				}
				loadHeaderSettingsConfig()
			case err, ok := <-w.Error:
				if !ok {
					return
				}
				if err != nil {
					log.Println("headers-setting watcher error:", err)
				}
			}
		}
	}()

	go func() {
		if err := w.Start(viper.GetDuration("headers-setting-directory-watch-interval")); err != nil {
			log.Println("unable to start headers-setting watcher:", err)
		}
	}()
}

func extractForwarderSubdomain(hostWithPort string) string {
	host := strings.TrimSpace(hostWithPort)
	if host == "" {
		return ""
	}

	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}

	host = strings.TrimSuffix(strings.ToLower(host), ".")
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(viper.GetString("domain"))), ".")
	if host == "" || domain == "" || host == domain {
		return ""
	}

	suffix := "." + domain
	if !strings.HasSuffix(host, suffix) {
		return ""
	}

	prefix := strings.TrimSuffix(host, suffix)
	prefix = strings.TrimSuffix(prefix, ".")
	if prefix == "" {
		return ""
	}

	return prefix
}

func resolveHeadersForSubdomain(subdomain string) map[string]resolvedHeaderSetting {
	resolved := map[string]resolvedHeaderSetting{}
	if subdomain == "" {
		return resolved
	}

	headerSettingsHolderLock.RLock()
	current := headerSettingsHolder
	headerSettingsHolderLock.RUnlock()

	if current == nil {
		return resolved
	}

	for rawKey, setting := range current.Defaults.Headers {
		normalized := normalizeHeaderKey(rawKey)
		parsed, ok := toResolvedHeaderSetting(setting)
		if !ok {
			continue
		}
		resolved[normalized] = parsed
	}

	overrideScope, ok := current.Subdomains[strings.ToLower(subdomain)]
	if !ok {
		// If requested host is nested (a.b), allow fallback to first label (a).
		parts := strings.SplitN(strings.ToLower(subdomain), ".", 2)
		if len(parts) > 0 {
			overrideScope, ok = current.Subdomains[parts[0]]
		}
	}

	if !ok {
		return resolved
	}

	for rawKey, setting := range overrideScope.Headers {
		normalized := normalizeHeaderKey(rawKey)

		// Explicit enabled:false removes the inherited default (README "merge").
		if setting.Enabled != nil && !*setting.Enabled {
			delete(resolved, normalized)
			continue
		}

		// B38: merge the override against the inherited default instead of
		// running it through toResolvedHeaderSetting, which rejected
		// enabled:true-without-value (ok=false) so the override was dropped and
		// never inherited the default value, and turned an always-only override
		// into a disabled header. Documented semantics (README_HEADERS.md):
		//   value   = override value if set, else inherit the default's value
		//   enabled = explicit override enabled; else true if override sets a
		//             value; else inherit the default's enabled state
		//   always  = override always if present, else inherit the default's
		base, hasBase := resolved[normalized]

		value := strings.TrimSpace(setting.Value)
		if value == "" && hasBase {
			value = base.Value
		}

		var enabled bool
		switch {
		case setting.Enabled != nil:
			enabled = *setting.Enabled // true here; false handled above
		case strings.TrimSpace(setting.Value) != "":
			enabled = true
		case hasBase:
			enabled = base.Enabled
		}

		always := false
		if setting.Always != nil {
			always = *setting.Always
		} else if hasBase {
			always = base.Always
		}

		// Nothing applicable (no value or disabled, and not an explicit delete):
		// leave any inherited default in place rather than storing a dead entry.
		if !enabled || value == "" {
			continue
		}

		resolved[normalized] = resolvedHeaderSetting{Enabled: enabled, Value: value, Always: always}
	}

	return resolved
}

// ApplyForwarderHeaders injects configured security headers for forwarded subdomain responses.
func ApplyForwarderHeaders(responseHeaders http.Header, hostWithPort string, statusCode int) {
	if !viper.GetBool("headers-managed") {
		return
	}

	subdomain := extractForwarderSubdomain(hostWithPort)
	if subdomain == "" {
		return
	}

	resolved := resolveHeadersForSubdomain(subdomain)
	for name, setting := range resolved {
		if !setting.Enabled || setting.Value == "" {
			continue
		}

		if !shouldApplyHeaderForStatus(setting.Always, statusCode) {
			continue
		}

		responseHeaders.Set(name, setting.Value)
	}
}

// ValidateHeaderSettingsConfig performs a structural validation of parsed headers settings.
func ValidateHeaderSettingsConfig(content []byte) error {
	parsed := &headerSettingsFile{}
	if err := yaml.Unmarshal(content, parsed); err != nil {
		return fmt.Errorf("invalid yaml: %w", err)
	}

	// B38: a subdomain override with enabled:true and no value is valid when the
	// defaults define that header with a value, because it inherits that value.
	defaultsHasValue := func(key string) bool {
		norm := normalizeHeaderKey(key)
		for dk, dv := range parsed.Defaults.Headers {
			if normalizeHeaderKey(dk) == norm && strings.TrimSpace(dv.Value) != "" {
				return true
			}
		}
		return false
	}

	validateScope := func(scope headerScope, where string, isSubdomain bool) error {
		for key, value := range scope.Headers {
			normalized := normalizeHeaderKey(key)
			if strings.TrimSpace(normalized) == "" {
				return fmt.Errorf("%s: header key cannot be empty", where)
			}

			enabled := false
			if value.Enabled != nil {
				enabled = *value.Enabled
			}

			if enabled && strings.TrimSpace(value.Value) == "" {
				if isSubdomain && defaultsHasValue(key) {
					continue
				}
				return fmt.Errorf("%s: enabled header %q requires a non-empty value", where, key)
			}
		}

		return nil
	}

	if err := validateScope(parsed.Defaults, "defaults", false); err != nil {
		return err
	}

	for subdomain, scope := range parsed.Subdomains {
		where := fmt.Sprintf("subdomains.%s", subdomain)
		if err := validateScope(scope, where, true); err != nil {
			return err
		}
	}

	return nil
}
