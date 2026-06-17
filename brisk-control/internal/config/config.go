// Package config loads brisk-control configuration from the environment (12-factor).
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Config is the typed control-plane configuration.
type Config struct {
	DatabaseURL string // postgres connection string (required)
	ListenAddr  string // e.g. ":8080"
	LogLevel    string // debug | info | warn | error
	Env         string // dev | prod

	// AgentControlPlaneURL is the URL the control plane writes into each agent's
	// agent.yaml so the agent can reach it (must be reachable FROM the edge).
	AgentControlPlaneURL string

	// NatsURL is the NATS JetStream server the CONTROL PLANE connects to for
	// publishing purges. Empty disables purge publishing.
	NatsURL string

	// AgentNatsURL is the NATS URL written into each edge's agent.yaml so the edge
	// can reach NATS (must be reachable FROM the edge; may differ from NatsURL).
	AgentNatsURL string

	// --- Bunny DNS (Phase 3) ---
	// BunnyAPIKey is the Bunny account API key (AccessKey header). Empty disables
	// DNS integration. NEVER logged or committed.
	BunnyAPIKey string
	// BunnyDNSZoneID is the numeric Bunny DNS zone id (preferred). If empty,
	// BunnyDNSZone (the domain name) is resolved to an id at startup.
	BunnyDNSZoneID string
	// BunnyDNSZone is the DNS zone domain (e.g. "test.example.com") — resolved to
	// an id when BunnyDNSZoneID is not set.
	BunnyDNSZone string
	// BriskCDNRecord is the default record name Brisk routes (e.g. "cdn" or "@").
	BriskCDNRecord string
	// BriskDNSTTL is the TTL (seconds) for routing records. Short by design so
	// Steps 3-4 fail over fast; clamped to 30..120.
	BriskDNSTTL int
	// BriskDNSRoutingMode is the network-wide Smart-Record routing mode
	// ("geographic" | "latency"). Default geographic (nearest by location).
	// Per-server overrides live in the servers table (routing_override).
	BriskDNSRoutingMode string

	// --- Bunny native DNS monitor (Layer-2 failover backstop) ---
	// GATED, off by default. When on, the reconciler sets MonitorType on every
	// Brisk-managed A record so Bunny's own infra probes each edge every ~30s from
	// 3 regions and drops a dead one from DNS responses even if the control plane is
	// down. Brisk's 5s health checker is still Layer-1 (acts first); this is the
	// belt-and-suspenders backstop. Off => records carry MonitorType 0 (byte-identical).
	BriskDNSMonitor     bool   // master switch (BRISK_DNS_MONITOR)
	BriskDNSMonitorType string // "ping" (default, safest) | "http" | "none"

	// --- Load-feedback steering (#3) — live load → DNS weight nudge ---
	// Off by default: when disabled, the load controller is never built and every
	// edge's weight factor is 1.0 (DNS renders byte-identical). When enabled, the
	// control plane samples each edge's live pressure (load ÷ capacity) every
	// LoadSteerInterval and nudges the per-edge Smart-Record weight (the #2 knob)
	// down for hot edges, bounded by LoadSteerMinFactor (never a full drain).
	LoadSteerEnabled   bool    // master switch (BRISK_LOAD_STEER_ENABLED)
	LoadSteerInterval  int     // seconds between live-load samples (default 45)
	LoadSteerLowWater  float64 // pressure ≤ this → factor 1.0 (default 0.55)
	LoadSteerHighWater float64 // pressure ≥ this → factor MinFactor (default 0.90)
	LoadSteerMinFactor float64 // floor on the weight factor (default 0.30)

	// DefaultShieldServerID is the network-wide default origin-shield PoP (Phase 4
	// Step 3): a zone with origin_shield_enabled but no explicit shield_server_id
	// uses this. 0 = no network default (such a zone shields only if it sets its own
	// shield_server_id). The target must be a role=shield server to take effect.
	DefaultShieldServerID int64

	// --- Health checking (Phase 3 Step 4 — fast failover) ---
	// Network-wide defaults; per-server overrides live in the servers table.
	// Detection ≈ HealthInterval × HealthFailThreshold (10s×2 ≈ 20s); end-to-end
	// failover ≈ detection + TTL (set BriskDNSTTL ~15s for ~30-35s typical).
	HealthEnabled       bool   // master switch for the checker
	HealthInterval      int    // seconds between probes per edge (default 10)
	HealthTimeout       int    // per-probe timeout seconds (< interval; default 3)
	HealthFailThreshold int    // consecutive fails to mark unhealthy (default 2)
	HealthRiseThreshold int    // consecutive successes to recover (default 3)
	HealthPath          string // probe path (default "/healthz")
	HealthScheme        string // "https" (default) | "http" (local testing)
	HealthPort          int    // 0 = scheme default (443/80)

	// --- Managed TLS (Phase 3.7 Step 2, Part 3 — lego Bunny DNS-01) ---
	// When enabled, the control plane issues + renews the wildcard cert centrally
	// and fans it to edges over the agent-config channel (retiring per-edge
	// acme.sh). Reuses BunnyAPIKey for the DNS-01 TXT challenge.
	TLSManaged    bool     // master switch (BRISK_TLS_MANAGED)
	TLSEmail      string   // ACME account email (BRISK_TLS_EMAIL) — required when managed
	TLSStaging    bool     // issue from LE staging (default true; flip to false at cutover)
	TLSDomains    []string // SANs, e.g. ["*.a2zjav.com","a2zjav.com"] (BRISK_TLS_DOMAINS, comma list)
	TLSCertName   string   // store key (BRISK_TLS_CERT_NAME); default = apex derived from domains
	TLSAccountDir string   // ACME account-key dir (BRISK_TLS_ACME_DIR; default /var/lib/brisk-control/acme)

	// --- Custom-domain TLS (Phase 4 Step 2 — HTTP-01) ---
	// CustomTLSStaging is INDEPENDENT of TLSStaging so customer-domain certs can
	// iterate on Let's Encrypt staging while the managed wildcard stays in
	// production (the two share an ACME account dir but a different LE directory).
	// Defaults to true (staging) — flip to false at the custom-domain cutover.
	CustomTLSStaging bool

	// --- Admin auth (Phase 3.7 Step 3) ---
	// CookieSecure marks session cookies Secure (HTTPS-only). Default false for
	// local http dev; MUST be true behind TLS in production.
	CookieSecure bool
	// DashboardOrigin is the browser origin(s) allowed to send credentialed CORS
	// requests (comma-separated). Credentialed CORS cannot use "*".
	DashboardOrigin string
	// AdminEmail / AdminPassword bootstrap the first admin account on startup when
	// the admin (account id 1) has no password yet. Never hardcoded; from env only.
	AdminEmail    string
	AdminPassword string

	// --- Agent self-update / rollout ---
	// AgentPubKeys are the base64 ed25519 PUBLIC keys the control plane uses to verify a
	// release's signature when it is uploaded (these MUST match the trusted keys compiled
	// into the agent). Comma-separated; usually 2 (primary + rotation spare). Empty => the
	// release-upload endpoint rejects everything (fail closed).
	AgentPubKeys []string

	// RolloutEnabled starts the rollout engine goroutine. Default true; the engine is idle
	// with no active rollout, so this only matters as a kill switch. Off => no rollout
	// progresses even if one is created (the API still accepts them).
	RolloutEnabled bool
}

// DashboardOrigins returns the allowed credentialed-CORS origins (split + trimmed).
func (c *Config) DashboardOrigins() []string {
	if o := splitCSV(c.DashboardOrigin); len(o) > 0 {
		return o
	}
	return []string{"http://localhost:5173"}
}

// Load reads config from env, applying defaults and failing fast on missing
// required values.
func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		ListenAddr:           envOr("LISTEN_ADDR", ":8080"),
		LogLevel:             envOr("LOG_LEVEL", "info"),
		Env:                  envOr("ENV", "dev"),
		AgentControlPlaneURL: os.Getenv("AGENT_CONTROL_PLANE_URL"),
		NatsURL:              os.Getenv("NATS_URL"),
		AgentNatsURL:         os.Getenv("AGENT_NATS_URL"),
		BunnyAPIKey:          os.Getenv("BUNNY_API_KEY"),
		BunnyDNSZoneID:       os.Getenv("BUNNY_DNS_ZONE_ID"),
		BunnyDNSZone:         os.Getenv("BUNNY_DNS_ZONE"),
		BriskCDNRecord:       envOr("BRISK_CDN_RECORD", "cdn"),
		BriskDNSTTL:          envIntOr("BRISK_DNS_TTL", 60),
		BriskDNSRoutingMode:  envOr("BRISK_DNS_ROUTING_MODE", "geographic"),
		BriskDNSMonitor:      envBoolOr("BRISK_DNS_MONITOR", false),
		BriskDNSMonitorType:  envOr("BRISK_DNS_MONITOR_TYPE", "ping"),
		DefaultShieldServerID: int64(envIntOr("BRISK_DEFAULT_SHIELD_SERVER_ID", 0)),
		LoadSteerEnabled:    envBoolOr("BRISK_LOAD_STEER_ENABLED", false),
		LoadSteerInterval:   envIntOr("BRISK_LOAD_STEER_INTERVAL", 45),
		LoadSteerLowWater:   envFloatOr("BRISK_LOAD_STEER_LOW", 0.55),
		LoadSteerHighWater:  envFloatOr("BRISK_LOAD_STEER_HIGH", 0.90),
		LoadSteerMinFactor:  envFloatOr("BRISK_LOAD_STEER_MIN_FACTOR", 0.30),
		HealthEnabled:        envBoolOr("BRISK_HEALTH_ENABLED", true),
		HealthInterval:       envIntOr("BRISK_HEALTH_INTERVAL", 10),
		HealthTimeout:        envIntOr("BRISK_HEALTH_TIMEOUT", 3),
		HealthFailThreshold:  envIntOr("BRISK_HEALTH_FAIL_THRESHOLD", 2),
		HealthRiseThreshold:  envIntOr("BRISK_HEALTH_RISE_THRESHOLD", 3),
		HealthPath:           envOr("BRISK_HEALTH_PATH", "/healthz"),
		HealthScheme:         envOr("BRISK_HEALTH_SCHEME", "https"),
		HealthPort:           envIntOr("BRISK_HEALTH_PORT", 0),
		TLSManaged:           envBoolOr("BRISK_TLS_MANAGED", false),
		TLSEmail:             os.Getenv("BRISK_TLS_EMAIL"),
		TLSStaging:           envBoolOr("BRISK_TLS_STAGING", true),
		TLSDomains:           splitCSV(os.Getenv("BRISK_TLS_DOMAINS")),
		TLSCertName:          os.Getenv("BRISK_TLS_CERT_NAME"),
		TLSAccountDir:        envOr("BRISK_TLS_ACME_DIR", "/var/lib/brisk-control/acme"),
		CustomTLSStaging:     envBoolOr("BRISK_CUSTOM_TLS_STAGING", true),
		CookieSecure:         envBoolOr("BRISK_COOKIE_SECURE", false),
		DashboardOrigin:      os.Getenv("BRISK_DASHBOARD_ORIGIN"),
		AdminEmail:           os.Getenv("BRISK_ADMIN_EMAIL"),
		AdminPassword:        os.Getenv("BRISK_ADMIN_PASSWORD"),
		AgentPubKeys:         splitCSV(os.Getenv("BRISK_AGENT_PUBKEYS")),
		RolloutEnabled:       envBoolOr("BRISK_ROLLOUT_ENABLED", true),
	}
	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	// Derive a stable cert store-key from the SANs when not given: prefer the
	// first non-wildcard domain (the apex), else strip "*." from the first SAN.
	if c.TLSCertName == "" && len(c.TLSDomains) > 0 {
		c.TLSCertName = deriveCertName(c.TLSDomains)
	}
	// Clamp the routing TTL to a sane window. Floor lowered to 10s in Step 4 so a
	// ~15s TTL is allowed for fast failover (detection + TTL ≈ ~30-35s); not so
	// short it spikes query volume. Ceiling stays 120s.
	if c.BriskDNSTTL < 10 {
		c.BriskDNSTTL = 10
	}
	if c.BriskDNSTTL > 120 {
		c.BriskDNSTTL = 120
	}
	return c, nil
}

// splitCSV parses a comma-separated env value into a trimmed, non-empty slice.
func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// deriveCertName returns the apex (first non-wildcard SAN), else the first SAN
// with a leading "*." stripped — used as the tls_certs store key.
func deriveCertName(domains []string) string {
	for _, d := range domains {
		if !strings.HasPrefix(d, "*.") {
			return d
		}
	}
	return strings.TrimPrefix(domains[0], "*.")
}

func envBoolOr(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloatOr(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return def
}

// SlogLevel maps the configured level string to slog.Level.
func (c *Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
