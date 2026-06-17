package nginx

import (
	"strconv"
	"strings"

	"brisk-agent/config"
)

// Per-zone Cache Settings rendering (Bunny-style controls). The single source of
// truth for how the dashboard toggles become nginx directives on the static-asset
// and dynamic-HTML locations. EVERY default here reproduces today's hard-coded
// behavior, so a zone with nil/default settings renders functionally identical nginx
// (same proxy_cache_valid / use_stale / key / Cache-Control) — the live fleet is
// unaffected until a tenant changes a control. Video locations keep their own
// profile-driven slice/coalescing behavior (untouched).

// Today's literal directive values — the defaults the controls fall back to.
const (
	defStaticKey    = "$host$uri"
	defHTMLKey      = "$host$request_uri"
	defStaticValid  = "200 301 302 30d"
	defHTMLValid    = "200 301 302 10m"
	defStale        = "error timeout updating http_500 http_502 http_503 http_504"
	defStaticIgnore = "Set-Cookie Cache-Control Expires Vary"
	defStaticCC     = "public, max-age=2592000"
)

// cacheRender holds the render-ready directive values for a zone's static + html
// locations. The template substitutes these where it used to hard-code the literals.
type cacheRender struct {
	StaticKey       string // proxy_cache_key on the static location
	HTMLKey         string // proxy_cache_key on the html location
	StaticValid     string // proxy_cache_valid 200-line args ("" => omit, honor origin)
	HTMLValid       string // same for html ("" => omit)
	Stale           string // proxy_cache_use_stale args ("off" => disable)
	StaticIgnore    string // proxy_ignore_headers args on static
	StaticBrowserCC string // Cache-Control sent to the browser on static ("" => omit)
	HTMLBrowserCC   string // Cache-Control sent to the browser on html ("" => omit)
	CacheErrors     bool   // add proxy_cache_valid 500 502 503 504 5s (shield origin from retry storms)
	HTMLIgnore      string // proxy_ignore_headers args on html ("" => no line, today's default)
	NoCacheStatic   bool   // edge no_cache: bypass cache on static
	NoCacheHTML     bool   // edge no_cache: bypass cache on html
	Slice           bool   // large-object: slice the static location (byte-range)
	// http-context map needs (aggregated across zones in renderData).
	NeedWebpMap   bool
	NeedDeviceMap bool
}

// defaultCacheRender is today's behavior — used for a zone with no Cache Settings.
func defaultCacheRender() cacheRender {
	return cacheRender{
		StaticKey:       defStaticKey,
		HTMLKey:         defHTMLKey,
		StaticValid:     defStaticValid,
		HTMLValid:       defHTMLValid,
		Stale:           defStale,
		StaticIgnore:    defStaticIgnore,
		StaticBrowserCC: defStaticCC,
		HTMLBrowserCC:   "",
	}
}

// cacheRenderFor maps a zone's Cache Settings to render-ready directives. nil => the
// exact current behavior. NOTE: query_sort + query_whitelist are persisted/shipped
// but enforced by the Lua edge (cache-key normalization needs Lua); they are no-ops
// here and surfaced in the dashboard with that dependency, like GeoIP for country.
func cacheRenderFor(cs *config.ZoneCacheSettings) cacheRender {
	cr := defaultCacheRender()
	if cs == nil {
		return cr
	}

	// ---- Vary Cache: compose extra dimensions into the cache key (native nginx
	// variables only; $host stays first so tenant isolation is never lost). ----
	var ss, hs strings.Builder
	add := func(v string) { ss.WriteString(v); hs.WriteString(v) }
	if cs.VaryWebp {
		cr.NeedWebpMap = true
		add("$brisk_webp")
	}
	if cs.VaryDevice {
		cr.NeedDeviceMap = true
		add("$brisk_device")
	}
	if cs.VaryCountry {
		add("$brisk_country") // GeoIP var (already defined; "-" without the DB)
	}
	if c := sanitizeToken(cs.VaryCookie); c != "" {
		add("$cookie_" + c)
	}
	for _, h := range splitList(cs.VaryHeaders) {
		add("$http_" + headerVar(h))
	}
	if cs.VaryQueryString {
		ss.WriteString("$is_args$args") // html already keys $request_uri (incl. query)
	}
	cr.StaticKey = defStaticKey + ss.String()
	cr.HTMLKey = defHTMLKey + hs.String()

	// ---- Edge (proxy) cache expiration ----
	switch cs.EdgeMode {
	case "respect_origin":
		cr.StaticValid = "" // omit proxy_cache_valid 200 -> honor origin Cache-Control
		cr.HTMLValid = ""
		cr.StaticIgnore = "Set-Cookie Vary" // stop ignoring Cache-Control/Expires
	case "override":
		if t := strings.TrimSpace(cs.EdgeTTL); t != "" {
			cr.StaticValid = "200 301 302 " + t
			cr.HTMLValid = "200 301 302 " + t
		}
	case "no_cache":
		cr.NoCacheStatic = true
		cr.NoCacheHTML = true
	}

	// ---- Smart Cache: cache the dynamic location like a static asset (long TTL,
	// cookie-agnostic) so a whole site accelerates regardless of origin Cache-Control.
	// Strip Set-Cookie: make a cookie-setting origin cacheable on html (Smart implies it
	// and also ignores origin Cache-Control/Expires). ----
	switch {
	case cs.Smart:
		cr.HTMLValid = cr.StaticValid
		cr.HTMLIgnore = "Set-Cookie Cache-Control Expires"
	case cs.StripCookies:
		cr.HTMLIgnore = "Set-Cookie"
	}

	cr.CacheErrors = cs.CacheErrors
	cr.Stale = staleFlags(cs.StaleOffline, cs.StaleUpdating)

	// ---- Large object: slice the static location for byte-range/video delivery.
	// Slicing requires $slice_range in the key + 206 in the valid set. ----
	if cs.LargeObject {
		cr.Slice = true
		cr.StaticKey += "$slice_range"
		if cr.StaticValid != "" {
			cr.StaticValid = with206(cr.StaticValid)
		}
	}

	// ---- Browser cache expiration (Cache-Control to the client) ----
	cr.StaticBrowserCC = browserCC(cs, defStaticCC, cr.StaticValid)
	cr.HTMLBrowserCC = browserCC(cs, "", cr.HTMLValid)

	return cr
}

// staleFlags builds the proxy_cache_use_stale argument set. Both on => today's full
// set (byte-identical). Either off narrows it; both off => "off" (no stale serving).
func staleFlags(offline, updating bool) string {
	if offline && updating {
		return defStale
	}
	var parts []string
	if offline {
		parts = append(parts, "error", "timeout")
	}
	if updating {
		parts = append(parts, "updating")
	}
	if offline {
		parts = append(parts, "http_500", "http_502", "http_503", "http_504")
	}
	if len(parts) == 0 {
		return "off"
	}
	return strings.Join(parts, " ")
}

// browserCC computes the Cache-Control value for a location. fallback is the current
// default ("public, max-age=2592000" on static, "" on html). edgeValid is the matching
// location's proxy_cache_valid (for match_server). Empty => the template omits the line.
func browserCC(cs *config.ZoneCacheSettings, fallback, edgeValid string) string {
	switch cs.BrowserMode {
	case "no_cache":
		return "no-store, no-cache, must-revalidate"
	case "override":
		if s := cacheTTLToSeconds(strings.TrimSpace(cs.BrowserTTL)); s > 0 {
			return "public, max-age=" + strconv.Itoa(s)
		}
		return fallback
	case "match_server":
		if s := cacheTTLToSeconds(lastToken(edgeValid)); s > 0 {
			return "public, max-age=" + strconv.Itoa(s)
		}
		return fallback
	default: // "default"
		return fallback
	}
}

// with206 inserts 206 after 200 in a proxy_cache_valid status list (for slicing).
func with206(valid string) string {
	if strings.Contains(" "+valid+" ", " 206 ") {
		return valid
	}
	return strings.Replace(valid, "200", "200 206", 1)
}

// lastToken returns the final space-separated token (the TTL in a valid list).
func lastToken(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

// ttlSeconds converts an nginx time (e.g. "30d","1h","90s","45m") to seconds. 0 on
// a value it can't parse (caller falls back).
func cacheTTLToSeconds(t string) int {
	t = strings.TrimSpace(t)
	if t == "" {
		return 0
	}
	unit := t[len(t)-1]
	mult := 1
	num := t
	switch unit {
	case 's':
		mult, num = 1, t[:len(t)-1]
	case 'm':
		mult, num = 60, t[:len(t)-1]
	case 'h':
		mult, num = 3600, t[:len(t)-1]
	case 'd':
		mult, num = 86400, t[:len(t)-1]
	case 'w':
		mult, num = 604800, t[:len(t)-1]
	case 'M':
		mult, num = 2592000, t[:len(t)-1]
	case 'y':
		mult, num = 31536000, t[:len(t)-1]
	default:
		if unit < '0' || unit > '9' {
			return 0 // unknown unit
		}
	}
	n, err := strconv.Atoi(num)
	if err != nil || n < 0 {
		return 0
	}
	return n * mult
}

// sanitizeToken keeps only the safe name chars (letters, digits, -, _) — for a cookie
// name folded into the cache key as $cookie_<name>.
func sanitizeToken(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// headerVar turns a header name into the nginx $http_ suffix (lowercase, - -> _).
func headerVar(h string) string {
	return strings.ToLower(strings.ReplaceAll(sanitizeToken(h), "-", "_"))
}

// splitList splits a comma-separated list, trimming + dropping blanks.
func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
