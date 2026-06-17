package dns

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Bunny native monitor (Layer-2 failover backstop). These tests pin the two
// invariants that keep it safe to run alongside Brisk's own health checker:
//   1. OFF (MonitorType 0) is byte-identical — no spurious update is planned for
//      a record that already has no monitor.
//   2. Brisk writes MonitorType (it owns on/off) but NEVER writes MonitorStatus
//      (Bunny owns the live offline/online verdict) — so the two brains can't
//      fight: our partial-merge writes always omit MonitorStatus.

func baseOpts(monitor int) DiffOpts {
	return DiffOpts{
		RoutingName: "cdn",
		TTL:         15,
		StaleAfter:  60 * time.Second,
		Mode:        ModeGeographic,
		MonitorType: monitor,
		Now:         time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC),
	}
}

// OFF + a record that never had a monitor => no plan action (byte-identical).
func TestDiff_MonitorOff_IsByteIdentical(t *testing.T) {
	eps := []Endpoint{{EdgeID: "US-NY", IP: "1.2.3.4", Status: "online", Health: "healthy",
		LastSeen: ptrTime(time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC))}}
	// Existing record exactly matches the desired off-state (MonitorType 0). The
	// endpoint has no Region, so smartFieldsFor yields SmartNone/0-coords — match it
	// so the ONLY thing under test is the monitor field.
	existing := []Record{{
		ID: 10, Type: TypeA, Name: "cdn", Value: "1.2.3.4", TTL: 15,
		Comment: ServerTag("US-NY"), Weight: 100,
		SmartRoutingType: SmartNone, MonitorType: MonitorNone,
	}}
	plan := Diff(eps, existing, baseOpts(MonitorNone))
	if !plan.Empty() {
		t.Fatalf("monitor off + converged record => empty plan, got %d actions: %+v", len(plan.Actions), plan.Actions)
	}
}

// ON => the desired record carries the Ping monitor, and a record that lacks it
// is planned for update (so enabling the flag actually writes the monitor).
func TestDiff_MonitorOn_PlansUpdate(t *testing.T) {
	eps := []Endpoint{{EdgeID: "US-NY", IP: "1.2.3.4", Status: "online", Health: "healthy",
		LastSeen: ptrTime(time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC))}}
	existing := []Record{{
		ID: 10, Type: TypeA, Name: "cdn", Value: "1.2.3.4", TTL: 15,
		Comment: ServerTag("US-NY"), Weight: 100,
		SmartRoutingType: SmartNone, MonitorType: MonitorNone, // converged except monitor
	}}
	plan := Diff(eps, existing, baseOpts(MonitorPing))
	if len(plan.Actions) != 1 || plan.Actions[0].Kind != "update" {
		t.Fatalf("monitor on => one update action, got %+v", plan.Actions)
	}
	if plan.Actions[0].Record.MonitorType != MonitorPing {
		t.Fatalf("desired record must carry MonitorPing(%d), got %d", MonitorPing, plan.Actions[0].Record.MonitorType)
	}
}

// The serialized desired record must include MonitorType but NEVER MonitorStatus
// (Bunny-owned). This is the anti-fight invariant.
func TestRecord_NeverWritesMonitorStatus(t *testing.T) {
	r := Record{
		Type: TypeA, Name: "cdn", Value: "1.2.3.4", TTL: 15,
		Comment: ServerTag("US-NY"), MonitorType: MonitorPing,
		// MonitorStatus left at zero — and even if Bunny had set it on a read,
		// we build `desired` fresh, so it's 0 here and omitempty drops it.
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, `"MonitorType":1`) {
		t.Errorf("body must set MonitorType: %s", body)
	}
	if strings.Contains(body, "MonitorStatus") {
		t.Errorf("body must NOT write MonitorStatus (Bunny-owned): %s", body)
	}
}

// MonitorTypeFromString maps config strings to the Bunny enum, defaulting to Ping.
func TestMonitorTypeFromString(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"ping", MonitorPing}, {"PING", MonitorPing}, {"http", MonitorHTTP},
		{"none", MonitorNone}, {"off", MonitorNone}, {"", MonitorNone},
		{"garbage", MonitorPing}, // unknown => safest (ping)
	} {
		if got := MonitorTypeFromString(tc.in); got != tc.want {
			t.Errorf("%q: want %d, got %d", tc.in, tc.want, got)
		}
	}
}

func ptrTime(t time.Time) *time.Time { return &t }
