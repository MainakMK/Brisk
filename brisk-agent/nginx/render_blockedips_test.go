package nginx

import (
	"strings"
	"testing"

	"brisk-agent/config"
)

// TestRenderBlockedIPs proves the per-zone Blocked-IP denylist (migration 00027):
//   - EMPTY list => byte-identical: no `deny` directive anywhere.
//   - non-empty => one `deny <entry>;` per IP/CIDR on the CONTENT locations, and the
//     /healthz location is NEVER denied (the control-plane checker must always reach it).
func TestRenderBlockedIPs(t *testing.T) {
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

	// --- OFF: byte-identical (no ipblockguard output) ---
	// NB: the http block has a pre-existing, unrelated `deny all;` (stub_status guard),
	// so we assert the absence of OUR guard's output specifically — the per-entry deny
	// lines + the denylist comment marker — not any `deny`.
	off := render(config.Zone{Domain: "a.example.com", Origin: "https://o:443", TLS: "selfsigned"})
	for _, bad := range []string{"deny 203.", "# blocked IP/CIDR (denylist)"} {
		if strings.Contains(off, bad) {
			t.Errorf("blocked IPs off: must NOT render %q", bad)
		}
	}

	// --- ON: deny lines for an IP and a CIDR ---
	on := render(config.Zone{
		Domain: "a.example.com", Origin: "https://o:443", TLS: "selfsigned",
		BlockedIPs: "203.0.113.4,203.0.113.0/24",
	})
	if !strings.Contains(on, "deny 203.0.113.4;") {
		t.Error("blocked IPs on: want deny 203.0.113.4;")
	}
	if !strings.Contains(on, "deny 203.0.113.0/24;") {
		t.Error("blocked IPs on: want deny 203.0.113.0/24;")
	}

	// The /healthz location must NEVER be denied: the deny lines live only on content
	// locations, so the health probe block stays a clean 200 return.
	if i := strings.Index(on, "location = /healthz {"); i >= 0 {
		block := on[i:]
		if j := strings.Index(block, "}"); j >= 0 {
			block = block[:j]
		}
		if strings.Contains(block, "deny ") {
			t.Error("blocked IPs on: /healthz must NOT carry a deny directive")
		}
	}
}
