package dns

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// EndpointsFunc returns the current edges the control plane knows about (mapped
// from the servers table by the caller).
type EndpointsFunc func(ctx context.Context) ([]Endpoint, error)

// AuditFunc records the DNS actions that were applied (for the audit trail).
type AuditFunc func(ctx context.Context, applied []Action, reason string)

// SyncOpts configure the reconciler/syncer.
type SyncOpts struct {
	RoutingName string        // record set name (e.g. "cdn")
	TTL         int           // routing record TTL (seconds)
	StaleAfter  time.Duration // heartbeat staleness => treat edge offline
	Periodic    time.Duration // safety reconcile interval (drift correction)
	Debounce    time.Duration // coalesce a burst of triggers into one run
	Mode        RoutingMode   // network-wide Smart-Record routing mode
	MonitorType int           // Bunny native record monitor (0=off; Layer-2 backstop)
}

// NetworkMode returns the configured network-wide routing mode (defaulting to
// geographic). Exposed so the API can surface the effective routing config.
func (s *Syncer) NetworkMode() RoutingMode {
	if s == nil || s.opts.Mode == "" {
		return ModeGeographic
	}
	return s.opts.Mode
}

// StaleAfterDur exposes the heartbeat staleness threshold (for effective-routing
// online derivation in the API).
func (s *Syncer) StaleAfterDur() time.Duration {
	if s == nil {
		return 60 * time.Second
	}
	return s.opts.StaleAfter
}

// Syncer converges Bunny DNS to the servers table. Lifecycle events Trigger a
// debounced reconcile; a periodic ticker catches drift. Reconciles are
// serialized so manual + background runs never overlap.
type Syncer struct {
	client    *Client
	opts      SyncOpts
	endpoints EndpointsFunc
	zones     ZonesFunc // Part 2.5: per-zone Smart A-sets (nil = per-zone DNS off)
	audit     AuditFunc
	trigger   chan string
	mu        sync.Mutex
}

// NewSyncer builds a syncer. Returns nil if the client is nil (DNS not configured).
// zones may be nil (per-zone DNS off); the apex `cdn` set + wildcard still run.
func NewSyncer(client *Client, opts SyncOpts, endpoints EndpointsFunc, zones ZonesFunc, audit AuditFunc) *Syncer {
	if client == nil {
		return nil
	}
	if opts.Debounce <= 0 {
		opts.Debounce = 3 * time.Second
	}
	if opts.Periodic <= 0 {
		opts.Periodic = 2 * time.Minute
	}
	if opts.StaleAfter <= 0 {
		opts.StaleAfter = 60 * time.Second
	}
	return &Syncer{
		client:    client,
		opts:      opts,
		endpoints: endpoints,
		zones:     zones,
		audit:     audit,
		trigger:   make(chan string, 1),
	}
}

// Trigger schedules a reconcile (non-blocking; coalesces if one is already queued).
func (s *Syncer) Trigger(reason string) {
	if s == nil {
		return
	}
	select {
	case s.trigger <- reason:
	default: // a reconcile is already queued — coalesce
	}
}

// Reconcile fetches the current servers + zone records, computes the diff, and
// (unless dryRun) applies it, auditing each applied action. Serialized.
func (s *Syncer) Reconcile(ctx context.Context, dryRun bool, reason string) (Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.client.ListRecords(ctx)
	if err != nil {
		return Plan{}, err
	}
	eps, err := s.endpoints(ctx)
	if err != nil {
		return Plan{}, err
	}
	plan := Diff(eps, records, DiffOpts{
		RoutingName: s.opts.RoutingName,
		TTL:         s.opts.TTL,
		StaleAfter:  s.opts.StaleAfter,
		Mode:        s.NetworkMode(),
		MonitorType: s.opts.MonitorType,
		Now:         time.Now().UTC(),
	})
	// Step 4.7 Part 2: ensure the tenant wildcard CNAME (*.<routing> -> <routing>.<zone>)
	// so every <tenant>.cdn.<zone> resolves to the geo-routed A-set with no per-zone DNS
	// write. Self-heals on every reconcile; best-effort (a Bunny hiccup here must not
	// block the A-set reconcile). Skipped on dry-run.
	if !dryRun {
		if _, werr := s.client.EnsureWildcardCNAME(ctx, s.opts.RoutingName, s.opts.TTL); werr != nil {
			slog.Warn("dns wildcard cname ensure failed", "reason", reason, "err", werr.Error())
		}
	}
	if dryRun {
		return plan, nil // preview is apex-only; per-zone changes nothing on dry-run
	}

	// Apply the apex `cdn` plan (if any).
	var applyErr error
	if !plan.Empty() {
		applied, aerr := s.client.Apply(ctx, plan)
		if s.audit != nil && len(applied) > 0 {
			s.audit(ctx, applied, reason)
		}
		applyErr = aerr
	}

	// Step 4.7 Part 2.5: per-zone Smart A-sets. Runs even when the apex plan is
	// empty, since a zone's PoP membership (server_zones) changes independently of
	// the apex set. Best-effort; never blocks the apex result.
	if s.zones != nil {
		s.reconcileZones(ctx, eps, reason)
	}

	return plan, applyErr
}

// reconcileZones converges every active tenant zone's per-zone Smart A-set
// (Part 2.5) from server_zones. Best-effort per zone — one zone's Bunny hiccup
// must not block the others or the apex set. Re-lists records so each DiffZone
// sees the post-apex state. Also sweeps A-sets for zones that no longer exist.
func (s *Syncer) reconcileZones(ctx context.Context, eps []Endpoint, reason string) {
	zrs, err := s.zones(ctx)
	if err != nil {
		slog.Warn("dns per-zone reconcile: list zones failed", "reason", reason, "err", err.Error())
		return
	}
	records, err := s.client.ListRecords(ctx)
	if err != nil {
		slog.Warn("dns per-zone reconcile: list records failed", "reason", reason, "err", err.Error())
		return
	}
	o := DiffOpts{
		RoutingName: s.opts.RoutingName,
		TTL:         s.opts.TTL,
		StaleAfter:  s.opts.StaleAfter,
		Mode:        s.NetworkMode(),
		MonitorType: s.opts.MonitorType,
		Now:         time.Now().UTC(),
	}
	activeZoneIDs := make(map[int64]bool, len(zrs))
	for _, zr := range zrs {
		activeZoneIDs[zr.ZoneID] = true
		plan := DiffZone(zr, eps, records, o)
		if plan.Empty() {
			continue
		}
		applied, aerr := s.client.Apply(ctx, plan)
		if s.audit != nil && len(applied) > 0 {
			s.audit(ctx, applied, reason+":zone:"+strconv.FormatInt(zr.ZoneID, 10))
		}
		if aerr != nil {
			slog.Warn("dns per-zone reconcile apply failed", "zone_id", zr.ZoneID, "err", aerr.Error())
		}
	}
	// Orphan sweep: drop per-zone A records for zones no longer active (deleted).
	if op := OrphanZonePlan(records, activeZoneIDs); !op.Empty() {
		applied, aerr := s.client.Apply(ctx, op)
		if s.audit != nil && len(applied) > 0 {
			s.audit(ctx, applied, reason+":zone-orphan")
		}
		if aerr != nil {
			slog.Warn("dns per-zone orphan sweep failed", "err", aerr.Error())
		}
	}
}

// Run is the background loop: debounced event reconciles + a periodic safety
// reconcile. It runs until ctx is cancelled. It also does one convergence run
// shortly after start.
func (s *Syncer) Run(ctx context.Context) {
	if s == nil {
		return
	}
	debounce := time.NewTimer(2 * time.Second) // initial convergence
	ticker := time.NewTicker(s.opts.Periodic)
	defer ticker.Stop()
	defer debounce.Stop()

	reason := "startup"
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-s.trigger:
			reason = r
			debounce.Reset(s.opts.Debounce)
		case <-debounce.C:
			run := reason
			reason = "event"
			if _, err := s.Reconcile(ctx, false, run); err != nil {
				slog.Warn("dns reconcile failed", "reason", run, "err", err.Error())
			}
		case <-ticker.C:
			if _, err := s.Reconcile(ctx, false, "periodic"); err != nil {
				slog.Warn("dns periodic reconcile failed", "err", err.Error())
			}
		}
	}
}
