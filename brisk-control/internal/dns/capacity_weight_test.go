package dns

import "testing"

// Capacity-weighted routing is opt-in. With nobody opted in, effectiveWeight must equal
// today's normWeight(ep.Weight) — the byte-identical safety gate.
func TestEffectiveWeight_OptOut_IsParity(t *testing.T) {
	eps := []Endpoint{
		{EdgeID: "a", Weight: 100, CapacityMbps: 1000}, // capacity set but toggle OFF
		{EdgeID: "b", Weight: 50, CapacityMbps: 10000}, // toggle OFF
		{EdgeID: "c", Weight: 0},                       // default -> 100
	}
	maxCap := maxCapacity(eps)
	if maxCap != 0 {
		t.Fatalf("no opt-in => maxCap must be 0, got %d", maxCap)
	}
	for _, tc := range []struct {
		ep   Endpoint
		want int
	}{
		{eps[0], 100}, {eps[1], 50}, {eps[2], 100},
	} {
		if got := effectiveWeight(tc.ep, maxCap); got != tc.want {
			t.Errorf("%s: want %d (manual weight), got %d", tc.ep.EdgeID, tc.want, got)
		}
	}
}

// With the toggle ON, weight = capacity normalized to the biggest capacity-weighted box.
func TestEffectiveWeight_OptIn_Normalizes(t *testing.T) {
	eps := []Endpoint{
		{EdgeID: "big", Weight: 100, CapacityMbps: 10000, WeightByCapacity: true}, // 10 Gbps -> 100
		{EdgeID: "mid", Weight: 100, CapacityMbps: 7000, WeightByCapacity: true},  // 7 Gbps  -> 70
		{EdgeID: "small", Weight: 100, CapacityMbps: 500, WeightByCapacity: true}, // 500 Mbps-> 5
		{EdgeID: "tiny", Weight: 100, CapacityMbps: 200, WeightByCapacity: true},  // 200 Mbps-> 2
	}
	maxCap := maxCapacity(eps)
	if maxCap != 10000 {
		t.Fatalf("maxCap want 10000, got %d", maxCap)
	}
	for _, tc := range []struct {
		ep   Endpoint
		want int
	}{
		{eps[0], 100}, {eps[1], 70}, {eps[2], 5}, {eps[3], 2},
	} {
		if got := effectiveWeight(tc.ep, maxCap); got != tc.want {
			t.Errorf("%s: want %d, got %d", tc.ep.EdgeID, tc.want, got)
		}
	}
}

// Opted in but no capacity entered yet => fall back to the manual weight (never 0/blackhole).
func TestEffectiveWeight_OptIn_NoCapacityFallsBack(t *testing.T) {
	eps := []Endpoint{
		{EdgeID: "x", Weight: 100, CapacityMbps: 0, WeightByCapacity: true},
		{EdgeID: "y", Weight: 100, CapacityMbps: 4000, WeightByCapacity: true},
	}
	maxCap := maxCapacity(eps) // 4000
	if got := effectiveWeight(eps[0], maxCap); got != 100 {
		t.Errorf("x (no capacity): want 100 fallback, got %d", got)
	}
	if got := effectiveWeight(eps[1], maxCap); got != 100 {
		t.Errorf("y (4000/4000): want 100, got %d", got)
	}
}
