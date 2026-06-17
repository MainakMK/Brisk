// Package rollout drives a self-service agent rollout: it opens one PoP's wave at a time and
// advances to the next only after the edge reports the new version healthy for a soak window.
// All state lives in the DB (rollouts/rollout_targets), so the engine resumes cleanly after a
// control-plane restart — Tick is a pure function of stored state + live edge state.
package rollout

import (
	"context"
	"log/slog"
	"time"

	"brisk-control/internal/store"
)

// EngineStore is the subset of *store.Store the engine needs. Defining it as an interface lets
// tests drive the state machine with an in-memory fake — no database, no real edges.
type EngineStore interface {
	ActiveRollout(ctx context.Context) (store.Rollout, bool, error)
	ListTargets(ctx context.Context, rolloutID int64) ([]store.RolloutTarget, error)
	OpenTarget(ctx context.Context, rolloutID int64, edge, fromVersion string) error
	StartSoak(ctx context.Context, rolloutID int64, edge string, until time.Time) error
	FinishTarget(ctx context.Context, rolloutID int64, edge, state, reason string) error
	SetRolloutStatus(ctx context.Context, id int64, status, reason string) error
}

// EdgeState is what the soak gate needs to know about an edge right now (from its heartbeat +
// the health checker).
type EdgeState struct {
	Online  bool
	Version string // the version the edge currently reports running
	Healthy bool
}

// EdgeStateFn reports the live state of an edge by edge_id.
type EdgeStateFn func(edgeID string) EdgeState

// Engine is the rollout state machine.
type Engine struct {
	st    EngineStore
	edge  EdgeStateFn
	now   func() time.Time // injectable clock (tests)
	grace time.Duration    // extra time beyond the soak before declaring a stuck edge failed
}

// NewEngine wires the engine to a store + an edge-state source.
func NewEngine(st EngineStore, edge EdgeStateFn) *Engine {
	return &Engine{st: st, edge: edge, now: time.Now, grace: 120 * time.Second}
}

// Run ticks the engine until ctx is cancelled.
func (e *Engine) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	slog.Info("rollout engine started", "interval", interval.String())
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := e.Tick(ctx); err != nil {
				slog.Warn("rollout tick", "err", err)
			}
		}
	}
}

// Tick advances the active rollout by at most one step. Idempotent / safe to call repeatedly.
func (e *Engine) Tick(ctx context.Context) error {
	r, ok, err := e.st.ActiveRollout(ctx)
	if err != nil || !ok {
		return err
	}

	switch r.Status {
	case "paused":
		return nil // pause = don't open the next PoP (the current one already finished its step)
	case "scheduled":
		if r.ScheduledAt != nil && !e.now().Before(*r.ScheduledAt) {
			return e.st.SetRolloutStatus(ctx, r.ID, "running", "")
		}
		return nil
	}

	targets, err := e.st.ListTargets(ctx, r.ID)
	if err != nil {
		return err
	}

	// 1. If a PoP is in flight, advance just that one.
	for _, t := range targets {
		if t.State == "in_progress" || t.State == "soaking" {
			return e.advance(ctx, r, t)
		}
	}
	// 2. Otherwise open the next queued PoP (skip it if it's offline at its turn).
	for _, t := range targets {
		if t.State == "queued" {
			es := e.edge(t.EdgeID)
			if !es.Online {
				return e.st.FinishTarget(ctx, r.ID, t.EdgeID, "skipped", "edge offline at its turn")
			}
			return e.st.OpenTarget(ctx, r.ID, t.EdgeID, es.Version)
		}
	}
	// 3. Nothing queued or in flight → every PoP is terminal → the rollout is done.
	return e.st.SetRolloutStatus(ctx, r.ID, "done", "")
}

// advance moves one in-flight target forward based on the edge's live state.
func (e *Engine) advance(ctx context.Context, r store.Rollout, t store.RolloutTarget) error {
	es := e.edge(t.EdgeID)
	soak := time.Duration(r.SoakSeconds) * time.Second
	healthyOnNew := es.Online && es.Version == t.ToVersion && es.Healthy

	if healthyOnNew {
		// Start the soak clock on first sight of health; complete when it elapses.
		if t.State == "in_progress" || t.SoakUntil == nil {
			return e.st.StartSoak(ctx, r.ID, t.EdgeID, e.now().Add(soak))
		}
		if !e.now().Before(*t.SoakUntil) {
			return e.st.FinishTarget(ctx, r.ID, t.EdgeID, "done", "")
		}
		return nil // still soaking — wait
	}

	// Not healthy on the new version yet. If we've waited past soak+grace, it's stuck → fail & halt.
	if e.now().After(t.UpdatedAt.Add(soak + e.grace)) {
		reason := "edge did not report a healthy new version within the soak window"
		if err := e.st.FinishTarget(ctx, r.ID, t.EdgeID, "failed", reason); err != nil {
			return err
		}
		return e.st.SetRolloutStatus(ctx, r.ID, "failed", reason)
	}
	return nil // the agent is still downloading/swapping — keep waiting
}
