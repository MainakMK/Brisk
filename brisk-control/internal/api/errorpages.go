package api

import (
	"net/http"
	"unicode/utf8"

	"brisk-control/internal/store"
)

// maxErrorPageBytes caps the custom 502/504 page so a tenant can't ship a huge blob
// to every edge (it's written to disk + served on each origin-down hit). 64 KiB is far
// more than any branded "we'll be right back" page needs.
const maxErrorPageBytes = 64 * 1024

// errorPageInput is the body for PUT /zones/{id}/error-page. `html` is the branded page;
// EMPTY clears it (back to nginx's default 502/504 — the byte-identical off state).
type errorPageInput struct {
	HTML string `json:"html"`
}

// setZoneErrorPage replaces a zone's custom 502/504 error page and bumps config_version
// so the zone's edges re-pull + reload. Tenant-scoped. Off by default (empty), so this
// never changes a zone the tenant didn't touch.
//
// The HTML is served verbatim by the edge as an INTERNAL static file (via error_page
// 502 504 -> an alias'd file); it is never interpolated into the nginx config, so there
// is no directive-injection surface. We only bound its size + require valid UTF-8.
func (a *API) setZoneErrorPage(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, ok := a.scopeZone(w, r, id); !ok {
		return
	}
	var in errorPageInput
	if !decode(w, r, &in) {
		return
	}
	if len(in.HTML) > maxErrorPageBytes {
		writeError(w, http.StatusBadRequest, "error page HTML too large (max 64 KB)")
		return
	}
	if !utf8.ValidString(in.HTML) {
		writeError(w, http.StatusBadRequest, "error page HTML must be valid UTF-8")
		return
	}

	z, err := a.store.SetZoneErrorPage(r.Context(), id, in.HTML)
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
