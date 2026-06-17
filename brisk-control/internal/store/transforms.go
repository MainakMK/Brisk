package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Phase 4 Step 5 — per-zone header transforms (enforced at the edge by Lua).
// Ordered per zone; each change bumps the zone's config_version (edges re-pull).
// Reuses deleteScopedBump (store/waf.go) for the scoped delete + bump.

// HeaderTransform is one request/response header rule.
type HeaderTransform struct {
	ID         int64     `json:"id"`
	ZoneID     int64     `json:"zone_id"`
	Priority   int32     `json:"priority"`
	Phase      string    `json:"phase"` // request | response
	Op         string    `json:"op"`    // set | remove
	Header     string    `json:"header"`
	Value      *string   `json:"value,omitempty"`
	MatchType  string    `json:"match_type"` // all | path_prefix | path_regex | method
	MatchValue *string   `json:"match_value,omitempty"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

// CreateHeaderTransformParams are the inputs for a header transform.
type CreateHeaderTransformParams struct {
	Priority   int32
	Phase      string
	Op         string
	Header     string
	Value      *string
	MatchType  string
	MatchValue *string
	Enabled    bool
}

const transformCols = `id, zone_id, priority, phase, op, header, value, match_type, match_value, enabled, created_at`

func scanTransform(row pgx.Row) (HeaderTransform, error) {
	var t HeaderTransform
	err := row.Scan(&t.ID, &t.ZoneID, &t.Priority, &t.Phase, &t.Op, &t.Header,
		&t.Value, &t.MatchType, &t.MatchValue, &t.Enabled, &t.CreatedAt)
	return t, err
}

// ListHeaderTransforms returns a zone's header transforms ordered by priority then id
// (the deterministic order the edge applies).
func (st *Store) ListHeaderTransforms(ctx context.Context, zoneID int64) ([]HeaderTransform, error) {
	rows, err := st.pool.Query(ctx,
		`SELECT `+transformCols+` FROM header_transforms WHERE zone_id = $1 ORDER BY priority, id`, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HeaderTransform{}
	for rows.Next() {
		t, err := scanTransform(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateHeaderTransform inserts a transform and bumps the zone's config_version in
// one transaction. ErrNotFound if the zone is absent.
func (st *Store) CreateHeaderTransform(ctx context.Context, zoneID int64, p CreateHeaderTransformParams) (HeaderTransform, error) {
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		return HeaderTransform{}, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx,
		`UPDATE zones SET config_version = config_version + 1, updated_at = now() WHERE id = $1`, zoneID)
	if err != nil {
		return HeaderTransform{}, err
	}
	if ct.RowsAffected() == 0 {
		return HeaderTransform{}, ErrNotFound
	}

	t, err := scanTransform(tx.QueryRow(ctx, `
		INSERT INTO header_transforms (zone_id, priority, phase, op, header, value, match_type, match_value, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING `+transformCols,
		zoneID, p.Priority, p.Phase, p.Op, p.Header, p.Value, p.MatchType, p.MatchValue, p.Enabled))
	if err != nil {
		return HeaderTransform{}, err
	}
	return t, tx.Commit(ctx)
}

// DeleteHeaderTransform removes a transform (scoped to its zone) + bumps config_version.
func (st *Store) DeleteHeaderTransform(ctx context.Context, zoneID, id int64) error {
	return st.deleteScopedBump(ctx, "header_transforms", zoneID, id)
}
