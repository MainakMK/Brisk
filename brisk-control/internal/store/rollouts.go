package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Rollout is one "deploy version X to these PoPs" action (migration 00031).
type Rollout struct {
	ID             int64      `json:"id"`
	ReleaseVersion string     `json:"release_version"`
	TargetPops     []string   `json:"target_pops"`
	SoakSeconds    int        `json:"soak_seconds"`
	Status         string     `json:"status"` // scheduled|running|paused|done|failed|cancelled
	ScheduledAt    *time.Time `json:"scheduled_at,omitempty"`
	ErrorReason    string     `json:"error_reason,omitempty"`
	CreatedBy      string     `json:"created_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

// RolloutTarget is one PoP within a rollout.
type RolloutTarget struct {
	RolloutID   int64      `json:"rollout_id"`
	EdgeID      string     `json:"edge_id"`
	FromVersion string     `json:"from_version"`
	ToVersion   string     `json:"to_version"`
	State       string     `json:"state"` // queued|in_progress|soaking|done|failed|skipped
	ErrorReason string     `json:"error_reason,omitempty"`
	SoakUntil   *time.Time `json:"soak_until,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

const rolloutCols = `id, release_version, target_pops, soak_seconds, status, scheduled_at, error_reason, created_by, created_at, started_at, finished_at`

func scanRollout(row pgx.Row) (Rollout, error) {
	var r Rollout
	err := row.Scan(&r.ID, &r.ReleaseVersion, &r.TargetPops, &r.SoakSeconds, &r.Status, &r.ScheduledAt,
		&r.ErrorReason, &r.CreatedBy, &r.CreatedAt, &r.StartedAt, &r.FinishedAt)
	return r, err
}

// CreateRolloutParams are the inputs for starting a rollout.
type CreateRolloutParams struct {
	ReleaseVersion string
	TargetPops     []string          // edge_ids, in order
	SoakSeconds    int               // <=0 => 90
	ScheduledAt    *time.Time        // nil => start now (status running)
	CreatedBy      string            // account id (audit)
	FromVersions   map[string]string // edge_id -> current running version (for the targets)
}

// CreateRollout inserts a rollout + one queued target per PoP in a transaction and returns the
// rollout id. The one-active partial unique index rejects a second active rollout (caller should
// also check ActiveRollout first to return a clean 409).
func (st *Store) CreateRollout(ctx context.Context, p CreateRolloutParams) (int64, error) {
	if p.SoakSeconds <= 0 {
		p.SoakSeconds = 90
	}
	status := "running"
	var startedAt *time.Time
	if p.ScheduledAt != nil {
		status = "scheduled"
	} else {
		now := time.Now()
		startedAt = &now
	}
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO rollouts (release_version, target_pops, soak_seconds, status, scheduled_at, created_by, started_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		p.ReleaseVersion, p.TargetPops, p.SoakSeconds, status, p.ScheduledAt, p.CreatedBy, startedAt).Scan(&id)
	if err != nil {
		return 0, err
	}
	for _, edge := range p.TargetPops {
		_, err = tx.Exec(ctx,
			`INSERT INTO rollout_targets (rollout_id, edge_id, from_version, to_version, state)
			 VALUES ($1,$2,$3,$4,'queued')`,
			id, edge, p.FromVersions[edge], p.ReleaseVersion)
		if err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return id, nil
}

// GetRollout returns one rollout by id.
func (st *Store) GetRollout(ctx context.Context, id int64) (Rollout, error) {
	return scanRollout(st.pool.QueryRow(ctx, `SELECT `+rolloutCols+` FROM rollouts WHERE id=$1`, id))
}

// ActiveRollout returns the single scheduled|running|paused rollout, if any.
func (st *Store) ActiveRollout(ctx context.Context) (Rollout, bool, error) {
	r, err := scanRollout(st.pool.QueryRow(ctx,
		`SELECT `+rolloutCols+` FROM rollouts WHERE status IN ('scheduled','running','paused') ORDER BY id DESC LIMIT 1`))
	if err == pgx.ErrNoRows {
		return Rollout{}, false, nil
	}
	if err != nil {
		return Rollout{}, false, err
	}
	return r, true, nil
}

// ListRollouts returns recent rollouts, newest first (for history / dashboard).
func (st *Store) ListRollouts(ctx context.Context, limit int) ([]Rollout, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := st.pool.Query(ctx, `SELECT `+rolloutCols+` FROM rollouts ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rollout{}
	for rows.Next() {
		r, err := scanRollout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListTargets returns a rollout's targets in PoP order.
func (st *Store) ListTargets(ctx context.Context, rolloutID int64) ([]RolloutTarget, error) {
	rows, err := st.pool.Query(ctx,
		`SELECT rollout_id, edge_id, from_version, to_version, state, error_reason, soak_until, updated_at
		   FROM rollout_targets WHERE rollout_id=$1 ORDER BY edge_id`, rolloutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RolloutTarget{}
	for rows.Next() {
		var t RolloutTarget
		if err := rows.Scan(&t.RolloutID, &t.EdgeID, &t.FromVersion, &t.ToVersion, &t.State, &t.ErrorReason, &t.SoakUntil, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// OpenTarget opens a PoP's wave: state=in_progress, records the version it's coming from.
func (st *Store) OpenTarget(ctx context.Context, rolloutID int64, edge, fromVersion string) error {
	_, err := st.pool.Exec(ctx,
		`UPDATE rollout_targets SET state='in_progress', from_version=$3, soak_until=NULL, updated_at=now()
		  WHERE rollout_id=$1 AND edge_id=$2`, rolloutID, edge, fromVersion)
	return err
}

// StartSoak marks a target soaking until the given time (edge is healthy on the new version).
func (st *Store) StartSoak(ctx context.Context, rolloutID int64, edge string, until time.Time) error {
	_, err := st.pool.Exec(ctx,
		`UPDATE rollout_targets SET state='soaking', soak_until=$3, updated_at=now()
		  WHERE rollout_id=$1 AND edge_id=$2`, rolloutID, edge, until)
	return err
}

// FinishTarget sets a terminal target state (done|failed|skipped) with an optional reason.
func (st *Store) FinishTarget(ctx context.Context, rolloutID int64, edge, state, reason string) error {
	_, err := st.pool.Exec(ctx,
		`UPDATE rollout_targets SET state=$3, error_reason=$4, updated_at=now()
		  WHERE rollout_id=$1 AND edge_id=$2`, rolloutID, edge, state, reason)
	return err
}

// SetRolloutStatus transitions a rollout, stamping started_at on first run and finished_at on
// a terminal status (done|failed|cancelled).
func (st *Store) SetRolloutStatus(ctx context.Context, id int64, status, reason string) error {
	terminal := status == "done" || status == "failed" || status == "cancelled"
	_, err := st.pool.Exec(ctx, `
		UPDATE rollouts SET
		  status = $2,
		  error_reason = CASE WHEN $3 = '' THEN error_reason ELSE $3 END,
		  started_at  = COALESCE(started_at, CASE WHEN $2='running' THEN now() ELSE NULL END),
		  finished_at = CASE WHEN $4 THEN now() ELSE finished_at END
		WHERE id=$1`, id, status, reason, terminal)
	return err
}

// WriteAudit appends an audit row (who/what/when).
func (st *Store) WriteAudit(ctx context.Context, actor, action, subject, details string) error {
	_, err := st.pool.Exec(ctx,
		`INSERT INTO audit_log (actor, action, subject, details) VALUES ($1,$2,$3,$4)`,
		actor, action, subject, details)
	return err
}

// AuditEntry is one row of the deploy/pause/rollback/upload audit trail.
type AuditEntry struct {
	ID      int64     `json:"id"`
	Actor   string    `json:"actor"`
	Action  string    `json:"action"`
	Subject string    `json:"subject"`
	Details string    `json:"details"`
	At      time.Time `json:"at"`
}

// ListAudit returns the most recent audit rows, newest first (capped at `limit`).
func (st *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := st.pool.Query(ctx,
		`SELECT id, actor, action, subject, details, created_at
		   FROM audit_log ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.Subject, &e.Details, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
