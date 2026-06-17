package api

import (
	"net"
	"net/http"
	"strings"

	"brisk-control/internal/dns"
)

// addDNSRecordInput is the body for POST /dns/records (thin test endpoint).
type addDNSRecordInput struct {
	Name    string `json:"name"`                      // subdomain ("" = apex)
	Value   string `json:"value" validate:"required"` // the A-record IP
	TTL     int    `json:"ttl"`                       // seconds; defaults to 60
	Weight  int    `json:"weight"`                    // optional, for record sets (Steps 3-4)
	Comment string `json:"comment"`                   // forced brisk:-tagged
}

// dnsStatus reports whether DNS is configured and reachable. A bad key returns
// configured:false with a clear error (no crash, no key leaked).
func (a *API) dnsStatus(w http.ResponseWriter, r *http.Request) {
	if a.dns == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"configured": false,
			"reason":     "BUNNY_API_KEY not set",
		})
		return
	}
	z, err := a.dns.GetZone(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"configured": false,
			"zone_id":    a.dns.ZoneID(),
			"error":      dnsErrMsg(err),
		})
		return
	}
	zone := a.dns.ZoneName()
	if zone == "" {
		zone = z.Domain // resolved from the API when configured by id
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured":   true,
		"zone":         zone,
		"zone_id":      a.dns.ZoneID(),
		"record_count": len(z.Records),
	})
}

// dnsListRecords lists the zone's records (typed). Read-only — shows all records
// (Brisk-managed and not), but writes only ever touch Brisk-tagged ones.
func (a *API) dnsListRecords(w http.ResponseWriter, r *http.Request) {
	if a.dns == nil {
		writeError(w, http.StatusServiceUnavailable, "DNS not configured")
		return
	}
	recs, err := a.dns.ListRecords(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, dnsErrMsg(err))
		return
	}
	writeJSON(w, http.StatusOK, recs)
}

// dnsAddRecord creates (idempotently) a Brisk-tagged A record.
func (a *API) dnsAddRecord(w http.ResponseWriter, r *http.Request) {
	if a.dns == nil {
		writeError(w, http.StatusServiceUnavailable, "DNS not configured")
		return
	}
	var in addDNSRecordInput
	if !decode(w, r, &in) {
		return
	}
	ip := net.ParseIP(strings.TrimSpace(in.Value))
	if ip == nil || ip.To4() == nil {
		writeError(w, http.StatusBadRequest, "value must be a valid IPv4 address (A record)")
		return
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = 60
	}

	rec := dns.Record{
		Type:    dns.TypeA,
		Name:    strings.TrimSpace(in.Name),
		Value:   ip.String(),
		TTL:     ttl,
		Weight:  in.Weight,
		Comment: ensureBriskTag(in.Comment),
	}
	out, created, err := a.dns.EnsureRecord(r.Context(), rec)
	if err != nil {
		writeError(w, http.StatusBadGateway, dnsErrMsg(err))
		return
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK // idempotent: already existed, no duplicate
	}
	writeJSON(w, status, map[string]any{"created": created, "record": out})
}

// dnsDeleteRecord deletes a record by id — but REFUSES to delete any record that
// Brisk didn't create (guardrail: never touch non-Brisk records).
func (a *API) dnsDeleteRecord(w http.ResponseWriter, r *http.Request) {
	if a.dns == nil {
		writeError(w, http.StatusServiceUnavailable, "DNS not configured")
		return
	}
	// Deletion-protection lock: removing a record requires a deliberate,
	// time-delayed unlock so routing records can't be deleted by accident.
	if !a.requireDNSUnlocked(w, r) {
		return
	}
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	recs, err := a.dns.ListRecords(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, dnsErrMsg(err))
		return
	}
	var target *dns.Record
	for i := range recs {
		if recs[i].ID == id {
			target = &recs[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "record not found in zone")
		return
	}
	if !target.IsBriskManaged() {
		writeError(w, http.StatusForbidden, "refusing to delete a record Brisk did not create (no brisk: tag)")
		return
	}
	if err := a.dns.DeleteRecord(r.Context(), id); err != nil {
		writeError(w, http.StatusBadGateway, dnsErrMsg(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ensureBriskTag guarantees every Brisk-created record carries the brisk: tag.
func ensureBriskTag(comment string) string {
	c := strings.TrimSpace(comment)
	if c == "" {
		return dns.CommentPrefix + "manual"
	}
	if !strings.HasPrefix(c, dns.CommentPrefix) {
		return dns.CommentPrefix + c
	}
	return c
}

// dnsErrMsg returns a safe error message (never includes the API key, which is
// only ever sent in the AccessKey header, never in URLs or bodies).
func dnsErrMsg(err error) string {
	var apiErr *dns.APIError
	if asAPIError(err, &apiErr) {
		if apiErr.IsAuth() {
			return "Bunny DNS authentication failed (check BUNNY_API_KEY)"
		}
	}
	return err.Error()
}

func asAPIError(err error, target **dns.APIError) bool {
	for err != nil {
		if e, ok := err.(*dns.APIError); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
