package nginx

import (
	"strings"
	"testing"

	"brisk-agent/config"
)

// TestRenderAccessFlags proves the per-zone access toggles (migration 00028):
//   - both OFF => byte-identical: neither `if` block renders.
//   - BlockPost  => a server-level `if ($request_method = POST) { return 405; }`.
//   - BlockRootPath => a server-level `if ($uri ~ "/$") { return 403; }`.
func TestRenderAccessFlags(t *testing.T) {
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
		postMark = `if ($request_method = POST) { return 405; }`
		rootMark = `if ($uri ~ "/$") { return 403; }`
	)

	// --- OFF: byte-identical (no access-toggle if blocks) ---
	off := render(config.Zone{Domain: "a.example.com", Origin: "https://o:443", TLS: "selfsigned"})
	for _, bad := range []string{postMark, rootMark} {
		if strings.Contains(off, bad) {
			t.Errorf("access flags off: must NOT render %q", bad)
		}
	}

	// --- Block POST ---
	bp := render(config.Zone{Domain: "a.example.com", Origin: "https://o:443", TLS: "selfsigned", BlockPost: true})
	if !strings.Contains(bp, postMark) {
		t.Errorf("block post: want %q", postMark)
	}
	if strings.Contains(bp, rootMark) {
		t.Error("block post: must NOT render the root-path block")
	}

	// --- Block root path ---
	br := render(config.Zone{Domain: "a.example.com", Origin: "https://o:443", TLS: "selfsigned", BlockRootPath: true})
	if !strings.Contains(br, rootMark) {
		t.Errorf("block root: want %q", rootMark)
	}
	if strings.Contains(br, postMark) {
		t.Error("block root: must NOT render the POST block")
	}
}
