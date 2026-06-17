package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"brisk-control/internal/dns"
	"brisk-control/internal/store"
)

// dnsRouting returns the current network-wide Smart-Record routing config: the
// mode, the region->location map, and the effective routing resolved per server
// (what the reconciler writes for each edge). Read-only — Step 5's UI renders it.
func (a *API) dnsRouting(w http.ResponseWriter, r *http.Request) {
	mode := dns.NormalizeMode(a.cfg.BriskDNSRoutingMode)
	staleAfter := 60 * time.Second
	if a.sync != nil {
		mode = a.sync.NetworkMode()
		staleAfter = a.sync.StaleAfterDur()
	}

	servers, err := a.store.ListServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	eff := make([]dns.Effective, 0, len(servers))
	for _, s := range servers {
		ep := dns.Endpoint{
			EdgeID: s.EdgeID, Region: s.Region, IP: s.IP, Status: s.Status, LastSeen: s.LastSeen,
			Weight: int(s.RoutingWeight), RoutingOverride: s.RoutingOverride,
		}
		e := dns.ResolveEffective(ep, mode, now, staleAfter)
		// Overlay the live load-steering factor (#3) so the UI can show the current nudge.
		e.LoadFactor = a.loadSteer.Factor(s.EdgeID)
		eff = append(eff, e)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"mode":         mode,
		"record":       a.cfg.BriskCDNRecord,
		"ttl":          a.cfg.BriskDNSTTL,
		"dns_enabled":  a.sync != nil,
		"regions":      dns.RegionList(),
		"servers":      eff,
		// Load-feedback steering (#3): whether it's enabled + the live per-edge state
		// (pressure / smoothed / factor). Empty list when off or not yet sampled.
		"load_steer": map[string]any{
			"enabled": a.loadSteer.Enabled(),
			"edges":   a.loadSteer.Snapshot(),
		},
		// Bunny native DNS monitor (Layer-2 failover backstop): whether the
		// reconciler stamps a record monitor + which method. "ping" (ICMP) or "http"
		// (GET :80) — Bunny probes each edge ~30s from 3 regions and drops a dead one
		// even with the control plane down. Surfaced so the DNS page can show it.
		"bunny_monitor": map[string]any{
			"enabled": a.cfg.BriskDNSMonitor,
			"method":  bunnyMonitorMethod(a.cfg.BriskDNSMonitor, a.cfg.BriskDNSMonitorType),
		},
		"latency_note": "LatencyZone codes must match Bunny's published latency zones; the region map is the single place to adjust them.",
	})
}

// bunnyMonitorMethod returns the human label for the active Bunny monitor method,
// or "" when the monitor is off. Mirrors dns.MonitorTypeFromString.
func bunnyMonitorMethod(enabled bool, typ string) string {
	if !enabled {
		return ""
	}
	switch dns.MonitorTypeFromString(typ) {
	case dns.MonitorHTTP:
		return "http"
	case dns.MonitorNone:
		return ""
	default:
		return "ping"
	}
}

type setRoutingInput struct {
	// Weight 0-100 (pointer so we can tell "omitted" from "0"); defaults to 100.
	Weight   *int   `json:"weight"`
	Override string `json:"override"` // "" | geographic | latency
	// Capacity-weighted routing (opt-in). Both optional (nil = leave unchanged) so the
	// routing card can edit a PoP's bandwidth + the capacity toggle in the same save.
	CapacityMbps     *int32 `json:"capacity_mbps"`
	WeightByCapacity *bool  `json:"weight_by_capacity"`
}

// setServerRouting updates a server's per-PoP routing (weight + optional mode
// override) and triggers a reconcile so the Smart-Record set follows.
func (a *API) setServerRouting(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r, "id")
	if !ok {
		return
	}
	var in setRoutingInput
	if !decode(w, r, &in) {
		return
	}

	weight := 100
	if in.Weight != nil {
		weight = *in.Weight
	}
	if weight < 0 || weight > 100 {
		writeError(w, http.StatusBadRequest, "weight must be between 0 and 100")
		return
	}
	override := strings.ToLower(strings.TrimSpace(in.Override))
	switch override {
	case "", string(dns.ModeGeographic), string(dns.ModeLatency):
		// ok
	default:
		writeError(w, http.StatusBadRequest, `override must be "", "geographic", or "latency"`)
		return
	}

	if in.CapacityMbps != nil && *in.CapacityMbps < 0 {
		writeError(w, http.StatusBadRequest, "capacity_mbps must be >= 0")
		return
	}

	srv, err := a.store.UpdateServerRouting(r.Context(), id, int32(weight), override, in.CapacityMbps, in.WeightByCapacity)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "server not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// The routing change should propagate to DNS on the next reconcile.
	a.triggerDNSReconcile("routing_change")
	writeJSON(w, http.StatusOK, srv)
}
