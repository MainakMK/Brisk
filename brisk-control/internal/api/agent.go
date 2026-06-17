package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"brisk-control/internal/auth"
	"brisk-control/internal/store"
)

type heartbeatInput struct {
	EdgeID       string `json:"edge_id"`
	AgentVersion string `json:"agent_version"`
	NginxVersion string `json:"nginx_version"`
	OS           string `json:"os"`
	Kernel       string `json:"kernel"`
	GoVersion    string `json:"go_version"`
}

// heartbeat marks the authenticated server online. The server identity comes
// from the bearer token (auth middleware), not the body.
func (a *API) heartbeat(w http.ResponseWriter, r *http.Request) {
	serverID, ok := auth.ServerIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in heartbeatInput
	_ = json.NewDecoder(r.Body).Decode(&in) // body is informational; ignore parse errors

	// Persist the reported tech/runtime stack (nginx + agent version, OS, kernel, Go)
	// so the dashboard can show "what's running on this PoP". Best-effort: a failure here
	// must not fail the heartbeat (which keeps the edge in DNS rotation). Empty fields
	// from an older agent never overwrite stored values (see store.SetServerTech).
	if err := a.store.SetServerTech(r.Context(), serverID, in.NginxVersion, in.AgentVersion, in.OS, in.Kernel, in.GoVersion); err != nil {
		log.Printf("heartbeat: persist tech for server %d: %v", serverID, err)
	}

	transitioned, err := a.store.MarkServerOnline(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// On the transition to online (not every heartbeat), converge DNS so the
	// edge's A record is enabled in the routing set, AND auto-assign the whole active
	// zone catalog to this edge (Step 4.7 Part 1) so a freshly-added/returning PoP
	// serves everything with no manual wiring. Both idempotent.
	if transitioned {
		a.triggerDNSReconcile("heartbeat")
		a.autoAssignAllZonesToServer(r.Context(), serverID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "online", "server_id": serverID})
}

// --- agent pull-config (Step 3) ---

type agentRule struct {
	Priority    int32   `json:"priority"`
	MatchType   string  `json:"match_type"`
	MatchValue  string  `json:"match_value"`
	Action      string  `json:"action"`
	ActionValue *string `json:"action_value,omitempty"`
}

type agentZone struct {
	CDNHostname  string      `json:"cdn_hostname"`
	CustomDomain string      `json:"custom_domain,omitempty"`
	OriginURL    string      `json:"origin_url"`
	HostHeader   string      `json:"host_header,omitempty"`
	TLSMode      string      `json:"tls_mode"`
	Video        bool        `json:"video"`
	Profile      string      `json:"profile"`
	PlaylistTTL  string      `json:"playlist_ttl"`
	SegmentTTL   string      `json:"segment_ttl"`
	CorsOrigin   string      `json:"cors_origin"`
	BrotliLevel  int32       `json:"brotli_level"`
	Status       string      `json:"status"`
	CacheRules   []agentRule `json:"cache_rules"`

	// --- Origin Shield (Phase 4 Step 3): the mid-tier upstream for THIS edge.
	// Non-empty (host:port) => this zone's misses proxy to the shield PoP instead
	// of the origin (the edge sends Host=$host so the shield caches under the SAME
	// key; origin is the error_page fallback). Empty => proxy the origin directly
	// (the default, the shield PoP itself, a self-reference, or a dead shield).
	ShieldUpstream string `json:"shield_upstream,omitempty"`

	// --- Managed TLS (Part 3): cert material for tls_mode="managed" zones.
	// Shipped only when a control-plane-managed cert covers this zone's served
	// hostname; the agent writes it and reloads. Serial drives the config ETag.
	TLSCert       string `json:"tls_cert,omitempty"`
	TLSKey        string `json:"tls_key,omitempty"`
	TLSCertSerial string `json:"tls_cert_serial,omitempty"`

	// --- WAF + rate limiting (Phase 4 Step 4): per-zone security config the edge
	// enforces (Coraza CRS + custom rules via auth_request; Nginx native rate
	// limits). nil/omitted => WAF off for this zone (no auth_request, no overhead).
	WAF *agentWAF `json:"waf,omitempty"`

	// --- Header transforms (Phase 4 Step 5): per-zone request/response header
	// add/remove/set, enforced by the edge Lua layer. (Cache rules ride in
	// CacheRules above and are now Lua-enforced too.) Empty => none.
	HeaderTransforms []agentHeaderTransform `json:"header_transforms,omitempty"`

	// --- Cache Settings (Bunny-style per-zone cache controls). Sent ONLY when the
	// zone differs from defaults — a default zone omits this entirely, so its
	// agent-config payload + ETag are byte-identical to before (the live fleet is
	// untouched). The agent renders the gated directives only when present.
	Cache *agentCacheSettings `json:"cache_settings,omitempty"`

	// --- Origin connection options (migration 00025). All omitempty => a false (off)
	// flag is omitted from the payload, so a default zone's agent-config + ETag are
	// byte-identical. The agent renders nginx directives only when a flag is true.
	OriginSSLVerify       bool `json:"origin_ssl_verify,omitempty"`
	OriginFollowRedirects bool `json:"origin_follow_redirects,omitempty"`
	ForwardHostHeader     bool `json:"forward_host_header,omitempty"`

	// --- Hotlink protection (Referer allowlist). nil/omitted => off for this zone
	// (no valid_referers, no 403 guard — byte-identical). Sent only when enabled.
	Hotlink *agentHotlink `json:"hotlink,omitempty"`

	// --- Custom 502/504 error page (Bunny-style, migration 00026). Branded HTML the
	// edge serves on a nginx-generated 502/504. omitempty => an empty page is omitted,
	// so a default zone's agent-config + ETag are byte-identical. The agent writes it
	// to a file and renders the error_page directive only when present.
	Error5xxHTML string `json:"error_5xx_html,omitempty"`

	// --- Blocked-IP denylist (Bunny-style, migration 00027). Comma-separated IP/CIDR
	// the edge blocks (403) on content locations. omitempty => an empty list is omitted,
	// so a default zone's agent-config + ETag are byte-identical. Rendered as nginx
	// `deny` lines only when present.
	BlockedIPs string `json:"blocked_ips,omitempty"`

	// --- Access toggles (Bunny-style, migration 00028). omitempty => a false flag is
	// omitted, so a default zone's agent-config + ETag are byte-identical. Rendered as a
	// gated server-level `if` (403 root/dir; 405 POST) only when true.
	BlockRootPath bool `json:"block_root_path,omitempty"`
	BlockPost     bool `json:"block_post,omitempty"`
}

// agentHotlink is the per-zone hotlink config shipped to the edge. The agent turns
// AllowedReferrers + AllowEmpty into an nginx `valid_referers` directive and guards
// content locations with `if ($invalid_referer) return 403;` (never /healthz/ACME).
type agentHotlink struct {
	AllowedReferrers string `json:"allowed_referrers"` // comma-separated hosts (may include *.x)
	AllowEmpty       bool   `json:"allow_empty"`       // allow requests with no Referer
}

// buildAgentHotlink returns a zone's hotlink config for the edge, or nil when the
// zone has hotlink protection off (so a non-hotlink zone ships nothing — byte-identical).
func buildAgentHotlink(z store.Zone) *agentHotlink {
	if !z.HotlinkEnabled {
		return nil
	}
	return &agentHotlink{
		AllowedReferrers: z.HotlinkAllowedReferrers,
		AllowEmpty:       z.HotlinkAllowEmptyRef,
	}
}

// agentCacheSettings mirrors store.CacheSettings on the wire (per-zone Cache Settings).
type agentCacheSettings struct {
	Smart           bool   `json:"smart"`
	EdgeMode        string `json:"edge_mode"`
	EdgeTTL         string `json:"edge_ttl"`
	BrowserMode     string `json:"browser_mode"`
	BrowserTTL      string `json:"browser_ttl"`
	QuerySort       bool   `json:"query_sort"`
	CacheErrors     bool   `json:"cache_errors"`
	VaryWebp        bool   `json:"vary_webp"`
	VaryDevice      bool   `json:"vary_device"`
	VaryCountry     bool   `json:"vary_country"`
	VaryCookie      string `json:"vary_cookie"`
	VaryQueryString bool   `json:"vary_querystring"`
	VaryHeaders     string `json:"vary_headers"`
	QueryWhitelist  string `json:"query_whitelist"`
	StripCookies    bool   `json:"strip_cookies"`
	LargeObject     bool   `json:"large_object"`
	StaleOffline    bool   `json:"stale_offline"`
	StaleUpdating   bool   `json:"stale_updating"`
}

// buildAgentCache returns the wire cache settings for a zone, or nil when the zone is
// at defaults (so a default zone ships nothing — byte-identical agent-config + ETag).
func buildAgentCache(c store.CacheSettings) *agentCacheSettings {
	if c == store.DefaultCacheSettings() {
		return nil
	}
	return &agentCacheSettings{
		Smart: c.Smart, EdgeMode: c.EdgeMode, EdgeTTL: c.EdgeTTL,
		BrowserMode: c.BrowserMode, BrowserTTL: c.BrowserTTL, QuerySort: c.QuerySort,
		CacheErrors: c.CacheErrors, VaryWebp: c.VaryWebp, VaryDevice: c.VaryDevice,
		VaryCountry: c.VaryCountry, VaryCookie: c.VaryCookie, VaryQueryString: c.VaryQueryString,
		VaryHeaders: c.VaryHeaders, QueryWhitelist: c.QueryWhitelist, StripCookies: c.StripCookies,
		LargeObject: c.LargeObject, StaleOffline: c.StaleOffline, StaleUpdating: c.StaleUpdating,
	}
}

// agentHeaderTransform is one header rule shipped to the edge (Lua-enforced).
type agentHeaderTransform struct {
	Priority   int32  `json:"priority"`
	Phase      string `json:"phase"` // request | response
	Op         string `json:"op"`    // set | remove
	Header     string `json:"header"`
	Value      string `json:"value,omitempty"`
	MatchType  string `json:"match_type"` // all | path_prefix | path_regex | method
	MatchValue string `json:"match_value,omitempty"`
}

// agentWAF is the per-zone WAF config shipped to the edge. The edge expands
// wp_preset locally (synthetic custom rules + a /wp-login.php rate limit) so the
// payload stays small and the dashboard is one-click.
type agentWAF struct {
	Enabled        bool             `json:"enabled"`
	Mode           string           `json:"mode"`            // detect | block
	ManagedRuleset string           `json:"managed_ruleset"` // off | owasp_crs
	Paranoia       int32            `json:"paranoia"`        // CRS paranoia 1..4
	FailOpen       bool             `json:"fail_open"`       // engine error -> open (true) vs closed
	WpPreset       bool             `json:"wp_preset"`
	Rules          []agentWAFRule   `json:"rules"`
	RateLimits     []agentRateLimit `json:"rate_limits"`
}

type agentWAFRule struct {
	ID         int64  `json:"id"`
	Priority   int32  `json:"priority"`
	Field      string `json:"field"`
	Op         string `json:"op"`
	Value      string `json:"value"`
	HeaderName string `json:"header_name,omitempty"`
	Action     string `json:"action"`
	Enabled    bool   `json:"enabled"`
}

type agentRateLimit struct {
	ID            int64  `json:"id"`
	PathMatch     string `json:"path_match"`
	MatchType     string `json:"match_type"`
	Requests      int32  `json:"requests"`
	PeriodSeconds int32  `json:"period_seconds"`
	Burst         int32  `json:"burst"`
	Key           string `json:"key"`
	Action        string `json:"action"`
	CountMode     string `json:"count_mode"`
	Enabled       bool   `json:"enabled"`
}

type agentConfigResp struct {
	ConfigVersion string      `json:"config_version"`
	Zones         []agentZone `json:"zones"`
}

// agentConfig returns the zones assigned to the calling server with an ETag.
// Honors If-None-Match (conditional GET) -> 304 when nothing changed.
func (a *API) agentConfig(w http.ResponseWriter, r *http.Request) {
	serverID, ok := auth.ServerIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	zones, err := a.store.ListServerZones(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	az := toAgentZones(zones)

	// Origin shield (Phase 4 Step 3): compute each real zone's upstream for THIS
	// server. A shielded zone on a normal edge proxies to the shield PoP; the shield
	// itself, a self-reference, a non-shield target, or a dead shield all fall back
	// to the origin (health/role/loop guards in shieldUpstreamFor). Indices align
	// with `zones` (custom-domain synthetic zones appended below stay origin-direct).
	servers, _ := a.store.ListServers(r.Context())
	byID := make(map[int64]store.Server, len(servers))
	for _, s := range servers {
		byID[s.ID] = s
	}
	me := byID[serverID]
	for i := range zones {
		az[i].ShieldUpstream = shieldUpstreamFor(zones[i], me, byID, a.cfg.DefaultShieldServerID)
	}

	// WAF (Phase 4 Step 4): attach each WAF-enabled zone's security config (managed
	// CRS knobs + custom rules + rate limits). config_version (already folded into
	// the ETag) bumps on any WAF change, so this re-pulls without extra ETag wiring.
	// Indices align with `zones`; custom-domain synthetic zones inherit their
	// parent's WAF below (same site, different served hostname).
	wafByZone := make(map[int64]*agentWAF, len(zones))
	for i := range zones {
		if w := a.buildAgentWAF(r.Context(), zones[i]); w != nil {
			az[i].WAF = w
			wafByZone[zones[i].ID] = w
		}
	}

	// Header transforms (Phase 4 Step 5): attach each zone's transforms (the edge
	// Lua layer enforces them). config_version (already in the ETag) bumps on any
	// change. Custom-domain synthetic zones inherit their parent's transforms below.
	transformsByZone := make(map[int64][]agentHeaderTransform, len(zones))
	for i := range zones {
		if ts := a.buildAgentTransforms(r.Context(), zones[i].ID); len(ts) > 0 {
			az[i].HeaderTransforms = ts
			transformsByZone[zones[i].ID] = ts
		}
	}

	// Custom domains (Phase 4 Step 2): each ACTIVE custom domain whose parent zone
	// is assigned to this edge renders as its OWN server block — server_name = the
	// custom domain, same origin/settings as the parent zone, tls_mode="managed".
	// We append them as synthetic agent zones so they flow through the exact same
	// rendering + managed-cert path as real zones (one cert pair per block; SNI
	// selects by hostname). The Step-1 default_server health fix is untouched.
	customs, _ := a.store.ListActiveCustomDomainsForServer(r.Context(), serverID)
	if len(customs) > 0 {
		byZone := make(map[int64]store.Zone, len(zones))
		for _, z := range zones {
			byZone[z.ID] = z
		}
		for _, cd := range customs {
			parent, ok := byZone[cd.ZoneID]
			if !ok {
				continue // parent not assigned here -> nothing to serve
			}
			caz := customDomainAgentZone(cd, parent)
			caz.WAF = wafByZone[cd.ZoneID]                       // inherit the parent zone's WAF (same site)
			caz.HeaderTransforms = transformsByZone[cd.ZoneID]   // inherit header transforms too
			az = append(az, caz)
		}
	}

	// Managed TLS: attach cert material to any zone in tls_mode="managed" that a
	// control-plane-issued cert covers — the managed wildcard for real zones, and
	// the per-domain cert (name = the domain) for custom domains. Optional: a missed
	// lookup just leaves the edge on its current cert (never fatal, never drops TLS).
	certs, _ := a.store.ListTLSCerts(r.Context())
	attachManagedCerts(az, certs)

	etag := configETag(zones, az)
	quoted := `"` + etag + `"`
	w.Header().Set("ETag", quoted)
	if match := r.Header.Get("If-None-Match"); match == quoted {
		w.WriteHeader(http.StatusNotModified) // unchanged -> tiny 304, no body
		return
	}
	writeJSON(w, http.StatusOK, agentConfigResp{ConfigVersion: etag, Zones: az})
}

// configETag hashes the assigned zones' (id, config_version) tuples and every
// rendered vhost's (hostname, cert serial). It changes when a zone is edited
// (config_version bumps), the assignment set changes, a managed cert is renewed
// (serial changes), OR a custom domain is added/removed/activated/renewed (the az
// set + serials change, and the manager also bumps the parent's config_version) —
// so any of those triggers an agent pull + cert write without a manual zone edit.
// az is deterministically ordered (real zones by id, then custom domains by
// zone+domain), so the hash is stable across identical states.
func configETag(zones []store.Zone, az []agentZone) string {
	h := sha256.New()
	for _, z := range zones { // already ordered by zone id (deterministic)
		fmt.Fprintf(h, "z:%d:%d;", z.ID, z.ConfigVersion)
	}
	for _, a := range az { // every rendered server block (incl. custom domains)
		// ShieldUpstream is folded in so a shield health/role/config flip (which
		// changes the computed upstream without a zone edit) bumps the ETag => re-pull.
		fmt.Fprintf(h, "h:%s:%s:%s;", a.CDNHostname, a.TLSCertSerial, a.ShieldUpstream)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// shieldUpstreamFor computes the origin-shield upstream (host:port) a given server
// should use for a zone, or "" to proxy the origin directly. It enforces every
// guard from the prompt: shield opt-in, never shield through self, the shield PoP
// goes to origin, the target must be a role=shield server, and a dead shield
// (offline / drained / health=unhealthy) degrades gracefully to direct origin.
func shieldUpstreamFor(z store.Zone, me store.Server, byID map[int64]store.Server, defaultShieldID int64) string {
	if !z.OriginShieldEnabled {
		return "" // shield off -> origin (today's behavior)
	}
	if strings.EqualFold(me.Role, "shield") {
		return "" // the shield PoP itself always pulls the real origin
	}
	shieldID := defaultShieldID
	if z.ShieldServerID != nil {
		shieldID = *z.ShieldServerID
	}
	if shieldID == 0 || shieldID == me.ID {
		return "" // no shield resolved, or would shield through itself (loop guard)
	}
	shield, ok := byID[shieldID]
	if !ok || !strings.EqualFold(shield.Role, "shield") {
		return "" // dangling / target isn't a shield -> skip (no loop, no misroute)
	}
	if !shieldServing(shield) {
		return "" // dead shield -> direct origin (degrade, never blackhole)
	}
	host := shield.IP
	if shield.Hostname != nil && strings.TrimSpace(*shield.Hostname) != "" {
		host = strings.TrimSpace(*shield.Hostname)
	}
	if host == "" {
		return ""
	}
	return host + ":443"
}

// shieldServing reports whether a shield PoP is fit to carry origin pulls: online,
// not drained, and not health-failed (unknown/healthy both count as serving).
func shieldServing(s store.Server) bool {
	return strings.EqualFold(s.Status, "online") && !s.Drained && !strings.EqualFold(s.HealthStatus, "unhealthy")
}

// buildAgentWAF returns the per-zone WAF config for the edge, or nil when WAF is
// off for the zone (so the edge renders no auth_request and pays zero overhead).
// Disabled rules/limits are filtered out here so the edge only sees live config.
func (a *API) buildAgentWAF(ctx context.Context, z store.Zone) *agentWAF {
	if !z.WAFEnabled {
		return nil
	}
	w := &agentWAF{
		Enabled:        true,
		Mode:           z.WAFMode,
		ManagedRuleset: z.WAFManagedRuleset,
		Paranoia:       z.WAFParanoia,
		FailOpen:       z.WAFFailOpen,
		WpPreset:       z.WAFWpPreset,
		Rules:          []agentWAFRule{},
		RateLimits:     []agentRateLimit{},
	}
	if rules, err := a.store.ListWAFRules(ctx, z.ID); err == nil {
		for _, ru := range rules {
			if !ru.Enabled {
				continue
			}
			hn := ""
			if ru.HeaderName != nil {
				hn = *ru.HeaderName
			}
			w.Rules = append(w.Rules, agentWAFRule{
				ID: ru.ID, Priority: ru.Priority, Field: ru.Field, Op: ru.Op,
				Value: ru.Value, HeaderName: hn, Action: ru.Action, Enabled: true,
			})
		}
	}
	if limits, err := a.store.ListWAFRateLimits(ctx, z.ID); err == nil {
		for _, rl := range limits {
			if !rl.Enabled {
				continue
			}
			w.RateLimits = append(w.RateLimits, agentRateLimit{
				ID: rl.ID, PathMatch: rl.PathMatch, MatchType: rl.MatchType,
				Requests: rl.Requests, PeriodSeconds: rl.PeriodSeconds, Burst: rl.Burst,
				Key: rl.Key, Action: rl.Action, CountMode: rl.CountMode, Enabled: true,
			})
		}
	}
	return w
}

// buildAgentTransforms returns a zone's enabled header transforms for the edge,
// or nil when none (so a zone without transforms ships nothing).
func (a *API) buildAgentTransforms(ctx context.Context, zoneID int64) []agentHeaderTransform {
	ts, err := a.store.ListHeaderTransforms(ctx, zoneID)
	if err != nil || len(ts) == 0 {
		return nil
	}
	out := make([]agentHeaderTransform, 0, len(ts))
	for _, t := range ts {
		if !t.Enabled {
			continue
		}
		val := ""
		if t.Value != nil {
			val = *t.Value
		}
		mv := ""
		if t.MatchValue != nil {
			mv = *t.MatchValue
		}
		out = append(out, agentHeaderTransform{
			Priority: t.Priority, Phase: t.Phase, Op: t.Op, Header: t.Header,
			Value: val, MatchType: t.MatchType, MatchValue: mv,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// customDomainAgentZone builds the synthetic agent zone for an active custom
// domain: server_name = the domain, all delivery settings inherited from the
// parent zone, tls_mode="managed" so the per-domain cert (attached later) is
// written + served via SNI. Cache is $host-isolated by the template, same as zones.
func customDomainAgentZone(cd store.CustomDomain, parent store.Zone) agentZone {
	az := agentZone{
		CDNHostname: cd.Domain,
		OriginURL:   parent.OriginURL,
		HostHeader:  parent.HostHeader,
		TLSMode:     "managed",
		Video:       parent.Video,
		Profile:     parent.Profile,
		PlaylistTTL: parent.PlaylistTTL,
		SegmentTTL:  parent.SegmentTTL,
		CorsOrigin:  parent.CorsOrigin,
		BrotliLevel: parent.BrotliLevel,
		Status:      parent.Status,
		CacheRules:  []agentRule{},
		Cache:       buildAgentCache(parent.Cache),   // custom domain inherits the parent zone's cache settings
		Hotlink:     buildAgentHotlink(parent),       // ...and the parent zone's hotlink protection (same site)
		OriginSSLVerify:       parent.OriginSSLVerify, // ...and the parent zone's origin options
		OriginFollowRedirects: parent.OriginFollowRedirects,
		ForwardHostHeader:     parent.ForwardHostHeader,
		Error5xxHTML:          parent.Error5xxHTML,    // ...and the parent zone's custom 502/504 page
		BlockedIPs:            parent.BlockedIPs,      // ...and the parent zone's Blocked-IP denylist
		BlockRootPath:         parent.BlockRootPath,   // ...and the parent zone's access toggles
		BlockPost:             parent.BlockPost,
	}
	for _, ru := range parent.Rules {
		az.CacheRules = append(az.CacheRules, agentRule{
			Priority: ru.Priority, MatchType: ru.MatchType, MatchValue: ru.MatchValue,
			Action: ru.Action, ActionValue: ru.ActionValue,
		})
	}
	return az
}

// attachManagedCerts fills TLSCert/TLSKey/TLSCertSerial for each zone in
// tls_mode="managed" whose served hostname is covered by a managed cert's SANs.
func attachManagedCerts(zones []agentZone, certs []store.TLSCert) {
	if len(certs) == 0 {
		return
	}
	for i := range zones {
		if zones[i].TLSMode != "managed" {
			continue
		}
		host := zones[i].CDNHostname
		if zones[i].CustomDomain != "" {
			host = zones[i].CustomDomain
		}
		for _, c := range certs {
			if certCovers(strings.Split(c.Domains, ","), host) {
				zones[i].TLSCert = c.FullChain
				zones[i].TLSKey = c.PrivKey
				zones[i].TLSCertSerial = c.Serial
				break
			}
		}
	}
}

// certCovers reports whether any SAN in domains covers host. A wildcard "*.base"
// matches exactly one extra left-hand label (the TLS rule), e.g. "*.a2zjav.com"
// covers "cdn.a2zjav.com" but not "a.b.a2zjav.com" nor the bare "a2zjav.com".
func certCovers(domains []string, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == host {
			return true
		}
		if base, ok := strings.CutPrefix(d, "*."); ok {
			if sub, ok := strings.CutSuffix(host, "."+base); ok && sub != "" && !strings.Contains(sub, ".") {
				return true
			}
		}
	}
	return false
}

func toAgentZones(zones []store.Zone) []agentZone {
	out := make([]agentZone, 0, len(zones))
	for _, z := range zones {
		az := agentZone{
			CDNHostname: z.CDNHostname,
			OriginURL:   z.OriginURL,
			HostHeader:  z.HostHeader,
			TLSMode:     z.TLSMode,
			Video:       z.Video,
			Profile:     z.Profile,
			PlaylistTTL: z.PlaylistTTL,
			SegmentTTL:  z.SegmentTTL,
			CorsOrigin:  z.CorsOrigin,
			BrotliLevel: z.BrotliLevel,
			Status:      z.Status,
			CacheRules:  []agentRule{},
		}
		if z.CustomDomain != nil {
			az.CustomDomain = *z.CustomDomain
		}
		for _, ru := range z.Rules {
			az.CacheRules = append(az.CacheRules, agentRule{
				Priority: ru.Priority, MatchType: ru.MatchType, MatchValue: ru.MatchValue,
				Action: ru.Action, ActionValue: ru.ActionValue,
			})
		}
		az.Cache = buildAgentCache(z.Cache)       // nil when default => byte-identical payload
		az.Hotlink = buildAgentHotlink(z)         // nil when off => byte-identical payload
		az.OriginSSLVerify = z.OriginSSLVerify    // false => omitted => byte-identical payload
		az.OriginFollowRedirects = z.OriginFollowRedirects
		az.ForwardHostHeader = z.ForwardHostHeader
		az.Error5xxHTML = z.Error5xxHTML          // "" => omitted => byte-identical payload
		az.BlockedIPs = z.BlockedIPs              // "" => omitted => byte-identical payload
		az.BlockRootPath = z.BlockRootPath        // false => omitted => byte-identical payload
		az.BlockPost = z.BlockPost
		out = append(out, az)
	}
	return out
}
