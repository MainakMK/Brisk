package api

import (
	"encoding/json"
	"net/http"
	"time"

	"brisk-control/internal/auth"
	"brisk-control/internal/store"
)

// ingestSample mirrors the agent's stats.Sample JSON.
type ingestSample struct {
	Time         time.Time `json:"time"`
	Zone         string    `json:"zone"`
	Requests     int64     `json:"requests"`
	Hits         int64     `json:"hits"`
	Misses       int64     `json:"misses"`
	BytesSent    int64     `json:"bytes_sent"`
	BandwidthBps int64     `json:"bandwidth_bps"`
	CPUPct       *float64  `json:"cpu_pct"`
	RAMPct       *float64  `json:"ram_pct"`
	DiskPct      *float64  `json:"disk_pct"`
	// Quasi-static hardware totals (server-level sample only); persisted to the
	// server row so the UI can show absolute "X free of Y" next to the percentages.
	RAMTotalBytes  *int64 `json:"ram_total_bytes"`
	DiskTotalBytes *int64 `json:"disk_total_bytes"`
}

// statsIngest accepts a JSON array of samples from an authenticated agent and
// bulk-inserts them into the stats hypertable.
func (a *API) statsIngest(w http.ResponseWriter, r *http.Request) {
	serverID, ok := auth.ServerIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var in []ingestSample
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(in) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Resolve zone host -> zone_id for this server's assigned zones.
	hostToID := map[string]int64{}
	if zones, err := a.store.ListServerZones(r.Context(), serverID); err == nil {
		for _, z := range zones {
			hostToID[z.CDNHostname] = z.ID
			if z.CustomDomain != nil && *z.CustomDomain != "" {
				hostToID[*z.CustomDomain] = z.ID
			}
		}
	}

	now := time.Now().UTC()
	samples := make([]store.StatSample, 0, len(in))
	for _, s := range in {
		ts := s.Time
		if ts.IsZero() {
			ts = now
		}
		var zoneID *int64
		if s.Zone != "" {
			if id, ok := hostToID[s.Zone]; ok {
				zoneID = &id
			} else {
				continue // unknown host -> skip (don't store unattributed zone rows)
			}
		}
		samples = append(samples, store.StatSample{
			Time:         ts,
			ZoneID:       zoneID,
			Requests:     nonNeg(s.Requests),
			Hits:         nonNeg(s.Hits),
			Misses:       nonNeg(s.Misses),
			BytesSent:    nonNeg(s.BytesSent),
			BandwidthBps: nonNeg(s.BandwidthBps),
			CPUPct:       s.CPUPct,
			RAMPct:       s.RAMPct,
			DiskPct:      s.DiskPct,
		})
	}
	if len(samples) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := a.store.InsertStats(r.Context(), serverID, samples); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Persist the agent-reported hardware totals from the server-level sample (Zone=="")
	// onto the server row, so the dashboard can show absolute "X free of Y". Best-effort.
	for _, s := range in {
		if s.Zone == "" && (s.RAMTotalBytes != nil || s.DiskTotalBytes != nil) {
			_ = a.store.SetServerResources(r.Context(), serverID, s.RAMTotalBytes, s.DiskTotalBytes)
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"inserted": len(samples)})
}

// overview returns network-wide totals (latest minute).
func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	o, err := a.store.Overview(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// serverLive returns the latest live snapshot for one PoP.
func (a *API) serverLive(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	live, err := a.store.ServerLive(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, live)
}

// statsSeries returns a time-series for graphs.
func (a *API) statsSeries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	serverID, ok := parseInt64(w, q.Get("server_id"), "server_id")
	if !ok {
		return
	}
	var zoneID *int64
	if zs := q.Get("zone_id"); zs != "" {
		z, ok := parseInt64(w, zs, "zone_id")
		if !ok {
			return
		}
		zoneID = &z
	}
	resolution := q.Get("resolution")
	if resolution != "1m" {
		resolution = "raw"
	}
	to := time.Now().UTC()
	from := to.Add(-1 * time.Hour)
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	series, err := a.store.StatsSeries(r.Context(), serverID, zoneID, from, to, resolution)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"server_id":  serverID,
		"resolution": resolution,
		"from":       from.Format(time.RFC3339),
		"to":         to.Format(time.RFC3339),
		"points":     series,
	})
}

// networkStats returns the network-wide aggregate time-series (server-side merge of
// all PoPs) — Phase 4 Step 6 backlog. Same window/resolution params as statsSeries.
func (a *API) networkStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	resolution := q.Get("resolution")
	if resolution != "1m" {
		resolution = "raw"
	}
	to := time.Now().UTC()
	from := to.Add(-1 * time.Hour)
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	series, err := a.store.NetworkSeries(r.Context(), from, to, resolution)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scope":      "network",
		"resolution": resolution,
		"from":       from.Format(time.RFC3339),
		"to":         to.Format(time.RFC3339),
		"points":     series,
	})
}

func nonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func parseInt64(w http.ResponseWriter, s, name string) (int64, bool) {
	if s == "" {
		writeError(w, http.StatusBadRequest, name+" is required")
		return 0, false
	}
	var v int64
	for _, c := range s {
		if c < '0' || c > '9' {
			writeError(w, http.StatusBadRequest, "invalid "+name)
			return 0, false
		}
		v = v*10 + int64(c-'0')
	}
	return v, true
}
