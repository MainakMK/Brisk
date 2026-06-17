package nginx

import (
	"strings"
	"testing"

	"brisk-agent/config"
)

// TestRenderShield proves Phase 4 Step 3: a shielded zone proxies its misses to the
// shield (Host=$host so the shield caches under the SAME key) with the origin as an
// error_page fallback and an X-Brisk-Shield observability header; a non-shielded
// zone on the same edge still proxies its origin directly (per-zone isolation).
func TestRenderShield(t *testing.T) {
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
		Zones: []config.Zone{
			// zone A: shielded -> proxy to the shield PoP
			{Domain: "a.example.com", Origin: "http://origin-a:8001", TLS: "selfsigned",
				ShieldUpstream: "shield:443"},
			// zone B: NOT shielded -> proxy origin B directly
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
		// zone A routes misses to the shield via the keepalive pool (Build Spec L3),
		// with the origin error_page fallback
		"proxy_pass https://brisk_shield_shield_443;",
		// http-context keepalive pool for the edge->shield leg (warm pooled conns)
		"upstream brisk_shield_shield_443 {",
		"server shield:443;",
		"keepalive 32;",
		"error_page 502 503 504 = @brisk_origin_fallback;",
		"location @brisk_origin_fallback {",
		"more_set_headers 'X-Brisk-Shield: $upstream_http_x_brisk_cache';",
		// shield hop forwards the served hostname so both tiers key on $host
		"proxy_set_header Host $host;",
		"proxy_ssl_name $host;",
		// zone B still hits its own origin directly (no shield)
		"set $brisk_origin origin-b:8002;",
		// zone A's fallback targets origin A (the real upstream)
		"set $brisk_origin origin-a:8001;",
	}
	for _, want := range mustContain {
		if !strings.Contains(conf, want) {
			t.Errorf("rendered config missing %q", want)
		}
	}

	// The shield appears in all of zone A's proxied locations (static + html = 2 for
	// a non-video zone) but NOT in zone B. Count the shield proxy_pass: 2.
	if n := strings.Count(conf, "proxy_pass https://brisk_shield_shield_443;"); n != 2 {
		t.Errorf("want 2 shield proxy_pass (zone A static+html), got %d", n)
	}
	// Exactly one pooled keepalive upstream for the single distinct shield host:port.
	if n := strings.Count(conf, "upstream brisk_shield_shield_443 {"); n != 1 {
		t.Errorf("want 1 shield keepalive upstream, got %d", n)
	}
	// Exactly one fallback location for the one shielded zone.
	if n := strings.Count(conf, "location @brisk_origin_fallback {"); n != 1 {
		t.Errorf("want 1 @brisk_origin_fallback (zone A only), got %d", n)
	}
	// X-Brisk-Shield only on the shielded zone.
	if n := strings.Count(conf, "X-Brisk-Shield:"); n != 1 {
		t.Errorf("want X-Brisk-Shield on the 1 shielded zone only, got %d", n)
	}
}
