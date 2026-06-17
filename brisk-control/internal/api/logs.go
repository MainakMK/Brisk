package api

import (
	"encoding/json"
	"net/http"
	"time"

	"brisk-control/internal/auth"
	"brisk-control/internal/store"
)

// Phase 4 Step 6 — the real edge logs API. Agents ship structured access-log
// entries (POST /agent/logs); the dashboard queries them per zone (tenant) or
// cross-tenant (admin). Tenant-scoped via scopeZone; high-volume, so capped + paged.

// ingestLog mirrors the agent's shipped access-log JSON.
type ingestLog struct {
	TS           time.Time `json:"ts"`
	Zone         string    `json:"zone"` // served host -> resolved to zone_id
	ClientIP     string    `json:"client_ip"`
	Country      string    `json:"country"`
	Method       string    `json:"method"`
	Path         string    `json:"path"`
	Status       int32     `json:"status"`
	Bytes        int64     `json:"bytes"`
	CacheStatus  string    `json:"cache_status"`
	RequestTime  float64   `json:"request_time"`
	UpstreamTime float64   `json:"upstream_time"`
	Referer      string    `json:"referer"`
	UserAgent    string    `json:"user_agent"`
	RequestID    string    `json:"request_id"`
}

// logsIngest accepts a JSON array of access-log entries from an authenticated agent
// and bulk-inserts them. Zone resolved from the served host (like statsIngest).
func (a *API) logsIngest(w http.ResponseWriter, r *http.Request) {
	serverID, ok := auth.ServerIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in []ingestLog
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(in) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	hostToID := map[string]int64{}
	if zones, err := a.store.ListServerZones(r.Context(), serverID); err == nil {
		for _, z := range zones {
			hostToID[z.CDNHostname] = z.ID
			if z.CustomDomain != nil && *z.CustomDomain != "" {
				hostToID[*z.CustomDomain] = z.ID
			}
		}
	}
	edgeID := ""
	if s, err := a.store.GetServer(r.Context(), serverID); err == nil {
		edgeID = s.EdgeID
	}

	now := time.Now().UTC()
	logs := make([]store.RequestLog, 0, len(in))
	for _, e := range in {
		ts := e.TS
		if ts.IsZero() {
			ts = now
		}
		var zoneID *int64
		if e.Zone != "" {
			if zid, ok := hostToID[e.Zone]; ok {
				zoneID = &zid
			}
			// unknown host: keep the row (zone_id NULL) — still useful by edge/host.
		}
		logs = append(logs, store.RequestLog{
			TS: ts, ZoneID: zoneID, ClientIP: e.ClientIP, Country: e.Country, Method: e.Method,
			Host: e.Zone, Path: e.Path, Status: e.Status, Bytes: e.Bytes, CacheStatus: e.CacheStatus,
			RequestTime: e.RequestTime, UpstreamTime: e.UpstreamTime, Referer: e.Referer,
			UserAgent: e.UserAgent, RequestID: e.RequestID,
		})
	}
	if _, err := a.store.InsertRequestLogs(r.Context(), serverID, edgeID, logs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"inserted": len(logs)})
}

// logFilterFromQuery builds the shared filter from query params.
func logFilterFromQuery(r *http.Request) store.RequestLogFilter {
	from, to := eventWindow(r) // last 24h default (reused from waf.go)
	q := r.URL.Query()
	f := store.RequestLogFilter{
		From: from, To: to,
		CacheStatus: q.Get("cache"),
		Path:        q.Get("path"),
		ClientIP:    q.Get("ip"),
		Country:     q.Get("country"),
	}
	if s := q.Get("status"); s != "" {
		n := 0
		for _, c := range s {
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + int(c-'0')
		}
		f.Status = n
	}
	if l := q.Get("limit"); l != "" {
		n := 0
		for _, c := range l {
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + int(c-'0')
		}
		f.Limit = n // QueryRequestLogs clamps to 1..1000 (default 200)
	}
	return f
}

// listZoneLogs returns a zone's request logs (tenant-scoped).
func (a *API) listZoneLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	f := logFilterFromQuery(r)
	f.ZoneIDs = []int64{z.ID}
	logs, err := a.store.QueryRequestLogs(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs, "from": f.From, "to": f.To})
}

// adminLogs returns request logs across all tenants (admin only). Optional ?zone_id.
func (a *API) adminLogs(w http.ResponseWriter, r *http.Request) {
	f := logFilterFromQuery(r)
	if zs := r.URL.Query().Get("zone_id"); zs != "" {
		zid, ok := parseInt64(w, zs, "zone_id")
		if !ok {
			return
		}
		f.ZoneIDs = []int64{zid}
	}
	logs, err := a.store.QueryRequestLogs(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs, "from": f.From, "to": f.To})
}

// logAnalyticsResponse wraps the aggregate with its window.
func (a *API) writeLogAnalytics(w http.ResponseWriter, r *http.Request, f store.RequestLogFilter) {
	out, err := a.store.LogAnalytics(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"analytics": out, "from": f.From, "to": f.To})
}

// zoneLogsAnalytics returns the real per-request aggregates for a zone (Parts 3+4):
// origin offload, status breakdown, latency percentiles, top paths/countries.
func (a *API) zoneLogsAnalytics(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	z, ok := a.scopeZone(w, r, id)
	if !ok {
		return
	}
	from, to := eventWindow(r)
	a.writeLogAnalytics(w, r, store.RequestLogFilter{ZoneIDs: []int64{z.ID}, From: from, To: to})
}

// adminLogsAnalytics returns the aggregates across all tenants (admin). Optional ?zone_id.
func (a *API) adminLogsAnalytics(w http.ResponseWriter, r *http.Request) {
	from, to := eventWindow(r)
	f := store.RequestLogFilter{From: from, To: to}
	if zs := r.URL.Query().Get("zone_id"); zs != "" {
		zid, ok := parseInt64(w, zs, "zone_id")
		if !ok {
			return
		}
		f.ZoneIDs = []int64{zid}
	}
	a.writeLogAnalytics(w, r, f)
}
