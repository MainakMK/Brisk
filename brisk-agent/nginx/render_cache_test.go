package nginx

import (
	"bytes"
	"strings"
	"testing"

	"brisk-agent/config"
)

// renderConf renders the full nginx.conf for the given zones (helper for the cache tests).
func renderConf(t *testing.T, zones []config.Zone) string {
	t.Helper()
	m, err := NewManager("nginx", "/tmp/nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		EdgeID: "E", Mode: config.ModeLocal, CacheDir: "/var/cache/brisk",
		CacheMaxSize: "10g", BrotliCompLevel: 5, Zones: zones,
	}
	normalize(cfg)
	rd, err := buildRenderData(cfg, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := m.tmpl.ExecuteTemplate(&buf, "nginx.conf.tmpl", rd); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestRenderCacheDefaults proves a zone with NO Cache Settings renders the original
// directives — the byte-identical guarantee for the live fleet (Part 3).
func TestRenderCacheDefaults(t *testing.T) {
	out := renderConf(t, []config.Zone{{Domain: "a.example.com", Origin: "http://o:80", TLS: "selfsigned"}})
	for _, w := range []string{
		"proxy_cache_key $host$uri;",
		"proxy_cache_valid 200 301 302 30d;",
		"proxy_cache_use_stale error timeout updating http_500 http_502 http_503 http_504;",
		"proxy_ignore_headers Set-Cookie Cache-Control Expires Vary;",
		"more_set_headers 'Cache-Control: public, max-age=2592000';",
		"proxy_cache_key $host$request_uri;",
		"proxy_cache_valid 200 301 302 10m;",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("default render missing original directive %q", w)
		}
	}
	// Default zones must NOT pull in the Vary maps (kept byte-identical).
	if strings.Contains(out, "map $http_accept $brisk_webp") || strings.Contains(out, "map $http_user_agent $brisk_device") {
		t.Error("default zone must not render the webp/device Vary maps")
	}
}

// TestRenderCacheToggles proves the controls change the rendered nginx: cache-key
// Vary dimensions, edge-TTL override, cache-error responses, stale-off, strip-cookies,
// large-object slice, and no-cache browser Cache-Control.
func TestRenderCacheToggles(t *testing.T) {
	cs := &config.ZoneCacheSettings{
		EdgeMode: "override", EdgeTTL: "1h",
		BrowserMode: "no_cache",
		CacheErrors: true,
		VaryWebp:    true, VaryDevice: true, VaryCountry: true,
		VaryCookie:    "sessionid",
		StripCookies:  true,
		StaleOffline:  false,
		StaleUpdating: false,
		LargeObject:   true,
	}
	out := renderConf(t, []config.Zone{{Domain: "a.example.com", Origin: "http://o:80", TLS: "selfsigned", CacheSettings: cs}})
	for _, w := range []string{
		// Vary dimensions folded into the static cache key ($host stays first), + slice.
		"proxy_cache_key $host$uri$brisk_webp$brisk_device$brisk_country$cookie_sessionid$slice_range;",
		"map $http_accept $brisk_webp",       // webp map rendered
		"map $http_user_agent $brisk_device", // device map rendered
		"slice 1m;",                          // large object
		"proxy_cache_valid 200 206 301 302 1h;",                                   // edge TTL override + 206 for slicing
		"proxy_cache_valid 500 502 503 504 5s;",                                   // cache error responses
		"proxy_cache_use_stale off;",                                              // both stale toggles off
		"more_set_headers 'Cache-Control: no-store, no-cache, must-revalidate';",  // browser no_cache
		"proxy_ignore_headers Set-Cookie;",                                        // strip cookies on html
	} {
		if !strings.Contains(out, w) {
			t.Errorf("toggled render missing %q\n", w)
		}
	}
	// $host must always lead the key (tenant isolation).
	if i := strings.Index(out, "proxy_cache_key $host$uri$brisk_webp"); i < 0 {
		t.Error("cache key must start with $host for tenant isolation")
	}
}
