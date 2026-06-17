package store

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Phase 4 Step 4 — WAF custom rules, rate limits, and the security-event log.
// Custom rules + rate limits are per-zone config that flows to edges over the
// pull channel (each change bumps the zone's config_version). security_events is
// the firewall log shipped back by edges (Timescale hypertable like stats).

// --- custom rules ---------------------------------------------------------

// WAFCustomRule is one ordered custom rule (evaluated before the managed CRS).
type WAFCustomRule struct {
	ID         int64     `json:"id"`
	ZoneID     int64     `json:"zone_id"`
	Priority   int32     `json:"priority"`
	Field      string    `json:"field"` // ip|country|path|method|header|user_agent
	Op         string    `json:"op"`    // eq|prefix|regex|cidr
	Value      string    `json:"value"`
	HeaderName *string   `json:"header_name,omitempty"`
	Action     string    `json:"action"` // block|challenge|log|allow
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

// CreateWAFRuleParams are the inputs for a custom rule.
type CreateWAFRuleParams struct {
	Priority   int32
	Field      string
	Op         string
	Value      string
	HeaderName *string
	Action     string
	Enabled    bool
}

const wafRuleCols = `id, zone_id, priority, field, op, value, header_name, action, enabled, created_at`

func scanWAFRule(row pgx.Row) (WAFCustomRule, error) {
	var r WAFCustomRule
	err := row.Scan(&r.ID, &r.ZoneID, &r.Priority, &r.Field, &r.Op, &r.Value,
		&r.HeaderName, &r.Action, &r.Enabled, &r.CreatedAt)
	return r, err
}

// ListWAFRules returns a zone's custom rules ordered by priority then id (the
// evaluation order the edge applies).
func (st *Store) ListWAFRules(ctx context.Context, zoneID int64) ([]WAFCustomRule, error) {
	rows, err := st.pool.Query(ctx,
		`SELECT `+wafRuleCols+` FROM waf_custom_rules WHERE zone_id = $1 ORDER BY priority, id`, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WAFCustomRule{}
	for rows.Next() {
		r, err := scanWAFRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateWAFRule inserts a custom rule and bumps the zone's config_version in one
// transaction (so the zone's edges re-pull). ErrNotFound if the zone is absent.
func (st *Store) CreateWAFRule(ctx context.Context, zoneID int64, p CreateWAFRuleParams) (WAFCustomRule, error) {
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		return WAFCustomRule{}, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx,
		`UPDATE zones SET config_version = config_version + 1, updated_at = now() WHERE id = $1`, zoneID)
	if err != nil {
		return WAFCustomRule{}, err
	}
	if ct.RowsAffected() == 0 {
		return WAFCustomRule{}, ErrNotFound
	}

	r, err := scanWAFRule(tx.QueryRow(ctx, `
		INSERT INTO waf_custom_rules (zone_id, priority, field, op, value, header_name, action, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING `+wafRuleCols,
		zoneID, p.Priority, p.Field, p.Op, p.Value, p.HeaderName, p.Action, p.Enabled))
	if err != nil {
		return WAFCustomRule{}, err
	}
	return r, tx.Commit(ctx)
}

// DeleteWAFRule removes a rule (scoped to its zone) and bumps config_version.
func (st *Store) DeleteWAFRule(ctx context.Context, zoneID, ruleID int64) error {
	return st.deleteScopedBump(ctx, "waf_custom_rules", zoneID, ruleID)
}

// --- rate limits ----------------------------------------------------------

// WAFRateLimit is one per-zone rate-limit rule (enforced by Nginx limit_req).
type WAFRateLimit struct {
	ID            int64     `json:"id"`
	ZoneID        int64     `json:"zone_id"`
	PathMatch     string    `json:"path_match"`
	MatchType     string    `json:"match_type"` // exact|prefix
	Requests      int32     `json:"requests"`
	PeriodSeconds int32     `json:"period_seconds"`
	Burst         int32     `json:"burst"`
	Key           string    `json:"key"`        // ip|ip_path
	Action        string    `json:"action"`     // block|challenge
	CountMode     string    `json:"count_mode"` // all|errors_only
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
}

// CreateWAFRateLimitParams are the inputs for a rate limit.
type CreateWAFRateLimitParams struct {
	PathMatch     string
	MatchType     string
	Requests      int32
	PeriodSeconds int32
	Burst         int32
	Key           string
	Action        string
	CountMode     string
	Enabled       bool
}

const wafRLCols = `id, zone_id, path_match, match_type, requests, period_seconds, burst, key, action, count_mode, enabled, created_at`

func scanWAFRateLimit(row pgx.Row) (WAFRateLimit, error) {
	var r WAFRateLimit
	err := row.Scan(&r.ID, &r.ZoneID, &r.PathMatch, &r.MatchType, &r.Requests, &r.PeriodSeconds,
		&r.Burst, &r.Key, &r.Action, &r.CountMode, &r.Enabled, &r.CreatedAt)
	return r, err
}

// ListWAFRateLimits returns a zone's rate limits ordered by id.
func (st *Store) ListWAFRateLimits(ctx context.Context, zoneID int64) ([]WAFRateLimit, error) {
	rows, err := st.pool.Query(ctx,
		`SELECT `+wafRLCols+` FROM waf_rate_limits WHERE zone_id = $1 ORDER BY id`, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WAFRateLimit{}
	for rows.Next() {
		r, err := scanWAFRateLimit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateWAFRateLimit inserts a rate limit and bumps config_version (one tx).
func (st *Store) CreateWAFRateLimit(ctx context.Context, zoneID int64, p CreateWAFRateLimitParams) (WAFRateLimit, error) {
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		return WAFRateLimit{}, err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx,
		`UPDATE zones SET config_version = config_version + 1, updated_at = now() WHERE id = $1`, zoneID)
	if err != nil {
		return WAFRateLimit{}, err
	}
	if ct.RowsAffected() == 0 {
		return WAFRateLimit{}, ErrNotFound
	}

	r, err := scanWAFRateLimit(tx.QueryRow(ctx, `
		INSERT INTO waf_rate_limits (zone_id, path_match, match_type, requests, period_seconds, burst, key, action, count_mode, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING `+wafRLCols,
		zoneID, p.PathMatch, p.MatchType, p.Requests, p.PeriodSeconds, p.Burst, p.Key, p.Action, p.CountMode, p.Enabled))
	if err != nil {
		return WAFRateLimit{}, err
	}
	return r, tx.Commit(ctx)
}

// DeleteWAFRateLimit removes a rate limit (scoped to its zone) + bumps config_version.
func (st *Store) DeleteWAFRateLimit(ctx context.Context, zoneID, rlID int64) error {
	return st.deleteScopedBump(ctx, "waf_rate_limits", zoneID, rlID)
}

// deleteScopedBump deletes id from a per-zone WAF table (scoped to zone_id) and
// bumps the zone's config_version, atomically. table is a fixed internal literal
// (never user input). ErrNotFound if the (id, zone) pair is absent.
func (st *Store) deleteScopedBump(ctx context.Context, table string, zoneID, id int64) error {
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE id = $1 AND zone_id = $2`, id, zoneID)
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

// --- security events (firewall log) ---------------------------------------

// SecurityEvent is one firewall-log row. Edges ship these; the dashboard queries
// them. ClientIP is a string (host form) at the boundary; stored as INET.
type SecurityEvent struct {
	TS       time.Time `json:"ts"`
	ZoneID   *int64    `json:"zone_id"`
	ServerID *int64    `json:"server_id"`
	EdgeID   string    `json:"edge_id"`
	ClientIP string    `json:"client_ip"`
	Country  string    `json:"country"`
	RuleID   string    `json:"rule_id"`
	RuleType string    `json:"rule_type"` // managed|custom|ratelimit
	Action   string    `json:"action"`    // block|detect|log|challenge
	Mode     string    `json:"mode"`      // detect|block
	Path     string    `json:"path"`
	Method   string    `json:"method"`
	UA       string    `json:"ua"`
	Message  string    `json:"message"`
}

var securityEventCols = []string{
	"ts", "zone_id", "server_id", "edge_id", "client_ip", "country",
	"rule_id", "rule_type", "action", "mode", "path", "method", "ua", "message",
}

// InsertSecurityEvents bulk-inserts firewall-log rows for a server via COPY. The
// caller supplies server_id (auth context) + edge_id; zone_id is resolved from the
// event's already-mapped value. Bad client IPs are stored NULL (never rejected).
func (st *Store) InsertSecurityEvents(ctx context.Context, serverID int64, edgeID string, events []SecurityEvent) (int64, error) {
	rows := make([][]any, 0, len(events))
	for _, e := range events {
		ts := e.TS
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		var ip any
		if a, err := netip.ParseAddr(strings.TrimSpace(e.ClientIP)); err == nil {
			ip = a
		}
		rows = append(rows, []any{
			ts, e.ZoneID, &serverID, edgeID, ip, nullIfEmpty(e.Country),
			nullIfEmpty(e.RuleID), nullIfEmpty(e.RuleType), nullIfEmpty(e.Action), nullIfEmpty(e.Mode),
			nullIfEmpty(e.Path), nullIfEmpty(e.Method), nullIfEmpty(e.UA), nullIfEmpty(e.Message),
		})
	}
	return st.pool.CopyFrom(ctx, pgx.Identifier{"security_events"}, securityEventCols, pgx.CopyFromRows(rows))
}

// SecurityEventFilter narrows a firewall-log query.
type SecurityEventFilter struct {
	ZoneIDs []int64 // empty = all zones (admin); else restrict to these
	From    time.Time
	To      time.Time
	Action  string // optional exact match
	Limit   int    // capped 1..1000 (default 200)
}

// QuerySecurityEvents returns firewall-log rows newest-first for the filter.
func (st *Store) QuerySecurityEvents(ctx context.Context, f SecurityEventFilter) ([]SecurityEvent, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var b strings.Builder
	b.WriteString(`SELECT ts, zone_id, server_id, edge_id, COALESCE(host(client_ip),''), COALESCE(country,''),
		COALESCE(rule_id,''), COALESCE(rule_type,''), COALESCE(action,''), COALESCE(mode,''),
		COALESCE(path,''), COALESCE(method,''), COALESCE(ua,''), COALESCE(message,'')
		FROM security_events WHERE ts >= $1 AND ts <= $2`)
	args := []any{f.From, f.To}
	if len(f.ZoneIDs) > 0 {
		args = append(args, f.ZoneIDs)
		b.WriteString(` AND zone_id = ANY($` + strconv.Itoa(len(args)) + `)`)
	}
	if f.Action != "" {
		args = append(args, f.Action)
		b.WriteString(` AND action = $` + strconv.Itoa(len(args)))
	}
	args = append(args, limit)
	b.WriteString(` ORDER BY ts DESC LIMIT $` + strconv.Itoa(len(args)))

	rows, err := st.pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SecurityEvent{}
	for rows.Next() {
		var e SecurityEvent
		if err := rows.Scan(&e.TS, &e.ZoneID, &e.ServerID, &e.EdgeID, &e.ClientIP, &e.Country,
			&e.RuleID, &e.RuleType, &e.Action, &e.Mode, &e.Path, &e.Method, &e.UA, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SecurityEventCount is a (label, count) row for the admin overview aggregates.
type SecurityEventCount struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// SecurityEventSummary is the admin cross-tenant overview (top attacked zones +
// top blocked client IPs) over a window. Only enforced actions (block/challenge)
// count toward "blocked"; detect/log rows are excluded so the view reflects real
// enforcement.
type SecurityEventSummary struct {
	From       time.Time            `json:"from"`
	To         time.Time            `json:"to"`
	TotalBlock int64                `json:"total_block"`
	TotalLog   int64                `json:"total_log"`
	TopZones   []SecurityEventCount `json:"top_zones"`
	TopIPs     []SecurityEventCount `json:"top_ips"`
}

// SecurityEventSummary computes the admin overview aggregates for [from,to].
func (st *Store) SecurityEventSummary(ctx context.Context, from, to time.Time) (SecurityEventSummary, error) {
	s := SecurityEventSummary{From: from, To: to, TopZones: []SecurityEventCount{}, TopIPs: []SecurityEventCount{}}

	if err := st.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE action IN ('block','challenge')),
		  COUNT(*) FILTER (WHERE action IN ('detect','log'))
		FROM security_events WHERE ts >= $1 AND ts <= $2`, from, to).
		Scan(&s.TotalBlock, &s.TotalLog); err != nil {
		return s, err
	}

	zoneRows, err := st.pool.Query(ctx, `
		SELECT COALESCE(z.name, 'zone '||se.zone_id::text), COUNT(*) AS c
		FROM security_events se LEFT JOIN zones z ON z.id = se.zone_id
		WHERE se.ts >= $1 AND se.ts <= $2 AND se.action IN ('block','challenge') AND se.zone_id IS NOT NULL
		GROUP BY se.zone_id, z.name ORDER BY c DESC LIMIT 10`, from, to)
	if err != nil {
		return s, err
	}
	defer zoneRows.Close()
	for zoneRows.Next() {
		var c SecurityEventCount
		if err := zoneRows.Scan(&c.Label, &c.Count); err != nil {
			return s, err
		}
		s.TopZones = append(s.TopZones, c)
	}
	if err := zoneRows.Err(); err != nil {
		return s, err
	}

	ipRows, err := st.pool.Query(ctx, `
		SELECT host(client_ip), COUNT(*) AS c
		FROM security_events
		WHERE ts >= $1 AND ts <= $2 AND action IN ('block','challenge') AND client_ip IS NOT NULL
		GROUP BY client_ip ORDER BY c DESC LIMIT 10`, from, to)
	if err != nil {
		return s, err
	}
	defer ipRows.Close()
	for ipRows.Next() {
		var c SecurityEventCount
		if err := ipRows.Scan(&c.Label, &c.Count); err != nil {
			return s, err
		}
		s.TopIPs = append(s.TopIPs, c)
	}
	return s, ipRows.Err()
}

// nullIfEmpty returns nil for an empty string so COPY stores NULL (not '').
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
