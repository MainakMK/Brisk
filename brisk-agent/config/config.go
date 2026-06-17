// Package config loads and validates the brisk-agent configuration.
//
// Phase 1: the configuration is a local YAML file (agent.yaml). The agent reads
// it once at startup. To keep edges independent of the (future) control plane,
// every successfully-loaded config is also written back to disk as the
// "last-known-good" copy — so if a later pull fails, the edge keeps serving.
//
// See Brisk_Phase1_Build_Spec.md §5 for the config shape and §12 for the
// Source interface (config/source.go) that Phase 2 plugs into.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Default values applied when a field is omitted from agent.yaml.
const (
	DefaultCacheDir     = "/var/cache/brisk"
	DefaultCacheMaxSize = "200g"
	ModeLocal           = "local"
	ModeProduction      = "production"

	DefaultConfigCache   = "/etc/brisk/config.cache.json"
	DefaultPollInterval  = 15 * time.Second
	DefaultStatsInterval = 10 * time.Second
)

// StatsIntervalDuration parses stats_interval, defaulting to 10s.
func (c *Config) StatsIntervalDuration() time.Duration {
	if c.StatsInterval != "" {
		if d, err := time.ParseDuration(c.StatsInterval); err == nil && d > 0 {
			return d
		}
	}
	return DefaultStatsInterval
}

// EffectiveToken returns the control-plane token: the contents of
// agent_token_file if set (trimmed), else the inline agent_token.
func (c *Config) EffectiveToken() string {
	if c.AgentTokenFile != "" {
		if b, err := os.ReadFile(c.AgentTokenFile); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return c.AgentToken
}

// PollIntervalDuration parses poll_interval, defaulting to 15s.
func (c *Config) PollIntervalDuration() time.Duration {
	if c.PollInterval != "" {
		if d, err := time.ParseDuration(c.PollInterval); err == nil && d > 0 {
			return d
		}
	}
	return DefaultPollInterval
}

// ConfigCachePath returns the last-known-good cache path (with default).
func (c *Config) ConfigCachePath() string {
	if c.ConfigCache != "" {
		return c.ConfigCache
	}
	return DefaultConfigCache
}

// PullMode reports whether the agent should pull config from the control plane.
func (c *Config) PullMode() bool {
	return c.ControlPlaneURL != "" && c.EffectiveToken() != ""
}

// Config is the full agent configuration (one edge node).
type Config struct {
	EdgeID          string `yaml:"edge_id"`           // shows up in X-Brisk-Edge
	Mode            string `yaml:"mode"`              // local | production
	ControlPlaneURL string `yaml:"control_plane_url"` // empty = standalone (Phase 1 behavior)
	AgentToken      string `yaml:"agent_token"`       // bearer token (inline); or use agent_token_file
	AgentTokenFile  string `yaml:"agent_token_file"`  // path to a 600 token file (preferred over inline)
	PollInterval    string `yaml:"poll_interval"`     // control-plane poll interval (default 15s)
	StatsInterval   string `yaml:"stats_interval"`    // metric collect/ship interval (default 10s)
	NatsURL         string `yaml:"nats_url"`          // NATS JetStream for instant purge (empty = disabled)
	ConfigCache     string `yaml:"config_cache"`      // last-known-good pulled config (JSON)
	CacheDir        string `yaml:"cache_dir"`
	CacheMaxSize    string `yaml:"cache_max_size"`

	// --- TLS (Step 4 / Step 7) ---
	TLSMode            string `yaml:"tls_mode"`            // selfsigned | mkcert | letsencrypt (default for zones)
	LetsEncryptEmail   string `yaml:"letsencrypt_email"`   // required when an effective mode is letsencrypt
	LetsEncryptStaging bool   `yaml:"letsencrypt_staging"` // use LE staging directory (test, untrusted certs)
	SessionTickets     *bool  `yaml:"session_tickets"`     // TLS session tickets (default true)

	// --- Compression (Step 5) ---
	BrotliCompLevel int `yaml:"brotli_comp_level"` // on-the-fly Brotli level 1..11 (default 5)

	// Resolver is the DNS server(s) Nginx uses to resolve variable proxy_pass
	// upstreams (the per-zone origin / shield fallback) at request time. Default is
	// public resolvers (origins are public hostnames in production); override to
	// e.g. "127.0.0.11" for Docker-internal origins in local tests. Space-separated.
	Resolver string `yaml:"resolver"`

	// WAFListen is the address the in-agent Coraza WAF service listens on for the
	// nginx auth_request subrequest (Phase 4 Step 4). Loopback-only by default;
	// nginx proxies /_waf to it. Default "127.0.0.1:9555".
	WAFListen string `yaml:"waf_listen"`

	// --- Origin lockdown (Phase 4 Step 6 Part 6) ---
	// When OriginPullSecret is set, the edge adds a secret header to every ORIGIN
	// request (not the shield hop, not the client). The customer origin rejects
	// requests lacking it, so traffic MUST traverse Brisk (the edge IP stays hidden;
	// the WAF/rate-limit/TLS stack can't be bypassed). Edge-level (shared across this
	// edge's zones); empty => no header (today's behavior). The secret is NEVER
	// logged. OriginPullHeader defaults to "X-Brisk-Pull-Token".
	OriginPullSecret string `yaml:"origin_pull_secret"`
	OriginPullHeader string `yaml:"origin_pull_header"`

	// --- Edge self-protection (#4) — the instant local brake ---
	// Edge-LOCAL by design (an agent-level setting, NOT a per-zone pulled field), so it
	// is independent of the control plane: it works even when the dashboard is down, and
	// a config poll never clobbers it (ControlPlaneSource overlays only zones onto the
	// base agent.yaml). Off by default => no limit directives rendered => byte-identical
	// to today's fleet. When on, nginx caps concurrent connections (and optionally the
	// request rate) PER CLIENT IP and answers 503/429 when a single IP floods — shedding
	// abuse at the edge in milliseconds while DNS-level steering (#3) catches up. The
	// per-IP keying means the low-volume health-checker IP keeps its own tiny budget, so
	// /healthz stays answerable and the existing health probe still governs DNS rotation.
	EdgeProtect        bool `yaml:"edge_protect"`             // master switch (default false)
	EdgeMaxConnPerIP   int  `yaml:"edge_max_conn_per_ip"`     // max concurrent conns per client IP (default 200 when on)
	EdgeReqPerSecPerIP int  `yaml:"edge_req_per_sec_per_ip"`  // sustained req/s per IP ceiling (0 = off)
	EdgeReqBurst       int  `yaml:"edge_req_burst"`           // burst allowance above the rate (default 2×rate)

	Zones []Zone `yaml:"zones"`
}

// ZoneWAF is one zone's WAF + rate-limit config (Phase 4 Step 4). Pulled from the
// control plane (json) or set directly in agent.yaml for the standalone lab (yaml).
// nil on a zone => WAF off (no auth_request rendered, no inspection).
type ZoneWAF struct {
	Enabled        bool            `yaml:"enabled" json:"enabled"`
	Mode           string          `yaml:"mode" json:"mode"`                       // detect | block
	ManagedRuleset string          `yaml:"managed_ruleset" json:"managed_ruleset"` // off | owasp_crs
	Paranoia       int             `yaml:"paranoia" json:"paranoia"`               // CRS paranoia 1..4
	FailOpen       bool            `yaml:"fail_open" json:"fail_open"`             // engine error -> open vs closed
	WpPreset       bool            `yaml:"wp_preset" json:"wp_preset"`             // WordPress hardening
	Rules          []ZoneWAFRule   `yaml:"rules" json:"rules"`
	RateLimits     []ZoneRateLimit `yaml:"rate_limits" json:"rate_limits"`
}

// ZoneWAFRule is one custom rule (evaluated before the managed CRS).
type ZoneWAFRule struct {
	ID         int64  `yaml:"id" json:"id"`
	Priority   int    `yaml:"priority" json:"priority"`
	Field      string `yaml:"field" json:"field"` // ip|country|path|method|header|user_agent
	Op         string `yaml:"op" json:"op"`       // eq|prefix|regex|cidr
	Value      string `yaml:"value" json:"value"`
	HeaderName string `yaml:"header_name" json:"header_name"`
	Action     string `yaml:"action" json:"action"` // block|challenge|log|allow
	Enabled    bool   `yaml:"enabled" json:"enabled"`
}

// ZoneRateLimit is one Nginx-native rate limit (per zone, per path).
type ZoneRateLimit struct {
	ID            int64  `yaml:"id" json:"id"`
	PathMatch     string `yaml:"path_match" json:"path_match"`
	MatchType     string `yaml:"match_type" json:"match_type"` // exact|prefix
	Requests      int    `yaml:"requests" json:"requests"`
	PeriodSeconds int    `yaml:"period_seconds" json:"period_seconds"`
	Burst         int    `yaml:"burst" json:"burst"`
	Key           string `yaml:"key" json:"key"`               // ip|ip_path
	Action        string `yaml:"action" json:"action"`         // block|challenge
	CountMode     string `yaml:"count_mode" json:"count_mode"` // all|errors_only
	Enabled       bool   `yaml:"enabled" json:"enabled"`
}

// TLS mode values (canonical strings shared with the tls package).
const (
	TLSModeSelfSigned  = "selfsigned"
	TLSModeMkcert      = "mkcert"
	TLSModeLetsEncrypt = "letsencrypt"
	// TLSModeManaged: the certificate is issued by the CONTROL PLANE (lego Bunny
	// DNS-01) and shipped to this edge over the config-pull channel (Phase 3.7
	// Step 2, Part 3). The agent writes the supplied cert/key and reloads; it does
	// NOT run ACME itself. Needs no letsencrypt_email.
	TLSModeManaged     = "managed"
	DefaultBrotliLevel = 5
)

// Zone is one cached site served by this edge.
type Zone struct {
	Domain     string `yaml:"domain"`      // e.g. test.example.com (the CDN hostname / server_name)
	Origin     string `yaml:"origin"`      // e.g. http://origin:8000
	HostHeader string `yaml:"host_header"` // upstream Host (empty = derive from origin); Phase 4 multi-tenant
	TLS        string `yaml:"tls"`         // selfsigned | mkcert | letsencrypt | managed

	// Origin Shield (Phase 4 Step 3): host:port of the shield PoP to proxy this
	// zone's misses to (set by the control plane). Empty = proxy the origin directly.
	ShieldUpstream string `yaml:"shield_upstream"`

	// --- Managed TLS (Part 3): cert material supplied by the control plane for a
	// tls=managed zone. Populated from the pulled config, not agent.yaml. When
	// present the agent writes them and reloads instead of running ACME itself.
	TLSCert       string `yaml:"-"` // fullchain PEM
	TLSKey        string `yaml:"-"` // private key PEM
	TLSCertSerial string `yaml:"-"` // leaf serial (for change detection / logging)

	// --- HLS / video (Step 3) ---
	Video       bool   `yaml:"video"`        // enable HLS/video locations (slice + CORS)
	Profile     string `yaml:"profile"`      // ""(unknown) | vod | live -> default TTLs
	PlaylistTTL string `yaml:"playlist_ttl"` // .m3u8/.mpd cache TTL (optional override)
	SegmentTTL  string `yaml:"segment_ttl"`  // .ts/.m4s/.mp4 cache TTL (optional override)
	CORSOrigin  string `yaml:"cors_origin"`  // Access-Control-Allow-Origin (default *)
	MinUses     int    `yaml:"min_uses"`     // proxy_cache_min_uses (default 1)

	// --- WAF + rate limiting (Phase 4 Step 4) ---
	// nil => WAF off for this zone (the default; no auth_request, no inspection).
	WAF *ZoneWAF `yaml:"waf" json:"waf"`

	// --- Lua edge logic (Phase 4 Step 5): per-zone custom cache rules + header
	// transforms, enforced at the edge by the Lua layer. Empty => no Lua hooks
	// rendered for this zone (byte-identical to before). ---
	CacheRules       []ZoneCacheRule       `yaml:"cache_rules" json:"cache_rules"`
	HeaderTransforms []ZoneHeaderTransform `yaml:"header_transforms" json:"header_transforms"`

	// --- Cache Settings (Bunny-style per-zone cache controls). nil => the zone is at
	// defaults (today's behavior), so the agent renders byte-identical nginx. The
	// control plane only sends this when a tenant changed something. ---
	CacheSettings *ZoneCacheSettings `yaml:"cache_settings" json:"cache_settings"`

	// --- Hotlink protection (Referer allowlist). nil => off for this zone (no
	// valid_referers, no 403 guard — byte-identical). The control plane sends this
	// only when a tenant enables it. Enforced on CONTENT locations only (never
	// /healthz or the ACME challenge — those must answer with no Referer). ---
	Hotlink *ZoneHotlink `yaml:"hotlink" json:"hotlink"`

	// --- Origin connection options (Bunny-style, migration 00025). All false =>
	// today's byte-identical edge behavior. OriginSSLVerify => proxy_ssl_verify on
	// (https origins). OriginFollowRedirects => follow an origin 3xx one hop instead of
	// passing it through. ForwardHostHeader => send the client's Host ($host) upstream
	// instead of HostHeader. ---
	OriginSSLVerify       bool `yaml:"origin_ssl_verify" json:"origin_ssl_verify"`
	OriginFollowRedirects bool `yaml:"origin_follow_redirects" json:"origin_follow_redirects"`
	ForwardHostHeader     bool `yaml:"forward_host_header" json:"forward_host_header"`

	// --- Custom 502/504 error page (Bunny-style, migration 00026). Branded HTML the
	// edge serves on a nginx-generated 502/504 (origin unreachable/timeout). Empty =>
	// off (today's byte-identical default). The control plane sends this only when a
	// tenant set one; the agent writes it to a file + renders error_page 502 504. ---
	Error5xxHTML string `yaml:"error_5xx_html" json:"error_5xx_html"`

	// --- Blocked-IP denylist (Bunny-style, migration 00027). Comma-separated IPs / CIDRs
	// the edge blocks (403) on content locations via nginx `deny`. Empty => off
	// (byte-identical). Never applied to /healthz or the ACME challenge. ---
	BlockedIPs string `yaml:"blocked_ips" json:"blocked_ips"`

	// --- Access toggles (Bunny-style, migration 00028). BlockRootPath => 403 the bare
	// root + any directory root (path ending in "/"). BlockPost => 405 POST requests.
	// Both false => off (byte-identical). Rendered as gated server-level `if`s on :443. ---
	BlockRootPath bool `yaml:"block_root_path" json:"block_root_path"`
	BlockPost     bool `yaml:"block_post" json:"block_post"`
}

// ZoneHotlink is one zone's hotlink protection config (Referer allowlist). The agent
// turns it into an nginx `valid_referers` directive + a content-location 403 guard.
type ZoneHotlink struct {
	AllowedReferrers string `yaml:"allowed_referrers" json:"allowed_referrers"` // comma-separated hosts (may include *.x)
	AllowEmpty       bool   `yaml:"allow_empty" json:"allow_empty"`             // allow requests with no Referer
}

// ZoneCacheSettings are the per-zone Smart-Cache controls (mirrors the control
// plane's store.CacheSettings on the wire). nil on Zone => all defaults.
type ZoneCacheSettings struct {
	Smart           bool   `yaml:"smart" json:"smart"`
	EdgeMode        string `yaml:"edge_mode" json:"edge_mode"`       // default | respect_origin | override | no_cache
	EdgeTTL         string `yaml:"edge_ttl" json:"edge_ttl"`         // nginx time when edge_mode=override
	BrowserMode     string `yaml:"browser_mode" json:"browser_mode"` // default | match_server | override | no_cache
	BrowserTTL      string `yaml:"browser_ttl" json:"browser_ttl"`
	QuerySort       bool   `yaml:"query_sort" json:"query_sort"`
	CacheErrors     bool   `yaml:"cache_errors" json:"cache_errors"`
	VaryWebp        bool   `yaml:"vary_webp" json:"vary_webp"`
	VaryDevice      bool   `yaml:"vary_device" json:"vary_device"`
	VaryCountry     bool   `yaml:"vary_country" json:"vary_country"`
	VaryCookie      string `yaml:"vary_cookie" json:"vary_cookie"`
	VaryQueryString bool   `yaml:"vary_querystring" json:"vary_querystring"`
	VaryHeaders     string `yaml:"vary_headers" json:"vary_headers"`       // comma-separated request header names
	QueryWhitelist  string `yaml:"query_whitelist" json:"query_whitelist"` // comma-separated args
	StripCookies    bool   `yaml:"strip_cookies" json:"strip_cookies"`
	LargeObject     bool   `yaml:"large_object" json:"large_object"`
	StaleOffline    bool   `yaml:"stale_offline" json:"stale_offline"`
	StaleUpdating   bool   `yaml:"stale_updating" json:"stale_updating"`
}

// ZoneCacheRule is one priority-ordered cache rule (first match wins), enforced by
// the edge Lua layer (Phase 4 Step 5). match_type: path_prefix|extension|regex;
// action: override_cache_ttl|bypass_cache|force_download|redirect; action_value is
// the TTL (duration, e.g. "30d") or the redirect target (may use $1 from a regex).
type ZoneCacheRule struct {
	Priority    int    `yaml:"priority" json:"priority"`
	MatchType   string `yaml:"match_type" json:"match_type"`
	MatchValue  string `yaml:"match_value" json:"match_value"`
	Action      string `yaml:"action" json:"action"`
	ActionValue string `yaml:"action_value" json:"action_value"`
}

// ZoneHeaderTransform is one request/response header transform (Phase 4 Step 5).
type ZoneHeaderTransform struct {
	Priority   int    `yaml:"priority" json:"priority"`
	Phase      string `yaml:"phase" json:"phase"` // request | response
	Op         string `yaml:"op" json:"op"`       // set | remove
	Header     string `yaml:"header" json:"header"`
	Value      string `yaml:"value" json:"value"`
	MatchType  string `yaml:"match_type" json:"match_type"` // all | path_prefix | path_regex | method
	MatchValue string `yaml:"match_value" json:"match_value"`
}

// Recognized per-zone delivery profiles.
const (
	ProfileVOD  = "vod"
	ProfileLive = "live"
)

// Load reads, parses, validates, and normalizes the agent config at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %q: %w", path, err)
	}
	return &cfg, nil
}

// applyDefaults fills in optional fields that were left empty.
func (c *Config) applyDefaults() {
	if c.Mode == "" {
		c.Mode = ModeLocal
	}
	if c.CacheDir == "" {
		c.CacheDir = DefaultCacheDir
	}
	if c.CacheMaxSize == "" {
		c.CacheMaxSize = DefaultCacheMaxSize
	}
	if c.TLSMode == "" {
		c.TLSMode = TLSModeSelfSigned
	}
	if c.BrotliCompLevel == 0 {
		c.BrotliCompLevel = DefaultBrotliLevel
	}
	if strings.TrimSpace(c.Resolver) == "" {
		c.Resolver = "1.1.1.1 8.8.8.8"
	}
	if strings.TrimSpace(c.WAFListen) == "" {
		c.WAFListen = "127.0.0.1:9555"
	}
	// Edge self-protection (#4): when enabled without an explicit per-IP connection
	// cap, default to a generous ceiling that only bites obvious floods (a normal
	// browser opens ~6 conns/host). The request-rate ceiling stays OFF unless set; when
	// a rate IS set without a burst, allow a 2× burst so legitimate bursts aren't 429'd.
	if c.EdgeProtect {
		if c.EdgeMaxConnPerIP <= 0 {
			c.EdgeMaxConnPerIP = 200
		}
		if c.EdgeReqPerSecPerIP > 0 && c.EdgeReqBurst <= 0 {
			c.EdgeReqBurst = c.EdgeReqPerSecPerIP * 2
		}
	}
	for i := range c.Zones {
		c.Zones[i].applyDefaults()
	}
}

// ZoneTLSMode returns the effective TLS mode for a zone: the per-zone tls field
// if set, else the top-level tls_mode, else selfsigned.
func (c *Config) ZoneTLSMode(z Zone) string {
	if z.TLS != "" {
		return z.TLS
	}
	if c.TLSMode != "" {
		return c.TLSMode
	}
	return TLSModeSelfSigned
}

// SessionTicketsEnabled reports whether TLS session tickets are on (default true).
func (c *Config) SessionTicketsEnabled() bool {
	return c.SessionTickets == nil || *c.SessionTickets
}

// validTLSMode reports whether mode is a recognized TLS mode.
func validTLSMode(mode string) bool {
	switch mode {
	case TLSModeSelfSigned, TLSModeMkcert, TLSModeLetsEncrypt, TLSModeManaged:
		return true
	}
	return false
}

// applyDefaults resolves per-zone video defaults. Profile sets baseline TTLs;
// explicit playlist_ttl / segment_ttl overrides win. Defaults are chosen so a
// zone with video enabled but no profile stays SAFE (short playlist TTL) —
// see Step 3 prompt Task 4.
func (z *Zone) applyDefaults() {
	if z.MinUses <= 0 {
		z.MinUses = 1
	}
	if z.CORSOrigin == "" {
		z.CORSOrigin = "*"
	}
	playlist, segment := profileTTLDefaults(z.Profile)
	if z.PlaylistTTL == "" {
		z.PlaylistTTL = playlist
	}
	if z.SegmentTTL == "" {
		z.SegmentTTL = segment
	}
}

// profileTTLDefaults returns (playlistTTL, segmentTTL) for a delivery profile.
//   - vod  : finished videos -> playlists & segments are immutable, cache long.
//   - live : playlists change every segment -> very short; segments short-ish.
//   - ""   : unknown -> short playlist TTL for safety, long segment TTL.
func profileTTLDefaults(profile string) (playlist, segment string) {
	switch profile {
	case ProfileVOD:
		return "1h", "12h"
	case ProfileLive:
		return "2s", "10s"
	default:
		return "2s", "12h"
	}
}

// Validate checks that the config is structurally usable before we render Nginx.
// We refuse a config we can't safely turn into a valid nginx.conf — generating a
// broken config and reloading is a golden-rule violation (Spec §13).
func (c *Config) Validate() error {
	if strings.TrimSpace(c.EdgeID) == "" {
		return fmt.Errorf("edge_id is required")
	}
	if c.Mode != ModeLocal && c.Mode != ModeProduction {
		return fmt.Errorf("mode must be %q or %q, got %q", ModeLocal, ModeProduction, c.Mode)
	}
	if len(c.Zones) == 0 {
		return fmt.Errorf("at least one zone is required")
	}
	if c.BrotliCompLevel < 1 || c.BrotliCompLevel > 11 {
		return fmt.Errorf("brotli_comp_level must be 1..11, got %d", c.BrotliCompLevel)
	}
	// Edge self-protection (#4) bounds — only checked when on (off => fields ignored).
	if c.EdgeProtect {
		if c.EdgeMaxConnPerIP < 1 {
			return fmt.Errorf("edge_max_conn_per_ip must be >= 1 when edge_protect is on, got %d", c.EdgeMaxConnPerIP)
		}
		if c.EdgeReqPerSecPerIP < 0 {
			return fmt.Errorf("edge_req_per_sec_per_ip must be >= 0, got %d", c.EdgeReqPerSecPerIP)
		}
		if c.EdgeReqBurst < 0 {
			return fmt.Errorf("edge_req_burst must be >= 0, got %d", c.EdgeReqBurst)
		}
	}
	if !validTLSMode(c.TLSMode) {
		return fmt.Errorf("tls_mode must be %q, %q, %q, or %q, got %q",
			TLSModeSelfSigned, TLSModeMkcert, TLSModeLetsEncrypt, TLSModeManaged, c.TLSMode)
	}
	seen := make(map[string]bool, len(c.Zones))
	for i := range c.Zones {
		z := &c.Zones[i]
		dom := strings.TrimSpace(z.Domain)
		if dom != "" && seen[dom] {
			return fmt.Errorf("duplicate zone domain %q", z.Domain)
		}
		if err := c.zoneError(z); err != nil {
			return fmt.Errorf("zone %q: %w", z.Domain, err)
		}
		seen[dom] = true
	}
	return nil
}

// zoneError reports why a single zone cannot be safely rendered to nginx, or nil
// if it is valid. It does NOT check cross-zone duplicates (the caller does). This
// is the single source of truth used by both Validate (whole-config) and
// DropInvalidZones (per-zone quarantine).
func (c *Config) zoneError(z *Zone) error {
	if strings.TrimSpace(z.Domain) == "" {
		return fmt.Errorf("domain is required")
	}
	if _, _, err := z.OriginParts(); err != nil {
		return err
	}
	if z.Profile != "" && z.Profile != ProfileVOD && z.Profile != ProfileLive {
		return fmt.Errorf("profile must be %q, %q, or empty, got %q", ProfileVOD, ProfileLive, z.Profile)
	}
	mode := c.ZoneTLSMode(*z)
	if !validTLSMode(mode) {
		return fmt.Errorf("tls must be %q, %q, %q, %q, or empty, got %q",
			TLSModeSelfSigned, TLSModeMkcert, TLSModeLetsEncrypt, TLSModeManaged, z.TLS)
	}
	if mode == TLSModeLetsEncrypt && strings.TrimSpace(c.LetsEncryptEmail) == "" {
		return fmt.Errorf("tls_mode letsencrypt requires letsencrypt_email")
	}
	return nil
}

// DropInvalidZones removes any zone that can't be safely rendered (bad/empty
// domain, bad origin, bad tls_mode, letsencrypt without an email, or a duplicate
// domain) and returns a human description of each dropped zone. This QUARANTINES
// one bad tenant zone so the agent keeps serving every good zone, instead of
// rejecting the whole pulled config — the all-or-nothing trap that froze NY/FRA
// and crash-looped BLR when a single zone had tls_mode=letsencrypt with no email.
// The caller still runs Validate() afterward, so a config left with ZERO valid
// zones is rejected by the >=1-zone guard (keep last-known-good).
func (c *Config) DropInvalidZones() []string {
	if len(c.Zones) == 0 {
		return nil
	}
	kept := make([]Zone, 0, len(c.Zones))
	seen := make(map[string]bool, len(c.Zones))
	var dropped []string
	for i := range c.Zones {
		z := c.Zones[i]
		dom := strings.TrimSpace(z.Domain)
		if dom != "" && seen[dom] {
			dropped = append(dropped, z.Domain+" (duplicate)")
			continue
		}
		if err := c.zoneError(&z); err != nil {
			label := z.Domain
			if strings.TrimSpace(label) == "" {
				label = fmt.Sprintf("zone[%d]", i)
			}
			dropped = append(dropped, label+" ("+err.Error()+")")
			continue
		}
		seen[dom] = true
		kept = append(kept, z)
	}
	c.Zones = kept
	return dropped
}

// OriginParts splits the zone origin URL into the Nginx upstream scheme
// ("http"/"https") and host:port (e.g. "origin:8000"). It is the single source
// of truth for turning a zone's origin into Nginx directives.
func (z Zone) OriginParts() (scheme, hostPort string, err error) {
	if strings.TrimSpace(z.Origin) == "" {
		return "", "", fmt.Errorf("origin is required")
	}
	u, err := url.Parse(z.Origin)
	if err != nil {
		return "", "", fmt.Errorf("origin %q: %w", z.Origin, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("origin %q: scheme must be http or https", z.Origin)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("origin %q: missing host", z.Origin)
	}
	hostPort = u.Host
	if u.Port() == "" {
		// Give Nginx an explicit port so the upstream is unambiguous.
		if u.Scheme == "https" {
			hostPort += ":443"
		} else {
			hostPort += ":80"
		}
	}
	return u.Scheme, hostPort, nil
}

// Save writes the config back to path as the last-known-good copy. Parent
// directories are created if missing. (Phase 2: the control-plane Source writes
// here after each successful pull so the edge survives control-plane downtime.)
func (c *Config) Save(path string) error {
	raw, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create config dir %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write config %q: %w", path, err)
	}
	return nil
}
