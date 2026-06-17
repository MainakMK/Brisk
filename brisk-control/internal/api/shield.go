package api

import (
	"errors"
	"net/http"
	"strings"

	"brisk-control/internal/store"
)

// --- Origin Shield (Phase 4 Step 3): per-zone toggle + server role ---

type setZoneShieldInput struct {
	Enabled        bool   `json:"enabled"`
	ShieldServerID *int64 `json:"shield_server_id"` // null => network default shield
}

// setZoneShield enables/disables origin shield for a zone (tenant-scoped). When
// enabling with an explicit shield PoP, that server must be role=shield (else 400).
// Bumps config_version so the zone's edges re-pull and switch upstream — live-safe
// (a config change over the poll interval; nginx is validated before reload).
func (a *API) setZoneShield(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	var in setZoneShieldInput
	if !decode(w, r, &in) {
		return
	}

	if in.Enabled && in.ShieldServerID != nil {
		s, err := a.store.GetServer(r.Context(), *in.ShieldServerID)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "shield_server_id: server not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !strings.EqualFold(s.Role, "shield") {
			writeError(w, http.StatusBadRequest, "shield_server_id must reference a role=shield server")
			return
		}
	}

	updated, err := a.store.SetZoneShield(r.Context(), z.ID, in.Enabled, in.ShieldServerID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "zone not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

type setServerRoleInput struct {
	Role string `json:"role" validate:"required,oneof=edge shield"`
	// Hybrid Shield+PoP (Build Spec Layer 4): when role=shield, serve_public=true keeps
	// the box in the public geo DNS set AND acting as the parent (a hybrid PoP); false is
	// a pure shield (excluded from DNS). Optional/nil => leave the flag as-is. For
	// role=edge it's ignored at render/DNS time (edges are always public).
	ServePublic *bool `json:"serve_public"`
}

// setServerRole sets a server's role ('edge' | 'shield') and optionally serve_public
// (Hybrid Shield+PoP). Admin-only. A pure shield is excluded from the geo DNS set, a
// hybrid (shield + serve_public) stays in it — so a role/serve_public change triggers a
// DNS reconcile (add/remove the A record). Marking a live edge as a PURE shield pulls it
// from rotation — the caller is responsible for capacity (the dashboard warns).
func (a *API) setServerRole(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	var in setServerRoleInput
	if !decode(w, r, &in) {
		return
	}
	role := strings.ToLower(strings.TrimSpace(in.Role))
	srv, err := a.store.SetServerRole(r.Context(), id, role, in.ServePublic)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Role changes DNS membership (shields are not in the geo set) — converge now.
	a.triggerDNSReconcile("server_role")
	writeJSON(w, http.StatusOK, srv)
}
