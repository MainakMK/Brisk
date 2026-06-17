package waf

import (
	"net/http"
	"testing"

	"brisk-agent/config"
)

// newTestEngine compiles a single-zone engine ("a.test") with the given mode +
// WordPress preset + custom rules. Coraza CRS v4 is embedded, so this is offline.
func newTestEngine(t *testing.T, mode string, wp bool, rules []config.ZoneWAFRule) *Engine {
	t.Helper()
	e := NewEngine(nil) // nil buffer: events only logged, not shipped
	e.Reload([]config.Zone{{
		Domain: "a.test",
		WAF: &config.ZoneWAF{
			Enabled: true, Mode: mode, ManagedRuleset: "owasp_crs", Paranoia: 1,
			FailOpen: true, WpPreset: wp, Rules: rules,
		},
	}})
	if e.Protecting() != 1 {
		t.Fatalf("want 1 protected zone, got %d", e.Protecting())
	}
	return e
}

func req(uri, ua string) InspectReq {
	return InspectReq{
		Host: "a.test", Method: "GET", URI: uri, ClientIP: "203.0.113.5",
		UserAgent: ua, Header: http.Header{},
	}
}

// TestEngineCRSBlocksSQLiInBlockMode: CRS blocks SQLi/XSS in block mode; clean passes.
func TestEngineCRSBlocksSQLiInBlockMode(t *testing.T) {
	e := newTestEngine(t, "block", false, nil)
	if !e.Inspect(req("/?id=1'%20OR%20'1'='1", "curl/8")).Block {
		t.Error("SQLi must be blocked in block mode")
	}
	if !e.Inspect(req("/?q=<script>alert(1)</script>", "curl/8")).Block {
		t.Error("XSS must be blocked in block mode")
	}
	if e.Inspect(req("/index.html?q=hello", "curl/8")).Block {
		t.Error("clean request must pass")
	}
}

// TestEngineDetectModeDoesNotBlock: detect mode never enforces (would-block only).
func TestEngineDetectModeDoesNotBlock(t *testing.T) {
	e := newTestEngine(t, "detect", false, nil)
	if e.Inspect(req("/?id=1'%20OR%20'1'='1", "curl/8")).Block {
		t.Error("detect mode must NOT block (logs would-block only)")
	}
}

// TestCustomRuleBlocks: a custom path-prefix block rule enforces in block mode.
func TestCustomRuleBlocks(t *testing.T) {
	rules := []config.ZoneWAFRule{
		{ID: 1, Priority: 1, Field: "path", Op: "prefix", Value: "/admin", Action: "block", Enabled: true},
	}
	e := newTestEngine(t, "block", false, rules)
	if !e.Inspect(req("/admin/secret", "curl/8")).Block {
		t.Error("custom /admin block rule must enforce")
	}
	if e.Inspect(req("/public", "curl/8")).Block {
		t.Error("non-matching path must pass")
	}
}

// TestAllowRuleShortCircuits: an allow rule skips the managed CRS (allowlist).
func TestAllowRuleShortCircuits(t *testing.T) {
	rules := []config.ZoneWAFRule{
		{ID: 2, Priority: 1, Field: "ip", Op: "eq", Value: "203.0.113.5", Action: "allow", Enabled: true},
	}
	e := newTestEngine(t, "block", false, rules)
	// Same SQLi that CRS would block — the allow rule short-circuits before CRS.
	if e.Inspect(req("/?id=1'%20OR%20'1'='1", "curl/8")).Block {
		t.Error("allow rule must short-circuit and skip the managed CRS")
	}
}

// TestWpPreset: the WordPress preset blocks /xmlrpc.php + scanner UAs; curl passes.
func TestWpPreset(t *testing.T) {
	e := newTestEngine(t, "block", true, nil)
	if !e.Inspect(req("/xmlrpc.php", "curl/8")).Block {
		t.Error("wp preset must block /xmlrpc.php")
	}
	if !e.Inspect(req("/", "sqlmap/1.5.2")).Block {
		t.Error("wp preset must block scanner user-agents")
	}
	if e.Inspect(req("/", "curl/8")).Block {
		t.Error("legitimate curl must pass the wp preset")
	}
}

// TestCountryRuleBlocks: a country block rule (Phase 4 Step 6 Part 5) blocks
// requests from the named country and allows others. Country comes from GeoIP
// (X-Brisk-WAF-Country); an empty country (GeoIP off) never matches.
func TestCountryRuleBlocks(t *testing.T) {
	rules := []config.ZoneWAFRule{
		{ID: 3, Priority: 1, Field: "country", Op: "eq", Value: "RU", Action: "block", Enabled: true},
	}
	e := newTestEngine(t, "block", false, rules)

	ru := req("/index.html", "curl/8")
	ru.Country = "RU"
	if !e.Inspect(ru).Block {
		t.Error("country=RU must be blocked by the country rule")
	}
	us := req("/index.html", "curl/8")
	us.Country = "US"
	if e.Inspect(us).Block {
		t.Error("country=US must pass (rule targets RU)")
	}
	none := req("/index.html", "curl/8") // GeoIP off -> no country
	if e.Inspect(none).Block {
		t.Error("empty country (GeoIP off) must not match a country rule")
	}
}

// TestUnknownZoneAllows: a host with no compiled WAF is allowed (nginx only calls
// us when a zone has WAF on; the engine fails safe to allow for an unknown host).
func TestUnknownZoneAllows(t *testing.T) {
	e := newTestEngine(t, "block", false, nil)
	r := req("/?id=1'%20OR%20'1'='1", "curl/8")
	r.Host = "other.test"
	if e.Inspect(r).Block {
		t.Error("unknown zone must be allowed (not configured here)")
	}
}
