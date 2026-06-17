package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"brisk-control/internal/auth"
	"brisk-control/internal/store"
)

// Phase 4 Step 4 — per-zone WAF + rate limiting API. All zone-scoped endpoints go
// through scopeZone (tenant RBAC: a customer manages only its own zones; admin
// all). WAF config changes bump the zone's config_version (store layer) so the
// zone's edges re-pull + reload. The agent ingest endpoint is bearer-auth.

// --- WAF settings (GET/PUT /zones/{id}/waf) ---

// wafConfigResp is the consolidated per-zone security config the dashboard reads.
type wafConfigResp struct {
	store.WAFSettings
	Rules      []store.WAFCustomRule `json:"rules"`
	RateLimits []store.WAFRateLimit  `json:"rate_limits"`
}

// wafSettingsOf extracts the WAF knobs from a zone row.
func wafSettingsOf(z store.Zone) store.WAFSettings {
	return store.WAFSettings{
		Enabled:        z.WAFEnabled,
		Mode:           z.WAFMode,
		ManagedRuleset: z.WAFManagedRuleset,
		Paranoia:       z.WAFParanoia,
		WpPreset:       z.WAFWpPreset,
		FailOpen:       z.WAFFailOpen,
	}
}

// getZoneWAF returns a zone's WAF settings + custom rules + rate limits.
func (a *API) getZoneWAF(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	rules, err := a.store.ListWAFRules(r.Context(), z.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	limits, err := a.store.ListWAFRateLimits(r.Context(), z.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, wafConfigResp{WAFSettings: wafSettingsOf(z), Rules: rules, RateLimits: limits})
}

type setZoneWAFInput struct {
	Enabled        bool   `json:"enabled"`
	Mode           string `json:"mode" validate:"required,oneof=detect block"`
	ManagedRuleset string `json:"managed_ruleset" validate:"required,oneof=off owasp_crs"`
	Paranoia       int32  `json:"paranoia" validate:"min=1,max=4"`
	WpPreset       bool   `json:"wp_preset"`
	FailOpen       bool   `json:"fail_open"`
}

// setZoneWAF updates a zone's WAF settings (tenant-scoped). Bumps config_version
// (store) so the zone's edges re-pull. Enabling for the first time should default
// to detect mode — the dashboard does that; the API accepts whatever is sent.
func (a *API) setZoneWAF(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	var in setZoneWAFInput
	if !decode(w, r, &in) {
		return
	}
	updated, err := a.store.SetZoneWAF(r.Context(), z.ID, store.WAFSettings{
		Enabled:        in.Enabled,
		Mode:           in.Mode,
		ManagedRuleset: in.ManagedRuleset,
		Paranoia:       in.Paranoia,
		WpPreset:       in.WpPreset,
		FailOpen:       in.FailOpen,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "zone not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, wafSettingsOf(updated))
}

// --- custom rules (GET/POST/DELETE /zones/{id}/waf/rules) ---

type createWAFRuleInput struct {
	Priority   int32   `json:"priority"`
	Field      string  `json:"field" validate:"required,oneof=ip country path method header user_agent"`
	Op         string  `json:"op" validate:"required,oneof=eq prefix regex cidr"`
	Value      string  `json:"value" validate:"required"`
	HeaderName *string `json:"header_name"`
	Action     string  `json:"action" validate:"required,oneof=block challenge log allow"`
	Enabled    *bool   `json:"enabled"`
}

func (a *API) listWAFRules(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	rules, err := a.store.ListWAFRules(r.Context(), z.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (a *API) createWAFRule(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	var in createWAFRuleInput
	if !decode(w, r, &in) {
		return
	}
	// header rules need a header name.
	if in.Field == "header" && (in.HeaderName == nil || strings.TrimSpace(*in.HeaderName) == "") {
		writeError(w, http.StatusBadRequest, "header_name is required when field=header")
		return
	}
	rule, err := a.store.CreateWAFRule(r.Context(), z.ID, store.CreateWAFRuleParams{
		Priority:   in.Priority,
		Field:      in.Field,
		Op:         in.Op,
		Value:      in.Value,
		HeaderName: in.HeaderName,
		Action:     in.Action,
		Enabled:    boolOr(in.Enabled, true),
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "zone not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (a *API) deleteWAFRule(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, ok := a.scopeZone(w, r, id); !ok {
		return
	}
	rid, ok := idParam(w, r, "rid")
	if !ok {
		return
	}
	if err := a.store.DeleteWAFRule(r.Context(), id, rid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- rate limits (GET/POST/DELETE /zones/{id}/waf/ratelimits) ---

type createWAFRateLimitInput struct {
	PathMatch     string `json:"path_match" validate:"required"`
	MatchType     string `json:"match_type" validate:"omitempty,oneof=exact prefix"`
	Requests      int32  `json:"requests" validate:"required,min=1"`
	PeriodSeconds int32  `json:"period_seconds" validate:"required,min=1"`
	Burst         int32  `json:"burst" validate:"min=0"`
	Key           string `json:"key" validate:"omitempty,oneof=ip ip_path"`
	Action        string `json:"action" validate:"omitempty,oneof=block challenge"`
	CountMode     string `json:"count_mode" validate:"omitempty,oneof=all errors_only"`
	Enabled       *bool  `json:"enabled"`
}

func (a *API) listWAFRateLimits(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	limits, err := a.store.ListWAFRateLimits(r.Context(), z.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, limits)
}

func (a *API) createWAFRateLimit(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	var in createWAFRateLimitInput
	if !decode(w, r, &in) {
		return
	}
	if !strings.HasPrefix(in.PathMatch, "/") {
		writeError(w, http.StatusBadRequest, "path_match must start with /")
		return
	}
	limit, err := a.store.CreateWAFRateLimit(r.Context(), z.ID, store.CreateWAFRateLimitParams{
		PathMatch:     in.PathMatch,
		MatchType:     strOr(&in.MatchType, "exact"),
		Requests:      in.Requests,
		PeriodSeconds: in.PeriodSeconds,
		Burst:         in.Burst,
		Key:           strOr(&in.Key, "ip"),
		Action:        strOr(&in.Action, "block"),
		CountMode:     strOr(&in.CountMode, "all"),
		Enabled:       boolOr(in.Enabled, true),
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "zone not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, limit)
}

func (a *API) deleteWAFRateLimit(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, ok := a.scopeZone(w, r, id); !ok {
		return
	}
	rlID, ok := idParam(w, r, "rid")
	if !ok {
		return
	}
	if err := a.store.DeleteWAFRateLimit(r.Context(), id, rlID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "rate limit not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- security events (firewall log) ---

// eventWindow parses ?from&to (RFC3339), defaulting to the last 24h.
func eventWindow(r *http.Request) (from, to time.Time) {
	to = time.Now().UTC()
	from = to.Add(-24 * time.Hour)
	q := r.URL.Query()
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	return from, to
}

// listZoneSecurityEvents returns a zone's firewall log (tenant-scoped).
func (a *API) listZoneSecurityEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	from, to := eventWindow(r)
	events, err := a.store.QuerySecurityEvents(r.Context(), store.SecurityEventFilter{
		ZoneIDs: []int64{z.ID},
		From:    from,
		To:      to,
		Action:  r.URL.Query().Get("action"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "from": from, "to": to})
}

// adminSecurityEvents returns the firewall log across all tenants (admin only).
// Optional ?zone_id filter; otherwise every zone.
func (a *API) adminSecurityEvents(w http.ResponseWriter, r *http.Request) {
	from, to := eventWindow(r)
	f := store.SecurityEventFilter{From: from, To: to, Action: r.URL.Query().Get("action")}
	if zs := r.URL.Query().Get("zone_id"); zs != "" {
		zid, ok := parseInt64(w, zs, "zone_id")
		if !ok {
			return
		}
		f.ZoneIDs = []int64{zid}
	}
	events, err := a.store.QuerySecurityEvents(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "from": from, "to": to})
}

// adminSecuritySummary returns the cross-tenant overview (top attacked zones, top
// blocked IPs) for the admin Security dashboard.
func (a *API) adminSecuritySummary(w http.ResponseWriter, r *http.Request) {
	from, to := eventWindow(r)
	sum, err := a.store.SecurityEventSummary(r.Context(), from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// --- agent ingest (POST /agent/security-events) ---

// ingestSecurityEvent mirrors the agent's shipped firewall-log JSON.
type ingestSecurityEvent struct {
	TS       time.Time `json:"ts"`
	Zone     string    `json:"zone"` // served host -> resolved to zone_id
	ClientIP string    `json:"client_ip"`
	Country  string    `json:"country"`
	RuleID   string    `json:"rule_id"`
	RuleType string    `json:"rule_type"`
	Action   string    `json:"action"`
	Mode     string    `json:"mode"`
	Path     string    `json:"path"`
	Method   string    `json:"method"`
	UA       string    `json:"ua"`
	Message  string    `json:"message"`
}

// securityEventsIngest accepts a JSON array of firewall-log events from an
// authenticated agent and bulk-inserts them. The zone is resolved from the served
// host (like statsIngest); unknown hosts are dropped (never stored unattributed).
func (a *API) securityEventsIngest(w http.ResponseWriter, r *http.Request) {
	serverID, ok := auth.ServerIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in []ingestSecurityEvent
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(in) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	hostToID := map[string]int64{}
	if zones, err := a.store.ListServerZones(r.Context(), serverID); err == nil {
		for _, z := range zones {
			hostToID[z.CDNHostname] = z.ID
			if z.CustomDomain != nil && *z.CustomDomain != "" {
				hostToID[*z.CustomDomain] = z.ID
			}
		}
	}
	// edge_id for the firewall log (best-effort; empty if the server row is gone).
	edgeID := ""
	if s, err := a.store.GetServer(r.Context(), serverID); err == nil {
		edgeID = s.EdgeID
	}

	now := time.Now().UTC()
	events := make([]store.SecurityEvent, 0, len(in))
	for _, e := range in {
		ts := e.TS
		if ts.IsZero() {
			ts = now
		}
		var zoneID *int64
		if e.Zone != "" {
			if zid, ok := hostToID[e.Zone]; ok {
				zoneID = &zid
			} else {
				continue // unknown host -> drop
			}
		}
		events = append(events, store.SecurityEvent{
			TS: ts, ZoneID: zoneID, ClientIP: e.ClientIP, Country: e.Country,
			RuleID: e.RuleID, RuleType: e.RuleType, Action: e.Action, Mode: e.Mode,
			Path: e.Path, Method: e.Method, UA: e.UA, Message: e.Message,
		})
	}
	if len(events) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := a.store.InsertSecurityEvents(r.Context(), serverID, edgeID, events); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"inserted": len(events)})
}
