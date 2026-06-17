package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// CacheRule is a per-zone edge rule.
type CacheRule struct {
	ID          int64     `json:"id"`
	ZoneID      int64     `json:"zone_id"`
	Priority    int32     `json:"priority"`
	MatchType   string    `json:"match_type"`
	MatchValue  string    `json:"match_value"`
	Action      string    `json:"action"`
	ActionValue *string   `json:"action_value,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// CreateRuleParams are the inputs for a cache rule.
type CreateRuleParams struct {
	Priority    int32
	MatchType   string
	MatchValue  string
	Action      string
	ActionValue *string
}

const ruleCols = `id, zone_id, priority, match_type, match_value, action, action_value, created_at`

// ListRules returns the rules for a zone, ordered by priority then id.
func (st *Store) ListRules(ctx context.Context, zoneID int64) ([]CacheRule, error) {
	rows, err := st.pool.Query(ctx,
		`SELECT `+ruleCols+` FROM cache_rules WHERE zone_id = $1 ORDER BY priority, id`, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CacheRule{}
	for rows.Next() {
		var r CacheRule
		if err := rows.Scan(&r.ID, &r.ZoneID, &r.Priority, &r.MatchType, &r.MatchValue,
			&r.Action, &r.ActionValue, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateRule adds a rule and bumps the zone's config_version in one transaction.
// Returns ErrNotFound if the zone does not exist.
func (st *Store) CreateRule(ctx context.Context, zoneID int64, p CreateRuleParams) (CacheRule, error) {
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		return CacheRule{}, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx,
		`UPDATE zones SET config_version = config_version + 1, updated_at = now() WHERE id = $1`, zoneID)
	if err != nil {
		return CacheRule{}, err
	}
	if ct.RowsAffected() == 0 {
		return CacheRule{}, ErrNotFound
	}

	var r CacheRule
	err = tx.QueryRow(ctx, `
		INSERT INTO cache_rules (zone_id, priority, match_type, match_value, action, action_value)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING `+ruleCols,
		zoneID, p.Priority, p.MatchType, p.MatchValue, p.Action, p.ActionValue).
		Scan(&r.ID, &r.ZoneID, &r.Priority, &r.MatchType, &r.MatchValue, &r.Action, &r.ActionValue, &r.CreatedAt)
	if err != nil {
		return CacheRule{}, err
	}
	return r, tx.Commit(ctx)
}

// UpdateRule edits a rule in place (scoped to its zone) and bumps the zone's
// config_version, atomically — so edits don't churn IDs (Phase 4 Step 6 backlog).
// ErrNotFound if the rule/zone pair does not exist.
func (st *Store) UpdateRule(ctx context.Context, zoneID, ruleID int64, p CreateRuleParams) (CacheRule, error) {
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		return CacheRule{}, err
	}
	defer tx.Rollback(ctx)

	var r CacheRule
	err = tx.QueryRow(ctx, `
		UPDATE cache_rules SET priority=$3, match_type=$4, match_value=$5, action=$6, action_value=$7
		WHERE id=$1 AND zone_id=$2
		RETURNING `+ruleCols,
		ruleID, zoneID, p.Priority, p.MatchType, p.MatchValue, p.Action, p.ActionValue).
		Scan(&r.ID, &r.ZoneID, &r.Priority, &r.MatchType, &r.MatchValue, &r.Action, &r.ActionValue, &r.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CacheRule{}, ErrNotFound
		}
		return CacheRule{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE zones SET config_version = config_version + 1, updated_at = now() WHERE id = $1`, zoneID); err != nil {
		return CacheRule{}, err
	}
	return r, tx.Commit(ctx)
}

// ReorderRules sets each rule's priority to its index in ruleIDs (scoped to the
// zone) and bumps config_version, atomically — the no-churn reorder (Phase 4 Step
// 6 backlog; replaces the dashboard's delete+recreate). Rules not in the list are
// left untouched. ErrNotFound if any id doesn't belong to the zone.
func (st *Store) ReorderRules(ctx context.Context, zoneID int64, ruleIDs []int64) error {
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for i, id := range ruleIDs {
		ct, err := tx.Exec(ctx,
			`UPDATE cache_rules SET priority=$1 WHERE id=$2 AND zone_id=$3`, i, id, zoneID)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE zones SET config_version = config_version + 1, updated_at = now() WHERE id = $1`, zoneID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteRule removes a rule (scoped to its zone) and bumps the zone's
// config_version. Returns ErrNotFound if the rule/zone pair does not exist.
func (st *Store) DeleteRule(ctx context.Context, zoneID, ruleID int64) error {
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `DELETE FROM cache_rules WHERE id = $1 AND zone_id = $2`, ruleID, zoneID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx,
		`UPDATE zones SET config_version = config_version + 1, updated_at = now() WHERE id = $1`, zoneID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
