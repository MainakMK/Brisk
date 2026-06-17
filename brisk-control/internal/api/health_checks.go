package api

import (
	"errors"
	"net/http"
	"time"

	"brisk-control/internal/dns"
	"brisk-control/internal/health"
	"brisk-control/internal/store"
)

// defaultStaleAfter mirrors the syncer's heartbeat-staleness window when the
// syncer isn't available to ask.
const defaultStaleAfter = 60 * time.Second

func (a *API) staleAfter() time.Duration {
	if a.sync != nil {
		return a.sync.StaleAfterDur()
	}
	return defaultStaleAfter
}

// edgeHealth is one row of GET /health/status: live probe data (when the checker
// is on) plus the persisted verdict and the computed in-rotation decision.
type edgeHealth struct {
	ServerID      int64     `json:"server_id"`
	EdgeID        string    `json:"edge_id"`
	Host          string    `json:"host"`
	Status        string    `json:"status"` // server lifecycle status
	State         string    `json:"state"`  // healthy | unhealthy | unknown
	Healthy       bool      `json:"healthy"`
	InRotation    bool      `json:"in_rotation"`     // enabled in the cdn set (all-down guard applied)
	RotationReason string   `json:"rotation_reason"` // in_rotation | drained | unhealthy | offline
	CheckEnabled  bool      `json:"check_enabled"`   // per-server health-check switch
	ConsecFails   int       `json:"consecutive_fails"`
	ConsecOK      int       `json:"consecutive_successes"`
	LastProbe     time.Time `json:"last_probe,omitempty"`
	LastLatencyMs int64     `json:"last_latency_ms"`
	LastError     string    `json:"last_error,omitempty"`
	Probing       bool      `json:"probing"`
	IntervalSec   int       `json:"interval_seconds"`
	FailThreshold int       `json:"fail_threshold"`
	RiseThreshold int       `json:"rise_threshold"`
}

// healthStatus returns per-edge health: live probe state (if the checker is on),
// the persisted verdict, and whether each edge is currently in DNS rotation
// (with the all-down blackhole guard applied).
func (a *API) healthStatus(w http.ResponseWriter, r *http.Request) {
	servers, err := a.store.ListServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	eps := make([]dns.Endpoint, 0, len(servers))
	for _, s := range servers {
		eps = append(eps, dns.Endpoint{
			EdgeID: s.EdgeID, Status: s.Status, LastSeen: s.LastSeen,
			Health: s.HealthStatus, Drained: s.Drained,
		})
	}
	rot := dns.RotationDecision(eps, time.Now().UTC(), a.staleAfter())

	live := map[string]health.Status{}
	if a.healthChecker != nil {
		for _, st := range a.healthChecker.Snapshot() {
			live[st.EdgeID] = st
		}
	}

	edges := make([]edgeHealth, 0, len(servers))
	for _, s := range servers {
		host := s.IP
		if s.Hostname != nil && *s.Hostname != "" {
			host = *s.Hostname
		}
		row := edgeHealth{
			ServerID: s.ID, EdgeID: s.EdgeID, Host: host, Status: s.Status,
			State:          s.HealthStatus,
			Healthy:        s.HealthStatus == "healthy",
			InRotation:     rot[s.EdgeID].InRotation,
			RotationReason: rot[s.EdgeID].Reason,
			CheckEnabled:   s.HealthEnabled,
		}
		if st, ok := live[s.EdgeID]; ok {
			row.State = st.State
			row.Healthy = st.Healthy
			row.ConsecFails = st.ConsecFails
			row.ConsecOK = st.ConsecOK
			row.LastProbe = st.LastProbe
			row.LastLatencyMs = st.LastLatencyMs
			row.LastError = st.LastError
			row.Probing = st.Probing
			row.IntervalSec = st.IntervalSec
			row.FailThreshold = st.FailThreshold
			row.RiseThreshold = st.RiseThreshold
		}
		edges = append(edges, row)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"checker_enabled": a.healthChecker != nil,
		"edges":           edges,
	})
}

// healthConfig returns the effective per-PoP health config: network-wide defaults
// plus each server's overrides resolved to effective values.
func (a *API) healthConfig(w http.ResponseWriter, r *http.Request) {
	servers, err := a.store.ListServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defaults := map[string]any{
		"enabled":        a.cfg.HealthEnabled,
		"interval_sec":   a.cfg.HealthInterval,
		"timeout_sec":    a.cfg.HealthTimeout,
		"fail_threshold": a.cfg.HealthFailThreshold,
		"rise_threshold": a.cfg.HealthRiseThreshold,
		"path":           a.cfg.HealthPath,
		"scheme":         a.cfg.HealthScheme,
		"port":           a.cfg.HealthPort,
		"ttl_seconds":    a.cfg.BriskDNSTTL,
	}
	per := make([]map[string]any, 0, len(servers))
	for _, s := range servers {
		per = append(per, map[string]any{
			"server_id":      s.ID,
			"edge_id":        s.EdgeID,
			"check_enabled":  s.HealthEnabled,
			"interval_sec":   effInt(int(s.HealthIntervalSeconds), a.cfg.HealthInterval),
			"fail_threshold": effInt(int(s.HealthFailThreshold), a.cfg.HealthFailThreshold),
			"rise_threshold": effInt(int(s.HealthRiseThreshold), a.cfg.HealthRiseThreshold),
			"overridden": s.HealthIntervalSeconds != 0 || s.HealthFailThreshold != 0 ||
				s.HealthRiseThreshold != 0 || !s.HealthEnabled,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"defaults": defaults,
		"servers":  per,
	})
}

// effInt returns the override when set (>0), else the network default.
func effInt(override, def int) int {
	if override > 0 {
		return override
	}
	return def
}

type setHealthInput struct {
	Enabled         *bool `json:"enabled"`
	IntervalSeconds *int  `json:"interval_seconds"` // 0 = inherit
	FailThreshold   *int  `json:"fail_threshold"`   // 0 = inherit
	RiseThreshold   *int  `json:"rise_threshold"`   // 0 = inherit
}

// setServerHealth sets a server's per-PoP health overrides; takes effect on the
// checker's next reload (≤5s). Omitted fields keep their current value.
func (a *API) setServerHealth(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	cur, err := a.store.GetServer(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var in setHealthInput
	if !decode(w, r, &in) {
		return
	}
	enabled := cur.HealthEnabled
	interval := int(cur.HealthIntervalSeconds)
	fail := int(cur.HealthFailThreshold)
	rise := int(cur.HealthRiseThreshold)
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if in.IntervalSeconds != nil {
		interval = *in.IntervalSeconds
	}
	if in.FailThreshold != nil {
		fail = *in.FailThreshold
	}
	if in.RiseThreshold != nil {
		rise = *in.RiseThreshold
	}
	// Bounds: 0 = inherit; otherwise sane windows (no thundering herd, no glacial
	// failover). Interval 1-300s, thresholds 1-10.
	if interval < 0 || interval > 300 {
		writeError(w, http.StatusBadRequest, "interval_seconds must be 0 (inherit) or 1-300")
		return
	}
	if fail < 0 || fail > 10 || rise < 0 || rise > 10 {
		writeError(w, http.StatusBadRequest, "fail_threshold/rise_threshold must be 0 (inherit) or 1-10")
		return
	}

	srv, err := a.store.UpdateServerHealthConfig(r.Context(), id, enabled, int32(interval), int32(fail), int32(rise))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, srv)
}
