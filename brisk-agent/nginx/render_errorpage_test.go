package nginx

import (
	"strings"
	"testing"

	"brisk-agent/config"
)

// TestRenderErrorPage proves the per-zone custom 502/504 error page (migration 00026):
//   - EMPTY html => byte-identical: no error_page 502 504, no /__brisk_5xx location.
//   - non-empty html => the server block renders error_page 502 504 -> an internal
//     location that aliases the agent-written file, and 503 is NOT intercepted.
func TestRenderErrorPage(t *testing.T) {
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
		errLine  = "error_page 502 504 /__brisk_5xx;"
		errLoc   = "location = /__brisk_5xx {"
		aliasDir = "alias /etc/brisk/errorpages/a_example_com.html;"
	)

	// --- OFF: byte-identical (no error-page directives render) ---
	off := render(config.Zone{Domain: "a.example.com", Origin: "https://o:443", TLS: "selfsigned"})
	for _, bad := range []string{errLine, errLoc, "/__brisk_5xx", "errorpages"} {
		if strings.Contains(off, bad) {
			t.Errorf("error page off: must NOT render %q", bad)
		}
	}

	// --- ON: branded 502/504 page -> internal alias'd file ---
	on := render(config.Zone{
		Domain: "a.example.com", Origin: "https://o:443", TLS: "selfsigned",
		Error5xxHTML: "<html><body>We'll be right back.</body></html>",
	})
	if !strings.Contains(on, errLine) {
		t.Errorf("error page on: want %q", errLine)
	}
	if !strings.Contains(on, errLoc) {
		t.Errorf("error page on: want the internal %q location", errLoc)
	}
	if !strings.Contains(on, aliasDir) {
		t.Errorf("error page on: want the per-zone alias %q", aliasDir)
	}
	// 503 must NOT be intercepted (edge self-protection / rate-limit 503s untouched).
	if strings.Contains(on, "error_page 502 503 504 /__brisk_5xx") || strings.Contains(on, "error_page 503 /__brisk_5xx") {
		t.Error("error page on: 503 must NOT be intercepted by the custom page")
	}
}
