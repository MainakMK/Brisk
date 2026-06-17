package rollout

import (
	"context"
	"testing"
	"time"

	"brisk-control/internal/store"
)

// fakeStore is an in-memory EngineStore for driving the state machine without a DB.
type fakeStore struct {
	r       store.Rollout
	targets []store.RolloutTarget
	now     func() time.Time
}

func (f *fakeStore) ActiveRollout(context.Context) (store.Rollout, bool, error) {
	switch f.r.Status {
	case "scheduled", "running", "paused":
		return f.r, true, nil
	}
	return store.Rollout{}, false, nil
}
func (f *fakeStore) ListTargets(context.Context, int64) ([]store.RolloutTarget, error) {
	return f.targets, nil
}
func (f *fakeStore) idx(edge string) int {
	for i := range f.targets {
		if f.targets[i].EdgeID == edge {
			return i
		}
	}
	return -1
}
func (f *fakeStore) OpenTarget(_ context.Context, _ int64, edge, fromVersion string) error {
	i := f.idx(edge)
	f.targets[i].State = "in_progress"
	f.targets[i].FromVersion = fromVersion
	f.targets[i].SoakUntil = nil
	f.targets[i].UpdatedAt = f.now()
	return nil
}
func (f *fakeStore) StartSoak(_ context.Context, _ int64, edge string, until time.Time) error {
	i := f.idx(edge)
	f.targets[i].State = "soaking"
	u := until
	f.targets[i].SoakUntil = &u
	f.targets[i].UpdatedAt = f.now()
	return nil
}
func (f *fakeStore) FinishTarget(_ context.Context, _ int64, edge, state, reason string) error {
	i := f.idx(edge)
	f.targets[i].State = state
	f.targets[i].ErrorReason = reason
	f.targets[i].UpdatedAt = f.now()
	return nil
}
func (f *fakeStore) SetRolloutStatus(_ context.Context, _ int64, status, reason string) error {
	f.r.Status = status
	if reason != "" {
		f.r.ErrorReason = reason
	}
	return nil
}

// newScenario builds a running rollout of `pops` to version 0.4.0 with a 90s soak.
func newScenario(pops []string, clk *time.Time) (*fakeStore, *Engine, map[string]EdgeState) {
	fs := &fakeStore{
		r:   store.Rollout{ID: 1, ReleaseVersion: "0.4.0", TargetPops: pops, SoakSeconds: 90, Status: "running"},
		now: func() time.Time { return *clk },
	}
	for _, p := range pops {
		fs.targets = append(fs.targets, store.RolloutTarget{RolloutID: 1, EdgeID: p, ToVersion: "0.4.0", State: "queued", UpdatedAt: *clk})
	}
	edges := map[string]EdgeState{}
	for _, p := range pops {
		edges[p] = EdgeState{Online: true, Version: "0.3.0", Healthy: true} // start on the OLD version
	}
	e := NewEngine(fs, func(id string) EdgeState { return edges[id] })
	e.now = func() time.Time { return *clk }
	return fs, e, edges
}

func state(fs *fakeStore, edge string) string { return fs.targets[fs.idx(edge)].State }

func TestHappyPathWalksOnePoPAtATime(t *testing.T) {
	clk := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	fs, e, edges := newScenario([]string{"NY", "DE"}, &clk)
	ctx := context.Background()

	// Tick 1: open NY (it's first).
	e.Tick(ctx)
	if state(fs, "NY") != "in_progress" || state(fs, "DE") != "queued" {
		t.Fatalf("NY should open first; got NY=%s DE=%s", state(fs, "NY"), state(fs, "DE"))
	}
	// NY updates to the new version + healthy → Tick starts the soak.
	edges["NY"] = EdgeState{Online: true, Version: "0.4.0", Healthy: true}
	e.Tick(ctx)
	if state(fs, "NY") != "soaking" {
		t.Fatalf("NY should be soaking; got %s", state(fs, "NY"))
	}
	// Before soak elapses, DE must NOT have started.
	e.Tick(ctx)
	if state(fs, "DE") != "queued" {
		t.Fatal("DE must not open while NY is still soaking")
	}
	// Advance past the soak → NY done.
	clk = clk.Add(91 * time.Second)
	e.Tick(ctx)
	if state(fs, "NY") != "done" {
		t.Fatalf("NY should be done after soak; got %s", state(fs, "NY"))
	}
	// Next Tick opens DE.
	e.Tick(ctx)
	if state(fs, "DE") != "in_progress" {
		t.Fatalf("DE should open after NY done; got %s", state(fs, "DE"))
	}
	edges["DE"] = EdgeState{Online: true, Version: "0.4.0", Healthy: true}
	e.Tick(ctx) // start soak
	clk = clk.Add(91 * time.Second)
	e.Tick(ctx) // DE done
	e.Tick(ctx) // no targets left → rollout done
	if fs.r.Status != "done" {
		t.Fatalf("rollout should be done; got %s", fs.r.Status)
	}
}

func TestUnhealthyEdgeFailsAndHalts(t *testing.T) {
	clk := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	fs, e, edges := newScenario([]string{"NY", "DE"}, &clk)
	ctx := context.Background()

	e.Tick(ctx) // open NY
	// NY never reports the new version (broken build). Advance past soak+grace.
	edges["NY"] = EdgeState{Online: true, Version: "0.3.0", Healthy: false}
	clk = clk.Add((90 + 120 + 1) * time.Second)
	e.Tick(ctx)
	if state(fs, "NY") != "failed" {
		t.Fatalf("NY should fail; got %s", state(fs, "NY"))
	}
	if fs.r.Status != "failed" {
		t.Fatalf("rollout should halt as failed; got %s", fs.r.Status)
	}
	if state(fs, "DE") != "queued" {
		t.Fatal("DE must never open after a halt")
	}
}

func TestSkipsOfflineEdge(t *testing.T) {
	clk := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	fs, e, edges := newScenario([]string{"NY", "DE"}, &clk)
	ctx := context.Background()

	edges["NY"] = EdgeState{Online: false} // NY offline at its turn
	e.Tick(ctx)
	if state(fs, "NY") != "skipped" {
		t.Fatalf("offline NY should be skipped; got %s", state(fs, "NY"))
	}
	// DE proceeds normally.
	e.Tick(ctx)
	if state(fs, "DE") != "in_progress" {
		t.Fatalf("DE should open after NY skipped; got %s", state(fs, "DE"))
	}
}

func TestPausedDoesNothing(t *testing.T) {
	clk := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	fs, e, _ := newScenario([]string{"NY"}, &clk)
	fs.r.Status = "paused"
	e.Tick(context.Background())
	if state(fs, "NY") != "queued" {
		t.Fatalf("paused rollout must not open any PoP; got %s", state(fs, "NY"))
	}
}

func TestScheduledStartsAtTime(t *testing.T) {
	clk := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	fs, e, _ := newScenario([]string{"NY"}, &clk)
	at := clk.Add(time.Hour)
	fs.r.Status = "scheduled"
	fs.r.ScheduledAt = &at
	ctx := context.Background()

	e.Tick(ctx)
	if fs.r.Status != "scheduled" {
		t.Fatal("must stay scheduled before its time")
	}
	clk = clk.Add(2 * time.Hour)
	e.Tick(ctx)
	if fs.r.Status != "running" {
		t.Fatalf("must flip to running after scheduled time; got %s", fs.r.Status)
	}
}
