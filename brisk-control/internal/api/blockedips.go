package api

import (
	"net"
	"net/http"
	"strings"

	"brisk-control/internal/store"
)

// maxBlockedIPs caps how many entries a tenant can block (each renders a `deny` line on
// every content location). Far more than any sane denylist; a huge list would bloat the
// config + slow nginx's per-request access check.
const maxBlockedIPs = 1000

// blockedIPsInput is the body for PUT /zones/{id}/blocked-ips. `blocked_ips` is a
// comma/newline-separated list of IPs or CIDRs; EMPTY clears it (off, byte-identical).
type blockedIPsInput struct {
	BlockedIPs string `json:"blocked_ips"`
}

// setZoneBlockedIPs replaces a zone's Blocked-IP denylist and bumps config_version so
// the zone's edges re-pull + reload. Tenant-scoped. Off by default (empty).
//
// Security: each entry is rendered into an nginx `deny <x>;` directive on the edge, so
// it is STRICTLY validated here — only well-formed IPv4/IPv6 addresses or CIDR blocks
// pass (net.ParseIP / net.ParseCIDR), which inherently blocks any character that could
// break out of the directive (space, ;, {, }, newline, quote).
func (a *API) setZoneBlockedIPs(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	if _, ok := a.scopeZone(w, r, id); !ok {
		return
	}
	var in blockedIPsInput
	if !decode(w, r, &in) {
		return
	}

	clean, bad := cleanBlockedIPs(in.BlockedIPs)
	if bad != "" {
		writeError(w, http.StatusBadRequest, "blocked_ips: "+bad)
		return
	}

	z, err := a.store.SetZoneBlockedIPs(r.Context(), id, clean)
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

// cleanBlockedIPs normalizes a comma/newline-separated list of IPs / CIDRs: it trims,
// validates each (single IP, or an explicit CIDR), dedups, and returns a comma-separated
// list, or ("", errmsg) on the first bad entry. Empty input is valid ("").
func cleanBlockedIPs(raw string) (string, string) {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' })
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		e := strings.TrimSpace(p)
		if e == "" {
			continue
		}
		norm, okEntry := normalizeIPOrCIDR(e)
		if !okEntry {
			return "", "invalid IP or CIDR " + p + " (use e.g. 203.0.113.4 or 203.0.113.0/24)"
		}
		if seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
		if len(out) > maxBlockedIPs {
			return "", "too many entries (max 1000)"
		}
	}
	return strings.Join(out, ","), ""
}

// normalizeIPOrCIDR returns the canonical form of a single IP or CIDR, or ok=false. A
// bare IP is returned as-is (the edge renders `deny <ip>;`); a CIDR is returned in its
// canonical network form (e.g. 203.0.113.5/24 -> 203.0.113.0/24).
func normalizeIPOrCIDR(s string) (string, bool) {
	if strings.Contains(s, "/") {
		_, ipnet, err := net.ParseCIDR(s)
		if err != nil {
			return "", false
		}
		return ipnet.String(), true
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return "", false
	}
	return ip.String(), true
}
