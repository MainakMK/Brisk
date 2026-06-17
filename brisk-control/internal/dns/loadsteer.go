package dns

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Load-feedback steering (#3) — the live half of Brisk load steering.
//
// #2 (capacity → weight) sets each edge's STATIC base DNS weight from its box size.
// #3 watches each edge's LIVE pressure (load ÷ capacity) and nudges that SAME weight:
// a hot edge's weight is scaled DOWN so Bunny sends it a smaller slice and its cooler
// peers absorb the rest; when it cools, the weight returns to base. It is the slow,
// network-wide steering knob (rides DNS, ~TTL latency) — distinct from #4, the edge's
// instant local 503 brake.
//
// It is deliberately:
//   - GATED + off by default (master env switch). Off => every Factor is 1.0 => DNS
//     renders byte-identical to today.
//   - DAMPED. Raw pressure is EWMA-smoothed, then mapped through a gentle ramp, then a
//     deadband suppresses sub-threshold wiggle — so weights don't oscillate every tick.
//   - BOUNDED. The factor never drops below MinFactor, so load alone can shrink an
//     edge's share but never blackhole it (drain/health own full removal).
//   - CONTROL-PLANE-LOCAL state. Factors live in memory; a CP restart resets them to
//     neutral and the next reconcile writes base weights. While the CP is DOWN, Bunny
//     simply keeps the last weights it was given (Golden Rule #3: data plane independent).

// LoadFunc returns the current raw pressure for each edge, keyed by edge id. Pressure
// is a unitless saturation in [0,1] (e.g. max(cpu%, bandwidth/capacity)). An edge
// absent from the map has no usable load signal this tick and is left at its previous
// smoothed value (or neutral if never seen).
type LoadFunc func(ctx context.Context) (map[string]float64, error)

// LoadSteerOpts tune the controller. Zero values are replaced with safe defaults by
// NewLoadController, so a caller can pass only the fields it cares about.
type LoadSteerOpts struct {
	Interval  time.Duration // how often to sample live load (default 45s)
	Alpha     float64       // EWMA weight for the new sample, 0<α≤1 (default 0.4 = smooth)
	LowWater  float64       // pressure at/below which factor = 1.0 (default 0.55)
	HighWater float64       // pressure at/above which factor = MinFactor (default 0.90)
	MinFactor float64       // floor on the factor — never fully drains (default 0.30)
	Deadband  float64       // min factor change before we re-trigger DNS (default 0.07)
}

func (o *LoadSteerOpts) withDefaults() {
	if o.Interval <= 0 {
		o.Interval = 45 * time.Second
	}
	if o.Alpha <= 0 || o.Alpha > 1 {
		o.Alpha = 0.4
	}
	if o.LowWater <= 0 {
		o.LowWater = 0.55
	}
	if o.HighWater <= 0 || o.HighWater <= o.LowWater {
		o.HighWater = 0.90
	}
	if o.HighWater <= o.LowWater { // still degenerate (caller passed a high LowWater) — widen
		o.HighWater = o.LowWater + 0.2
	}
	if o.MinFactor <= 0 || o.MinFactor >= 1 {
		o.MinFactor = 0.30
	}
	if o.Deadband <= 0 {
		o.Deadband = 0.07
	}
}

// loadState is the per-edge smoothed pressure + the factor last published to DNS.
type loadState struct {
	smoothed    float64 // EWMA of raw pressure
	factor      float64 // factor currently reflected in DNS (post-deadband)
	hasData     bool    // a real sample has been seen at least once
	lastPressed float64 // most recent raw pressure (for the API snapshot)
}

// LoadController samples live edge pressure on a timer, smooths it, maps it to a DNS
// weight factor, and triggers a DNS reconcile when a factor moves past the deadband.
// The reconcile reads the published factor via Factor() (wired into the endpoints
// closure), so the controller never touches the dns plan directly.
type LoadController struct {
	opts    LoadSteerOpts
	load    LoadFunc
	trigger func(reason string) // usually syncer.Trigger

	mu    sync.RWMutex
	state map[string]*loadState
}

// NewLoadController builds a controller. Returns nil when load is nil (load steering
// disabled) so callers can leave the wiring in place unconditionally — a nil
// *LoadController's Factor() returns the neutral 1.0 and Run() is a no-op.
func NewLoadController(load LoadFunc, trigger func(string), opts LoadSteerOpts) *LoadController {
	if load == nil {
		return nil
	}
	opts.withDefaults()
	return &LoadController{
		opts:    opts,
		load:    load,
		trigger: trigger,
		state:   map[string]*loadState{},
	}
}

// Factor returns the DNS weight multiplier currently published for an edge: 1.0 when
// load steering is off, the edge is unknown, or it has no load data yet. Nil-safe.
func (c *LoadController) Factor(edgeID string) float64 {
	if c == nil {
		return 1.0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if st, ok := c.state[edgeID]; ok && st.hasData {
		return st.factor
	}
	return 1.0
}

// factorFor maps a smoothed pressure to a DNS weight factor via the low/high ramp.
//   - pressure ≤ LowWater  → 1.0 (cool: full share)
//   - pressure ≥ HighWater → MinFactor (hot: floored share, never zero)
//   - in between           → linear ramp from 1.0 down to MinFactor
func (c *LoadController) factorFor(pressure float64) float64 {
	o := c.opts
	if pressure <= o.LowWater {
		return 1.0
	}
	if pressure >= o.HighWater {
		return o.MinFactor
	}
	frac := (pressure - o.LowWater) / (o.HighWater - o.LowWater) // 0..1
	return 1.0 - frac*(1.0-o.MinFactor)
}

// refresh samples load once, updates the smoothed pressure + factor for every edge,
// and reports whether any published factor moved past the deadband (=> DNS reconcile).
func (c *LoadController) refresh(ctx context.Context) (changed bool) {
	pressures, err := c.load(ctx)
	if err != nil {
		slog.Warn("load steer: sample failed", "err", err.Error())
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for edgeID, raw := range pressures {
		if raw < 0 {
			raw = 0
		}
		if raw > 1 {
			raw = 1
		}
		st := c.state[edgeID]
		if st == nil {
			st = &loadState{smoothed: raw, factor: 1.0}
			c.state[edgeID] = st
		} else {
			st.smoothed = c.opts.Alpha*raw + (1-c.opts.Alpha)*st.smoothed
		}
		st.lastPressed = raw
		st.hasData = true

		want := c.factorFor(st.smoothed)
		if absDiff(want, st.factor) >= c.opts.Deadband ||
			(want == 1.0 && st.factor != 1.0) { // always allow a full return to neutral
			st.factor = want
			changed = true
		}
	}
	return changed
}

// Run is the background sampling loop. It samples once shortly after start, then every
// Interval, triggering a DNS reconcile whenever a factor crosses the deadband. No-op on
// a nil controller (load steering disabled). Returns when ctx is cancelled.
func (c *LoadController) Run(ctx context.Context) {
	if c == nil {
		return
	}
	slog.Info("load steer running", "interval", c.opts.Interval, "low", c.opts.LowWater,
		"high", c.opts.HighWater, "min_factor", c.opts.MinFactor)
	first := time.NewTimer(5 * time.Second) // let the first stats arrive
	defer first.Stop()
	ticker := time.NewTicker(c.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			if c.refresh(ctx) && c.trigger != nil {
				c.trigger("load_steer")
			}
		case <-ticker.C:
			if c.refresh(ctx) && c.trigger != nil {
				c.trigger("load_steer")
			}
		}
	}
}

// LoadStatus is the read-only per-edge steering view for the API (/dns/routing).
type LoadStatus struct {
	EdgeID   string  `json:"edge_id"`
	Pressure float64 `json:"pressure"` // last raw sample (0..1)
	Smoothed float64 `json:"smoothed"` // EWMA pressure (0..1)
	Factor   float64 `json:"factor"`   // weight multiplier currently in DNS (0..1]
}

// Snapshot returns the current per-edge steering state, sorted by edge id (stable API
// output). Nil/empty when steering is off or nothing has been sampled yet.
func (c *LoadController) Snapshot() []LoadStatus {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]LoadStatus, 0, len(c.state))
	for id, st := range c.state {
		if !st.hasData {
			continue
		}
		out = append(out, LoadStatus{EdgeID: id, Pressure: st.lastPressed, Smoothed: st.smoothed, Factor: st.factor})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EdgeID < out[j].EdgeID })
	return out
}

// Enabled reports whether load steering is active (a non-nil controller).
func (c *LoadController) Enabled() bool { return c != nil }

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}
