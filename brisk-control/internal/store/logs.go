package store

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Phase 4 Step 6 — the real edge request log. Agents ship structured access-log
// entries; the dashboard Logs page queries them. Mirrors the stats/security_events
// shipping + storage pattern (COPY insert, Timescale hypertable, retention).

// RequestLog is one structured access-log row.
type RequestLog struct {
	TS           time.Time `json:"ts"`
	ServerID     *int64    `json:"server_id,omitempty"`
	EdgeID       string    `json:"edge_id"`
	ZoneID       *int64    `json:"zone_id,omitempty"`
	ClientIP     string    `json:"client_ip"`
	Country      string    `json:"country"`
	Method       string    `json:"method"`
	Host         string    `json:"host"`
	Path         string    `json:"path"`
	Status       int32     `json:"status"`
	Bytes        int64     `json:"bytes"`
	CacheStatus  string    `json:"cache_status"`
	RequestTime  float64   `json:"request_time"`
	UpstreamTime float64   `json:"upstream_time"`
	Referer      string    `json:"referer"`
	UserAgent    string    `json:"user_agent"`
	RequestID    string    `json:"request_id"`
}

var requestLogCols = []string{
	"ts", "server_id", "edge_id", "zone_id", "client_ip", "country", "method", "host",
	"path", "status", "bytes", "cache_status", "request_time", "upstream_time",
	"referer", "user_agent", "request_id",
}

// InsertRequestLogs bulk-inserts access-log rows for a server via COPY.
func (st *Store) InsertRequestLogs(ctx context.Context, serverID int64, edgeID string, logs []RequestLog) (int64, error) {
	rows := make([][]any, 0, len(logs))
	for _, l := range logs {
		ts := l.TS
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		var ip any
		if a, err := netip.ParseAddr(strings.TrimSpace(l.ClientIP)); err == nil {
			ip = a
		}
		rows = append(rows, []any{
			ts, &serverID, edgeID, l.ZoneID, ip, nullIfEmpty(l.Country), nullIfEmpty(l.Method),
			nullIfEmpty(l.Host), nullIfEmpty(l.Path), l.Status, l.Bytes, nullIfEmpty(l.CacheStatus),
			l.RequestTime, l.UpstreamTime, nullIfEmpty(l.Referer), nullIfEmpty(l.UserAgent), nullIfEmpty(l.RequestID),
		})
	}
	return st.pool.CopyFrom(ctx, pgx.Identifier{"request_logs"}, requestLogCols, pgx.CopyFromRows(rows))
}

// RequestLogFilter narrows a log query.
type RequestLogFilter struct {
	ZoneIDs     []int64 // empty = all (admin); else restrict
	From        time.Time
	To          time.Time
	Status      int    // 0 = any; 200/404/500 exact; 2/4/5 = class (2xx/4xx/5xx)
	CacheStatus string // optional exact (HIT/MISS/BYPASS)
	Path        string // optional substring
	ClientIP    string // optional exact
	Country     string // optional exact
	Limit       int    // capped 1..1000 (default 200)
}

// --- log analytics (Phase 4 Step 6, Parts 3+4) ---
// Aggregates over request_logs make the "not collected yet" metrics REAL: origin
// offload (cache hit vs origin fetch), status-code breakdown, latency percentiles,
// top paths, top countries. Computed from per-request rows, not estimated.

// LabeledCount is one (label, count) aggregate row.
type LabeledCount struct {
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// PathStat is one top-path aggregate.
type PathStat struct {
	Path  string `json:"path"`
	Count int64  `json:"count"`
	Bytes int64  `json:"bytes"`
}

// CacheBreakdown is the real origin-offload picture for a window.
type CacheBreakdown struct {
	Hit          int64   `json:"hit"`
	Miss         int64   `json:"miss"` // origin-fetched (MISS/EXPIRED/STALE/REVALIDATED/UPDATING)
	Bypass       int64   `json:"bypass"`
	Other        int64   `json:"other"` // uncacheable / not-proxied ("-"/"")
	HitBytes     int64   `json:"hit_bytes"`
	TotalBytes   int64   `json:"total_bytes"`
	OffloadReq   float64 `json:"offload_req"`   // hit/(hit+miss) by request count
	OffloadBytes float64 `json:"offload_bytes"` // hit_bytes/total_bytes
}

// LatencyStats are edge request_time percentiles (seconds).
type LatencyStats struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Avg float64 `json:"avg"`
}

// LogAnalytics is the full aggregate over a window (optionally zone-scoped).
type LogAnalytics struct {
	Total         int64          `json:"total"`
	StatusClasses []LabeledCount `json:"status_classes"`
	Cache         CacheBreakdown `json:"cache"`
	Latency       LatencyStats   `json:"latency"`
	TopPaths      []PathStat     `json:"top_paths"`
	TopCountries  []LabeledCount `json:"top_countries"`
}

// logWhere builds the shared "WHERE ts in window [AND zone_id = ANY]" + args.
func logWhere(f RequestLogFilter) (string, []any) {
	w := "ts >= $1 AND ts <= $2"
	args := []any{f.From, f.To}
	if len(f.ZoneIDs) > 0 {
		args = append(args, f.ZoneIDs)
		w += " AND zone_id = ANY($3)"
	}
	return w, args
}

// LogAnalytics computes the window aggregates from request_logs. Three queries:
// summary (counts + percentiles via FILTER/percentile_cont), top paths, top
// countries — each scoped by the same window/zone filter.
func (st *Store) LogAnalytics(ctx context.Context, f RequestLogFilter) (LogAnalytics, error) {
	var a LogAnalytics
	where, args := logWhere(f)

	var s2, s3, s4, s5 int64
	var c CacheBreakdown
	var lat LatencyStats
	err := st.pool.QueryRow(ctx, `SELECT
			count(*),
			count(*) FILTER (WHERE status BETWEEN 200 AND 299),
			count(*) FILTER (WHERE status BETWEEN 300 AND 399),
			count(*) FILTER (WHERE status BETWEEN 400 AND 499),
			count(*) FILTER (WHERE status BETWEEN 500 AND 599),
			count(*) FILTER (WHERE cache_status = 'HIT'),
			count(*) FILTER (WHERE cache_status IN ('MISS','EXPIRED','STALE','REVALIDATED','UPDATING')),
			count(*) FILTER (WHERE cache_status = 'BYPASS'),
			COALESCE(sum(bytes),0),
			COALESCE(sum(bytes) FILTER (WHERE cache_status = 'HIT'),0),
			COALESCE(percentile_cont(0.5)  WITHIN GROUP (ORDER BY request_time),0),
			COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY request_time),0),
			COALESCE(percentile_cont(0.99) WITHIN GROUP (ORDER BY request_time),0),
			COALESCE(avg(request_time),0)
		FROM request_logs WHERE `+where,
		args...).Scan(&a.Total, &s2, &s3, &s4, &s5,
		&c.Hit, &c.Miss, &c.Bypass, &c.TotalBytes, &c.HitBytes,
		&lat.P50, &lat.P95, &lat.P99, &lat.Avg)
	if err != nil {
		return a, err
	}
	c.Other = a.Total - c.Hit - c.Miss - c.Bypass
	if c.Other < 0 {
		c.Other = 0
	}
	if hm := c.Hit + c.Miss; hm > 0 {
		c.OffloadReq = float64(c.Hit) / float64(hm)
	}
	if c.TotalBytes > 0 {
		c.OffloadBytes = float64(c.HitBytes) / float64(c.TotalBytes)
	}
	a.Cache = c
	a.Latency = lat
	a.StatusClasses = []LabeledCount{
		{Label: "2xx", Count: s2}, {Label: "3xx", Count: s3},
		{Label: "4xx", Count: s4}, {Label: "5xx", Count: s5},
	}

	// Top paths (by request count, carrying bytes).
	a.TopPaths = []PathStat{}
	prows, err := st.pool.Query(ctx, `SELECT COALESCE(path,''), count(*), COALESCE(sum(bytes),0)
		FROM request_logs WHERE `+where+` AND path <> ''
		GROUP BY path ORDER BY count(*) DESC LIMIT 20`, args...)
	if err != nil {
		return a, err
	}
	for prows.Next() {
		var p PathStat
		if err := prows.Scan(&p.Path, &p.Count, &p.Bytes); err != nil {
			prows.Close()
			return a, err
		}
		a.TopPaths = append(a.TopPaths, p)
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return a, err
	}

	// Top countries (GeoIP; only when populated).
	a.TopCountries = []LabeledCount{}
	crows, err := st.pool.Query(ctx, `SELECT country, count(*)
		FROM request_logs WHERE `+where+` AND country <> '' AND country IS NOT NULL
		GROUP BY country ORDER BY count(*) DESC LIMIT 20`, args...)
	if err != nil {
		return a, err
	}
	for crows.Next() {
		var lc LabeledCount
		if err := crows.Scan(&lc.Label, &lc.Count); err != nil {
			crows.Close()
			return a, err
		}
		a.TopCountries = append(a.TopCountries, lc)
	}
	crows.Close()
	return a, crows.Err()
}

// QueryRequestLogs returns access-log rows newest-first for the filter.
func (st *Store) QueryRequestLogs(ctx context.Context, f RequestLogFilter) ([]RequestLog, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var b strings.Builder
	b.WriteString(`SELECT ts, server_id, COALESCE(edge_id,''), zone_id, COALESCE(host(client_ip),''),
		COALESCE(country,''), COALESCE(method,''), COALESCE(host,''), COALESCE(path,''),
		COALESCE(status,0), COALESCE(bytes,0), COALESCE(cache_status,''),
		COALESCE(request_time,0), COALESCE(upstream_time,0), COALESCE(referer,''),
		COALESCE(user_agent,''), COALESCE(request_id,'')
		FROM request_logs WHERE ts >= $1 AND ts <= $2`)
	args := []any{f.From, f.To}
	add := func(cond string, val any) {
		args = append(args, val)
		b.WriteString(" AND " + cond + " $" + strconv.Itoa(len(args)))
	}
	if len(f.ZoneIDs) > 0 {
		args = append(args, f.ZoneIDs)
		b.WriteString(" AND zone_id = ANY($" + strconv.Itoa(len(args)) + ")")
	}
	switch {
	case f.Status >= 100:
		add("status =", f.Status)
	case f.Status >= 2 && f.Status <= 5:
		args = append(args, f.Status*100, f.Status*100+99)
		b.WriteString(" AND status BETWEEN $" + strconv.Itoa(len(args)-1) + " AND $" + strconv.Itoa(len(args)))
	}
	if f.CacheStatus != "" {
		add("cache_status =", f.CacheStatus)
	}
	if f.ClientIP != "" {
		add("host(client_ip) =", f.ClientIP)
	}
	if f.Country != "" {
		add("country =", f.Country)
	}
	if f.Path != "" {
		args = append(args, "%"+f.Path+"%")
		b.WriteString(" AND path LIKE $" + strconv.Itoa(len(args)))
	}
	args = append(args, limit)
	b.WriteString(" ORDER BY ts DESC LIMIT $" + strconv.Itoa(len(args)))

	rows, err := st.pool.Query(ctx, b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RequestLog{}
	for rows.Next() {
		var l RequestLog
		if err := rows.Scan(&l.TS, &l.ServerID, &l.EdgeID, &l.ZoneID, &l.ClientIP, &l.Country,
			&l.Method, &l.Host, &l.Path, &l.Status, &l.Bytes, &l.CacheStatus,
			&l.RequestTime, &l.UpstreamTime, &l.Referer, &l.UserAgent, &l.RequestID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
