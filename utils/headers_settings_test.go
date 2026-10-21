package utils

import "testing"

func bptr(b bool) *bool { return &b }

func withHeaderSettings(t *testing.T, f *headerSettingsFile) {
	t.Helper()

	headerSettingsHolderLock.Lock()
	prev := headerSettingsHolder
	headerSettingsHolder = f
	headerSettingsHolderLock.Unlock()

	t.Cleanup(func() {
		headerSettingsHolderLock.Lock()
		headerSettingsHolder = prev
		headerSettingsHolderLock.Unlock()
	})
}

// TestResolveHeadersForSubdomainMerge exercises the documented
// defaults+subdomains merge semantics (README_HEADERS.md), with B38 as the
// central case: an override that enables a header without a value must inherit
// the default's value instead of being dropped.
func TestResolveHeadersForSubdomainMerge(t *testing.T) {
	file := &headerSettingsFile{
		Defaults: headerScope{Headers: map[string]headerSetting{
			"x_frame_options":         {Enabled: bptr(true), Value: "SAMEORIGIN", Always: bptr(true)},
			"content_security_policy": {Enabled: bptr(true), Value: "default-src *", Always: bptr(true)},
			"referrer_policy":         {Enabled: bptr(true), Value: "no-referrer", Always: bptr(true)},
			"x_content_type_options":  {Enabled: bptr(true), Value: "nosniff"}, // no always -> whitelist only
		}},
		Subdomains: map[string]headerScope{
			"app": {Headers: map[string]headerSetting{
				// enabled:false removes the inherited default
				"x_frame_options": {Enabled: bptr(false)},
				// enabled:true without value inherits the default value (B38)
				"content_security_policy": {Enabled: bptr(true)},
				// always-only override: inherit value+enabled, flip always off
				"referrer_policy": {Always: bptr(false)},
			}},
			"custom": {Headers: map[string]headerSetting{
				// value without enabled -> enabled implicitly true, replaces
				"x_frame_options": {Value: "DENY"},
				// enabled:true without a value and no default -> nothing to apply
				"strict_transport_security": {Enabled: bptr(true)},
			}},
		},
	}
	withHeaderSettings(t, file)

	app := resolveHeadersForSubdomain("app")

	if _, ok := app["X-Frame-Options"]; ok {
		t.Error("app: X-Frame-Options should be removed by enabled:false override")
	}

	csp, ok := app["Content-Security-Policy"]
	if !ok {
		t.Fatal("B38: app CSP override enabled:true without value was dropped instead of inheriting")
	}
	if !csp.Enabled || csp.Value != "default-src *" || !csp.Always {
		t.Errorf("B38: app CSP = %+v, want inherited value/enabled/always", csp)
	}

	ref, ok := app["Referrer-Policy"]
	if !ok {
		t.Fatal("app Referrer-Policy missing; always-only override should inherit the default")
	}
	if !ref.Enabled || ref.Value != "no-referrer" || ref.Always {
		t.Errorf("app Referrer-Policy = %+v, want enabled/value inherited and always=false", ref)
	}

	// x_content_type_options inherited untouched from defaults.
	if cto, ok := app["X-Content-Type-Options"]; !ok || !cto.Enabled || cto.Value != "nosniff" || cto.Always {
		t.Errorf("app X-Content-Type-Options = %+v (ok=%v), want inherited default", cto, ok)
	}

	custom := resolveHeadersForSubdomain("custom")
	if xfo, ok := custom["X-Frame-Options"]; !ok || !xfo.Enabled || xfo.Value != "DENY" {
		t.Errorf("custom X-Frame-Options = %+v (ok=%v), want enabled DENY (value implies enabled)", xfo, ok)
	}
	if _, ok := custom["Strict-Transport-Security"]; ok {
		t.Error("custom HSTS enabled:true without value and no default should not be applied")
	}
}

// TestValidateHeaderSettingsConfigInheritance guards the B38 validation change:
// a subdomain override enabling a header without a value is valid iff the
// defaults supply that header's value.
func TestValidateHeaderSettingsConfigInheritance(t *testing.T) {
	ok := []byte(`
defaults:
  headers:
    content_security_policy:
      enabled: true
      value: "default-src *"
subdomains:
  app:
    headers:
      content_security_policy:
        enabled: true
`)
	if err := ValidateHeaderSettingsConfig(ok); err != nil {
		t.Fatalf("B38: inheriting override should validate, got: %v", err)
	}

	badDefault := []byte(`
defaults:
  headers:
    content_security_policy:
      enabled: true
`)
	if err := ValidateHeaderSettingsConfig(badDefault); err == nil {
		t.Fatal("defaults enabled:true without value must be rejected")
	}

	badSub := []byte(`
defaults:
  headers:
    referrer_policy:
      enabled: true
      value: "no-referrer"
subdomains:
  app:
    headers:
      content_security_policy:
        enabled: true
`)
	if err := ValidateHeaderSettingsConfig(badSub); err == nil {
		t.Fatal("subdomain enabled:true without value and no matching default value must be rejected")
	}
}
