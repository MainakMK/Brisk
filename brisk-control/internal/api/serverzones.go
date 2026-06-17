package api

import (
	"errors"
	"net/http"

	"brisk-control/internal/store"
)

type assignZoneInput struct {
	ZoneID int64 `json:"zone_id" validate:"required"`
}

// listServerZones returns the zones assigned to a server.
func (a *API) listServerZones(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, err := a.store.GetServer(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	zones, err := a.store.ListServerZones(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, zones)
}

// assignZone maps a zone to a server.
func (a *API) assignZone(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	var in assignZoneInput
	if !decode(w, r, &in) {
		return
	}
	if err := a.store.AssignZone(r.Context(), id, in.ZoneID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "server or zone not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// server_zones drives BOTH layers (Part 2.5): bump config_version so the edge
	// renders this zone's vhost on next pull, and trigger a DNS reconcile so the
	// edge IP joins the zone's per-zone Smart A-set.
	_ = a.store.BumpZoneConfigVersion(r.Context(), in.ZoneID)
	a.triggerDNSReconcile("zone_assign")
	writeJSON(w, http.StatusCreated, map[string]any{"server_id": id, "zone_id": in.ZoneID})
}

// unassignZone removes a zone↔server mapping.
func (a *API) unassignZone(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	zoneID, ok := idParam(w, r, "zoneId")
	if !ok {
		return
	}
	if err := a.store.UnassignZone(r.Context(), id, zoneID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "assignment not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// PoP toggle OFF (Part 2.5): bump config_version so the edge drops this zone's
	// vhost on next pull, and trigger a DNS reconcile so the edge IP leaves the
	// zone's per-zone Smart A-set. Keeps server_zones ↔ vhost ↔ DNS in lockstep.
	_ = a.store.BumpZoneConfigVersion(r.Context(), zoneID)
	a.triggerDNSReconcile("zone_unassign")
	w.WriteHeader(http.StatusNoContent)
}
