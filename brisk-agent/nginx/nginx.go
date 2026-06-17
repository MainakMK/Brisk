// Package nginx renders the Nginx configuration from the agent config and
// applies it safely.
//
// Golden rule (Spec §13): never reload Nginx with a bad config. Apply always
// validates with `nginx -t` BEFORE reloading, and keeps the previous good
// config so it can roll back if validation fails.
//
// Phase 1 scope so far: HTTP caching of the origin (Step 2) plus headers-more
// branding (Server: Brisk + X-Brisk-* on every status), HLS/video slice caching
// with per-slice request coalescing and CORS, and per-zone playlist/segment
// TTLs (Step 3). TLS (Step 4) and Brotli (Step 5) are added in later steps.
package nginx

import (
	"bytes"
	"embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"brisk-agent/config"
	"brisk-agent/tls"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// luaFS holds the static Brisk edge Lua library (Phase 4 Step 5). The agent writes
// these to LuaDir on each apply; the per-zone data (zones_data.lua) is rendered.
//
//go:embed lua/*.lua
var luaFS embed.FS

// defaultLuaDir is where the agent writes the Lua library + per-zone data, and
// where nginx loads them (init_by_lua_file / *_by_lua_file). Matches the path
// baked into the Lua files + the lua_package_path in nginx.conf.
const defaultLuaDir = "/etc/brisk/lua"

// luaModulePath is the dynamic lua module the agent renders load_module for only
// when present (so edges WITHOUT the module — today's live fleet — stay
// byte-identical until the gated one-edge-at-a-time rollout adds it).
const luaModulePath = "/etc/nginx/modules/ngx_http_lua_module.so"

// defaultErrorPagesDir is where the agent writes per-zone custom 502/504 error-page
// HTML (migration 00026), one file per zone (<slug>.html). The server.tmpl `alias`es
// each zone's file by the SAME path, so this must match the template's render path.
const defaultErrorPagesDir = "/etc/brisk/errorpages"

// Manager renders and applies the Nginx configuration.
type Manager struct {
	// Bin is the nginx executable (name on PATH or absolute path).
	Bin string
	// ConfPath is the nginx.conf the agent owns and overwrites.
	ConfPath string
	// LuaDir is where the Lua library + per-zone data are written (Phase 4 Step 5).
	LuaDir string
	// ErrPagesDir is where per-zone custom 502/504 error-page HTML is written
	// (migration 00026). Defaults to defaultErrorPagesDir, which the template aliases.
	ErrPagesDir string

	tmpl *template.Template
}

// NewManager returns a Manager. bin defaults to "nginx" and confPath to
// "/etc/nginx/nginx.conf" when empty.
func NewManager(bin, confPath string) (*Manager, error) {
	if bin == "" {
		bin = "nginx"
	}
	if confPath == "" {
		confPath = "/etc/nginx/nginx.conf"
	}
	tmpl, err := template.ParseFS(templatesFS, "templates/*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Manager{Bin: bin, ConfPath: confPath, LuaDir: defaultLuaDir, ErrPagesDir: defaultErrorPagesDir, tmpl: tmpl}, nil
}

// luaAvailable reports whether the dynamic lua-nginx-module is installed (so the
// agent should render the Lua layer). Detected by the module file's presence, like
// http2OnSupported detects the nginx version — no config flag needed.
func luaAvailable() bool {
	_, err := os.Stat(luaModulePath)
	return err == nil
}

// geoipModulePath is the ngx_http_geoip2 dynamic module + the GeoLite2 DB; both must
// exist for the edge to resolve $brisk_country (Phase 4 Step 6 Part 5). Until then,
// the country placeholder ("-") is used — same gated pattern as lua.
const (
	geoipModulePath = "/etc/nginx/modules/ngx_http_geoip2_module.so"
	geoipDBPath     = "/etc/brisk/geoip/GeoLite2-Country.mmdb"
)

// geoipAvailable reports whether GeoIP is installed (module + DB present).
func geoipAvailable() bool {
	if _, err := os.Stat(geoipModulePath); err != nil {
		return false
	}
	_, err := os.Stat(geoipDBPath)
	return err == nil
}

// --- template data ---------------------------------------------------------

type renderData struct {
	EdgeID          string
	CacheDir        string
	CacheMaxSize    string
	SessionTickets  bool // TLS session tickets on/off (Step 4)
	BrotliCompLevel int  // on-the-fly Brotli level (Step 5)
	HTTP2On         bool // HTTP/2 syntax for the default_server (Phase 4 Step 1)
	// default_server cert (multi-tenant): the IP/no-SNI health probe lands on the
	// default_server, which needs a cert to complete the TLS handshake. Reuse the
	// first zone's managed cert path (all Brisk-subdomain zones share the wildcard).
	DefaultCertPath string
	DefaultKeyPath  string
	// ACMEChallengeProxy is the control-plane base URL (the agent tunnel port) the
	// edge proxies :80 /.well-known/acme-challenge/* to (Phase 4 Step 2 custom-domain
	// HTTP-01). Empty (standalone/local) => fall back to the LE webroot. A literal
	// 127.0.0.1 URL, so proxy_pass needs no resolver.
	ACMEChallengeProxy string

	// RateLimitZones are the http-context limit_req_zone declarations (Phase 4
	// Step 4) — one per configured rate limit across all zones (flattened).
	RateLimitZones []limitZoneRender

	// ShieldUpstreams are the http-context `upstream {}` pools for the edge->shield
	// leg (Hybrid Shield+PoP Build Spec, Layer 3 — keepalive). One per DISTINCT
	// shield host:port across all shielded zones, so the edge reuses warm pooled
	// connections to the shield instead of re-handshaking on every miss (the
	// PoP-to-PoP speed-up). Empty when no zone is shielded => byte-identical config.
	ShieldUpstreams []shieldUpstreamRender

	// LuaEnabled => the dynamic lua-nginx-module is installed, so the http block
	// loads it + init_by_lua (Phase 4 Step 5). False on edges without the module
	// (today's live fleet) => no Lua directives, byte-identical to before.
	LuaEnabled bool

	// GeoIPEnabled => the ngx_http_geoip2 module + GeoLite2 DB are installed, so
	// $brisk_country resolves the client country (Phase 4 Step 6). Else a "-"
	// placeholder map. Edge id appears in the JSON access log via EdgeID.
	GeoIPEnabled bool

	// Cache-Settings Vary maps (Part 3): rendered into the http block only when at
	// least one zone varies its cache by browser image support / device class, so a
	// fleet with no such zone stays byte-identical.
	NeedWebpMap   bool
	NeedDeviceMap bool

	// Edge self-protection (#4): the instant per-IP brake. When EdgeProtect is on, the
	// http block declares a limit_conn_zone (+ an optional limit_req_zone for a
	// per-IP request-rate ceiling) and every server block applies the caps; an IP that
	// floods gets 503 (conns) / 429 (rate). Off => no directives => byte-identical.
	EdgeProtect        bool
	EdgeMaxConnPerIP   int
	EdgeReqPerSecPerIP int // 0 => no request-rate ceiling (only the conn cap)
	EdgeReqBurst       int

	Zones []zoneRender
}

// limitZoneRender is one http-context limit_req_zone (shared-memory counter).
type limitZoneRender struct {
	Name string // unique zone name, e.g. brisk_rl_a_example_com_0
	Key  string // $binary_remote_addr or $binary_remote_addr$uri
	Rate string // e.g. 5r/m
}

// shieldUpstreamRender is one http-context `upstream {}` pool for the edge->shield
// leg (Build Spec Layer 3). Name is a unique nginx upstream name; Server is the
// shield host:port. The pool adds `keepalive` so misses reuse warm connections.
type shieldUpstreamRender struct {
	Name   string // e.g. brisk_shield_203_0_113_7_443
	Server string // shield host:port, e.g. 203.0.113.7:443
}

// rateLimitRender is one per-zone limit_req location (the rate-limited path).
type rateLimitRender struct {
	LocationMatcher string // "= /wp-login.php" (exact) or "^~ /api" (prefix)
	ZoneName        string // matches a limitZoneRender.Name
	Burst           int
	PathMatch       string // for the comment
	Requests        int    // for the comment
	PeriodSeconds   int    // for the comment
	Key             string // ip | ip_path (comment)
}

// hotlinkRenderFor turns a zone's hotlink config into the nginx `valid_referers`
// directive argument + an enabled flag. nil/off => (false, "") so the template renders
// nothing (byte-identical). The control plane already strictly validated each host, so
// here we only assemble the token list:
//   - "none blocked" included only when empty referers are allowed (no-Referer passes)
//   - "server_names" always (a referer from this CDN host itself passes)
//   - then each allowed host (may be a *.x / x.* wildcard)
func hotlinkRenderFor(h *config.ZoneHotlink) (enabled bool, validReferers string) {
	if h == nil {
		return false, ""
	}
	tokens := make([]string, 0, 8)
	if h.AllowEmpty {
		tokens = append(tokens, "none", "blocked")
	}
	tokens = append(tokens, "server_names")
	for _, ref := range strings.Split(h.AllowedReferrers, ",") {
		if ref = strings.TrimSpace(ref); ref != "" {
			tokens = append(tokens, ref)
		}
	}
	return true, strings.Join(tokens, " ")
}

type zoneRender struct {
	EdgeID         string
	Domain         string
	Upstream       string // sanitized upstream name, unique per zone
	OriginScheme   string // http | https
	OriginHostPort string // host:port
	OriginHost     string // host only (derived from origin URL)
	UpstreamHost   string // Host header + TLS SNI to send upstream (per-zone; Phase 4)

	// --- HLS / video (Step 3) ---
	Video       bool
	PlaylistTTL string
	SegmentTTL  string
	CORSOrigin  string
	MinUses     int

	// --- TLS (Step 4) ---
	TLSCertPath string
	TLSKeyPath  string
	HTTP2On     bool // true => "http2 on;" (nginx>=1.25.1); false => legacy "listen ssl http2"

	// ACMEChallengeProxy is the control-plane base URL the :80 challenge location
	// proxies to (Phase 4 Step 2). Empty => LE webroot fallback. Same value on every
	// zone (copied from the agent config) so the per-zone :80 block can read it.
	ACMEChallengeProxy string

	// --- Origin Shield (Phase 4 Step 3) ---
	// Shielded => this zone's proxied locations target the shield (Host=$host, so the
	// shield caches under the SAME key) with the origin as an error_page fallback,
	// and the response carries X-Brisk-Shield. ShieldHostPort is the shield host:port.
	Shielded       bool
	ShieldHostPort string
	// ShieldUpstreamName is the http-context `upstream {}` pool name this zone's
	// shield leg proxies to (Build Spec Layer 3 — keepalive). Empty unless Shielded.
	ShieldUpstreamName string

	// Resolver is the DNS server(s) for runtime resolution of the variable origin
	// proxy_pass (default public; "127.0.0.11" for Docker-internal origins in tests).
	Resolver string

	// --- Origin lockdown (Phase 4 Step 6 Part 6) ---
	// When OriginPullSecret is set, the edge adds OriginPullHeader: <secret> to every
	// ORIGIN request so the origin can reject non-Brisk traffic. Empty => no header.
	OriginPullHeader string
	OriginPullSecret string

	// --- WAF + rate limiting (Phase 4 Step 4) ---
	// WAFEnabled => inspected content locations carry auth_request /_waf (the agent's
	// Coraza service); WAFUpstream is the service host:port; WAFFailOpen picks the
	// service-unreachable behavior. RateLimits are this zone's limit_req locations.
	WAFEnabled  bool
	WAFFailOpen bool
	WAFUpstream string
	RateLimits  []rateLimitRender

	// --- Lua edge logic (Phase 4 Step 5) ---
	// LuaHooks => this zone has cache rules and/or header transforms AND the lua
	// module is available, so its server block wires rewrite/header_filter Lua +
	// the bypass var. BriskZone is the $brisk_zone key the Lua reads to find this
	// zone's data. Zones without rules/transforms get NO Lua hooks (byte-identical).
	LuaHooks  bool
	BriskZone string
	// LuaRateLimit => this zone has errors-only rate limits (count_mode=errors_only),
	// enforced by the Lua access/log phases (nginx limit_req can't do response-aware
	// limiting). Implies LuaHooks. The "all"-mode limits stay nginx-native (RateLimits).
	LuaRateLimit bool

	// --- Cache Settings (Bunny-style per-zone cache controls). Render-ready directive
	// values for the static + html locations; defaults reproduce today's behavior so a
	// default zone renders functionally identical. See cache.go. ---
	Cache cacheRender

	// --- Edge self-protection (#4): copied from the agent config onto each zone so the
	// per-server-block "edgeprotect" template (which only sees the zone context) can
	// emit the limit_conn / limit_req directives. Off => nothing rendered. ---
	EdgeProtect        bool
	EdgeMaxConnPerIP   int
	EdgeReqPerSecPerIP int
	EdgeReqBurst       int

	// --- Hotlink protection (Referer allowlist). HotlinkEnabled => the server block
	// declares `valid_referers <HotlinkValidReferers>;` and the CONTENT locations (not
	// /healthz/ACME) carry `if ($invalid_referer) return 403;`. Off => nothing rendered
	// (byte-identical). HotlinkValidReferers is the pre-built, validated directive arg. ---
	HotlinkEnabled       bool
	HotlinkValidReferers string

	// --- Origin connection options (Bunny-style, migration 00025). All false => the
	// origin proxy renders byte-identical. OriginSSLVerify => proxy_ssl_verify on (https
	// origins; validates against the system CA bundle). ForwardHostHeader => send the
	// client's Host ($host) upstream instead of UpstreamHost (and SNI follows).
	// OriginFollowRedirects => intercept an origin 3xx and follow it one hop server-side
	// via @brisk_follow_redirect (non-shielded path only). ---
	OriginSSLVerify       bool
	ForwardHostHeader     bool
	OriginFollowRedirects bool

	// --- Custom 502/504 error page (Bunny-style, migration 00026). Error5xxEnabled =>
	// the server block renders `error_page 502 504 /__brisk_5xx;` + an internal location
	// that aliases Error5xxPath (the agent-written HTML file). 503 is intentionally NOT
	// included so edge self-protection / rate-limit 503s are untouched. Off => nothing
	// rendered (byte-identical). ---
	Error5xxEnabled bool
	Error5xxPath    string

	// --- Blocked-IP denylist (Bunny-style, migration 00027). One `deny <cidr>;` per
	// entry, applied on CONTENT locations only (the "ipblockguard" template) — never
	// /healthz or the ACME challenge, so the control-plane health checker is never
	// locked out. Empty => nothing rendered (byte-identical). The control plane already
	// validated each entry as an IP/CIDR. ---
	BlockedIPs []string

	// --- Access toggles (Bunny-style, migration 00028). BlockRootPath => a server-level
	// `if ($uri ~ "/$") { return 403; }` (bare root + directory roots). BlockPost => a
	// server-level `if ($request_method = POST) { return 405; }`. Both false => nothing
	// rendered (byte-identical). On the :443 vhost only; /healthz + :80 ACME untouched. ---
	BlockRootPath bool
	BlockPost     bool
}

// buildRenderData turns the validated agent config into template inputs.
// http2On selects the HTTP/2 syntax for the installed Nginx version.
func buildRenderData(cfg *config.Config, http2On, luaEnabled, geoipEnabled bool) (renderData, error) {
	rd := renderData{
		EdgeID:             cfg.EdgeID,
		CacheDir:           cfg.CacheDir,
		CacheMaxSize:       cfg.CacheMaxSize,
		SessionTickets:     cfg.SessionTicketsEnabled(),
		BrotliCompLevel:    cfg.BrotliCompLevel,
		HTTP2On:            http2On,
		ACMEChallengeProxy: cfg.ControlPlaneURL, // empty in standalone/local => LE webroot fallback
		LuaEnabled:         luaEnabled,
		GeoIPEnabled:       geoipEnabled,
		EdgeProtect:        cfg.EdgeProtect,
		EdgeMaxConnPerIP:   cfg.EdgeMaxConnPerIP,
		EdgeReqPerSecPerIP: cfg.EdgeReqPerSecPerIP,
		EdgeReqBurst:       cfg.EdgeReqBurst,
	}
	// tlsMgr is used only to derive the standard cert paths (no I/O); the agent
	// provisions the actual certs before reload (see main.go / tls.Manager.Ensure).
	tlsMgr := tls.NewManager("", "", false, "")
	resolver := cfg.Resolver
	if strings.TrimSpace(resolver) == "" {
		resolver = "1.1.1.1 8.8.8.8" // default (Load() also sets this; guard direct callers/tests)
	}
	// Shield keepalive pools (Build Spec Layer 3): dedup distinct shield host:ports
	// across zones into one named http-context upstream each, so the edge->shield
	// connections are pooled (keepalive). Stays nil/empty when no zone is shielded.
	shieldByName := map[string]bool{}
	for _, z := range cfg.Zones {
		scheme, hostPort, err := z.OriginParts()
		if err != nil {
			return renderData{}, fmt.Errorf("zone %q: %w", z.Domain, err)
		}
		certs := tlsMgr.Paths(z.Domain)
		originHost := hostPort
		if i := strings.LastIndex(hostPort, ":"); i >= 0 {
			originHost = hostPort[:i]
		}
		// Upstream Host header is per-zone (Phase 4 multi-tenant): an explicit
		// host_header wins; otherwise default to the origin's own host. Defaulting
		// to originHost keeps the existing single-tenant zones byte-for-byte.
		upstreamHost := z.HostHeader
		if upstreamHost == "" {
			upstreamHost = originHost
		}
		// WAF + rate limiting (Phase 4 Step 4). When the zone has WAF on, its
		// inspected content locations get auth_request /_waf, and each configured
		// rate limit (plus the WordPress-preset /wp-login.php limit) renders as a
		// limit_req location + an http-context limit_req_zone (accumulated on rd).
		zw := z.WAF
		wafEnabled := zw != nil && zw.Enabled
		wafUpstream := cfg.WAFListen
		if strings.TrimSpace(wafUpstream) == "" {
			wafUpstream = "127.0.0.1:9555"
		}
		var rls []rateLimitRender
		errorsOnly := false
		if wafEnabled {
			slug := upstreamSlug(z.Domain)
			for i, rl := range zoneRateLimits(z) {
				// errors-only limits are enforced by Lua (response-aware), not nginx
				// limit_req — skip them here; they're emitted into zones_data.lua.
				if rl.CountMode == "errors_only" {
					errorsOnly = true
					continue
				}
				zoneName := fmt.Sprintf("brisk_rl_%s_%d", slug, i)
				key := "$binary_remote_addr"
				if rl.Key == "ip_path" {
					key = "$binary_remote_addr$uri"
				}
				burst := rl.Burst
				if burst <= 0 { // "N per period" => allow the first N, limit the (N+1)th
					burst = rl.Requests - 1
					if burst < 0 {
						burst = 0
					}
				}
				matcher := "= " + rl.PathMatch
				if rl.MatchType == "prefix" {
					matcher = "^~ " + rl.PathMatch
				}
				rls = append(rls, rateLimitRender{
					LocationMatcher: matcher, ZoneName: zoneName, Burst: burst,
					PathMatch: rl.PathMatch, Requests: rl.Requests, PeriodSeconds: rl.PeriodSeconds, Key: rl.Key,
				})
				rd.RateLimitZones = append(rd.RateLimitZones, limitZoneRender{
					Name: zoneName, Key: key, Rate: nginxRate(rl.Requests, rl.PeriodSeconds),
				})
			}
		}

		// One pooled upstream per distinct shield host:port (keepalive); the zone's
		// shield leg proxy_passes to this name instead of the bare host:port.
		shieldName := ""
		if z.ShieldUpstream != "" {
			shieldName = "brisk_shield_" + upstreamSlug(z.ShieldUpstream)
			if !shieldByName[shieldName] {
				shieldByName[shieldName] = true
				rd.ShieldUpstreams = append(rd.ShieldUpstreams, shieldUpstreamRender{Name: shieldName, Server: z.ShieldUpstream})
			}
		}

		// Hotlink protection (Referer allowlist). Off => ("", false) => nothing rendered.
		hotlinkOn, hotlinkReferers := hotlinkRenderFor(z.Hotlink)

		// Custom 502/504 error page (migration 00026). Enabled when the zone has
		// non-empty HTML; the file path mirrors writeErrorPages (defaultErrorPagesDir/
		// <slug>.html). Empty => disabled => nothing rendered (byte-identical). Use "/"
		// (nginx path), not filepath.Join, so the rendered config is OS-independent.
		err5xxEnabled := strings.TrimSpace(z.Error5xxHTML) != ""
		err5xxPath := ""
		if err5xxEnabled {
			err5xxPath = defaultErrorPagesDir + "/" + upstreamSlug(z.Domain) + ".html"
		}

		// Blocked-IP denylist (migration 00027): split the validated comma list into the
		// per-entry `deny` lines. Empty => nil => nothing rendered (byte-identical).
		var blockedIPs []string
		for _, e := range strings.Split(z.BlockedIPs, ",") {
			if e = strings.TrimSpace(e); e != "" {
				blockedIPs = append(blockedIPs, e)
			}
		}

		rd.Zones = append(rd.Zones, zoneRender{
			EdgeID:         cfg.EdgeID,
			Domain:         z.Domain,
			Upstream:       "brisk_origin_" + upstreamSlug(z.Domain),
			OriginScheme:   scheme,
			OriginHostPort: hostPort,
			OriginHost:     originHost,
			UpstreamHost:   upstreamHost,
			Video:          z.Video,
			PlaylistTTL:    z.PlaylistTTL,
			SegmentTTL:     z.SegmentTTL,
			CORSOrigin:     z.CORSOrigin,
			MinUses:        z.MinUses,
			TLSCertPath:        certs.FullChain,
			TLSKeyPath:         certs.PrivKey,
			HTTP2On:            http2On,
			ACMEChallengeProxy: cfg.ControlPlaneURL,
			Shielded:           z.ShieldUpstream != "",
			ShieldHostPort:     z.ShieldUpstream,
			ShieldUpstreamName: shieldName,
			Resolver:           cfg.Resolver,
			OriginPullHeader:   originPullHeader(cfg),
			OriginPullSecret:   cfg.OriginPullSecret,
			WAFEnabled:         wafEnabled,
			WAFFailOpen:        wafEnabled && zw.FailOpen,
			WAFUpstream:        wafUpstream,
			RateLimits:         rls,
			LuaHooks:           luaEnabled && (len(z.CacheRules) > 0 || len(z.HeaderTransforms) > 0 || errorsOnly),
			BriskZone:          z.Domain,
			LuaRateLimit:       luaEnabled && errorsOnly,
			Cache:              cacheRenderFor(z.CacheSettings),
			EdgeProtect:        cfg.EdgeProtect,
			EdgeMaxConnPerIP:   cfg.EdgeMaxConnPerIP,
			EdgeReqPerSecPerIP: cfg.EdgeReqPerSecPerIP,
			EdgeReqBurst:       cfg.EdgeReqBurst,
			HotlinkEnabled:       hotlinkOn,
			HotlinkValidReferers: hotlinkReferers,
			OriginSSLVerify:       z.OriginSSLVerify,
			ForwardHostHeader:     z.ForwardHostHeader,
			OriginFollowRedirects: z.OriginFollowRedirects,
			Error5xxEnabled:       err5xxEnabled,
			Error5xxPath:          err5xxPath,
			BlockedIPs:            blockedIPs,
			BlockRootPath:         z.BlockRootPath,
			BlockPost:             z.BlockPost,
		})
		// Aggregate the http-block Vary maps across zones (rendered once if any zone needs them).
		cr := cacheRenderFor(z.CacheSettings)
		rd.NeedWebpMap = rd.NeedWebpMap || cr.NeedWebpMap
		rd.NeedDeviceMap = rd.NeedDeviceMap || cr.NeedDeviceMap
	}
	// default_server cert = first zone's cert path (Validate guarantees >=1 zone).
	if len(cfg.Zones) > 0 {
		dc := tlsMgr.Paths(cfg.Zones[0].Domain)
		rd.DefaultCertPath = dc.FullChain
		rd.DefaultKeyPath = dc.PrivKey
	}
	return rd, nil
}

// http2OnSupported reports whether the installed Nginx supports the "http2 on;"
// directive (introduced in 1.25.1). Older versions need "listen ... http2".
// On any detection failure it returns false (the legacy syntax works everywhere).
func http2OnSupported(bin string) bool {
	out, err := exec.Command(bin, "-v").CombinedOutput() // prints to stderr
	if err != nil {
		return false
	}
	m := regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`).FindStringSubmatch(string(out))
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	switch {
	case major != 1:
		return major > 1
	case minor != 25:
		return minor > 25
	default:
		return patch >= 1
	}
}

// upstreamSlug makes a domain safe to use inside an Nginx upstream name.
func upstreamSlug(domain string) string {
	return strings.NewReplacer(".", "_", "-", "_", ":", "_").Replace(domain)
}

// zoneRateLimits returns the zone's effective rate limits: the WordPress-preset
// /wp-login.php brute-force limit (when wp_preset is on) first, then the tenant's
// configured limits. The xmlrpc + bad-UA WP blocks are Coraza custom rules (the
// WAF engine), not rate limits — so they are not added here.
func zoneRateLimits(z config.Zone) []config.ZoneRateLimit {
	if z.WAF == nil {
		return nil
	}
	var out []config.ZoneRateLimit
	if z.WAF.WpPreset {
		out = append(out, config.ZoneRateLimit{
			PathMatch: "/wp-login.php", MatchType: "exact",
			Requests: 5, PeriodSeconds: 60, Burst: 4, Key: "ip", Action: "block", Enabled: true,
		})
	}
	for _, rl := range z.WAF.RateLimits {
		if rl.Enabled {
			out = append(out, rl)
		}
	}
	return out
}

// originPullHeader is the header name for origin lockdown (Phase 4 Step 6 Part 6):
// the configured name, or the default when a secret is set without a custom name.
func originPullHeader(cfg *config.Config) string {
	if strings.TrimSpace(cfg.OriginPullSecret) == "" {
		return ""
	}
	if h := strings.TrimSpace(cfg.OriginPullHeader); h != "" {
		return h
	}
	return "X-Brisk-Pull-Token"
}

// zoneErrorsOnlyLimits returns the zone's enabled errors-only rate limits (Phase 4
// Step 6 Part 5) — enforced by the Lua access/log phases, not nginx limit_req. The
// WordPress-preset limit is "all"-mode (every request), so it's not included here.
func zoneErrorsOnlyLimits(z config.Zone) []config.ZoneRateLimit {
	if z.WAF == nil {
		return nil
	}
	var out []config.ZoneRateLimit
	for _, rl := range z.WAF.RateLimits {
		if rl.Enabled && rl.CountMode == "errors_only" {
			out = append(out, rl)
		}
	}
	return out
}

// nginxRate converts "requests per periodSeconds" to an nginx limit_req rate
// string (Nr/s or Nr/m — the only units nginx accepts). Prefers a clean
// per-minute/per-second value; otherwise rounds up to per-minute. Counters are
// per-edge + approximate (documented), so rounding is acceptable.
func nginxRate(requests, period int) string {
	if requests <= 0 {
		requests = 1
	}
	if period <= 0 {
		period = 1
	}
	perMin := requests * 60
	if perMin%period == 0 {
		rpm := perMin / period
		if rpm >= 60 && rpm%60 == 0 {
			return strconv.Itoa(rpm/60) + "r/s"
		}
		return strconv.Itoa(rpm) + "r/m"
	}
	rpm := (perMin + period - 1) / period // ceil
	if rpm < 1 {
		rpm = 1
	}
	return strconv.Itoa(rpm) + "r/m"
}

// --- rendering -------------------------------------------------------------

// Render produces the nginx.conf bytes for cfg (without touching disk).
func (m *Manager) Render(cfg *config.Config) ([]byte, error) {
	rd, err := buildRenderData(cfg, http2OnSupported(m.Bin), luaAvailable(), geoipAvailable())
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := m.tmpl.ExecuteTemplate(&buf, "nginx.conf.tmpl", rd); err != nil {
		return nil, fmt.Errorf("render nginx.conf: %w", err)
	}
	return buf.Bytes(), nil
}

// --- Lua edge logic (Phase 4 Step 5) ---------------------------------------

// writeLua writes the static Brisk Lua library + the rendered per-zone data
// (zones_data.lua) to LuaDir. No-op when the lua-nginx-module isn't installed (so
// non-lua edges never get a lua dir and stay byte-identical).
func (m *Manager) writeLua(cfg *config.Config) error {
	if !luaAvailable() {
		return nil
	}
	dir := m.LuaDir
	if dir == "" {
		dir = defaultLuaDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create lua dir: %w", err)
	}
	entries, err := luaFS.ReadDir("lua")
	if err != nil {
		return err
	}
	for _, e := range entries {
		b, rerr := luaFS.ReadFile("lua/" + e.Name())
		if rerr != nil {
			return rerr
		}
		if werr := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); werr != nil {
			return werr
		}
	}
	return os.WriteFile(filepath.Join(dir, "zones_data.lua"), renderLuaData(cfg.Zones), 0o644)
}

// writeErrorPages writes each zone's custom 502/504 page HTML to ErrPagesDir/<slug>.html
// (migration 00026). It only writes files for zones with non-empty HTML, and only creates
// the directory when there is at least one such zone — so a fleet with no custom pages
// never gets an errorpages dir and stays byte-identical. The path MUST match the one
// buildRenderData renders into the `alias` directive (defaultErrorPagesDir/<slug>.html).
func (m *Manager) writeErrorPages(cfg *config.Config) error {
	dir := m.ErrPagesDir
	if dir == "" {
		dir = defaultErrorPagesDir
	}
	made := false
	for _, z := range cfg.Zones {
		if strings.TrimSpace(z.Error5xxHTML) == "" {
			continue
		}
		if !made {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create error pages dir: %w", err)
			}
			made = true
		}
		path := filepath.Join(dir, upstreamSlug(z.Domain)+".html")
		if err := os.WriteFile(path, []byte(z.Error5xxHTML), 0o644); err != nil {
			return fmt.Errorf("write error page %s: %w", path, err)
		}
	}
	return nil
}

// renderLuaData renders the per-zone cache rules + header transforms into a Lua
// table literal the edge Lua reads (zones_data.lua). TTLs are pre-converted to
// seconds; strings are Lua-escaped; rules/transforms are priority-ordered. Only
// zones with rules/transforms are emitted (so the table is small).
func renderLuaData(zones []config.Zone) []byte {
	var b strings.Builder
	b.WriteString("-- generated by brisk-agent — DO NOT EDIT BY HAND (Phase 4 Step 5)\nreturn {\n")
	for _, z := range zones {
		eo := zoneErrorsOnlyLimits(z)
		if len(z.CacheRules) == 0 && len(z.HeaderTransforms) == 0 && len(eo) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  [%s] = {\n", luaStr(z.Domain))

		if len(eo) > 0 {
			b.WriteString("    rate_limits = {\n")
			for i, rl := range eo {
				key := "ip"
				if rl.Key == "ip_path" {
					key = "ip_path"
				}
				fmt.Fprintf(&b, "      { id=%s, path_match=%s, match_type=%s, requests=%d, period=%d, key=%s },\n",
					luaStr(fmt.Sprintf("%d", i)), luaStr(rl.PathMatch), luaStr(rl.MatchType), rl.Requests, rl.PeriodSeconds, luaStr(key))
			}
			b.WriteString("    },\n")
		}

		if len(z.CacheRules) > 0 {
			rules := append([]config.ZoneCacheRule(nil), z.CacheRules...)
			sort.SliceStable(rules, func(i, j int) bool { return rules[i].Priority < rules[j].Priority })
			b.WriteString("    cache_rules = {\n")
			for _, r := range rules {
				av := r.ActionValue
				if r.Action == "override_cache_ttl" {
					av = ttlSeconds(av)
				}
				fmt.Fprintf(&b, "      { match_type=%s, match_value=%s, action=%s, action_value=%s },\n",
					luaStr(r.MatchType), luaStr(r.MatchValue), luaStr(r.Action), luaStr(av))
			}
			b.WriteString("    },\n")
		}

		writeLuaTransforms(&b, "req_headers", transformsForPhase(z.HeaderTransforms, "request"))
		writeLuaTransforms(&b, "resp_headers", transformsForPhase(z.HeaderTransforms, "response"))
		b.WriteString("  },\n")
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

// transformsForPhase returns the transforms for a phase, priority-ordered.
func transformsForPhase(ts []config.ZoneHeaderTransform, phase string) []config.ZoneHeaderTransform {
	out := make([]config.ZoneHeaderTransform, 0, len(ts))
	for _, t := range ts {
		if t.Phase == phase {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

func writeLuaTransforms(b *strings.Builder, key string, ts []config.ZoneHeaderTransform) {
	if len(ts) == 0 {
		return
	}
	fmt.Fprintf(b, "    %s = {\n", key)
	for _, t := range ts {
		mt := t.MatchType
		if mt == "" {
			mt = "all"
		}
		fmt.Fprintf(b, "      { op=%s, header=%s, value=%s, match_type=%s, match_value=%s },\n",
			luaStr(t.Op), luaStr(t.Header), luaStr(t.Value), luaStr(mt), luaStr(t.MatchValue))
	}
	b.WriteString("    },\n")
}

// luaStr returns a safely-escaped Lua double-quoted string literal.
func luaStr(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&b, "\\%03d", c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ttlSeconds converts an nginx-style duration ("30d","1h","5m","2s" or a plain
// number) to a seconds string for the Lua Cache-Control max-age. Bad input -> "0".
func ttlSeconds(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "0"
	}
	mult := 1
	num := s
	switch s[len(s)-1] {
	case 's':
		num = s[:len(s)-1]
	case 'm':
		mult, num = 60, s[:len(s)-1]
	case 'h':
		mult, num = 3600, s[:len(s)-1]
	case 'd':
		mult, num = 86400, s[:len(s)-1]
	}
	n, err := strconv.Atoi(strings.TrimSpace(num))
	if err != nil || n < 0 {
		return "0"
	}
	return strconv.Itoa(n * mult)
}

// --- safe apply ------------------------------------------------------------

// Apply renders the config, writes it to ConfPath, validates it with `nginx -t`,
// and only then reloads (or starts) Nginx. If validation fails it rolls back to
// the previous config so a bad render can never take down the edge.
func (m *Manager) Apply(cfg *config.Config) error {
	rendered, err := m.Render(cfg)
	if err != nil {
		return err
	}

	// Preserve the current good config so we can roll back.
	backup, hadPrev, err := m.backup()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(m.ConfPath), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(m.ConfPath, rendered, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", m.ConfPath, err)
	}

	// Write the Lua library + per-zone data BEFORE validation, so `nginx -t` can
	// execute init_by_lua (which loads them) and catch any error pre-reload. No-op
	// when the lua-nginx-module isn't installed (today's live fleet).
	if err := m.writeLua(cfg); err != nil {
		return fmt.Errorf("write lua: %w", err)
	}

	// Write per-zone custom 502/504 error pages (migration 00026) BEFORE validation so
	// the alias'd files exist when nginx serves them. No-op for zones without a custom
	// page (so a default fleet never gets an errorpages dir — byte-identical).
	if err := m.writeErrorPages(cfg); err != nil {
		return fmt.Errorf("write error pages: %w", err)
	}

	if out, err := m.Test(); err != nil {
		// Bad config — restore the previous good one and refuse to reload.
		if rbErr := m.restore(backup, hadPrev); rbErr != nil {
			return fmt.Errorf("nginx -t failed (%v) AND rollback failed: %w\n%s", err, rbErr, out)
		}
		return fmt.Errorf("nginx -t failed, rolled back to previous config: %w\n%s", err, out)
	}

	if err := m.reloadOrStart(); err != nil {
		return fmt.Errorf("config valid but reload/start failed: %w", err)
	}
	return nil
}

// ApplyWithTLS provisions certificates and applies the Nginx config.
//
// For Let's Encrypt zones it is two-phase: (1) ensure a self-signed placeholder
// so Nginx can start and serve the HTTP-01 webroot on :80, apply; (2) obtain the
// real cert over that webroot and reload. selfsigned/mkcert zones are provisioned
// in phase 1 directly. This is the single entrypoint used by both the agent
// runtime and the bootstrap installer.
func (m *Manager) ApplyWithTLS(cfg *config.Config, tlsMgr *tls.Manager) error {
	// Phase 1: every zone gets SOME cert so the rendered config is valid.
	for _, z := range cfg.Zones {
		mode := cfg.ZoneTLSMode(z)
		switch mode {
		case config.TLSModeLetsEncrypt:
			if err := tlsMgr.EnsurePlaceholder(z.Domain); err != nil {
				return fmt.Errorf("tls placeholder for %s: %w", z.Domain, err)
			}
		case config.TLSModeManaged:
			// Cert issued by the control plane (lego Bunny DNS-01) and shipped over
			// the config-pull channel. Write it when present; on any problem (bad/
			// missing material) fall back to ensuring SOME cert exists so the edge
			// keeps serving its current cert — a bad shipment never drops TLS.
			if z.TLSCert != "" && z.TLSKey != "" {
				if changed, err := tlsMgr.WriteManaged(z.Domain, z.TLSCert, z.TLSKey); err != nil {
					log.Printf("managed tls for %s failed, keeping existing cert: %v", z.Domain, err)
					if perr := tlsMgr.EnsurePlaceholder(z.Domain); perr != nil {
						return fmt.Errorf("tls fallback for %s: %w", z.Domain, perr)
					}
				} else if changed {
					log.Printf("managed tls for %s installed (serial %s)", z.Domain, z.TLSCertSerial)
				}
			} else if err := tlsMgr.EnsurePlaceholder(z.Domain); err != nil {
				// No cert shipped yet (additive phase) — keep/ensure a cert so nginx starts.
				return fmt.Errorf("tls placeholder for %s: %w", z.Domain, err)
			}
		default:
			if _, err := tlsMgr.Ensure(z.Domain, mode); err != nil {
				return fmt.Errorf("tls for %s: %w", z.Domain, err)
			}
		}
	}
	if err := m.Apply(cfg); err != nil {
		return err
	}

	// Phase 2: obtain/renew real Let's Encrypt certs over the live :80 webroot.
	changed := false
	for _, z := range cfg.Zones {
		if cfg.ZoneTLSMode(z) != config.TLSModeLetsEncrypt {
			continue
		}
		if tlsMgr.NeedsLetsEncrypt(z.Domain) {
			if err := tlsMgr.ObtainLetsEncrypt(z.Domain); err != nil {
				return fmt.Errorf("letsencrypt for %s: %w", z.Domain, err)
			}
			changed = true
		}
	}
	if changed {
		return m.Apply(cfg) // reload with the real certs (nginx -t first, rollback on fail)
	}
	return nil
}

// RenewTLS forces a Let's Encrypt re-issue for every LE zone (used to test
// renewal) and reloads Nginx. Non-LE zones are skipped.
func (m *Manager) RenewTLS(cfg *config.Config, tlsMgr *tls.Manager) error {
	changed := false
	for _, z := range cfg.Zones {
		if cfg.ZoneTLSMode(z) != config.TLSModeLetsEncrypt {
			continue
		}
		if err := tlsMgr.ObtainLetsEncrypt(z.Domain); err != nil {
			return fmt.Errorf("renew %s: %w", z.Domain, err)
		}
		changed = true
	}
	if changed {
		return m.Apply(cfg)
	}
	return nil
}

// backup copies the current ConfPath to ConfPath+".bak". hadPrev is false when
// there was no existing config yet (fresh install).
func (m *Manager) backup() (backupPath string, hadPrev bool, err error) {
	backupPath = m.ConfPath + ".bak"
	cur, err := os.ReadFile(m.ConfPath)
	if os.IsNotExist(err) {
		return backupPath, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read current config: %w", err)
	}
	if err := os.WriteFile(backupPath, cur, 0o644); err != nil {
		return "", false, fmt.Errorf("write backup: %w", err)
	}
	return backupPath, true, nil
}

// restore puts the previous config back after a failed validation.
func (m *Manager) restore(backupPath string, hadPrev bool) error {
	if !hadPrev {
		// There was no prior config; remove the bad one we just wrote.
		if err := os.Remove(m.ConfPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	prev, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	return os.WriteFile(m.ConfPath, prev, 0o644)
}

// Test runs `nginx -t -c <ConfPath>` and returns its combined output.
func (m *Manager) Test() (string, error) {
	out, err := exec.Command(m.Bin, "-t", "-c", m.ConfPath).CombinedOutput()
	return string(out), err
}

// Reload gracefully reloads Nginx (zero dropped conns). On a systemd host it uses
// `systemctl reload nginx` so the reload is delivered to the service-managed master
// (the bare `nginx -s reload -c <path>` can target the wrong/none master when nginx
// was started by systemd, silently NOT applying the new config). Falls back to the
// signal form off systemd (local Docker/WSL2).
func (m *Manager) Reload() error {
	if systemdActive() {
		if out, err := exec.Command("systemctl", "reload", "nginx").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl reload nginx: %w\n%s", err, out)
		}
		return nil
	}
	out, err := exec.Command(m.Bin, "-s", "reload", "-c", m.ConfPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nginx -s reload: %w\n%s", err, out)
	}
	return nil
}

// Start launches Nginx. On a systemd host it uses `systemctl start nginx` so the
// service manager owns the process (correct for the VPS / reboot survival);
// otherwise it runs `nginx -c <ConfPath>` directly (local Docker/WSL2 tests).
func (m *Manager) Start() error {
	if systemdActive() {
		if out, err := exec.Command("systemctl", "start", "nginx").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl start nginx: %w\n%s", err, out)
		}
		return nil
	}
	// IMPORTANT: do NOT use CombinedOutput here. Nginx daemonizes (forks a
	// long-lived master) and, with the common error_log -> /dev/stderr setup,
	// the master keeps the inherited stderr pipe open — CombinedOutput would
	// block forever waiting for EOF. Wiring stdout/stderr to the agent's own
	// *os.File fds avoids the pipe entirely, so Run() returns once the foreground
	// process forks and exits.
	cmd := exec.Command(m.Bin, "-c", m.ConfPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("nginx start: %w", err)
	}
	return nil
}

// systemdActive reports whether systemd is the running init (so `systemctl` works).
func systemdActive() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

// reloadOrStart reloads a running master, or starts Nginx if none is running.
func (m *Manager) reloadOrStart() error {
	if err := m.Reload(); err != nil {
		// No running master yet (fresh boot) — start one.
		if startErr := m.Start(); startErr != nil {
			return fmt.Errorf("reload failed (%v) and start failed: %w", err, startErr)
		}
	}
	return nil
}
