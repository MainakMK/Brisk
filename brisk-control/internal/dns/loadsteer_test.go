package dns

import (
	"context"
	"testing"
	"time"
)

// With no load factor (zero value) the weight is exactly the base — load steering off
// is byte-identical to #2 / manual weighting.
func TestEffectiveWeight_LoadFactor_OffIsParity(t *testing.T) {
	eps := []Endpoint{
		{EdgeID: "a", Weight: 100}, // LoadFactor 0 => neutral
		{EdgeID: "b", Weight: 100, CapacityMbps: 10000, WeightByCapacity: true},
		{EdgeID: "c", Weight: 40},
	}
	maxCap := maxCapacity(eps)
	for _, tc := range []struct {
		ep   Endpoint
		want int
	}{
		{eps[0], 100}, {eps[1], 100}, {eps[2], 40},
	} {
		if got := effectiveWeight(tc.ep, maxCap); got != tc.want {
			t.Errorf("%s: want %d (no steering), got %d", tc.ep.EdgeID, tc.want, got)
		}
	}
}

// A hot edge's factor scales its base weight down (rounded), but never below 1.
func TestEffectiveWeight_LoadFactor_ScalesAndFloors(t *testing.T) {
	cases := []struct {
		name   string
		ep     Endpoint
		maxCap int
		want   int
	}{
		{"half of 100", Endpoint{Weight: 100, LoadFactor: 0.5}, 0, 50},
		{"0.3 of 100", Endpoint{Weight: 100, LoadFactor: 0.3}, 0, 30},
		{"rounds 0.337*70", Endpoint{Weight: 70, LoadFactor: 0.337}, 0, 24}, // 23.59 -> 24
		{"floor at 1", Endpoint{Weight: 2, LoadFactor: 0.1}, 0, 1},          // 0.2 -> floored 1
		{"factor>=1 ignored", Endpoint{Weight: 80, LoadFactor: 1.2}, 0, 80},
		{"capacity base then factor", Endpoint{Weight: 100, CapacityMbps: 5000, WeightByCapacity: true, LoadFactor: 0.5}, 10000, 25}, // base 50 -> 25
	}
	for _, tc := range cases {
		if got := effectiveWeight(tc.ep, tc.maxCap); got != tc.want {
			t.Errorf("%s: want %d, got %d", tc.name, tc.want, got)
		}
	}
}

// The low/high ramp: cool => 1.0, hot => MinFactor, linear between.
func TestLoadController_FactorFor_Ramp(t *testing.T) {
	c := NewLoadController(func(context.Context) (map[string]float64, error) { return nil, nil }, nil,
		LoadSteerOpts{LowWater: 0.5, HighWater: 0.9, MinFactor: 0.2})
	cases := []struct {
		pressure float64
		want     float64
	}{
		{0.0, 1.0},
		{0.5, 1.0}, // at low water
		{0.9, 0.2}, // at high water
		{1.0, 0.2}, // above high water clamps
		{0.7, 0.6}, // midpoint: 1 - 0.5*(0.8) = 0.6
	}
	for _, tc := range cases {
		if got := c.factorFor(tc.pressure); absDiff(got, tc.want) > 1e-9 {
			t.Errorf("factorFor(%.2f): want %.3f, got %.3f", tc.pressure, tc.want, got)
		}
	}
}

// A nil controller is fully neutral (steering disabled): Factor 1.0, Run a no-op,
// Enabled false, empty Snapshot.
func TestLoadController_NilIsNeutral(t *testing.T) {
	var c *LoadController
	if c.Factor("x") != 1.0 {
		t.Error("nil controller Factor must be 1.0")
	}
	if c.Enabled() {
		t.Error("nil controller must report disabled")
	}
	if c.Snapshot() != nil {
		t.Error("nil controller Snapshot must be nil")
	}
	c.Run(context.Background()) // must not panic / block
}

// refresh smooths pressure (EWMA), publishes a factor past the deadband, and reports
// the change so the syncer is triggered; a tiny subsequent move stays within the
// deadband (no thrash).
func TestLoadController_Refresh_SmoothsAndDeadbands(t *testing.T) {
	pressure := 0.95 // hot
	c := NewLoadController(
		func(context.Context) (map[string]float64, error) {
			return map[string]float64{"hot": pressure}, nil
		},
		nil,
		LoadSteerOpts{Alpha: 1.0, LowWater: 0.5, HighWater: 0.9, MinFactor: 0.2, Deadband: 0.07},
	)
	if !c.refresh(context.Background()) {
		t.Fatal("first hot sample should change the factor (1.0 -> 0.2)")
	}
	if f := c.Factor("hot"); absDiff(f, 0.2) > 1e-9 {
		t.Fatalf("hot factor want 0.2, got %.3f", f)
	}
	// A second identical sample is steady => no change.
	if c.refresh(context.Background()) {
		t.Error("identical sample should not re-trigger (deadband)")
	}
	// Cooling fully back returns to neutral (always allowed past the deadband).
	pressure = 0.1
	if !c.refresh(context.Background()) {
		t.Error("cooling to neutral should change the factor back to 1.0")
	}
	if f := c.Factor("hot"); absDiff(f, 1.0) > 1e-9 {
		t.Errorf("cooled factor want 1.0, got %.3f", f)
	}
}

// Snapshot reflects sampled edges, sorted by edge id.
func TestLoadController_Snapshot(t *testing.T) {
	c := NewLoadController(
		func(context.Context) (map[string]float64, error) {
			return map[string]float64{"b": 0.95, "a": 0.1}, nil
		}, nil, LoadSteerOpts{})
	c.refresh(context.Background())
	snap := c.Snapshot()
	if len(snap) != 2 || snap[0].EdgeID != "a" || snap[1].EdgeID != "b" {
		t.Fatalf("snapshot should be sorted [a,b], got %+v", snap)
	}
	if snap[1].Factor >= 1.0 {
		t.Errorf("hot edge b should have factor <1, got %.3f", snap[1].Factor)
	}
}

// withDefaults fills sane values and keeps a degenerate high<=low from breaking the ramp.
func TestLoadSteerOpts_Defaults(t *testing.T) {
	o := LoadSteerOpts{}
	o.withDefaults()
	if o.Interval != 45*time.Second || o.Alpha != 0.4 || o.MinFactor != 0.30 {
		t.Errorf("unexpected defaults: %+v", o)
	}
	if o.HighWater <= o.LowWater {
		t.Errorf("high water (%.2f) must exceed low water (%.2f)", o.HighWater, o.LowWater)
	}
}
