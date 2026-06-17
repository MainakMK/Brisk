package nginx

import (
	"strings"
	"testing"

	"brisk-agent/config"
)

// TestRenderCustomDomain proves Phase 4 Step 2 at the agent: with a control-plane
// URL set, every :80 block (per-zone + default_server) proxies the ACME HTTP-01
// challenge to the control plane (NOT the legacy webroot) and does NOT redirect it
// to HTTPS, while a managed custom-domain zone renders its OWN SNI server block
// with its own cert — reusing the multi-tenant rendering path.
func TestRenderCustomDomain(t *testing.T) {
	m, err := NewManager("nginx", "/tmp/nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		EdgeID:          "TEST-01",
		Mode:            config.ModeLocal,
		ControlPlaneURL: "http://127.0.0.1:18080", // pull mode -> challenge proxy
		CacheDir:        "/var/cache/brisk",
		CacheMaxSize:    "10g",
		BrotliCompLevel: 5,
		Zones: []config.Zone{
			// the tenant's Brisk zone (managed wildcard cert)
			{Domain: "cdn.a2zjav.com", Origin: "https://origin.internal", TLS: "managed"},
			// a custom domain rendered as its own managed vhost (synthetic zone the
			// control plane appends; at the agent it's just a managed zone with cert)
			{Domain: "cdn.customer.com", Origin: "https://origin.internal", TLS: "managed",
				TLSCert: "x", TLSKey: "y", TLSCertSerial: "abc"},
		},
	}
	normalize(cfg)

	out, err := m.Render(cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	conf := string(out)

	mustContain := []string{
		// the custom domain has its OWN server block + own cert path (SNI selects it)
		"server_name cdn.customer.com;",
		"ssl_certificate     /etc/brisk/tls/cdn.customer.com/fullchain.pem;",
		// challenge proxies to the control plane (the tunnel), on plain :80
		"proxy_pass http://127.0.0.1:18080;",
		"location ^~ /.well-known/acme-challenge/ {",
		// default_server still answers /healthz (by-IP probe) and 444s unknown hosts
		"server_name _;",
		"location / { return 444; }",
	}
	for _, want := range mustContain {
		if !strings.Contains(conf, want) {
			t.Errorf("rendered config missing %q", want)
		}
	}

	// With a control plane configured, the legacy LE webroot must NOT be used.
	if strings.Contains(conf, "root /var/www/brisk-acme;") {
		t.Errorf("expected challenge proxy, but legacy webroot root is present")
	}

	// The challenge location must NOT be swallowed by the 80->443 redirect: the
	// acme-challenge proxy_pass must appear (it's ^~, so it wins). Count the proxy
	// occurrences: 2 per-zone :80 blocks + 1 default_server = 3.
	if n := strings.Count(conf, "proxy_pass http://127.0.0.1:18080;"); n != 3 {
		t.Errorf("want 3 challenge proxy_pass (2 zones + default_server), got %d", n)
	}
}
