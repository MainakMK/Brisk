package nginx

import (
	"bytes"
	"strings"
	"testing"

	"brisk-agent/config"
)

// TestRenderWAF proves Phase 4 Step 4: a WAF-enabled zone renders the auth_request
// hook + the loopback /_waf inspection location + the fail-open fallback, and its
// rate limits render as http-context limit_req_zone declarations + per-path
// limit_req locations (incl. the WordPress-preset /wp-login.php). A non-WAF zone on
// the same edge renders NONE of this (per-zone isolation; WAF off by default).
func TestRenderWAF(t *testing.T) {
	m, err := NewManager("nginx", "/tmp/nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		EdgeID:          "EDGE-1",
		Mode:            config.ModeLocal,
		CacheDir:        "/var/cache/brisk",
		CacheMaxSize:    "10g",
		BrotliCompLevel: 5,
		WAFListen:       "127.0.0.1:9555",
		Zones: []config.Zone{
			// zone A: WAF block mode + CRS + wp preset + a custom rate limit.
			{Domain: "a.example.com", Origin: "http://origin-a:8001", TLS: "selfsigned",
				WAF: &config.ZoneWAF{
					Enabled: true, Mode: "block", ManagedRuleset: "owasp_crs", Paranoia: 1,
					FailOpen: true, WpPreset: true,
					Rules: []config.ZoneWAFRule{
						{ID: 1, Priority: 1, Field: "path", Op: "prefix", Value: "/admin", Action: "block", Enabled: true},
					},
					RateLimits: []config.ZoneRateLimit{
						{ID: 9, PathMatch: "/api/login", MatchType: "exact", Requests: 10, PeriodSeconds: 60, Key: "ip", Action: "block", Enabled: true},
					},
				}},
			// zone B: WAF OFF -> no auth_request, no rate limits.
			{Domain: "b.example.com", Origin: "http://origin-b:8002", TLS: "selfsigned"},
		},
	}
	normalize(cfg)

	out, err := m.Render(cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := string(out)

	mustContain := []string{
		"location = /_waf {",
		"proxy_pass http://127.0.0.1:9555/inspect;",
		"proxy_set_header X-Brisk-WAF-Zone a.example.com;",
		"error_page 500 502 503 504 = @waf_failopen;", // fail-open enabled
		"location @waf_failopen { internal; return 200; }",
		// WordPress preset: /wp-login.php rate limit (5/min -> 5r/m, burst 4)
		"location = /wp-login.php {",
		"rate=5r/m;",
		"burst=4 nodelay;",
		// custom rate limit /api/login (10/min -> 10r/m)
		"location = /api/login {",
		"rate=10r/m;",
		// zone B still serves its origin directly, untouched
		"set $brisk_origin origin-b:8002;",
	}
	for _, want := range mustContain {
		if !strings.Contains(conf, want) {
			t.Errorf("rendered config missing %q", want)
		}
	}

	// The custom rule (/admin block) is enforced by the Coraza WAF engine, NOT nginx,
	// so it must NOT appear as an nginx location.
	if strings.Contains(conf, "location ^~ /admin") || strings.Contains(conf, "location = /admin") {
		t.Errorf("custom rule /admin must be enforced by the WAF engine, not rendered as an nginx location")
	}

	// auth_request appears only on zone A's inspected locations: static + html (2) +
	// the two rate-limit locations (/wp-login.php, /api/login) (2) = 4. Zone B: none.
	if n := strings.Count(conf, "auth_request /_waf;"); n != 4 {
		t.Errorf("want 4 auth_request hooks (zone A static+html+2 rate-limit locs), got %d", n)
	}
	// Exactly one /_waf inspection location (the one WAF zone).
	if n := strings.Count(conf, "location = /_waf {"); n != 1 {
		t.Errorf("want 1 /_waf location (zone A only), got %d", n)
	}
	// Two http-context limit_req_zone declarations (wp-login + api/login).
	if n := strings.Count(conf, "limit_req_zone $binary_remote_addr zone=brisk_rl_"); n != 2 {
		t.Errorf("want 2 limit_req_zone declarations, got %d", n)
	}
	// X-Brisk-WAF-Zone only references zone A (zone B has no WAF).
	if strings.Contains(conf, "X-Brisk-WAF-Zone b.example.com") {
		t.Errorf("zone B (WAF off) must not render any WAF inspection")
	}

	// The WAF subrequest always passes the client country (Phase 4 Step 6 Part 5),
	// so country rules can match — regardless of whether GeoIP is installed.
	if !strings.Contains(conf, "proxy_set_header X-Brisk-WAF-Country $brisk_country;") {
		t.Error("WAF subrequest must pass X-Brisk-WAF-Country for country rules")
	}
}

// TestRenderPart5 proves Phase 4 Step 6 Part 5: GeoIP gating ($brisk_country from
// the geoip2 block when enabled, "-" map otherwise) and errors-only rate limiting
// routed through Lua (access/log hooks + zones_data rate_limits) — NOT nginx
// limit_req — while "all"-mode limits stay nginx-native.
func TestRenderPart5(t *testing.T) {
	m, err := NewManager("nginx", "/tmp/nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		EdgeID: "E", Mode: config.ModeLocal, CacheDir: "/var/cache/brisk",
		CacheMaxSize: "10g", BrotliCompLevel: 5, WAFListen: "127.0.0.1:9555",
		Zones: []config.Zone{
			{Domain: "a.example.com", Origin: "http://o:80", TLS: "selfsigned",
				WAF: &config.ZoneWAF{
					Enabled: true, Mode: "block", ManagedRuleset: "owasp_crs", Paranoia: 1, FailOpen: true,
					RateLimits: []config.ZoneRateLimit{
						// errors-only -> Lua; all -> nginx-native.
						{ID: 1, PathMatch: "/login", MatchType: "exact", Requests: 5, PeriodSeconds: 60, Key: "ip", CountMode: "errors_only", Enabled: true},
						{ID: 2, PathMatch: "/api/", MatchType: "prefix", Requests: 100, PeriodSeconds: 60, Key: "ip", CountMode: "all", Enabled: true},
					},
				}},
		},
	}
	normalize(cfg)

	renderConf := func(lua, geo bool) string {
		rd, err := buildRenderData(cfg, false, lua, geo)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := m.tmpl.ExecuteTemplate(&buf, "nginx.conf.tmpl", rd); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	// --- GeoIP gating ---
	geoOn := renderConf(false, true)
	if !strings.Contains(geoOn, "geoip2 /etc/brisk/geoip/GeoLite2-Country.mmdb") ||
		!strings.Contains(geoOn, "$brisk_country source=$remote_addr country iso_code;") {
		t.Error("geoip on: must render the geoip2 block")
	}
	geoOff := renderConf(false, false)
	if strings.Contains(geoOff, "geoip2 /etc/brisk") {
		t.Error("geoip off: must NOT render the geoip2 block")
	}
	if !strings.Contains(geoOff, `map $remote_addr $brisk_country { default "-"; }`) {
		t.Error("geoip off: must render the \"-\" country map fallback")
	}

	// --- errors-only rate limiting via Lua ---
	luaOn := renderConf(true, true)
	if !strings.Contains(luaOn, "lua_shared_dict brisk_rl") {
		t.Error("lua on: must declare the brisk_rl shared dict")
	}
	if !strings.Contains(luaOn, "access_by_lua_file /etc/brisk/lua/access.lua;") ||
		!strings.Contains(luaOn, "log_by_lua_file /etc/brisk/lua/log.lua;") {
		t.Error("zone with errors-only limit must render the Lua access/log hooks")
	}
	// the "all"-mode /api/ limit stays nginx-native; the errors-only /login does NOT.
	if !strings.Contains(luaOn, "location ^~ /api/ {") {
		t.Error("all-mode limit must render as a native limit_req location")
	}
	if strings.Contains(luaOn, "location = /login {\n            limit_req") {
		t.Error("errors-only /login must NOT render as a native limit_req location")
	}

	// with lua OFF, the errors-only limit is simply not enforced (no native, no lua) —
	// and no Lua hooks render at all (byte-identical to a non-lua edge).
	luaOff := renderConf(false, false)
	if strings.Contains(luaOff, "access_by_lua_file") || strings.Contains(luaOff, "lua_shared_dict") {
		t.Error("lua off: no Lua rate-limit directives may render")
	}

	// zones_data.lua carries the errors-only limit (id/path/requests/period) for Lua.
	data := string(renderLuaData(cfg.Zones))
	for _, w := range []string{"rate_limits", `path_match="/login"`, "requests=5", "period=60"} {
		if !strings.Contains(data, w) {
			t.Errorf("renderLuaData missing %q\n%s", w, data)
		}
	}
	// the all-mode /api/ limit is nginx-native -> must NOT be in the Lua data.
	if strings.Contains(data, `path_match="/api/"`) {
		t.Error("all-mode limit must not be emitted into zones_data.lua")
	}
}

// TestRenderOriginLockdown proves Phase 4 Step 6 Part 6: when an origin pull secret
// is set, the edge adds the secret header on origin requests (and the fallback),
// using the default header name; with no secret, no such header renders.
func TestRenderOriginLockdown(t *testing.T) {
	m, err := NewManager("nginx", "/tmp/nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	base := func(secret, header string) string {
		cfg := &config.Config{
			EdgeID: "E", Mode: config.ModeLocal, CacheDir: "/var/cache/brisk", CacheMaxSize: "10g",
			BrotliCompLevel: 5, OriginPullSecret: secret, OriginPullHeader: header,
			Zones: []config.Zone{{Domain: "a.example.com", Origin: "http://o:80", TLS: "selfsigned"}},
		}
		normalize(cfg)
		out, err := m.Render(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}

	// default header name when a secret is set without a custom name.
	on := base("s3cr3t-token", "")
	if !strings.Contains(on, `proxy_set_header X-Brisk-Pull-Token "s3cr3t-token";`) {
		t.Error("origin lockdown: must add the default pull-secret header on origin requests")
	}
	// it must appear on BOTH the origin path and the @brisk_origin_fallback location.
	if n := strings.Count(on, `proxy_set_header X-Brisk-Pull-Token "s3cr3t-token";`); n < 2 {
		t.Errorf("pull-secret header must render on origin + fallback (>=2), got %d", n)
	}
	// custom header name honored.
	if cn := base("abc", "X-Origin-Auth"); !strings.Contains(cn, `proxy_set_header X-Origin-Auth "abc";`) {
		t.Error("origin lockdown: custom header name must be honored")
	}
	// no secret -> no pull header at all (byte-identical to before).
	if off := base("", ""); strings.Contains(off, "X-Brisk-Pull-Token") {
		t.Error("no secret: must not render any pull-secret header")
	}
}
