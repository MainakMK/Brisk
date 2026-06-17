// Package health is brisk-control's self-driven edge health checker (Phase 3
// Step 4 — fast failover).
//
// It probes each online edge on a short interval and, on failure, flips the
// edge's DNS record to Disabled IMMEDIATELY (via the reconciler) instead of
// waiting on Bunny's ~30s built-in monitor. Combined with a low TTL (~15s) this
// targets ~30-35s end-to-end failover.
//
//	detection ≈ interval × fail_threshold   (10s × 2 ≈ 20s)
//	failover  ≈ detection + TTL             (~20s + ~15s ≈ ~35s typical)
//
// Honest caveats (see README): ~30s is a TYPICAL target, NOT a guarantee — some
// resolvers cache past the TTL. In-flight HLS viewers recover via the player's
// segment retry + re-resolution, not DNS. Guaranteed instant failover needs
// Anycast (own IP space + BGP) — a future phase, out of scope here.
//
// Design choices that matter:
//   - Asymmetric thresholds: fail fast (2 consecutive fails → unhealthy), recover
//     careful (3 consecutive successes → healthy). Prevents flapping.
//   - Probes are shallow, side-effect-free, with timeout < interval so they never
//     pile up. Probes are staggered across edges (no thundering herd).
//   - The checker only persists + reconciles on a state CHANGE, never every
//     probe, so Bunny writes stay rare and rate-limit-safe.
//   - The external probe is the routing truth, distinct from the Step-2 heartbeat
//     (last_seen = "agent talked to control plane"; probe = "edge serves users").
//   - Restart resilience: last-known health is seeded from the DB on start, so a
//     restart neither blackholes nor thrashes the zone.
//   - All-down blackhole protection lives in the reconciler (dns.RotationState):
//     if every online edge is unhealthy, none are disabled (matches Bunny's own
//     all-offline behavior). The checker never special-cases this — it records
//     the honest per-edge verdict and lets the reconciler guard rotation.
package health

import (
	"context"
	"crypto/tls"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config is the network-wide health-check configuration (per-server overrides
// arrive via Target).
type Config struct {
	Enabled       bool
	Interval      time.Duration // base probe interval per edge
	Timeout       time.Duration // per-probe timeout (must be < Interval)
	FailThreshold int           // consecutive fails → unhealthy (default 2)
	RiseThreshold int           // consecutive successes → healthy (default 3)
	Path          string        // probe path (e.g. "/healthz")
	Scheme        string        // "https" | "http"
	Port          int           // 0 = scheme default
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = 10 * time.Second
	}
	if c.Timeout <= 0 || c.Timeout >= c.Interval {
		c.Timeout = c.Interval / 3
		if c.Timeout <= 0 {
			c.Timeout = 3 * time.Second
		}
	}
	if c.FailThreshold <= 0 {
		c.FailThreshold = 2
	}
	if c.RiseThreshold <= 0 {
		c.RiseThreshold = 3
	}
	if c.Path == "" {
		c.Path = "/healthz"
	}
	if c.Scheme == "" {
		c.Scheme = "https"
	}
	return c
}

// Target is one edge to probe, with its persisted health and any per-server
// overrides (0 = inherit network default).
type Target struct {
	ServerID        int64
	EdgeID          string
	Host            string // hostname (preferred) or IP to probe
	Persisted       string // last-known health from DB (seed on first sight)
	Enabled         bool   // per-server health-check switch
	IntervalSeconds int    // override; 0 = inherit
	FailThreshold   int    // override; 0 = inherit
	RiseThreshold   int    // override; 0 = inherit
}

// Status is the public per-edge health snapshot (for GET /health/status).
type Status struct {
	ServerID      int64     `json:"server_id"`
	EdgeID        string    `json:"edge_id"`
	Host          string    `json:"host"`
	State         string    `json:"state"` // healthy | unhealthy | unknown
	Healthy       bool      `json:"healthy"`
	ConsecFails   int       `json:"consecutive_fails"`
	ConsecOK      int       `json:"consecutive_successes"`
	LastProbe     time.Time `json:"last_probe,omitempty"`
	LastLatencyMs int64     `json:"last_latency_ms"`
	LastError     string    `json:"last_error,omitempty"`
	Probing       bool      `json:"probing"`
	Enabled       bool      `json:"check_enabled"`
	IntervalSec   int       `json:"interval_seconds"`
	FailThreshold int       `json:"fail_threshold"`
	RiseThreshold int       `json:"rise_threshold"`
}

// TargetsFunc returns the edges to probe (mapped from the servers table).
type TargetsFunc func(ctx context.Context) ([]Target, error)

// TransitionFunc is called when an edge flips healthy<->unhealthy. The
// implementation persists the new health to the DB, audits the change, and
// triggers an immediate reconcile so DNS follows.
type TransitionFunc func(ctx context.Context, t Target, healthy bool)

type edgeState struct {
	target      Target
	current     string // healthy | unhealthy | unknown
	consecFails int
	consecOK    int
	lastProbe   time.Time
	lastLatency time.Duration
	lastErr     string
	inflight    bool
	nextDue     time.Time
}

// Checker runs the probe loop.
type Checker struct {
	cfg        Config
	targets    TargetsFunc
	onTrans    TransitionFunc
	http       *http.Client
	mu         sync.Mutex
	state      map[string]*edgeState
	lastReload time.Time
}

// New builds a Checker. Returns nil if disabled or no targets source wired.
func New(cfg Config, targets TargetsFunc, onTrans TransitionFunc) *Checker {
	if !cfg.Enabled || targets == nil {
		return nil
	}
	cfg = cfg.withDefaults()
	return &Checker{
		cfg:     cfg,
		targets: targets,
		onTrans: onTrans,
		http: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				// Liveness probe: we check that the edge SERVES, not that its
				// cert validates — and we may probe by IP (no SNI match). Skipping
				// verification here is intentional and scoped to health probes.
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
				DisableKeepAlives:   true,
				MaxIdleConns:        4,
				IdleConnTimeout:     30 * time.Second,
				TLSHandshakeTimeout: cfg.Timeout,
			},
		},
		state: map[string]*edgeState{},
	}
}

// Run is the probe loop: a 1s scheduler ticks each edge on its own (possibly
// overridden) interval; probes run concurrently and report back. State mutation
// is serialized through the loop. Stops when ctx is cancelled.
func (c *Checker) Run(ctx context.Context) {
	if c == nil {
		return
	}
	slog.Info("health checker running",
		"interval", c.cfg.Interval, "timeout", c.cfg.Timeout,
		"fail_threshold", c.cfg.FailThreshold, "rise_threshold", c.cfg.RiseThreshold,
		"path", c.cfg.Path, "scheme", c.cfg.Scheme)

	c.reload(ctx) // seed from DB immediately (restart resilience)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	results := make(chan probeResult, 64)

	for {
		select {
		case <-ctx.Done():
			return
		case res := <-results:
			c.handleResult(ctx, res)
		case now := <-tick.C:
			if now.Sub(c.lastReload) >= 5*time.Second {
				c.reload(ctx)
			}
			c.scheduleDue(ctx, now, results)
		}
	}
}

// reload refreshes the target set from the store, adding new edges (seeded from
// their persisted health so a restart doesn't churn) and dropping gone ones.
func (c *Checker) reload(ctx context.Context) {
	targets, err := c.targets(ctx)
	if err != nil {
		slog.Warn("health: load targets failed", "err", err.Error())
		return
	}
	c.lastReload = time.Now()
	seen := map[string]bool{}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range targets {
		seen[t.EdgeID] = true
		es, ok := c.state[t.EdgeID]
		if !ok {
			// New edge: seed current state from the DB (no transition fired).
			es = &edgeState{
				target:  t,
				current: normState(t.Persisted),
				nextDue: time.Now().Add(c.stagger(t)),
			}
			c.state[t.EdgeID] = es
			continue
		}
		es.target = t // refresh host/overrides
	}
	for id := range c.state {
		if !seen[id] {
			delete(c.state, id)
		}
	}
}

// scheduleDue launches probes for every enabled edge whose interval has elapsed.
func (c *Checker) scheduleDue(ctx context.Context, now time.Time, results chan<- probeResult) {
	c.mu.Lock()
	due := make([]Target, 0)
	for _, es := range c.state {
		if !es.target.Enabled {
			continue // health check disabled for this PoP
		}
		if es.inflight || now.Before(es.nextDue) {
			continue
		}
		es.inflight = true
		es.nextDue = now.Add(c.interval(es.target))
		due = append(due, es.target)
	}
	c.mu.Unlock()

	for _, t := range due {
		go func(t Target) {
			ok, lat, errMsg := c.probe(ctx, t.Host)
			select {
			case results <- probeResult{edgeID: t.EdgeID, ok: ok, latency: lat, errMsg: errMsg, at: time.Now()}:
			case <-ctx.Done():
			}
		}(t)
	}
}

type probeResult struct {
	edgeID  string
	ok      bool
	latency time.Duration
	errMsg  string
	at      time.Time
}

// handleResult applies a probe result, updating counters and firing a transition
// (persist + reconcile) only when the health state actually changes.
func (c *Checker) handleResult(ctx context.Context, res probeResult) {
	c.mu.Lock()
	es, ok := c.state[res.edgeID]
	if !ok {
		c.mu.Unlock()
		return
	}
	es.inflight = false
	es.lastProbe = res.at
	es.lastLatency = res.latency
	es.lastErr = res.errMsg

	failTh := c.failThreshold(es.target)
	riseTh := c.riseThreshold(es.target)

	var transition *bool // nil = none; pointer to healthy bool
	var counterAtTransition int
	if res.ok {
		es.consecOK++
		es.consecFails = 0
		if es.current != "healthy" && es.consecOK >= riseTh {
			es.current = "healthy"
			v := true
			transition = &v
			counterAtTransition = es.consecOK
		}
	} else {
		es.consecFails++
		es.consecOK = 0
		if es.current != "unhealthy" && es.consecFails >= failTh {
			es.current = "unhealthy"
			v := false
			transition = &v
			counterAtTransition = es.consecFails
		}
	}
	t := es.target // copy out under lock; don't touch es after unlock
	c.mu.Unlock()

	if transition != nil && c.onTrans != nil {
		slog.Info("health transition",
			"edge_id", res.edgeID, "healthy", *transition,
			"streak", counterAtTransition, "latency_ms", res.latency.Milliseconds(),
			"err", res.errMsg)
		c.onTrans(ctx, t, *transition)
	}
}

// probe does one shallow GET; healthy = a response with status < 500 within the
// timeout. Connection refused / timeout / 5xx all count as a failure.
func (c *Checker) probe(ctx context.Context, host string) (ok bool, latency time.Duration, errMsg string) {
	url := c.url(host)
	pctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, url, nil)
	if err != nil {
		return false, 0, "build request: " + err.Error()
	}
	req.Header.Set("User-Agent", "brisk-health/1.0")
	start := time.Now()
	resp, err := c.http.Do(req)
	lat := time.Since(start)
	if err != nil {
		return false, lat, trimErr(err.Error())
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 500 {
		return false, lat, "status " + strconv.Itoa(resp.StatusCode)
	}
	return true, lat, ""
}

func (c *Checker) url(host string) string {
	hostport := host
	if c.cfg.Port != 0 {
		hostport = net.JoinHostPort(host, strconv.Itoa(c.cfg.Port))
	}
	path := c.cfg.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return fmt.Sprintf("%s://%s%s", c.cfg.Scheme, hostport, path)
}

// --- effective per-edge config (override else network default) ---

func (c *Checker) interval(t Target) time.Duration {
	if t.IntervalSeconds > 0 {
		return time.Duration(t.IntervalSeconds) * time.Second
	}
	return c.cfg.Interval
}

func (c *Checker) failThreshold(t Target) int {
	if t.FailThreshold > 0 {
		return t.FailThreshold
	}
	return c.cfg.FailThreshold
}

func (c *Checker) riseThreshold(t Target) int {
	if t.RiseThreshold > 0 {
		return t.RiseThreshold
	}
	return c.cfg.RiseThreshold
}

// stagger spreads each edge's first probe across the interval window (by a stable
// hash of the edge id) so probes don't all fire on the same tick.
func (c *Checker) stagger(t Target) time.Duration {
	iv := c.interval(t)
	secs := uint32(iv/time.Second) + 1
	h := fnv.New32a()
	_, _ = h.Write([]byte(t.EdgeID))
	return time.Duration(h.Sum32()%secs) * time.Second
}

// Snapshot returns the current per-edge health (for GET /health/status). Safe to
// call concurrently with the probe loop.
func (c *Checker) Snapshot() []Status {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Status, 0, len(c.state))
	for _, es := range c.state {
		out = append(out, Status{
			ServerID:      es.target.ServerID,
			EdgeID:        es.target.EdgeID,
			Host:          es.target.Host,
			State:         es.current,
			Healthy:       es.current == "healthy",
			ConsecFails:   es.consecFails,
			ConsecOK:      es.consecOK,
			LastProbe:     es.lastProbe,
			LastLatencyMs: es.lastLatency.Milliseconds(),
			LastError:     es.lastErr,
			Probing:       es.inflight,
			Enabled:       es.target.Enabled,
			IntervalSec:   int(c.interval(es.target) / time.Second),
			FailThreshold: c.failThreshold(es.target),
			RiseThreshold: c.riseThreshold(es.target),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EdgeID < out[j].EdgeID })
	return out
}

func normState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "healthy":
		return "healthy"
	case "unhealthy":
		return "unhealthy"
	default:
		return "unknown"
	}
}

func trimErr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
