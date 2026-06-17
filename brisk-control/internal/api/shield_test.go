package api

import (
	"testing"

	"brisk-control/internal/store"
)

// TestShieldUpstreamFor proves every Phase 4 Step 3 guard in the control-plane
// computation: opt-in, never-shield-through-self, the shield PoP goes to origin,
// the target must be a role=shield server, and a dead shield degrades to origin.
func TestShieldUpstreamFor(t *testing.T) {
	hn := func(s string) *string { return &s }
	edge1 := store.Server{ID: 1, EdgeID: "EDGE-1", Role: "edge", Status: "online"}
	edge2 := store.Server{ID: 2, EdgeID: "EDGE-2", Role: "edge", Status: "online"}
	shield := store.Server{ID: 9, EdgeID: "SHIELD-1", Role: "shield", Status: "online",
		Hostname: hn("shield.brisk.net"), HealthStatus: "healthy"}
	byID := map[int64]store.Server{1: edge1, 2: edge2, 9: shield}

	sid := func(id int64) *int64 { return &id }
	zOn := store.Zone{ID: 7, OriginShieldEnabled: true, ShieldServerID: sid(9)}
	zOff := store.Zone{ID: 7, OriginShieldEnabled: false, ShieldServerID: sid(9)}

	cases := []struct {
		name      string
		zone      store.Zone
		me        store.Server
		defShield int64
		want      string
	}{
		{"edge, shield on, healthy -> shield", zOn, edge1, 0, "shield.brisk.net:443"},
		{"shield off -> origin", zOff, edge1, 0, ""},
		{"the shield PoP itself -> origin", zOn, shield, 0, ""},
		{"shield_server_id == me (self) -> origin", store.Zone{OriginShieldEnabled: true, ShieldServerID: sid(1)}, edge1, 0, ""},
		{"network default used when zone has none", store.Zone{OriginShieldEnabled: true}, edge1, 9, "shield.brisk.net:443"},
		{"no shield resolved -> origin", store.Zone{OriginShieldEnabled: true}, edge1, 0, ""},
	}
	for _, c := range cases {
		if got := shieldUpstreamFor(c.zone, c.me, byID, c.defShield); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}

	// target isn't a role=shield server (misconfig) -> skip (origin), no loop/misroute.
	bad := map[int64]store.Server{1: edge1, 2: edge2}
	if got := shieldUpstreamFor(store.Zone{OriginShieldEnabled: true, ShieldServerID: sid(2)}, edge1, bad, 0); got != "" {
		t.Errorf("non-shield target should yield origin, got %q", got)
	}

	// dead shield (drained / unhealthy / offline) -> graceful fallback to origin.
	for _, dead := range []store.Server{
		{ID: 9, Role: "shield", Status: "online", Drained: true, Hostname: hn("s")},
		{ID: 9, Role: "shield", Status: "online", HealthStatus: "unhealthy", Hostname: hn("s")},
		{ID: 9, Role: "shield", Status: "offline", Hostname: hn("s")},
	} {
		bm := map[int64]store.Server{1: edge1, 9: dead}
		if got := shieldUpstreamFor(zOn, edge1, bm, 0); got != "" {
			t.Errorf("dead shield (%+v) should degrade to origin, got %q", dead, got)
		}
	}
}

// TestConfigETagShieldSensitive: the ETag must change when a zone's computed shield
// upstream changes (e.g. the shield went unhealthy => upstream flips to origin), so
// edges re-pull and switch tiers even without a zone edit.
func TestConfigETagShieldSensitive(t *testing.T) {
	zones := []store.Zone{{ID: 7, ConfigVersion: 2}}
	shielded := []agentZone{{CDNHostname: "a.example.com", ShieldUpstream: "shield:443"}}
	direct := []agentZone{{CDNHostname: "a.example.com", ShieldUpstream: ""}}
	if configETag(zones, shielded) == configETag(zones, direct) {
		t.Errorf("ETag must change when the computed shield upstream flips")
	}
}
