package dns

import (
	"context"
	"strconv"
	"strings"
)

// Phase 4 / Step 7 Part 2.5: per-zone Smart A-record sets.
//
// The wildcard CNAME `*.cdn -> cdn.a2zjav.com` (Part 2) makes EVERY tenant
// hostname share the single apex `cdn` A-set, so all tenants route to the same
// PoPs — you cannot disable BLR for one zone while keeping it for another. The
// fix is an explicit, authoritative per-zone Smart A-set for each tenant
// hostname, derived from `server_zones` (the single source of truth for both
// vhost rendering AND DNS membership). An explicit A-set beats the wildcard, so
// a zone restricted off a PoP is NOT silently re-added by the catch-all CNAME.

// ZoneRouting is the control plane's per-zone DNS intent: the zone's relative
// Bunny record name (e.g. "testjim.cdn") plus the edge ids currently assigned to
// it (from server_zones, shields already excluded). The caller maps store rows
// to this so the dns package stays store-free.
type ZoneRouting struct {
	ZoneID          int64
	Name            string   // relative Bunny name, e.g. "testjim.cdn"
	AssignedEdgeIDs []string // edge ids from server_zones (shields excluded)
}

// ZonesFunc returns the per-zone DNS intent for every active tenant zone (the
// apex `cdn` zone is excluded by the caller — it's owned by the apex reconciler).
type ZonesFunc func(ctx context.Context) ([]ZoneRouting, error)

// zoneTag is the comment prefix linking an A record to a tenant zone's per-zone
// Smart A-set: brisk:zone:<zoneID>. Each record additionally carries the edge id
// (brisk:zone:<zoneID>:<edgeID>) so the diff matches per (zone,edge) and writes
// the correct geo/latency Smart fields for that edge — while still being tagged
// brisk:zone:<id> for ownership checks.
func zoneTag(zoneID int64) string {
	return CommentPrefix + "zone:" + strconv.FormatInt(zoneID, 10)
}

// zoneEdgeTag is the per-record comment: brisk:zone:<zoneID>:<edgeID>.
func zoneEdgeTag(zoneID int64, edgeID string) string {
	return zoneTag(zoneID) + ":" + edgeID
}

// edgeIDFromZoneComment returns the edge id for a record belonging to zoneID, and
// whether the comment is this zone's tag at all. Accepts both the bare
// "brisk:zone:<id>" (edge "") and "brisk:zone:<id>:<edge>".
func edgeIDFromZoneComment(comment string, zoneID int64) (string, bool) {
	tag := zoneTag(zoneID)
	if comment == tag {
		return "", true
	}
	if strings.HasPrefix(comment, tag+":") {
		return strings.TrimPrefix(comment, tag+":"), true
	}
	return "", false
}

// zoneIDFromComment parses the zone id out of any brisk:zone:<id>[:<edge>]
// comment (for the orphan sweep). Returns ok=false for non-zone comments.
func zoneIDFromComment(comment string) (int64, bool) {
	prefix := CommentPrefix + "zone:"
	if !strings.HasPrefix(comment, prefix) {
		return 0, false
	}
	rest := strings.TrimPrefix(comment, prefix)
	if i := strings.IndexByte(rest, ':'); i >= 0 {
		rest = rest[:i]
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// DiffZone computes the changes to converge ONE tenant zone's per-zone Smart
// A-set to its assigned edges. It is pure (no API calls), so it doubles as the
// dry-run preview. Only this zone's brisk:zone:<id> records at its Name are
// considered; the apex `cdn` set, the wildcard CNAME, and every manual record
// are ignored entirely.
//
// Membership mirrors the apex set: one A record per ASSIGNED edge, with Disabled
// set from the GLOBAL rotation decision (drain > health > heartbeat). So a
// drained/unhealthy edge's record is disabled (kept, for fast re-enable), an
// UNASSIGNED edge has no record (removed — that's the per-zone PoP toggle), and a
// zone with zero usable edges ends up with no records at all (its A-set
// disappears → NXDOMAIN, never a mis-route). Smart fields (geo/latency) are
// identical to the apex set's, so tenant hostnames inherit the same routing.
func DiffZone(zr ZoneRouting, endpoints []Endpoint, records []Record, o DiffOpts) Plan {
	epByID := make(map[string]Endpoint, len(endpoints))
	for _, ep := range endpoints {
		if ep.EdgeID != "" {
			epByID[ep.EdgeID] = ep
		}
	}
	inRotation := RotationState(endpoints, o.Now, o.StaleAfter)
	// Capacity-weighted routing reference across the fleet (0 => normWeight path).
	maxCap := maxCapacity(endpoints)

	// Existing managed records for THIS zone at its Name, indexed by edge id.
	managed := map[string]Record{}
	for _, r := range records {
		if r.Type != TypeA || !strings.EqualFold(r.Name, zr.Name) {
			continue
		}
		if edgeID, ok := edgeIDFromZoneComment(r.Comment, zr.ZoneID); ok {
			managed[edgeID] = r
		}
	}

	plan := Plan{Actions: []Action{}}
	for _, edgeID := range zr.AssignedEdgeIDs {
		ep, ok := epByID[edgeID]
		if !ok || ep.IP == "" {
			continue // assigned to an edge we don't know about / has no IP — no record
		}
		desiredDisabled := !inRotation[edgeID]
		srt, lat, long, latZone := smartFieldsFor(ep, o.Mode)
		desired := Record{
			Type:             TypeA,
			Name:             zr.Name,
			Value:            ep.IP,
			TTL:              o.TTL,
			Disabled:         desiredDisabled,
			Comment:          zoneEdgeTag(zr.ZoneID, edgeID),
			Weight:           effectiveWeight(ep, maxCap),
			SmartRoutingType: srt,
			LatencyZone:      latZone,
			GeoLatitude:      lat,
			GeoLongitude:     long,
			MonitorType:      o.MonitorType, // Bunny native monitor (Layer-2 backstop)
		}

		existing, ok := managed[edgeID]
		if !ok {
			plan.Actions = append(plan.Actions, Action{
				Kind: "add", EdgeID: edgeID, Record: desired, Name: desired.Name, Value: desired.Value,
			})
			continue
		}
		delete(managed, edgeID) // seen

		smartChanged := existing.SmartRoutingType != desired.SmartRoutingType ||
			existing.LatencyZone != desired.LatencyZone ||
			existing.Weight != desired.Weight ||
			existing.MonitorType != desired.MonitorType || // Bunny monitor on/off/type
			!floatEq(existing.GeoLatitude, desired.GeoLatitude) ||
			!floatEq(existing.GeoLongitude, desired.GeoLongitude)
		contentChanged := existing.Value != ep.IP || existing.Name != zr.Name ||
			existing.TTL != o.TTL || existing.Comment != desired.Comment || smartChanged
		disabledChanged := existing.Disabled != desiredDisabled
		if !contentChanged && !disabledChanged {
			continue // already converged
		}

		kind := "update"
		if !contentChanged && disabledChanged {
			if desiredDisabled {
				kind = "disable"
			} else {
				kind = "enable"
			}
		}
		desired.ID = existing.ID
		plan.Actions = append(plan.Actions, Action{
			Kind: kind, EdgeID: edgeID, RecordID: existing.ID, Record: desired, Name: desired.Name, Value: desired.Value,
		})
	}

	// Anything left in managed is an UNASSIGNED edge (the PoP was toggled off) or
	// an orphan → remove that record. Zero assigned edges ⇒ all removed ⇒ the
	// zone's A-set disappears (NXDOMAIN), and it has no vhost anywhere either.
	for edgeID, r := range managed {
		plan.Actions = append(plan.Actions, Action{
			Kind: "remove", EdgeID: edgeID, RecordID: r.ID, Name: r.Name, Value: r.Value,
		})
	}
	return plan
}

// OrphanZonePlan removes per-zone A records whose zone is no longer active (the
// zone was deleted). activeZoneIDs is the set of zone ids the reconciler still
// manages; any brisk:zone:<id> record outside it is swept. This complements the
// explicit delete-teardown signal (Part 4) and the zero-assigned path above so a
// deleted tenant hostname stops resolving. Never touches non-zone records.
func OrphanZonePlan(records []Record, activeZoneIDs map[int64]bool) Plan {
	plan := Plan{Actions: []Action{}}
	for _, r := range records {
		zid, ok := zoneIDFromComment(r.Comment)
		if !ok || activeZoneIDs[zid] {
			continue
		}
		plan.Actions = append(plan.Actions, Action{
			Kind: "remove", RecordID: r.ID, Name: r.Name, Value: r.Value,
		})
	}
	return plan
}
