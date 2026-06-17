package nginx

import (
	"strings"
	"testing"

	"brisk-agent/config"
)

// TestRenderMultiTenant proves Phase 4 Step 1: the agent renders one server block
// per assigned zone (server_name = cdn hostname, proxy_pass = that zone's origin),
// a default_server for unknown hosts, $host-based cache isolation, and a per-zone
// upstream Host header (host_header override, else the origin host).
func TestRenderMultiTenant(t *testing.T) {
	m, err := NewManager("nginx", "/tmp/nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		EdgeID:          "TEST-01",
		Mode:            config.ModeLocal,
		CacheDir:        "/var/cache/brisk",
		CacheMaxSize:    "10g",
		BrotliCompLevel: 5,
		Zones: []config.Zone{
			// tenant A: plain origin, no host_header -> upstream Host = origin host
			{Domain: "cust1.a2zjav.com", Origin: "http://origin-a:8001", TLS: "managed"},
			// tenant B: different origin + explicit upstream host_header override
			{Domain: "cust2.a2zjav.com", Origin: "https://origin-b.internal:8443", TLS: "managed", HostHeader: "www.tenantb.com"},
		},
	}
	// default the few per-zone fields the template reads when Video is off.
	normalize(cfg)

	out, err := m.Render(cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := string(out)

	mustContain := []string{
		// one server block per zone, keyed on the CDN hostname
		"server_name cust1.a2zjav.com;",
		"server_name cust2.a2zjav.com;",
		// each proxies to ITS OWN origin
		"set $brisk_origin origin-a:8001;",
		"set $brisk_origin origin-b.internal:8443;",
		// default_server catches unknown hosts (no tenant leak) but still answers
		// /healthz for the IP/no-SNI control-plane probe.
		"server_name _;",
		"listen 80 default_server;",
		"location / { return 444; }",
		// cache isolation: keys include $host
		"proxy_cache_key $host$uri;",
		"proxy_cache_key $host$request_uri;",
		// per-zone upstream Host: A defaults to origin host, B uses the override
		"proxy_set_header Host origin-a;",
		"proxy_set_header Host www.tenantb.com;",
		// B is https origin -> SNI uses the per-zone upstream host
		"proxy_ssl_name www.tenantb.com;",
	}
	for _, want := range mustContain {
		if !strings.Contains(conf, want) {
			t.Errorf("rendered config missing %q", want)
		}
	}

	if n := strings.Count(conf, "server_name _;"); n != 1 {
		t.Errorf("want exactly 1 default_server (server_name _), got %d", n)
	}
	if n := strings.Count(conf, "return 444;"); n != 1 {
		t.Errorf("want exactly 1 default catch-all (return 444), got %d", n)
	}
	// each zone renders two blocks (:80 redirect + :443), so 2 zones => 4 occurrences.
	if n := strings.Count(conf, "server_name cust"); n != 4 {
		t.Errorf("want 4 (2 zones x :80+:443) tenant server_name lines, got %d", n)
	}
}

// normalize defaults the per-zone fields the template reads (mirrors Load()).
func normalize(cfg *config.Config) {
	for i := range cfg.Zones {
		if cfg.Zones[i].MinUses == 0 {
			cfg.Zones[i].MinUses = 1
		}
		if cfg.Zones[i].CORSOrigin == "" {
			cfg.Zones[i].CORSOrigin = "*"
		}
		if cfg.Zones[i].PlaylistTTL == "" {
			cfg.Zones[i].PlaylistTTL = "2s"
		}
		if cfg.Zones[i].SegmentTTL == "" {
			cfg.Zones[i].SegmentTTL = "12h"
		}
	}
}
