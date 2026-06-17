package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"brisk-control/internal/dns"
	"brisk-control/internal/store"

	"github.com/go-chi/chi/v5"
)

// reconcileNow applies DNS changes synchronously (so a drain/undrain takes effect
// immediately, not on the debounce). Falls back to a debounced trigger if the
// synchronous reconcile errors. The DB drain flag is the source of truth either way.
func (a *API) reconcileNow(ctx context.Context, reason string) {
	if a.sync == nil {
		return
	}
	if _, err := a.sync.Reconcile(ctx, false, reason); err != nil {
		a.triggerDNSReconcile(reason)
	}
}

// endpointsFor builds the rotation-relevant view of all servers, optionally
// overriding the drained flag for a set of server ids (to simulate "what if I
// drain these?").
func endpointsFor(servers []store.Server, overrideDrained map[int64]bool) []dns.Endpoint {
	eps := make([]dns.Endpoint, 0, len(servers))
	for _, s := range servers {
		drained := s.Drained
		if overrideDrained != nil {
			if v, ok := overrideDrained[s.ID]; ok {
				drained = v
			}
		}
		eps = append(eps, dns.Endpoint{
			EdgeID: s.EdgeID, Region: s.Region, IP: s.IP, Status: s.Status, LastSeen: s.LastSeen,
			Health: s.HealthStatus, Drained: drained,
		})
	}
	return eps
}

func (a *API) inRotationCount(servers []store.Server, overrideDrained map[int64]bool) int {
	dec := dns.RotationDecision(endpointsFor(servers, overrideDrained), time.Now().UTC(), a.staleAfter())
	n := 0
	for _, d := range dec {
		if d.InRotation {
			n++
		}
	}
	return n
}

// auditDrain records a drain/undrain action in the DNS audit trail.
func (a *API) auditDrain(ctx context.Context, s store.Server, action, reason string) {
	if reason == "" {
		reason = action
	}
	_ = a.store.AddDNSAudit(ctx, store.DNSAudit{
		Action: action, EdgeID: s.EdgeID, RecordName: a.cfg.BriskCDNRecord, Value: s.IP, Reason: reason,
	})
}

type drainInput struct {
	Reason string `json:"reason"`
	Force  bool   `json:"force"` // override the all-PoP "would empty the pool" guard
}

// drainServer pulls one PoP out of rotation (record Disabled=true, box keeps
// serving). Guards against draining the LAST in-rotation PoP unless force=true.
func (a *API) drainServer(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, err := a.store.GetServer(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "server not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var in drainInput
	_ = decodeOptional(r, &in)

	servers, err := a.store.ListServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	before := a.inRotationCount(servers, nil)
	after := a.inRotationCount(servers, map[int64]bool{id: true})
	if before > 0 && after == 0 && !in.Force {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":          "draining this PoP would empty the rotation pool — no edge would be in rotation",
			"would_empty":    true,
			"requires_force": true,
			"in_rotation":    before,
		})
		return
	}

	updated, err := a.store.SetServerDrain(r.Context(), id, true, strings.TrimSpace(in.Reason))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.auditDrain(r.Context(), updated, "drain", strings.TrimSpace(in.Reason))
	a.reconcileNow(r.Context(), "drain")
	writeJSON(w, http.StatusOK, map[string]any{"server": updated, "in_rotation_after": after})
}

// undrainServer returns a PoP to HEALTH-governed rotation. It does NOT force a
// sick box back in: if it's still unhealthy it stays out (as "unhealthy", not
// "drained").
func (a *API) undrainServer(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	updated, err := a.store.SetServerDrain(r.Context(), id, false, "")
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.auditDrain(r.Context(), updated, "undrain", "resume")
	a.reconcileNow(r.Context(), "undrain")
	writeJSON(w, http.StatusOK, map[string]any{"server": updated})
}

// drainRegion drains every PoP in a region (bulk maintenance). Same all-PoP guard.
func (a *API) drainRegion(w http.ResponseWriter, r *http.Request) {
	region := strings.TrimSpace(chi.URLParam(r, "region"))
	if region == "" {
		writeError(w, http.StatusBadRequest, "region is required")
		return
	}
	var in drainInput
	_ = decodeOptional(r, &in)

	servers, err := a.store.ListServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ids := map[int64]bool{}
	for _, s := range servers {
		if strings.EqualFold(s.Region, region) {
			ids[s.ID] = true
		}
	}
	if len(ids) == 0 {
		writeError(w, http.StatusNotFound, "no servers in region "+region)
		return
	}
	before := a.inRotationCount(servers, nil)
	after := a.inRotationCount(servers, ids)
	if before > 0 && after == 0 && !in.Force {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":          "draining region " + region + " would empty the rotation pool",
			"would_empty":    true,
			"requires_force": true,
			"in_rotation":    before,
		})
		return
	}

	affected, err := a.store.DrainRegion(r.Context(), region, true, strings.TrimSpace(in.Reason))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, s := range affected {
		a.auditDrain(r.Context(), s, "drain", "region:"+region)
	}
	a.reconcileNow(r.Context(), "region_drain")
	writeJSON(w, http.StatusOK, map[string]any{"region": region, "drained": len(affected), "servers": affected})
}

// undrainRegion resumes every PoP in a region.
func (a *API) undrainRegion(w http.ResponseWriter, r *http.Request) {
	region := strings.TrimSpace(chi.URLParam(r, "region"))
	if region == "" {
		writeError(w, http.StatusBadRequest, "region is required")
		return
	}
	affected, err := a.store.DrainRegion(r.Context(), region, false, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(affected) == 0 {
		writeError(w, http.StatusNotFound, "no servers in region "+region)
		return
	}
	for _, s := range affected {
		a.auditDrain(r.Context(), s, "undrain", "region:"+region)
	}
	a.reconcileNow(r.Context(), "region_undrain")
	writeJSON(w, http.StatusOK, map[string]any{"region": region, "resumed": len(affected), "servers": affected})
}

// serverRotation returns one server's effective rotation state + reason.
func (a *API) serverRotation(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	srv, err := a.store.GetServer(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	servers, err := a.store.ListServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dec := dns.RotationDecision(endpointsFor(servers, nil), time.Now().UTC(), a.staleAfter())
	d := dec[srv.EdgeID]
	writeJSON(w, http.StatusOK, map[string]any{
		"edge_id":      srv.EdgeID,
		"in_rotation":  d.InRotation,
		"reason":       d.Reason,
		"drained":      srv.Drained,
		"drain_reason": srv.DrainReason,
		"health":       srv.HealthStatus,
		"status":       srv.Status,
	})
}
