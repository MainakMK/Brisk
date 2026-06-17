package api

import (
	"errors"
	"net/http"
	"strings"

	"brisk-control/internal/store"
)

// Phase 4 Step 5 — per-zone header-transform API (request/response add/remove/set),
// enforced at the edge by Lua. Tenant-scoped via scopeZone; each change bumps the
// zone's config_version (store) so edges re-pull + reload. A managed-header
// deny-list stops a tenant clobbering Brisk-managed / TLS / internal headers.

// managedHeaderDenied reports whether a transform may NOT target this header for
// the given phase. Brisk owns X-Brisk-*, the Server header, HSTS, and the hop/
// framing headers; the upstream Host is per-zone (multi-tenant routing). Enforced
// here AND in the edge Lua (defense in depth).
func managedHeaderDenied(phase, header string) bool {
	h := strings.ToLower(strings.TrimSpace(header))
	if strings.HasPrefix(h, "x-brisk-") {
		return true
	}
	switch h {
	case "content-length", "transfer-encoding", "connection", "":
		return true
	}
	if phase == "response" {
		switch h {
		case "server", "strict-transport-security", "content-encoding", "date":
			return true
		}
	}
	if phase == "request" {
		switch h {
		case "host", "x-forwarded-proto", "x-forwarded-for":
			return true
		}
	}
	return false
}

type createHeaderTransformInput struct {
	Priority   int32   `json:"priority"`
	Phase      string  `json:"phase" validate:"required,oneof=request response"`
	Op         string  `json:"op" validate:"required,oneof=set remove"`
	Header     string  `json:"header" validate:"required"`
	Value      *string `json:"value"`
	MatchType  string  `json:"match_type" validate:"omitempty,oneof=all path_prefix path_regex method"`
	MatchValue *string `json:"match_value"`
	Enabled    *bool   `json:"enabled"`
}

func (a *API) listHeaderTransforms(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	ts, err := a.store.ListHeaderTransforms(r.Context(), z.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ts)
}

func (a *API) createHeaderTransform(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	var in createHeaderTransformInput
	if !decode(w, r, &in) {
		return
	}
	if managedHeaderDenied(in.Phase, in.Header) {
		writeError(w, http.StatusBadRequest,
			"header is managed by Brisk and cannot be overridden (X-Brisk-*, Server, HSTS, Host, framing headers)")
		return
	}
	if in.Op == "set" && (in.Value == nil || strings.TrimSpace(*in.Value) == "") {
		writeError(w, http.StatusBadRequest, "value is required when op=set")
		return
	}
	if (in.MatchType == "path_prefix" || in.MatchType == "path_regex" || in.MatchType == "method") &&
		(in.MatchValue == nil || strings.TrimSpace(*in.MatchValue) == "") {
		writeError(w, http.StatusBadRequest, "match_value is required for this match_type")
		return
	}
	t, err := a.store.CreateHeaderTransform(r.Context(), z.ID, store.CreateHeaderTransformParams{
		Priority:   in.Priority,
		Phase:      in.Phase,
		Op:         in.Op,
		Header:     strings.TrimSpace(in.Header),
		Value:      in.Value,
		MatchType:  strOr(&in.MatchType, "all"),
		MatchValue: in.MatchValue,
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
	writeJSON(w, http.StatusCreated, t)
}

func (a *API) deleteHeaderTransform(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, ok := a.scopeZone(w, r, id); !ok {
		return
	}
	tid, ok := idParam(w, r, "tid")
	if !ok {
		return
	}
	if err := a.store.DeleteHeaderTransform(r.Context(), id, tid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "transform not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
