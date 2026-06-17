package nginx

import (
	"strings"
	"testing"

	"brisk-agent/config"
)

// TestRenderHotlink proves the per-zone hotlink protection (Referer allowlist):
//   - OFF (nil) => no valid_referers, no 403 guard (byte-identical to before).
//   - ON => a server-level `valid_referers` + the `if ($invalid_referer) return 403;`
//     guard on CONTENT locations only (never /healthz or the ACME challenge — those
//     get no Referer and must always answer, or the health checker would pull the edge).
//   - allow-empty toggles the "none blocked" tokens.
func TestRenderHotlink(t *testing.T) {
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

	const guard = "if ($invalid_referer) { return 403; }"

	// --- OFF: byte-identical (no directive, no guard) ---
	off := render(config.Zone{Domain: "a.example.com", Origin: "http://o:80", TLS: "selfsigned"})
	if strings.Contains(off, "valid_referers") || strings.Contains(off, "$invalid_referer") {
		t.Error("hotlink off: must render no valid_referers and no guard")
	}

	// --- ON, empty BLOCKED, two hosts, non-video ---
	// server-level valid_referers with the hosts and NO "none" (empty blocked); the
	// guard appears exactly on the 2 content locations (static + html). The 4 /healthz
	// + redirect blocks must NOT carry it — proven by the count being exactly 2.
	on := render(config.Zone{
		Domain: "a.example.com", Origin: "http://o:80", TLS: "selfsigned",
		Hotlink: &config.ZoneHotlink{AllowedReferrers: "example.com,*.cdn.example.com", AllowEmpty: false},
	})
	if !strings.Contains(on, "valid_referers server_names example.com *.cdn.example.com;") {
		t.Errorf("want valid_referers with the hosts and no 'none' (empty blocked)")
	}
	if n := strings.Count(on, guard); n != 2 {
		t.Errorf("want 2 hotlink guards (static + html content locations), got %d", n)
	}
	if !strings.Contains(on, "location = /healthz {") {
		t.Error("healthz location must still render (and must NOT carry the guard)")
	}

	// --- ON, empty ALLOWED: "none blocked" precede server_names ---
	allowEmpty := render(config.Zone{
		Domain: "a.example.com", Origin: "http://o:80", TLS: "selfsigned",
		Hotlink: &config.ZoneHotlink{AllowedReferrers: "example.com", AllowEmpty: true},
	})
	if !strings.Contains(allowEmpty, "valid_referers none blocked server_names example.com;") {
		t.Errorf("allow-empty: want 'none blocked' in valid_referers")
	}

	// --- ON + VIDEO: 4 content-location guards (m3u8 + ts/mp4 + static + html) ---
	vid := render(config.Zone{
		Domain: "a.example.com", Origin: "http://o:80", TLS: "selfsigned", Video: true,
		Hotlink: &config.ZoneHotlink{AllowedReferrers: "example.com", AllowEmpty: true},
	})
	if n := strings.Count(vid, guard); n != 4 {
		t.Errorf("video zone: want 4 hotlink guards (m3u8 + ts + static + html), got %d", n)
	}
}
