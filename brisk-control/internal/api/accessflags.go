package api

import (
	"net/http"

	"brisk-control/internal/store"
)

// accessFlagsInput is the body for PUT /zones/{id}/access-flags. The dashboard sends the
// full set. Both default false (off) => byte-identical edge config.
type accessFlagsInput struct {
	BlockRootPath bool `json:"block_root_path"`
	BlockPost     bool `json:"block_post"`
}

// setZoneAccessFlags replaces a zone's access toggles (block-root-path / block-POST) and
// bumps config_version so the zone's edges re-pull + reload. Tenant-scoped. Off by default.
func (a *API) setZoneAccessFlags(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, ok := a.scopeZone(w, r, id); !ok {
		return
	}
	var in accessFlagsInput
	if !decode(w, r, &in) {
		return
	}

	z, err := a.store.SetZoneAccessFlags(r.Context(), id, in.BlockRootPath, in.BlockPost)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "zone not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, z)
}
