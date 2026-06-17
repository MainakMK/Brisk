package config

import (
	"encoding/json"
	"testing"
)

// TestWireZoneHotlink guards the agent-config WIRE layer: the per-zone hotlink block
// in the control plane's /agent/config JSON must survive unmarshal into wireZone AND
// the toZone() mapping into config.Zone. (Regression guard — the render test exercises
// config.Zone directly and would NOT have caught a dropped wire field, which is exactly
// the bug that made hotlink inert on live edges until this mapping was added.)
func TestWireZoneHotlink(t *testing.T) {
	raw := `{
	  "config_version":"abc",
	  "zones":[
	    {"cdn_hostname":"on.example.com","origin_url":"http://o:80","tls_mode":"managed",
	     "hotlink":{"allowed_referrers":"good.example.com,*.good.example.com","allow_empty":false}},
	    {"cdn_hostname":"off.example.com","origin_url":"http://o:80","tls_mode":"managed"}
	  ]
	}`
	var wc wireConfig
	if err := json.Unmarshal([]byte(raw), &wc); err != nil {
		t.Fatal(err)
	}
	if len(wc.Zones) != 2 {
		t.Fatalf("want 2 zones, got %d", len(wc.Zones))
	}

	// Zone with hotlink => wireZone carries it AND toZone() maps it.
	if wc.Zones[0].Hotlink == nil {
		t.Fatal("wireZone dropped the hotlink block on unmarshal")
	}
	z0 := wc.Zones[0].toZone()
	if z0.Hotlink == nil {
		t.Fatal("toZone() dropped the hotlink block")
	}
	if z0.Hotlink.AllowedReferrers != "good.example.com,*.good.example.com" || z0.Hotlink.AllowEmpty {
		t.Errorf("hotlink fields not mapped: %+v", z0.Hotlink)
	}

	// Zone without hotlink => nil (off; renders byte-identical).
	if wc.Zones[1].Hotlink != nil || wc.Zones[1].toZone().Hotlink != nil {
		t.Error("zone without hotlink must map to nil Hotlink")
	}
}
