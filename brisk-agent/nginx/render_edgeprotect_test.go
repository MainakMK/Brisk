package nginx

import (
	"strings"
	"testing"

	"brisk-agent/config"
)

// edgeProtectCfg builds a minimal single-zone config for the edge-protection render
// tests. Render does not run config.applyDefaults (that happens in config.Load at
// runtime), so each test sets the Edge* fields explicitly.
func edgeProtectCfg() *config.Config {
	cfg := &config.Config{
		EdgeID:          "TEST-EP",
		Mode:            config.ModeLocal,
		CacheDir:        "/var/cache/brisk",
		CacheMaxSize:    "10g",
		BrotliCompLevel: 5,
		Zones: []config.Zone{
			{Domain: "cust.a2zjav.com", Origin: "http://origin:8000", TLS: "managed"},
		},
	}
	normalize(cfg)
	return cfg
}

// Off (the default) => NONE of the edge-protection directives render: byte-identical.
func TestRenderEdgeProtect_OffByDefault(t *testing.T) {
	m, _ := NewManager("nginx", "/tmp/nginx.conf")
	out, err := m.Render(edgeProtectCfg())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := string(out)
	for _, forbidden := range []string{
		"limit_conn_zone $binary_remote_addr zone=brisk_perip",
		"limit_conn brisk_perip",
		"limit_conn_status 503",
		"zone=brisk_edge",
	} {
		if strings.Contains(conf, forbidden) {
			t.Errorf("edge protect OFF must not render %q", forbidden)
		}
	}
}

// On with just the connection cap (no rate): the http zone + per-server caps render,
// 503 on saturation, and NO request-rate zone.
func TestRenderEdgeProtect_ConnCapOnly(t *testing.T) {
	cfg := edgeProtectCfg()
	cfg.EdgeProtect = true
	cfg.EdgeMaxConnPerIP = 150
	// (no rate set)

	m, _ := NewManager("nginx", "/tmp/nginx.conf")
	out, err := m.Render(cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := string(out)

	for _, want := range []string{
		"limit_conn_zone $binary_remote_addr zone=brisk_perip:10m;",
		"limit_conn_status 503;",
		"limit_conn brisk_perip 150;",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(conf, "zone=brisk_edge") {
		t.Error("no request-rate zone should render when the rate is 0")
	}
	// The cap is applied in the tenant :80 + :443 blocks AND the default_server (per-IP,
	// so the health-checker IP keeps its own budget). Expect at least those three.
	if n := strings.Count(conf, "limit_conn brisk_perip 150;"); n < 3 {
		t.Errorf("expected the conn cap in tenant :80/:443 + default_server (>=3), got %d", n)
	}
}

// On with a request-rate ceiling too: the limit_req_zone + per-server limit_req render
// with the burst.
func TestRenderEdgeProtect_WithRequestRate(t *testing.T) {
	cfg := edgeProtectCfg()
	cfg.EdgeProtect = true
	cfg.EdgeMaxConnPerIP = 200
	cfg.EdgeReqPerSecPerIP = 20
	cfg.EdgeReqBurst = 40 // runtime applyDefaults would set 2×rate; set it explicitly here

	m, _ := NewManager("nginx", "/tmp/nginx.conf")
	out, err := m.Render(cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := string(out)

	for _, want := range []string{
		"limit_req_zone $binary_remote_addr zone=brisk_edge:10m rate=20r/s;",
		"limit_req zone=brisk_edge burst=40 nodelay;",
		"limit_conn brisk_perip 200;",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q", want)
		}
	}
}
