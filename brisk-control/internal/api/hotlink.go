package api

import (
	"net/http"
	"strings"

	"brisk-control/internal/store"
)

// hotlinkInput is the body for PUT /zones/{id}/hotlink. The dashboard sends the full
// set. allowed_referrers is a comma-separated host list (validated below) — empty is
// allowed (with allow_empty_referer that means only same-host/empty referers pass).
type hotlinkInput struct {
	Enabled           bool   `json:"enabled"`
	AllowedReferrers  string `json:"allowed_referrers"`
	AllowEmptyReferer bool   `json:"allow_empty_referer"`
}

// setZoneHotlink replaces a zone's hotlink protection (Referer allowlist) and bumps
// config_version so the zone's edges re-pull + reload. Tenant-scoped. Off by default,
// so this never changes a zone the tenant didn't touch.
//
// Security: allowed_referrers is rendered into an nginx `valid_referers` directive on
// the edge, so each host is strictly validated here — only [A-Za-z0-9 . * -] is
// permitted, blocking any character (space, ;, {, }, newline, quote) that could break
// out of the directive. Scheme + path are stripped so a pasted URL still works.
func (a *API) setZoneHotlink(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, ok := a.scopeZone(w, r, id); !ok {
		return
	}
	var in hotlinkInput
	if !decode(w, r, &in) {
		return
	}

	clean, bad := cleanRefererList(in.AllowedReferrers)
	if bad != "" {
		writeError(w, http.StatusBadRequest, "allowed_referrers: "+bad)
		return
	}
	// Enabling with no referers AND blocking empty would reject essentially all
	// traffic except same-host — almost certainly a mistake. Guard against it.
	if in.Enabled && clean == "" && !in.AllowEmptyReferer {
		writeError(w, http.StatusBadRequest,
			"add at least one allowed referrer, or allow empty referrers — otherwise nearly all requests would be blocked")
		return
	}

	z, err := a.store.SetZoneHotlink(r.Context(), id, in.Enabled, clean, in.AllowEmptyReferer)
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

// cleanRefererList normalizes a comma-separated referer host list: per entry it strips
// any scheme + path + port, lowercases, and validates the remaining host (optionally a
// *.x / x.* wildcard) against a strict charset. Returns ("", errmsg) on a bad host.
// An empty/blank input is valid (returns "").
func cleanRefererList(raw string) (string, string) {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		h := normalizeRefererHost(p)
		if h == "" {
			continue
		}
		if !validRefererHost(h) {
			return "", "invalid referrer " + p + " (use a hostname like example.com or *.example.com)"
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return strings.Join(out, ","), ""
}

// normalizeRefererHost strips scheme, path, and port from a pasted referer and
// lowercases it, so "https://Example.com/page" -> "example.com".
func normalizeRefererHost(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// strip scheme
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// strip path/query/fragment
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	// strip port
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// validRefererHost enforces a strict charset so the host is safe to drop into the
// nginx valid_referers directive: only letters, digits, dot, dash, and an optional
// single leading "*." or trailing ".*" wildcard. No spaces/;/{}/quotes/newlines.
func validRefererHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	core := h
	core = strings.TrimPrefix(core, "*.")
	core = strings.TrimSuffix(core, ".*")
	if core == "" {
		return false
	}
	for _, c := range core {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '-':
			// ok
		default:
			return false
		}
	}
	// must contain at least one alphanumeric label char (not just dots/dashes)
	return strings.ContainsAny(core, "abcdefghijklmnopqrstuvwxyz0123456789")
}
