package api

import (
	"net/http"
	"regexp"
	"strings"

	"brisk-control/internal/store"
)

// cacheSettingsInput is the body for PUT /zones/{id}/cache-settings. All fields are
// required (the dashboard sends the full set); validation keeps the agent render safe.
type cacheSettingsInput struct {
	Smart           bool   `json:"smart"`
	EdgeMode        string `json:"edge_mode" validate:"required,oneof=default respect_origin override no_cache"`
	EdgeTTL         string `json:"edge_ttl"`
	BrowserMode     string `json:"browser_mode" validate:"required,oneof=default match_server override no_cache"`
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

// nginxTime accepts an nginx time value: digits + optional unit (ms s m h d w M y).
var nginxTime = regexp.MustCompile(`^[0-9]+(ms|[smhdwMy])?$`)

// tokenName guards cookie/header/arg names folded into the cache key or directives —
// letters, digits, dash, underscore only (no spaces/newlines that could break config).
var tokenName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// setZoneCacheSettings replaces a zone's Cache Settings (Bunny-style controls) and
// bumps config_version so the zone's edges re-pull + reload. Tenant-scoped. Defaults
// reproduce current behavior, so this never changes a zone the tenant didn't touch.
func (a *API) setZoneCacheSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, ok := a.scopeZone(w, r, id); !ok {
		return
	}
	var in cacheSettingsInput
	if !decode(w, r, &in) {
		return
	}

	// TTL required + nginx-valid when the matching mode is "override".
	if in.EdgeMode == "override" && !nginxTime.MatchString(strings.TrimSpace(in.EdgeTTL)) {
		writeError(w, http.StatusBadRequest, "edge_ttl must be an nginx time (e.g. 30s, 1h, 7d) when edge_mode=override")
		return
	}
	if in.BrowserMode == "override" && !nginxTime.MatchString(strings.TrimSpace(in.BrowserTTL)) {
		writeError(w, http.StatusBadRequest, "browser_ttl must be an nginx time (e.g. 1h, 30d) when browser_mode=override")
		return
	}
	if c := strings.TrimSpace(in.VaryCookie); c != "" && !tokenName.MatchString(c) {
		writeError(w, http.StatusBadRequest, "vary_cookie must be a single cookie name (letters, digits, -, _)")
		return
	}
	headers, herr := cleanTokenList(in.VaryHeaders)
	if herr != "" {
		writeError(w, http.StatusBadRequest, "vary_headers: "+herr)
		return
	}
	whitelist, werr := cleanTokenList(in.QueryWhitelist)
	if werr != "" {
		writeError(w, http.StatusBadRequest, "query_whitelist: "+werr)
		return
	}

	cs := store.CacheSettings{
		Smart:           in.Smart,
		EdgeMode:        in.EdgeMode,
		EdgeTTL:         strings.TrimSpace(in.EdgeTTL),
		BrowserMode:     in.BrowserMode,
		BrowserTTL:      strings.TrimSpace(in.BrowserTTL),
		QuerySort:       in.QuerySort,
		CacheErrors:     in.CacheErrors,
		VaryWebp:        in.VaryWebp,
		VaryDevice:      in.VaryDevice,
		VaryCountry:     in.VaryCountry,
		VaryCookie:      strings.TrimSpace(in.VaryCookie),
		VaryQueryString: in.VaryQueryString,
		VaryHeaders:     headers,
		QueryWhitelist:  whitelist,
		StripCookies:    in.StripCookies,
		LargeObject:     in.LargeObject,
		StaleOffline:    in.StaleOffline,
		StaleUpdating:   in.StaleUpdating,
	}
	z, err := a.store.SetZoneCacheSettings(r.Context(), id, cs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, z)
}

// cleanTokenList normalizes a comma-separated list of names: trims, drops blanks,
// validates each against tokenName, and rejoins with ",". Returns ("", errmsg) on a
// bad token. An empty/blank input is valid (returns "").
func cleanTokenList(raw string) (string, string) {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !tokenName.MatchString(p) {
			return "", "invalid name " + p + " (letters, digits, -, _ only)"
		}
		out = append(out, p)
	}
	return strings.Join(out, ","), ""
}
