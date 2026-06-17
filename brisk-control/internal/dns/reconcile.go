package dns

import (
	"context"
	"strings"
	"time"
)

// serverTag is the comment prefix that links a routing record to a server.
const serverTag = CommentPrefix + "server:" // "brisk:server:"

// ServerTag builds the durable comment tag for an edge.
func ServerTag(edgeID string) string { return serverTag + edgeID }

// edgeIDFromComment extracts the edge id from a brisk:server:<edge_id> comment,
// or "" if the comment isn't a server routing tag.
func edgeIDFromComment(comment string) string {
	if !strings.HasPrefix(comment, serverTag) {
		return ""
	}
	return strings.TrimPrefix(comment, serverTag)
}

// Endpoint is the control plane's view of one edge for DNS purposes (mapped from
// a store.Server by the caller, so this package stays store-free).
type Endpoint struct {
	EdgeID   string
	Region   string
	IP       string
	Status   string
	LastSeen *time.Time

	// Smart-Record routing (Phase 3 Step 3).
	Weight          int    // 0-100; splits load within a location group (default 100)
	RoutingOverride string // "" = use network-wide mode; else geographic|latency

	// Capacity-weighted routing (opt-in). When WeightByCapacity is true, the record's
	// weight is derived from CapacityMbps (normalized to the biggest capacity-weighted
	// endpoint in the set) instead of Weight. CapacityMbps is the provisioned bandwidth.
	CapacityMbps     int
	WeightByCapacity bool

	// Load-feedback steering (#3). A live damping factor in (0,1] the load
	// controller sets from this edge's measured pressure (load ÷ capacity): 1.0 =
	// cool, no change; <1.0 = hot, reduce this edge's share. It MULTIPLIES the base
	// weight (capacity- or manual-derived) in effectiveWeight, so a hot box bleeds
	// share to its cooler peers without ever fully draining (bounded by the
	// controller's floor). 0 (the zero value) means "no load data" => treated as 1.0,
	// so a fleet with load steering off renders byte-identical. Held in memory by the
	// controller, so a control-plane restart resets it to neutral (weights un-nudge),
	// matching Golden Rule #3 (data plane independent; CP down => weights freeze in
	// Bunny at their last value).
	LoadFactor float64

	// Health (Phase 3 Step 4). The external probe verdict — distinct from the
	// heartbeat. "unhealthy" pulls the edge from rotation; "healthy"/"unknown"
	// leave it in (an un-probed edge gets the benefit of the doubt).
	Health string // unknown | healthy | unhealthy

	// Admin drain (Phase 3 Step 5). An explicitly drained PoP is forced out of
	// rotation regardless of health (drain > health > heartbeat).
	Drained bool
}

// Online reports whether the edge is heartbeat-online: status online AND a fresh
// heartbeat. A stale heartbeat (no beat within staleAfter) counts as offline,
// matching the dashboard's offline derivation. This is the Step-2 signal.
func (e Endpoint) Online(now time.Time, staleAfter time.Duration) bool {
	if !strings.EqualFold(e.Status, "online") {
		return false
	}
	if e.LastSeen == nil {
		return false
	}
	return now.Sub(*e.LastSeen) <= staleAfter
}

// Unhealthy reports whether the external health probe marked this edge down.
func (e Endpoint) Unhealthy() bool { return strings.EqualFold(e.Health, "unhealthy") }

// Decision is the per-edge rotation verdict plus the reason it's in/out, so the
// UI can show "drained" vs "unhealthy" vs "offline".
type Decision struct {
	InRotation bool   `json:"in_rotation"`
	Reason     string `json:"reason"` // in_rotation | drained | unhealthy | offline
}

// RotationDecision computes each edge's rotation membership AND the reason, with
// the precedence the operator expects: admin DRAIN > HEALTH > HEARTBEAT.
//
//   - drained        → out, reason "drained" (wins even if healthy)
//   - unhealthy      → out, reason "unhealthy" (unless the all-down guard applies)
//   - offline (stale heartbeat / not online) → out, reason "offline"
//   - otherwise      → in rotation
//
// Two blackhole guards keep a control-plane-side fault from NXDOMAIN-ing the whole
// zone (matching Bunny's "all offline → return all"). Drained edges are explicit
// operator intent and are never re-included by either guard; emptying the pool by
// draining is prevented at the API layer (the all-PoP confirm), not here.
//
//   - ALL-DOWN guard: if every NON-DRAINED heartbeat-online edge is UNHEALTHY
//     (external probes failing), those are kept IN rotation — a checker-side blip
//     must not black-hole the CDN.
//   - ALL-OFFLINE guard: if every NON-DRAINED edge is heartbeat-OFFLINE (the control
//     plane lost every heartbeat — laptop link dropped, a refresher died, or all
//     agents briefly can't reach the control plane), keep them IN rotation instead
//     of disabling every record. The edges' DATA planes are independent of the
//     control plane (golden rule #3: dashboard down ≠ CDN down), so they're very
//     likely still serving; NXDOMAIN-ing the zone is strictly worse than routing to
//     a box that may still answer. Genuinely-dead boxes are still pulled by the
//     external HEALTH checker (the unhealthy path) — heartbeat staleness alone is
//     not proof the box is down. (Fix for the Phase-3.7 outage where a dead
//     heartbeat refresher let the reconciler disable all three records at once.)
func RotationDecision(endpoints []Endpoint, now time.Time, staleAfter time.Duration) map[string]Decision {
	nonDrained, onlineCount, healthyOnline := 0, 0, 0
	for _, ep := range endpoints {
		if ep.Drained {
			continue // drained edges are out by intent; exclude from the guard pool
		}
		nonDrained++
		if ep.Online(now, staleAfter) {
			onlineCount++
			if !ep.Unhealthy() {
				healthyOnline++
			}
		}
	}
	allDown := onlineCount > 0 && healthyOnline == 0 // online, but all unhealthy
	allOffline := nonDrained > 0 && onlineCount == 0 // none heartbeat-online at all

	out := make(map[string]Decision, len(endpoints))
	for _, ep := range endpoints {
		online := ep.Online(now, staleAfter)
		switch {
		case ep.Drained:
			out[ep.EdgeID] = Decision{false, "drained"}
		case ep.Unhealthy():
			if online && allDown {
				out[ep.EdgeID] = Decision{true, "in_rotation"} // all-down guard
			} else {
				out[ep.EdgeID] = Decision{false, "unhealthy"}
			}
		case !online:
			if allOffline {
				out[ep.EdgeID] = Decision{true, "in_rotation"} // all-offline guard
			} else {
				out[ep.EdgeID] = Decision{false, "offline"}
			}
		default:
			out[ep.EdgeID] = Decision{true, "in_rotation"}
		}
	}
	return out
}

// RotationState is the boolean view of RotationDecision (edgeID -> inRotation),
// kept for the reconciler diff and callers that only need membership.
func RotationState(endpoints []Endpoint, now time.Time, staleAfter time.Duration) map[string]bool {
	dec := RotationDecision(endpoints, now, staleAfter)
	out := make(map[string]bool, len(dec))
	for id, d := range dec {
		out[id] = d.InRotation
	}
	return out
}

// Action is one planned DNS change.
type Action struct {
	Kind     string `json:"kind"` // add | enable | disable | update | remove
	EdgeID   string `json:"edge_id"`
	RecordID int64  `json:"record_id,omitempty"`
	Record   Record `json:"-"` // desired state (add/update/enable/disable)
	Name     string `json:"name"`
	Value    string `json:"value"`
}

// Plan is the set of changes that converge DNS to the servers table.
type Plan struct {
	Actions []Action `json:"actions"`
}

// Empty reports whether the plan changes nothing (idempotent steady state).
func (p Plan) Empty() bool { return len(p.Actions) == 0 }

// DiffOpts parameterize a reconcile diff.
type DiffOpts struct {
	RoutingName string        // the record set name (e.g. "cdn")
	TTL         int           // routing record TTL (seconds)
	StaleAfter  time.Duration // heartbeat staleness threshold
	Mode        RoutingMode   // network-wide Smart-Record mode (geographic|latency)
	// MonitorType is Bunny's native record monitor (Layer-2 backstop): MonitorNone
	// (off, default) | MonitorPing | MonitorHTTP. Written to every managed A record
	// so an explicit 0 clears it when the feature is toggled off (declarative).
	MonitorType int
	Now         time.Time
}

// coordEps is the tolerance for comparing stored vs desired coordinates (Bunny
// stores them as doubles; avoid spurious updates from float noise).
const coordEps = 1e-6

func floatEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= coordEps
}

// Diff computes the changes needed to converge the zone's brisk:server records
// to the given endpoints. It is pure (no API calls) so it doubles as the
// dry-run preview. Only brisk:server:<edge_id> records are ever considered;
// every other record (manual or otherwise) is ignored entirely.
func Diff(endpoints []Endpoint, records []Record, o DiffOpts) Plan {
	// Index existing managed records by edge id.
	managed := map[string]Record{}
	// Untagged A records in the routing set, indexed by value. Used for lost-tag
	// recovery: if a record that used to be ours loses its brisk: comment (a manual
	// Bunny edit, a cross-session relic, an API glitch), we ADOPT the same-IP record
	// and re-tag it instead of creating a duplicate at that IP. Without this, a
	// lost-tag record lingers forever (we never touch non-brisk records) AND the
	// reconciler adds a second record for the same edge — the "two records, same
	// IP, one geolocated to 0,0/Ghana" bug.
	untaggedByValue := map[string]Record{}
	for _, r := range records {
		if id := edgeIDFromComment(r.Comment); id != "" {
			managed[id] = r
			continue
		}
		if r.Type == TypeA && strings.EqualFold(r.Name, o.RoutingName) && r.Value != "" {
			untaggedByValue[r.Value] = r
		}
	}

	// Health-aware rotation with the all-down blackhole guard.
	inRotation := RotationState(endpoints, o.Now, o.StaleAfter)
	// Capacity-weighted routing reference (0 when nobody opts in => normWeight path).
	maxCap := maxCapacity(endpoints)

	plan := Plan{Actions: []Action{}}
	for _, ep := range endpoints {
		if ep.EdgeID == "" || ep.IP == "" {
			continue
		}
		desiredDisabled := !inRotation[ep.EdgeID]
		srt, lat, long, latZone := smartFieldsFor(ep, o.Mode)
		desired := Record{
			Type:             TypeA,
			Name:             o.RoutingName,
			Value:            ep.IP,
			TTL:              o.TTL,
			Disabled:         desiredDisabled,
			Comment:          ServerTag(ep.EdgeID),
			Weight:           effectiveWeight(ep, maxCap),
			SmartRoutingType: srt,
			LatencyZone:      latZone,
			GeoLatitude:      lat,
			GeoLongitude:     long,
			MonitorType:      o.MonitorType, // Bunny native monitor (Layer-2 backstop)
		}

		existing, ok := managed[ep.EdgeID]
		adopted := false
		if !ok {
			// Lost-tag recovery: an untagged record in the routing set already
			// points at this edge's IP — adopt + re-tag it instead of adding a
			// duplicate (prevents the same-IP duplicate / stale-coords record).
			if relic, found := untaggedByValue[ep.IP]; found {
				existing, ok, adopted = relic, true, true
				delete(untaggedByValue, ep.IP)
			}
		}
		if !ok {
			plan.Actions = append(plan.Actions, Action{
				Kind: "add", EdgeID: ep.EdgeID, Record: desired, Name: desired.Name, Value: desired.Value,
			})
			continue
		}
		delete(managed, ep.EdgeID) // seen

		smartChanged := existing.SmartRoutingType != desired.SmartRoutingType ||
			existing.LatencyZone != desired.LatencyZone ||
			existing.Weight != desired.Weight ||
			existing.MonitorType != desired.MonitorType || // Bunny monitor on/off/type
			!floatEq(existing.GeoLatitude, desired.GeoLatitude) ||
			!floatEq(existing.GeoLongitude, desired.GeoLongitude)
		contentChanged := existing.Value != ep.IP || existing.Name != o.RoutingName ||
			existing.TTL != o.TTL || existing.Comment != desired.Comment || smartChanged
		disabledChanged := existing.Disabled != desiredDisabled
		if !adopted && !contentChanged && !disabledChanged {
			continue // already converged
		}

		// An adopted record always needs a write (to re-apply the brisk: tag).
		kind := "update"
		if !adopted && !contentChanged && disabledChanged {
			if desiredDisabled {
				kind = "disable"
			} else {
				kind = "enable"
			}
		}
		desired.ID = existing.ID
		plan.Actions = append(plan.Actions, Action{
			Kind: kind, EdgeID: ep.EdgeID, RecordID: existing.ID, Record: desired, Name: desired.Name, Value: desired.Value,
		})
	}

	// Anything left in managed has no corresponding server → orphan from a
	// deleted server → remove it (lifecycle delete).
	for edgeID, r := range managed {
		plan.Actions = append(plan.Actions, Action{
			Kind: "remove", EdgeID: edgeID, RecordID: r.ID, Name: r.Name, Value: r.Value,
		})
	}
	return plan
}

// Apply executes a plan against Bunny and returns the actions that were applied
// (so the caller can audit them). Stops on the first error, returning what was
// applied so far.
func (c *Client) Apply(ctx context.Context, plan Plan) ([]Action, error) {
	applied := make([]Action, 0, len(plan.Actions))
	for _, a := range plan.Actions {
		switch a.Kind {
		case "add":
			out, err := c.AddRecord(ctx, a.Record)
			if err != nil {
				return applied, err
			}
			a.RecordID = out.ID
		case "enable", "disable", "update":
			if err := c.UpdateRecord(ctx, a.RecordID, a.Record); err != nil {
				return applied, err
			}
		case "remove":
			if err := c.DeleteRecord(ctx, a.RecordID); err != nil {
				return applied, err
			}
		}
		applied = append(applied, a)
	}
	return applied, nil
}
