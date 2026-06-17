package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// Source is where the agent gets its configuration from.
//
// Phase 1: FileSource reads the local agent.yaml (standalone mode).
// Phase 2 Step 3: ControlPlaneSource serves the zones pulled from brisk-control,
// persisted to a local last-known-good cache so the edge keeps serving even when
// the control plane is unreachable (dashboard down ≠ edge down).
type Source interface {
	// Fetch returns a validated, normalized configuration.
	Fetch() (*Config, error)
}

// FileSource loads the agent config from a local YAML file.
type FileSource struct {
	Path string
}

// NewFileSource returns a Source backed by the YAML file at path.
func NewFileSource(path string) *FileSource { return &FileSource{Path: path} }

// Fetch implements Source by loading and validating the local file.
func (s *FileSource) Fetch() (*Config, error) { return Load(s.Path) }

// --- control-plane source (Step 3) ---

// ControlPlaneSource serves agent-level settings from the local base config
// (agent.yaml) combined with the ZONES pulled from the control plane and cached
// locally. The poll loop calls WriteCache on a 200; Fetch reads the cache (or
// falls back to the base config's zones until the first successful pull).
type ControlPlaneSource struct {
	base      *Config // agent-level settings + initial (fallback) zones from agent.yaml
	cachePath string  // last-known-good pulled config (JSON)
}

// NewControlPlaneSource builds a source that overlays pulled zones onto base.
func NewControlPlaneSource(base *Config, cachePath string) *ControlPlaneSource {
	if cachePath == "" {
		cachePath = DefaultConfigCache
	}
	return &ControlPlaneSource{base: base, cachePath: cachePath}
}

// wire types mirror the control plane's /api/v1/agent/config response.
type wireZone struct {
	CDNHostname  string `json:"cdn_hostname"`
	CustomDomain string `json:"custom_domain"`
	OriginURL    string `json:"origin_url"`
	HostHeader   string `json:"host_header"`
	TLSMode      string `json:"tls_mode"`
	Video        bool   `json:"video"`
	Profile      string `json:"profile"`
	PlaylistTTL  string `json:"playlist_ttl"`
	SegmentTTL   string `json:"segment_ttl"`
	CorsOrigin   string `json:"cors_origin"`
	BrotliLevel  int    `json:"brotli_level"`

	// Origin Shield (Phase 4 Step 3): host:port of the shield PoP this edge should
	// proxy this zone's misses to. Empty => proxy the origin directly.
	ShieldUpstream string `json:"shield_upstream"`

	// Managed TLS (Part 3): cert material for tls_mode="managed" zones.
	TLSCert       string `json:"tls_cert"`
	TLSKey        string `json:"tls_key"`
	TLSCertSerial string `json:"tls_cert_serial"`

	// WAF + rate limiting (Phase 4 Step 4): nil/absent => WAF off for this zone.
	WAF *ZoneWAF `json:"waf"`

	// Lua edge logic (Phase 4 Step 5): per-zone cache rules + header transforms.
	CacheRules       []ZoneCacheRule       `json:"cache_rules"`
	HeaderTransforms []ZoneHeaderTransform `json:"header_transforms"`

	// Hotlink protection (Referer allowlist): nil/absent => off for this zone.
	Hotlink *ZoneHotlink `json:"hotlink"`

	// Origin connection options (migration 00025): absent/false => off (byte-identical).
	OriginSSLVerify       bool `json:"origin_ssl_verify"`
	OriginFollowRedirects bool `json:"origin_follow_redirects"`
	ForwardHostHeader     bool `json:"forward_host_header"`

	// Custom 502/504 error page (migration 00026): absent/empty => off (byte-identical).
	Error5xxHTML string `json:"error_5xx_html"`

	// Blocked-IP denylist (migration 00027): absent/empty => off (byte-identical).
	BlockedIPs string `json:"blocked_ips"`

	// Access toggles (migration 00028): absent/false => off (byte-identical).
	BlockRootPath bool `json:"block_root_path"`
	BlockPost     bool `json:"block_post"`
}

type wireConfig struct {
	ConfigVersion string     `json:"config_version"`
	Zones         []wireZone `json:"zones"`
}

// Fetch returns base agent settings with zones from the cached pulled config.
// If no cache exists yet, it returns the base config unchanged (agent.yaml zones)
// so the edge serves its last-known-good from the very first boot.
func (s *ControlPlaneSource) Fetch() (*Config, error) {
	cfg := *s.base // copy agent-level settings (token, edge_id, le_email, tls_mode, ...)

	if wc, err := s.readCache(); err == nil && len(wc.Zones) > 0 {
		cfg.Zones = make([]Zone, 0, len(wc.Zones))
		for _, wz := range wc.Zones {
			cfg.Zones = append(cfg.Zones, wz.toZone())
		}
		if wc.Zones[0].BrotliLevel > 0 {
			cfg.BrotliCompLevel = wc.Zones[0].BrotliLevel
		}
	}
	// else: keep base.Zones (agent.yaml fallback)

	cfg.applyDefaults()
	// Quarantine any individually-bad zone so ONE broken tenant zone can't reject
	// the whole pulled config (the all-or-nothing trap that froze NY/FRA + crashed
	// BLR). Dropped zones are logged; the >=1-zone guard in Validate still rejects a
	// config left with no valid zones (then the caller keeps last-known-good).
	if dropped := cfg.DropInvalidZones(); len(dropped) > 0 {
		log.Printf("brisk-agent: quarantined %d invalid zone(s) from pulled config: %v", len(dropped), dropped)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid pulled config: %w", err)
	}
	return &cfg, nil
}

// WriteCache validates and atomically writes the raw control-plane response as
// the new last-known-good. It rejects empty-zone configs so an accidental
// unassignment can never blank out the edge.
func (s *ControlPlaneSource) WriteCache(raw []byte) error {
	var wc wireConfig
	if err := json.Unmarshal(raw, &wc); err != nil {
		return fmt.Errorf("parse pulled config: %w", err)
	}
	if len(wc.Zones) == 0 {
		return fmt.Errorf("pulled config has no zones; keeping last-known-good")
	}
	if err := os.MkdirAll(filepath.Dir(s.cachePath), 0o755); err != nil {
		return err
	}
	tmp := s.cachePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.cachePath) // atomic replace
}

// CachedETag returns the config_version stored in the cache file, or "".
func (s *ControlPlaneSource) CachedETag() string {
	wc, err := s.readCache()
	if err != nil {
		return ""
	}
	return wc.ConfigVersion
}

func (s *ControlPlaneSource) readCache() (wireConfig, error) {
	var wc wireConfig
	raw, err := os.ReadFile(s.cachePath)
	if err != nil {
		return wc, err
	}
	if err := json.Unmarshal(raw, &wc); err != nil {
		return wc, err
	}
	return wc, nil
}

func (wz wireZone) toZone() Zone {
	domain := wz.CustomDomain
	if domain == "" {
		domain = wz.CDNHostname
	}
	return Zone{
		Domain:        domain,
		Origin:        wz.OriginURL,
		HostHeader:    wz.HostHeader,
		TLS:           wz.TLSMode,
		Video:         wz.Video,
		Profile:       wz.Profile,
		PlaylistTTL:   wz.PlaylistTTL,
		SegmentTTL:    wz.SegmentTTL,
		CORSOrigin:     wz.CorsOrigin,
		MinUses:        1,
		ShieldUpstream: wz.ShieldUpstream,
		TLSCert:          wz.TLSCert,
		TLSKey:           wz.TLSKey,
		TLSCertSerial:    wz.TLSCertSerial,
		WAF:              wz.WAF,
		CacheRules:       wz.CacheRules,
		HeaderTransforms: wz.HeaderTransforms,
		Hotlink:          wz.Hotlink,
		OriginSSLVerify:       wz.OriginSSLVerify,
		OriginFollowRedirects: wz.OriginFollowRedirects,
		ForwardHostHeader:     wz.ForwardHostHeader,
		Error5xxHTML:          wz.Error5xxHTML,
		BlockedIPs:            wz.BlockedIPs,
		BlockRootPath:         wz.BlockRootPath,
		BlockPost:             wz.BlockPost,
	}
}
