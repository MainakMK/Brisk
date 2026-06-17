package nginx

import (
	"strings"
	"testing"

	"brisk-agent/config"
)

// TestRenderOriginOptions proves the per-zone origin connection options (migration
// 00025), all OFF => byte-identical:
//   - Verify origin SSL certificate => proxy_ssl_verify on (https origins).
//   - Forward host header => the client's Host ($host) goes upstream.
//   - Follow redirects => a @brisk_follow_redirect named location + the 3xx error_page
//     intercept on the origin proxy.
func TestRenderOriginOptions(t *testing.T) {
	m, err := NewManager("nginx", "/tmp/nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	render := func(z config.Zone) string {
		cfg := &config.Config{
			EdgeID: "E", Mode: config.ModeLocal, CacheDir: "/var/cache/brisk",
			CacheMaxSize: "10g", BrotliCompLevel: 5,
			Zones: []config.Zone{z},
		}
		normalize(cfg)
		out, err := m.Render(cfg)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		return string(out)
	}

	const (
		verifyMark  = "Verify origin SSL certificate"
		forwardMark = "Forward host header: send the client's Host upstream"
		followLoc   = "location @brisk_follow_redirect {"
		followErr   = "error_page 301 302 303 307 308 = @brisk_follow_redirect;"
	)

	// --- OFF: byte-identical (no new directive renders) ---
	off := render(config.Zone{Domain: "a.example.com", Origin: "https://o:443", TLS: "selfsigned"})
	for _, bad := range []string{"proxy_ssl_verify on", verifyMark, forwardMark, followLoc, followErr, "@brisk_follow_redirect"} {
		if strings.Contains(off, bad) {
			t.Errorf("origin options off: must NOT render %q", bad)
		}
	}
	// Off + https origin still sends the upstream Host (derived "o"), not the client's.
	if !strings.Contains(off, "proxy_set_header Host o;") {
		t.Error("off: origin proxy must send the upstream Host (Host o;)")
	}

	// --- Verify origin SSL (https origin) ---
	ssl := render(config.Zone{
		Domain: "a.example.com", Origin: "https://o:443", TLS: "selfsigned",
		OriginSSLVerify: true,
	})
	if !strings.Contains(ssl, "proxy_ssl_verify on;") {
		t.Error("verify-ssl: want proxy_ssl_verify on")
	}
	if !strings.Contains(ssl, "proxy_ssl_trusted_certificate /etc/ssl/certs/ca-certificates.crt;") {
		t.Error("verify-ssl: want the system CA bundle as trusted cert")
	}

	// --- Forward host header ---
	fwd := render(config.Zone{
		Domain: "a.example.com", Origin: "http://o:80", TLS: "selfsigned",
		ForwardHostHeader: true,
	})
	if !strings.Contains(fwd, forwardMark) {
		t.Error("forward-host: want the client Host forwarded to the origin")
	}

	// --- Follow redirects ---
	fol := render(config.Zone{
		Domain: "a.example.com", Origin: "http://o:80", TLS: "selfsigned",
		OriginFollowRedirects: true,
	})
	if !strings.Contains(fol, followLoc) {
		t.Error("follow-redirects: want the @brisk_follow_redirect location")
	}
	if !strings.Contains(fol, followErr) {
		t.Error("follow-redirects: want the 3xx error_page intercept on the origin proxy")
	}
	if !strings.Contains(fol, "set $brisk_redirect $upstream_http_location;") {
		t.Error("follow-redirects: want the redirect target from $upstream_http_location")
	}
}
