package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Zone is a site being accelerated.
type Zone struct {
	ID            int64       `json:"id"`
	AccountID     int64       `json:"account_id"`
	Name          string      `json:"name"`
	CDNHostname   string      `json:"cdn_hostname"`
	CustomDomain  *string     `json:"custom_domain,omitempty"`
	OriginURL     string      `json:"origin_url"`
	HostHeader    string      `json:"host_header"` // upstream Host (empty = derive from origin_url)
	TLSMode       string      `json:"tls_mode"`
	Video         bool        `json:"video"`
	Profile       string      `json:"profile"`
	PlaylistTTL   string      `json:"playlist_ttl"`
	SegmentTTL    string      `json:"segment_ttl"`
	CorsOrigin    string      `json:"cors_origin"`
	BrotliLevel   int32       `json:"brotli_level"`
	Status        string      `json:"status"`
	ConfigVersion int64       `json:"config_version"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
	Rules         []CacheRule `json:"rules,omitempty"`

	// Origin Shield (Phase 4 Step 3). When enabled, normal edges proxy this zone's
	// misses through ShieldServerID (a role=shield PoP) instead of the origin.
	// ShieldServerID NULL => use the network-wide default shield.
	OriginShieldEnabled bool   `json:"origin_shield_enabled"`
	ShieldServerID      *int64 `json:"shield_server_id,omitempty"`

	// WAF + rate limiting (Phase 4 Step 4). Per-zone security: a managed OWASP CRS
	// ruleset + custom rules + rate limits, at a detect (log-only) or block mode.
	// OFF by default (live zones unaffected); enabling defaults to detect mode.
	WAFEnabled        bool   `json:"waf_enabled"`
	WAFMode           string `json:"waf_mode"`            // detect | block
	WAFManagedRuleset string `json:"waf_managed_ruleset"` // off | owasp_crs
	WAFParanoia       int32  `json:"waf_paranoia"`        // CRS paranoia 1..4
	WAFWpPreset       bool   `json:"waf_wp_preset"`       // WordPress hardening preset
	WAFFailOpen       bool   `json:"waf_fail_open"`       // engine error -> fail open (true) vs closed

	// Cache Settings (Bunny-style per-zone cache controls, migration 00018). Every
	// field defaults to today's edge behavior, so a zone is byte-identical until a
	// tenant changes one. The agent emits these only when non-default (omitempty).
	Cache CacheSettings `json:"cache_settings"`

	// Hotlink Protection (Referer allowlist, migration 00024). OFF by default; when
	// enabled the edge blocks (403) requests whose Referer isn't an allowed host, via
	// nginx valid_referers on content locations only. AllowEmptyReferer (default true)
	// decides whether no-Referer requests pass — the safe Bunny-style default.
	HotlinkEnabled          bool   `json:"hotlink_enabled"`
	HotlinkAllowedReferrers string `json:"hotlink_allowed_referrers"` // comma-separated hosts
	HotlinkAllowEmptyRef    bool   `json:"hotlink_allow_empty_referer"`

	// Origin connection options (Bunny-style "Origin" panel, migration 00025). All OFF
	// by default => today's byte-identical edge behavior. OriginSSLVerify => the edge
	// validates the origin's TLS cert (proxy_ssl_verify). OriginFollowRedirects => the
	// edge follows an origin 3xx one hop server-side instead of passing it through.
	// ForwardHostHeader => the edge sends the client's Host ($host) upstream instead of
	// the per-zone host_header.
	OriginSSLVerify       bool `json:"origin_ssl_verify"`
	OriginFollowRedirects bool `json:"origin_follow_redirects"`
	ForwardHostHeader     bool `json:"forward_host_header"`

	// Custom 502/504 error page (Bunny-style, migration 00026). Branded HTML the edge
	// serves on a nginx-generated 502/504 (origin unreachable/timeout). EMPTY => off
	// (today's byte-identical default). The agent emits it only when non-empty.
	Error5xxHTML string `json:"error_5xx_html"`

	// Blocked-IP denylist (Bunny-style, migration 00027). Comma-separated IPs / CIDRs the
	// edge blocks (403) on content locations. EMPTY => off (byte-identical). The agent
	// emits it only when non-empty.
	BlockedIPs string `json:"blocked_ips"`

	// Access toggles (Bunny-style, migration 00028). BlockRootPath => the edge 403s the
	// bare root + any directory root (path ending in "/"). BlockPost => the edge 405s
	// POST requests. Both false => off (byte-identical). The agent emits each only when true.
	BlockRootPath bool `json:"block_root_path"`
	BlockPost     bool `json:"block_post"`
}

// CacheSettings are the per-zone Smart-Cache controls surfaced in the dashboard and
// rendered into the edge nginx (gated so defaults == current behavior).
type CacheSettings struct {
	Smart           bool   `json:"smart"`
	EdgeMode        string `json:"edge_mode"`    // default | respect_origin | override | no_cache
	EdgeTTL         string `json:"edge_ttl"`     // nginx time when edge_mode=override
	BrowserMode     string `json:"browser_mode"` // default | match_server | override | no_cache
	BrowserTTL      string `json:"browser_ttl"`  // nginx time when browser_mode=override
	QuerySort       bool   `json:"query_sort"`
	CacheErrors     bool   `json:"cache_errors"`
	VaryWebp        bool   `json:"vary_webp"`
	VaryDevice      bool   `json:"vary_device"`
	VaryCountry     bool   `json:"vary_country"`
	VaryCookie      string `json:"vary_cookie"`
	VaryQueryString bool   `json:"vary_querystring"`
	VaryHeaders     string `json:"vary_headers"`    // comma-separated request header names
	QueryWhitelist  string `json:"query_whitelist"` // comma-separated args; only these count
	StripCookies    bool   `json:"strip_cookies"`
	LargeObject     bool   `json:"large_object"`
	StaleOffline    bool   `json:"stale_offline"`
	StaleUpdating   bool   `json:"stale_updating"`
}

// DefaultCacheSettings returns the settings that reproduce today's behavior (the
// migration column defaults). Used as the create-time value in agent rendering.
func DefaultCacheSettings() CacheSettings {
	return CacheSettings{
		EdgeMode: "default", BrowserMode: "default",
		StaleOffline: true, StaleUpdating: true,
	}
}

// CreateZoneParams are the (already defaulted) inputs for creating a zone.
type CreateZoneParams struct {
	Name         string
	CDNHostname  string
	CustomDomain *string
	OriginURL    string
	HostHeader   string
	TLSMode      string
	Video        bool
	Profile      string
	PlaylistTTL  string
	SegmentTTL   string
	CorsOrigin   string
	BrotliLevel  int32
}

// UpdateZoneParams are nil-able fields; nil means "leave unchanged" (COALESCE).
type UpdateZoneParams struct {
	Name         *string
	CustomDomain *string
	OriginURL    *string
	HostHeader   *string
	TLSMode      *string
	Video        *bool
	Profile      *string
	PlaylistTTL  *string
	SegmentTTL   *string
	CorsOrigin   *string
	BrotliLevel  *int32
	Status       *string

	// Origin options (migration 00025); nil => leave unchanged.
	OriginSSLVerify       *bool
	OriginFollowRedirects *bool
	ForwardHostHeader     *bool
}

const zoneCols = `id, account_id, name, cdn_hostname, custom_domain, origin_url, host_header, tls_mode,
	video, profile, playlist_ttl, segment_ttl, cors_origin, brotli_level, status,
	config_version, created_at, updated_at, origin_shield_enabled, shield_server_id,
	waf_enabled, waf_mode, waf_managed_ruleset, waf_paranoia, waf_wp_preset, waf_fail_open,
	cache_smart, cache_edge_mode, cache_edge_ttl, cache_browser_mode, cache_browser_ttl,
	cache_query_sort, cache_error_responses, cache_vary_webp, cache_vary_device, cache_vary_country,
	cache_vary_cookie, cache_vary_querystring, cache_vary_headers, cache_query_whitelist,
	cache_strip_cookies, cache_large_object, cache_stale_offline, cache_stale_updating,
	hotlink_enabled, hotlink_allowed_referrers, hotlink_allow_empty_referer,
	origin_ssl_verify, origin_follow_redirects, forward_host_header,
	error_5xx_html, blocked_ips, block_root_path, block_post`

func scanZone(row pgx.Row) (Zone, error) {
	var z Zone
	err := row.Scan(&z.ID, &z.AccountID, &z.Name, &z.CDNHostname, &z.CustomDomain, &z.OriginURL,
		&z.HostHeader, &z.TLSMode, &z.Video, &z.Profile, &z.PlaylistTTL, &z.SegmentTTL, &z.CorsOrigin,
		&z.BrotliLevel, &z.Status, &z.ConfigVersion, &z.CreatedAt, &z.UpdatedAt,
		&z.OriginShieldEnabled, &z.ShieldServerID,
		&z.WAFEnabled, &z.WAFMode, &z.WAFManagedRuleset, &z.WAFParanoia, &z.WAFWpPreset, &z.WAFFailOpen,
		&z.Cache.Smart, &z.Cache.EdgeMode, &z.Cache.EdgeTTL, &z.Cache.BrowserMode, &z.Cache.BrowserTTL,
		&z.Cache.QuerySort, &z.Cache.CacheErrors, &z.Cache.VaryWebp, &z.Cache.VaryDevice, &z.Cache.VaryCountry,
		&z.Cache.VaryCookie, &z.Cache.VaryQueryString, &z.Cache.VaryHeaders, &z.Cache.QueryWhitelist,
		&z.Cache.StripCookies, &z.Cache.LargeObject, &z.Cache.StaleOffline, &z.Cache.StaleUpdating,
		&z.HotlinkEnabled, &z.HotlinkAllowedReferrers, &z.HotlinkAllowEmptyRef,
		&z.OriginSSLVerify, &z.OriginFollowRedirects, &z.ForwardHostHeader,
		&z.Error5xxHTML, &z.BlockedIPs, &z.BlockRootPath, &z.BlockPost)
	return z, err
}

// CreateZone inserts a zone (config_version starts at 1) and returns it.
func (st *Store) CreateZone(ctx context.Context, p CreateZoneParams) (Zone, error) {
	row := st.pool.QueryRow(ctx, `
		INSERT INTO zones (name, cdn_hostname, custom_domain, origin_url, host_header, tls_mode,
			video, profile, playlist_ttl, segment_ttl, cors_origin, brotli_level)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING `+zoneCols,
		p.Name, p.CDNHostname, p.CustomDomain, p.OriginURL, p.HostHeader, p.TLSMode,
		p.Video, p.Profile, p.PlaylistTTL, p.SegmentTTL, p.CorsOrigin, p.BrotliLevel)
	return scanZone(row)
}

// ListZones returns all zones (admin scope; account_id-scopable later).
func (st *Store) ListZones(ctx context.Context) ([]Zone, error) {
	rows, err := st.pool.Query(ctx, `SELECT `+zoneCols+` FROM zones ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Zone{}
	for rows.Next() {
		z, err := scanZone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// GetZone returns one zone or ErrNotFound (rules attached by the handler).
func (st *Store) GetZone(ctx context.Context, id int64) (Zone, error) {
	row := st.pool.QueryRow(ctx, `SELECT `+zoneCols+` FROM zones WHERE id = $1`, id)
	z, err := scanZone(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Zone{}, ErrNotFound
	}
	return z, err
}

// UpdateZone applies the non-nil fields, bumps config_version + updated_at, returns the zone.
func (st *Store) UpdateZone(ctx context.Context, id int64, p UpdateZoneParams) (Zone, error) {
	row := st.pool.QueryRow(ctx, `
		UPDATE zones SET
			name          = COALESCE($2, name),
			custom_domain = COALESCE($3, custom_domain),
			origin_url    = COALESCE($4, origin_url),
			host_header   = COALESCE($5, host_header),
			tls_mode      = COALESCE($6, tls_mode),
			video         = COALESCE($7, video),
			profile       = COALESCE($8, profile),
			playlist_ttl  = COALESCE($9, playlist_ttl),
			segment_ttl   = COALESCE($10, segment_ttl),
			cors_origin   = COALESCE($11, cors_origin),
			brotli_level  = COALESCE($12, brotli_level),
			status        = COALESCE($13, status),
			origin_ssl_verify       = COALESCE($14, origin_ssl_verify),
			origin_follow_redirects = COALESCE($15, origin_follow_redirects),
			forward_host_header     = COALESCE($16, forward_host_header),
			config_version = config_version + 1,
			updated_at    = now()
		WHERE id = $1
		RETURNING `+zoneCols,
		id, p.Name, p.CustomDomain, p.OriginURL, p.HostHeader, p.TLSMode, p.Video, p.Profile,
		p.PlaylistTTL, p.SegmentTTL, p.CorsOrigin, p.BrotliLevel, p.Status,
		p.OriginSSLVerify, p.OriginFollowRedirects, p.ForwardHostHeader)
	z, err := scanZone(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Zone{}, ErrNotFound
	}
	return z, err
}

// SetZoneShield sets a zone's origin-shield config (enabled + which shield PoP,
// NULL = network default) directly (not COALESCE, so the shield can be cleared to
// NULL), bumps config_version so assigned edges re-pull, and returns the zone.
func (st *Store) SetZoneShield(ctx context.Context, id int64, enabled bool, shieldServerID *int64) (Zone, error) {
	row := st.pool.QueryRow(ctx, `
		UPDATE zones SET
			origin_shield_enabled = $2,
			shield_server_id      = $3,
			config_version        = config_version + 1,
			updated_at            = now()
		WHERE id = $1
		RETURNING `+zoneCols, id, enabled, shieldServerID)
	z, err := scanZone(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Zone{}, ErrNotFound
	}
	return z, err
}

// WAFSettings are the per-zone WAF knobs (Phase 4 Step 4).
type WAFSettings struct {
	Enabled        bool
	Mode           string // detect | block
	ManagedRuleset string // off | owasp_crs
	Paranoia       int32  // 1..4
	WpPreset       bool
	FailOpen       bool
}

// SetZoneWAF updates a zone's WAF settings directly and bumps config_version so
// the zone's edges re-pull and reload (managed CRS + custom rules + rate limits
// re-render). Live-safe: a config change over the poll interval, nginx validated
// before reload. WAF stays OFF until a tenant explicitly enables it.
func (st *Store) SetZoneWAF(ctx context.Context, id int64, s WAFSettings) (Zone, error) {
	row := st.pool.QueryRow(ctx, `
		UPDATE zones SET
			waf_enabled         = $2,
			waf_mode            = $3,
			waf_managed_ruleset = $4,
			waf_paranoia        = $5,
			waf_wp_preset       = $6,
			waf_fail_open       = $7,
			config_version      = config_version + 1,
			updated_at          = now()
		WHERE id = $1
		RETURNING `+zoneCols,
		id, s.Enabled, s.Mode, s.ManagedRuleset, s.Paranoia, s.WpPreset, s.FailOpen)
	z, err := scanZone(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Zone{}, ErrNotFound
	}
	return z, err
}

// SetZoneCacheSettings replaces a zone's Cache Settings directly and bumps
// config_version so assigned edges re-pull and reload with the new cache directives.
// Live-safe: defaults reproduce current behavior; nginx is validated before reload.
func (st *Store) SetZoneCacheSettings(ctx context.Context, id int64, c CacheSettings) (Zone, error) {
	row := st.pool.QueryRow(ctx, `
		UPDATE zones SET
			cache_smart            = $2,
			cache_edge_mode        = $3,
			cache_edge_ttl         = $4,
			cache_browser_mode     = $5,
			cache_browser_ttl      = $6,
			cache_query_sort       = $7,
			cache_error_responses  = $8,
			cache_vary_webp        = $9,
			cache_vary_device      = $10,
			cache_vary_country     = $11,
			cache_vary_cookie      = $12,
			cache_vary_querystring = $13,
			cache_vary_headers     = $14,
			cache_query_whitelist  = $15,
			cache_strip_cookies    = $16,
			cache_large_object     = $17,
			cache_stale_offline    = $18,
			cache_stale_updating   = $19,
			config_version         = config_version + 1,
			updated_at             = now()
		WHERE id = $1
		RETURNING `+zoneCols,
		id, c.Smart, c.EdgeMode, c.EdgeTTL, c.BrowserMode, c.BrowserTTL, c.QuerySort,
		c.CacheErrors, c.VaryWebp, c.VaryDevice, c.VaryCountry, c.VaryCookie, c.VaryQueryString,
		c.VaryHeaders, c.QueryWhitelist, c.StripCookies, c.LargeObject, c.StaleOffline, c.StaleUpdating)
	z, err := scanZone(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Zone{}, ErrNotFound
	}
	return z, err
}

// SetZoneHotlink updates a zone's hotlink protection (Referer allowlist) directly
// and bumps config_version so the zone's edges re-pull + reload. Live-safe: a config
// change over the poll interval, nginx validated before reload. Defaults reproduce
// today's behavior (off), so this never changes a zone the tenant didn't touch.
func (st *Store) SetZoneHotlink(ctx context.Context, id int64, enabled bool, allowedReferrers string, allowEmpty bool) (Zone, error) {
	row := st.pool.QueryRow(ctx, `
		UPDATE zones SET
			hotlink_enabled             = $2,
			hotlink_allowed_referrers   = $3,
			hotlink_allow_empty_referer = $4,
			config_version              = config_version + 1,
			updated_at                  = now()
		WHERE id = $1
		RETURNING `+zoneCols, id, enabled, allowedReferrers, allowEmpty)
	z, err := scanZone(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Zone{}, ErrNotFound
	}
	return z, err
}

// SetZoneErrorPage replaces a zone's custom 502/504 error page HTML directly and
// bumps config_version so the zone's edges re-pull + reload. Empty html => off (the
// edge renders byte-identical nginx). Live-safe: a config change over the poll
// interval, nginx validated before reload.
func (st *Store) SetZoneErrorPage(ctx context.Context, id int64, html string) (Zone, error) {
	row := st.pool.QueryRow(ctx, `
		UPDATE zones SET
			error_5xx_html = $2,
			config_version = config_version + 1,
			updated_at     = now()
		WHERE id = $1
		RETURNING `+zoneCols, id, html)
	z, err := scanZone(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Zone{}, ErrNotFound
	}
	return z, err
}

// SetZoneBlockedIPs replaces a zone's Blocked-IP denylist (comma-separated IP/CIDR)
// directly and bumps config_version so the zone's edges re-pull + reload. Empty =>
// off (byte-identical). Live-safe: nginx validated before reload.
func (st *Store) SetZoneBlockedIPs(ctx context.Context, id int64, blockedIPs string) (Zone, error) {
	row := st.pool.QueryRow(ctx, `
		UPDATE zones SET
			blocked_ips    = $2,
			config_version = config_version + 1,
			updated_at     = now()
		WHERE id = $1
		RETURNING `+zoneCols, id, blockedIPs)
	z, err := scanZone(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Zone{}, ErrNotFound
	}
	return z, err
}

// SetZoneAccessFlags replaces a zone's access toggles (block-root-path / block-POST)
// directly and bumps config_version so the zone's edges re-pull + reload. Both false =>
// off (byte-identical). Live-safe: nginx validated before reload.
func (st *Store) SetZoneAccessFlags(ctx context.Context, id int64, blockRoot, blockPost bool) (Zone, error) {
	row := st.pool.QueryRow(ctx, `
		UPDATE zones SET
			block_root_path = $2,
			block_post      = $3,
			config_version  = config_version + 1,
			updated_at      = now()
		WHERE id = $1
		RETURNING `+zoneCols, id, blockRoot, blockPost)
	z, err := scanZone(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Zone{}, ErrNotFound
	}
	return z, err
}

// ListActiveZones returns all zones with status='active' (for auto-assigning the
// whole catalog to a newly-online edge).
func (st *Store) ListActiveZones(ctx context.Context) ([]Zone, error) {
	rows, err := st.pool.Query(ctx, `SELECT `+zoneCols+` FROM zones WHERE status = 'active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Zone{}
	for rows.Next() {
		z, err := scanZone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// DeleteZone removes a zone (cascades rules); ErrNotFound if absent.
func (st *Store) DeleteZone(ctx context.Context, id int64) error {
	ct, err := st.pool.Exec(ctx, `DELETE FROM zones WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
