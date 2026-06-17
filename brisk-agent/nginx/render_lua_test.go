package nginx

import (
	"bytes"
	"strings"
	"testing"

	"brisk-agent/config"
)

// TestRenderLuaHooks proves Phase 4 Step 5: when the lua module is available, a
// zone WITH cache rules / header transforms gets the rewrite/header_filter Lua
// hooks + the http-block load_module + init_by_lua; a zone WITHOUT rules gets no
// hooks (byte-identical). With luaEnabled=false, no Lua is rendered at all.
func TestRenderLuaHooks(t *testing.T) {
	m, err := NewManager("nginx", "/tmp/nginx.conf")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		EdgeID: "E", Mode: config.ModeLocal, CacheDir: "/var/cache/brisk",
		CacheMaxSize: "10g", BrotliCompLevel: 5,
		Zones: []config.Zone{
			{Domain: "a.example.com", Origin: "http://o:80", TLS: "selfsigned",
				CacheRules: []config.ZoneCacheRule{
					{Priority: 0, MatchType: "path_prefix", MatchValue: "/old", Action: "redirect", ActionValue: "/new"},
				},
				HeaderTransforms: []config.ZoneHeaderTransform{
					{Priority: 0, Phase: "response", Op: "set", Header: "X-Demo", Value: "1", MatchType: "all"},
				}},
			{Domain: "b.example.com", Origin: "http://o:80", TLS: "selfsigned"}, // no rules -> no lua
		},
	}
	normalize(cfg)

	render := func(luaEnabled bool) string {
		rd, err := buildRenderData(cfg, false, luaEnabled, false)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := m.tmpl.ExecuteTemplate(&buf, "nginx.conf.tmpl", rd); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	on := render(true)
	for _, w := range []string{
		"load_module /etc/nginx/modules/ngx_http_lua_module.so;",
		"init_by_lua_file /etc/brisk/lua/init.lua;",
		"set $brisk_zone a.example.com;",
		"rewrite_by_lua_file /etc/brisk/lua/rewrite.lua;",
		"header_filter_by_lua_file /etc/brisk/lua/header_filter.lua;",
	} {
		if !strings.Contains(on, w) {
			t.Errorf("lua-on render missing %q", w)
		}
	}
	if strings.Contains(on, "set $brisk_zone b.example.com;") {
		t.Error("zone b (no rules/transforms) must NOT get Lua hooks")
	}

	off := render(false)
	if strings.Contains(off, "lua_module") || strings.Contains(off, "rewrite_by_lua_file") || strings.Contains(off, "init_by_lua") {
		t.Error("luaEnabled=false must render no Lua directives (byte-identical to before)")
	}
}

// TestRenderLuaData proves the per-zone data file: TTLs converted to seconds,
// priority-ordered, header transforms split by phase, only zones with data emitted.
func TestRenderLuaData(t *testing.T) {
	zones := []config.Zone{
		{Domain: "a.example.com",
			CacheRules: []config.ZoneCacheRule{
				{Priority: 1, MatchType: "extension", MatchValue: "css", Action: "override_cache_ttl", ActionValue: "30d"},
				{Priority: 0, MatchType: "path_prefix", MatchValue: "/api/", Action: "bypass_cache"},
			},
			HeaderTransforms: []config.ZoneHeaderTransform{
				{Priority: 0, Phase: "request", Op: "set", Header: "X-Up", Value: "1", MatchType: "all"},
				{Priority: 0, Phase: "response", Op: "remove", Header: "X-Drop", MatchType: "all"},
			}},
		{Domain: "empty.example.com"}, // no rules/transforms -> not emitted
	}
	out := string(renderLuaData(zones))
	for _, w := range []string{
		`["a.example.com"]`, "cache_rules", `action="override_cache_ttl"`, `action_value="2592000"`,
		`action="bypass_cache"`, "req_headers", "resp_headers", `op="remove"`, `header="X-Drop"`,
	} {
		if !strings.Contains(out, w) {
			t.Errorf("renderLuaData missing %q\n%s", w, out)
		}
	}
	if strings.Contains(out, "empty.example.com") {
		t.Error("a zone with no rules/transforms must not be emitted")
	}
	// priority order: bypass (p0) before the css override_ttl (p1).
	if strings.Index(out, "bypass_cache") > strings.Index(out, `match_value="css"`) {
		t.Error("cache rules not priority-ordered (bypass p0 should precede css p1)")
	}
}

func TestTTLSeconds(t *testing.T) {
	cases := map[string]string{"30d": "2592000", "1h": "3600", "5m": "300", "2s": "2", "600": "600", "": "0", "bad": "0"}
	for in, want := range cases {
		if got := ttlSeconds(in); got != want {
			t.Errorf("ttlSeconds(%q)=%q want %q", in, got, want)
		}
	}
}
