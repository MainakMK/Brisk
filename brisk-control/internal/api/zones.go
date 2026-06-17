package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"brisk-control/internal/identity"
	"brisk-control/internal/purge"
	"brisk-control/internal/store"
)

// scopeZone fetches a zone and enforces tenant access via the identity chokepoint:
// admin may touch any zone; a customer only its own account's. Returns ok=false
// (and writes 404/403) when missing or not permitted. The portal-safe gate.
func (a *API) scopeZone(w http.ResponseWriter, r *http.Request, id int64) (store.Zone, bool) {
	z, err := a.store.GetZone(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "zone not found")
		return store.Zone{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return store.Zone{}, false
	}
	cid, _ := identity.FromContext(r.Context())
	if identity.Authorize(cid, z.AccountID) != nil {
		writeError(w, http.StatusForbidden, "forbidden")
		return store.Zone{}, false
	}
	return z, true
}

// --- zones ---

type createZoneInput struct {
	Name         string  `json:"name" validate:"required"`
	CDNHostname  string  `json:"cdn_hostname" validate:"required,hostname"`
	CustomDomain *string `json:"custom_domain"`
	OriginURL    string  `json:"origin_url" validate:"required,url"`
	HostHeader   *string `json:"host_header" validate:"omitempty,hostname"`
	TLSMode      *string `json:"tls_mode" validate:"omitempty,oneof=selfsigned mkcert letsencrypt managed"`
	Video        *bool   `json:"video"`
	Profile      *string `json:"profile" validate:"omitempty,oneof=vod live"`
	PlaylistTTL  *string `json:"playlist_ttl"`
	SegmentTTL   *string `json:"segment_ttl"`
	CorsOrigin   *string `json:"cors_origin"`
	BrotliLevel  *int32  `json:"brotli_level" validate:"omitempty,min=1,max=11"`
	// AssignAll (Step 4.7 Part 1): auto-assign the new zone to every online+in-rotation
	// edge on create (default true). A future customer-portal flow can opt out.
	AssignAll *bool `json:"assign_all"`
}

type updateZoneInput struct {
	Name         *string `json:"name"`
	CustomDomain *string `json:"custom_domain"`
	OriginURL    *string `json:"origin_url" validate:"omitempty,url"`
	HostHeader   *string `json:"host_header" validate:"omitempty,hostname"`
	TLSMode      *string `json:"tls_mode" validate:"omitempty,oneof=selfsigned mkcert letsencrypt managed"`
	Video        *bool   `json:"video"`
	Profile      *string `json:"profile" validate:"omitempty,oneof=vod live"`
	PlaylistTTL  *string `json:"playlist_ttl"`
	SegmentTTL   *string `json:"segment_ttl"`
	CorsOrigin   *string `json:"cors_origin"`
	BrotliLevel  *int32  `json:"brotli_level" validate:"omitempty,min=1,max=11"`
	Status       *string `json:"status"`

	// Origin options (migration 00025). Field order MUST match store.UpdateZoneParams —
	// updateZone() does a direct struct conversion store.UpdateZoneParams(in).
	OriginSSLVerify       *bool `json:"origin_ssl_verify"`
	OriginFollowRedirects *bool `json:"origin_follow_redirects"`
	ForwardHostHeader     *bool `json:"forward_host_header"`
}

func (a *API) listZones(w http.ResponseWriter, r *http.Request) {
	zones, err := a.store.ListZones(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Tenant scoping: admin sees all; a customer sees only its own account's zones.
	cid, _ := identity.FromContext(r.Context())
	if !cid.IsAdmin() {
		scoped := zones[:0:0]
		for _, z := range zones {
			if cid.CanAccessAccount(z.AccountID) {
				scoped = append(scoped, z)
			}
		}
		zones = scoped
	}
	writeJSON(w, http.StatusOK, zones)
}

func (a *API) createZone(w http.ResponseWriter, r *http.Request) {
	var in createZoneInput
	if !decode(w, r, &in) {
		return
	}
	// TLS realignment: Automatic SSL is the only certificate path. Default to
	// `managed` (control-plane lego Bunny DNS-01 wildcard, fanned to edges, auto
	// renewed) and reject the broken/dev modes with a clear, user-facing message
	// rather than silently rewriting them.
	if mode := strOr(in.TLSMode, "managed"); mode != "managed" {
		writeError(w, http.StatusUnprocessableEntity,
			"Only Automatic SSL is supported (tls_mode=managed). Per-edge Let's Encrypt, self-signed, and mkcert aren't available for zones.")
		return
	}
	// The hostname must be covered by a managed wildcard cert (*.cdn.a2zjav.com /
	// *.a2zjav.com, incl. apex SAN). An external/custom domain can't be auto-issued
	// yet — that's the verified Custom-Domains flow (Step 4.8).
	if !a.hostCoveredByManagedCert(r.Context(), in.CDNHostname) {
		writeError(w, http.StatusUnprocessableEntity,
			"Custom external domains aren't supported yet (Step 4.8). Onboard under *.cdn.a2zjav.com.")
		return
	}
	z, err := a.store.CreateZone(r.Context(), store.CreateZoneParams{
		Name:         in.Name,
		CDNHostname:  in.CDNHostname,
		CustomDomain: in.CustomDomain,
		OriginURL:    in.OriginURL,
		HostHeader:   strOr(in.HostHeader, ""),
		TLSMode:      "managed",
		Video:        boolOr(in.Video, false),
		Profile:      strOr(in.Profile, "vod"),
		PlaylistTTL:  strOr(in.PlaylistTTL, "2s"),
		SegmentTTL:   strOr(in.SegmentTTL, "12h"),
		CorsOrigin:   strOr(in.CorsOrigin, "*"),
		BrotliLevel:  i32Or(in.BrotliLevel, 5),
	})
	if err != nil {
		if store.IsUniqueViolation(err) {
			writeError(w, http.StatusConflict, "cdn_hostname already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Auto-assign (Step 4.7 Part 1): a new zone goes live everywhere with zero manual
	// steps — assign it to every online + in-rotation edge (skip drained/offline/unhealthy),
	// then bump config_version once so those edges pull + render it on the next poll.
	// Idempotent (AssignZone is upsert). Opt out with assign_all=false.
	if boolOr(in.AssignAll, true) {
		if z2 := a.autoAssignZone(r.Context(), z.ID); z2.ID != 0 {
			z = z2
		}
	}
	writeJSON(w, http.StatusCreated, z)
}

// hostCoveredByManagedCert reports whether host is covered by a managed wildcard
// cert already issued by the control plane (Automatic SSL). Reuses certCovers (the
// Part-3 covering helper) over every cert in the store, so a `<x>.cdn.a2zjav.com`
// host matches `*.cdn.a2zjav.com` and `cdn.a2zjav.com` matches the apex `*.a2zjav.com`.
// A host no managed cert covers is an external/custom domain we can't auto-issue for
// yet (Step 4.8) — the caller rejects it. Fails closed if certs can't be listed.
func (a *API) hostCoveredByManagedCert(ctx context.Context, host string) bool {
	certs, err := a.store.ListTLSCerts(ctx)
	if err != nil {
		return false
	}
	for _, c := range certs {
		if certCovers(strings.Split(c.Domains, ","), host) {
			return true
		}
	}
	return false
}

// autoAssignZone assigns the zone to every serving edge (online, not drained, healthy)
// and bumps config_version if at least one new assignment landed. Returns the updated
// zone (or a zero Zone if nothing changed / on error — caller keeps the original).
func (a *API) autoAssignZone(ctx context.Context, zoneID int64) store.Zone {
	servers, err := a.store.ListServers(ctx)
	if err != nil {
		return store.Zone{}
	}
	assigned := 0
	for _, s := range servers {
		if serverServing(s) {
			if err := a.store.AssignZone(ctx, s.ID, zoneID); err == nil {
				assigned++
			}
		}
	}
	if assigned == 0 {
		return store.Zone{}
	}
	if err := a.store.BumpZoneConfigVersion(ctx, zoneID); err != nil {
		return store.Zone{}
	}
	z, err := a.store.GetZone(ctx, zoneID)
	if err != nil {
		return store.Zone{}
	}
	return z
}

// autoAssignAllZonesToServer assigns every active zone to a newly-online edge and
// bumps config_version only on zones that gained the assignment (so a re-online edge
// doesn't churn config_version on already-assigned zones). Best-effort (Part 1).
func (a *API) autoAssignAllZonesToServer(ctx context.Context, serverID int64) {
	zones, err := a.store.ListActiveZones(ctx)
	if err != nil {
		return
	}
	for _, z := range zones {
		existing, _ := a.store.ServersForZone(ctx, z.ID)
		already := false
		for _, s := range existing {
			if s.ID == serverID {
				already = true
				break
			}
		}
		if already {
			continue
		}
		if err := a.store.AssignZone(ctx, serverID, z.ID); err == nil {
			_ = a.store.BumpZoneConfigVersion(ctx, z.ID)
		}
	}
}

func (a *API) getZone(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	rules, err := a.store.ListRules(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	z.Rules = rules
	writeJSON(w, http.StatusOK, z)
}

func (a *API) updateZone(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, ok := a.scopeZone(w, r, id); !ok {
		return
	}
	var in updateZoneInput
	if !decode(w, r, &in) {
		return
	}
	// TLS realignment: Automatic SSL only. An explicit non-managed tls_mode is
	// rejected (don't silently rewrite); omitting tls_mode leaves the zone unchanged.
	if in.TLSMode != nil && *in.TLSMode != "managed" {
		writeError(w, http.StatusUnprocessableEntity,
			"Only Automatic SSL is supported (tls_mode=managed). Per-edge Let's Encrypt, self-signed, and mkcert aren't available for zones.")
		return
	}
	z, err := a.store.UpdateZone(r.Context(), id, store.UpdateZoneParams(in))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "zone not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, z)
}

// deleteZone removes a zone AND tears it down across every edge serving it: a
// whole-zone purge fans out over NATS (cache cleared in ms-seconds) and the vhost
// drops on the next config pull (the zone leaves ListServerZones, so the agent's
// config ETag changes -> the agent re-renders without the server block, ~15s). Net
// effect: the deleted zone stops serving (no stale HITs) across all PoPs in ~20-30s.
//
// Accidental-delete guard (the cdn.a2zjav.com incident): a zone that is serving on
// any live/in-rotation edge requires type-the-hostname confirmation — the caller
// must pass the exact cdn_hostname (?confirm=<hostname> or JSON body {"confirm":...}),
// enforced HERE on the server so a stray DELETE can't nuke a live site.
func (a *API) deleteZone(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}

	// Capture the serving edges BEFORE deletion — server_zones cascades on DELETE,
	// so afterwards there is no way to know which PoPs to purge.
	servers, err := a.store.ServersForZone(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	live := 0
	for _, s := range servers {
		if serverServing(s) { // online, not drained, not health-failed
			live++
		}
	}

	// Guard: a live/in-rotation zone needs the exact hostname to confirm. 412 with a
	// machine-readable shape so the dashboard can pop the type-the-hostname modal.
	if live > 0 && deleteConfirmValue(r) != z.CDNHostname {
		writeJSON(w, http.StatusPreconditionFailed, map[string]any{
			"error":                 "confirmation required: zone is serving on live edges",
			"confirmation_required": true,
			"hostname":              z.CDNHostname,
			"live_edges":            live,
		})
		return
	}

	// Whole-zone purge over NATS (instant) BEFORE the row goes away, so the purge
	// job keeps its zone_id for audit. Cache clear and DB delete are independent on
	// the edge (the file purger keys off $host, not nginx config), so order is safe.
	if a.pub != nil && len(servers) > 0 {
		hosts := zoneHosts(z)
		if job, jerr := a.store.CreatePurgeJob(r.Context(), &id, "zone", "/", len(servers)); jerr == nil {
			a.publishToServers(r.Context(), servers, purge.Message{
				Type: "zone", Hosts: hosts, Target: "/", JobID: job.ID, ZoneID: id,
			})
		}
		// Audit (who/when) on each affected edge's provision log — the existing,
		// dashboard-visible audit surface. The purge_jobs row records it too.
		cid, _ := identity.FromContext(r.Context())
		actor := fmt.Sprintf("account %d (%s)", cid.AccountID, cid.Role)
		for _, s := range servers {
			_ = a.store.AddProvisionLog(r.Context(), s.ID, "info",
				fmt.Sprintf("zone %q (id %d) deleted by %s — whole-zone cache purged; vhost drops on next config pull", z.CDNHostname, id, actor))
		}
	}

	if err := a.store.DeleteZone(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "zone not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serverServing reports whether an edge is live/in-rotation: online, not drained,
// not health-failed (unknown/healthy both count). Mirrors shieldServing's fitness
// test; named here for the delete-guard's "is this zone live" question.
func serverServing(s store.Server) bool {
	return strings.EqualFold(s.Status, "online") && !s.Drained && !strings.EqualFold(s.HealthStatus, "unhealthy")
}

// deleteConfirmValue reads the type-the-hostname confirmation from the request —
// a ?confirm= query param (the dashboard's path) or a JSON body {"confirm":"..."}.
func deleteConfirmValue(r *http.Request) string {
	if v := strings.TrimSpace(r.URL.Query().Get("confirm")); v != "" {
		return v
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	_ = decodeOptional(r, &body)
	return strings.TrimSpace(body.Confirm)
}

// --- cache rules (nested under a zone) ---

type createRuleInput struct {
	Priority    int32   `json:"priority"`
	MatchType   string  `json:"match_type" validate:"required,oneof=path_prefix extension regex"`
	MatchValue  string  `json:"match_value" validate:"required"`
	Action      string  `json:"action" validate:"required,oneof=override_cache_ttl bypass_cache force_download redirect"`
	ActionValue *string `json:"action_value"`
}

func (a *API) listRules(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, err := a.store.GetZone(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "zone not found")
		return
	}
	rules, err := a.store.ListRules(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

func (a *API) createRule(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	var in createRuleInput
	if !decode(w, r, &in) {
		return
	}
	rule, err := a.store.CreateRule(r.Context(), id, store.CreateRuleParams{
		Priority: in.Priority, MatchType: in.MatchType, MatchValue: in.MatchValue,
		Action: in.Action, ActionValue: in.ActionValue,
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

// updateRule edits a rule in place (atomic, no ID churn) — Phase 4 Step 6 backlog.
func (a *API) updateRule(w http.ResponseWriter, r *http.Request) {
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
	var in createRuleInput
	if !decode(w, r, &in) {
		return
	}
	rule, err := a.store.UpdateRule(r.Context(), id, rid, store.CreateRuleParams{
		Priority: in.Priority, MatchType: in.MatchType, MatchValue: in.MatchValue,
		Action: in.Action, ActionValue: in.ActionValue,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "rule not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

type reorderRulesInput struct {
	RuleIDs []int64 `json:"rule_ids" validate:"required,min=1,dive,gt=0"`
}

// reorderRules sets rule priorities to the given order, atomically (no delete+recreate).
func (a *API) reorderRules(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, ok := a.scopeZone(w, r, id); !ok {
		return
	}
	var in reorderRulesInput
	if !decode(w, r, &in) {
		return
	}
	if err := a.store.ReorderRules(r.Context(), id, in.RuleIDs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "a rule_id does not belong to this zone")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rules, err := a.store.ListRules(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

// listZoneServers returns the servers serving a zone — the inverse lookup.
func (a *API) listZoneServers(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, ok := a.scopeZone(w, r, id); !ok {
		return
	}
	servers, err := a.store.ListZoneServers(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, servers)
}

func (a *API) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	rid, ok := idParam(w, r, "rid")
	if !ok {
		return
	}
	if err := a.store.DeleteRule(r.Context(), id, rid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "rule not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// small default helpers
func strOr(p *string, def string) string {
	if p != nil {
		return *p
	}
	return def
}
func boolOr(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}
func i32Or(p *int32, def int32) int32 {
	if p != nil {
		return *p
	}
	return def
}
