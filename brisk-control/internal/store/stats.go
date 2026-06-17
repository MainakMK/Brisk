package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// StatSample is one row to insert. ZoneID nil means a server-level summary.
type StatSample struct {
	Time         time.Time
	ZoneID       *int64
	Requests     int64
	Hits         int64
	Misses       int64
	BytesSent    int64
	BandwidthBps int64
	CPUPct       *float64
	RAMPct       *float64
	DiskPct      *float64
}

var statsColumns = []string{
	"time", "server_id", "zone_id", "requests", "hits", "misses",
	"bytes_sent", "bandwidth_bps", "cpu_pct", "ram_pct", "disk_pct", "hit_ratio",
}

// InsertStats bulk-inserts samples for a server via COPY. hit_ratio is stored
// NULL and computed at read time.
func (st *Store) InsertStats(ctx context.Context, serverID int64, samples []StatSample) (int64, error) {
	rows := make([][]any, 0, len(samples))
	for _, s := range samples {
		rows = append(rows, []any{
			s.Time, serverID, s.ZoneID, s.Requests, s.Hits, s.Misses,
			s.BytesSent, s.BandwidthBps, s.CPUPct, s.RAMPct, s.DiskPct, nil,
		})
	}
	return st.pool.CopyFrom(ctx, pgx.Identifier{"stats"}, statsColumns, pgx.CopyFromRows(rows))
}

// --- queries ---

// Overview is the network-wide summary (latest minute).
type Overview struct {
	OnlineServers int64   `json:"online_servers"`
	ReqPerSec     float64 `json:"req_per_sec"`
	BandwidthBps  float64 `json:"bandwidth_bps"`
	HitRatio      float64 `json:"hit_ratio"`
	WindowSeconds int     `json:"window_seconds"`
}

// Overview returns fleet totals over the last 60s of server-level samples.
func (st *Store) Overview(ctx context.Context) (Overview, error) {
	o := Overview{WindowSeconds: 60}
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*) FROM servers WHERE status = 'online'`).Scan(&o.OnlineServers); err != nil {
		return o, err
	}
	var req, hits, misses, bytes int64
	err := st.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(requests),0), COALESCE(sum(hits),0),
		       COALESCE(sum(misses),0), COALESCE(sum(bytes_sent),0)
		FROM stats
		WHERE zone_id IS NULL AND time > now() - interval '60 seconds'`).
		Scan(&req, &hits, &misses, &bytes)
	if err != nil {
		return o, err
	}
	o.ReqPerSec = float64(req) / 60.0
	o.BandwidthBps = float64(bytes) * 8 / 60.0
	o.HitRatio = ratio(hits, misses)
	return o, nil
}

// ServerLive is the latest live snapshot for one PoP.
type ServerLive struct {
	ServerID     int64      `json:"server_id"`
	CPUPct       *float64   `json:"cpu_pct"`
	RAMPct       *float64   `json:"ram_pct"`
	DiskPct      *float64   `json:"disk_pct"`
	ReqPerSec    float64    `json:"req_per_sec"`
	BandwidthBps float64    `json:"bandwidth_bps"`
	HitRatio     float64    `json:"hit_ratio"`
	LastSample   *time.Time `json:"last_sample"`
	// Total RAM + cache-disk capacity (bytes) from the server row — lets the UI show
	// absolute "X free of Y" alongside the used% above. Nil until the agent reports them.
	RAMTotalBytes  *int64 `json:"ram_total_bytes"`
	DiskTotalBytes *int64 `json:"disk_total_bytes"`
}

// ServerLive returns the latest cpu/ram/disk plus 60s-windowed rates for a server.
func (st *Store) ServerLive(ctx context.Context, serverID int64) (ServerLive, error) {
	l := ServerLive{ServerID: serverID}

	var ts time.Time
	err := st.pool.QueryRow(ctx, `
		SELECT time, cpu_pct, ram_pct, disk_pct
		FROM stats
		WHERE server_id = $1 AND zone_id IS NULL
		ORDER BY time DESC LIMIT 1`, serverID).
		Scan(&ts, &l.CPUPct, &l.RAMPct, &l.DiskPct)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return l, err
	}
	if err == nil {
		l.LastSample = &ts
	}

	var req, hits, misses, bytes int64
	if err := st.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(requests),0), COALESCE(sum(hits),0),
		       COALESCE(sum(misses),0), COALESCE(sum(bytes_sent),0)
		FROM stats
		WHERE server_id = $1 AND zone_id IS NULL AND time > now() - interval '60 seconds'`,
		serverID).Scan(&req, &hits, &misses, &bytes); err != nil {
		return l, err
	}
	l.ReqPerSec = float64(req) / 60.0
	l.BandwidthBps = float64(bytes) * 8 / 60.0
	l.HitRatio = ratio(hits, misses)

	// Hardware totals (quasi-static) from the server row — used by the UI to show
	// absolute "X free of Y" next to the live percentages. Best-effort: nil if unset.
	_ = st.pool.QueryRow(ctx,
		`SELECT ram_total_bytes, disk_total_bytes FROM servers WHERE id = $1`, serverID).
		Scan(&l.RAMTotalBytes, &l.DiskTotalBytes)

	return l, nil
}

// SeriesPoint is one time-bucketed data point for graphs.
type SeriesPoint struct {
	Time         time.Time `json:"time"`
	Requests     int64     `json:"requests"`
	Hits         int64     `json:"hits"`
	Misses       int64     `json:"misses"`
	BytesSent    int64     `json:"bytes_sent"`
	BandwidthBps *float64  `json:"bandwidth_bps"`
	CPUPct       *float64  `json:"cpu_pct"`
	RAMPct       *float64  `json:"ram_pct"`
	DiskPct      *float64  `json:"disk_pct"`
	HitRatio     float64   `json:"hit_ratio"`
}

// StatsSeries returns a time-series for a server (server-level when zoneID is nil,
// else for that zone). resolution "1m" reads the continuous aggregate; "raw"
// reads the hypertable.
func (st *Store) StatsSeries(ctx context.Context, serverID int64, zoneID *int64, from, to time.Time, resolution string) ([]SeriesPoint, error) {
	timeCol, table := "time", "stats"
	if resolution == "1m" {
		timeCol, table = "bucket", "stats_1m"
	}
	zoneCond := "zone_id IS NULL"
	args := []any{serverID, from, to}
	if zoneID != nil {
		zoneCond = "zone_id = $4"
		args = append(args, *zoneID)
	}
	q := `SELECT ` + timeCol + `, requests, hits, misses, bytes_sent, bandwidth_bps, cpu_pct, ram_pct, disk_pct
		FROM ` + table + `
		WHERE server_id = $1 AND ` + timeCol + ` >= $2 AND ` + timeCol + ` <= $3 AND ` + zoneCond + `
		ORDER BY ` + timeCol

	rows, err := st.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeriesPoint{}
	for rows.Next() {
		var p SeriesPoint
		var bw *float64
		if err := rows.Scan(&p.Time, &p.Requests, &p.Hits, &p.Misses, &p.BytesSent,
			&bw, &p.CPUPct, &p.RAMPct, &p.DiskPct); err != nil {
			return nil, err
		}
		p.BandwidthBps = bw
		p.HitRatio = ratio(p.Hits, p.Misses)
		out = append(out, p)
	}
	return out, rows.Err()
}

// NetworkSeries returns the network-wide aggregate time-series (summed across all
// servers per bucket) — the server-side merge (Phase 4 Step 6 backlog; replaces the
// client-side "All PoPs" merge). requests/hits/misses/bytes are summed; bandwidth
// summed; cpu/ram/disk averaged. resolution "1m" reads the rollup, else the raw table.
func (st *Store) NetworkSeries(ctx context.Context, from, to time.Time, resolution string) ([]SeriesPoint, error) {
	timeCol, table := "time", "stats"
	if resolution == "1m" {
		timeCol, table = "bucket", "stats_1m"
	}
	q := `SELECT ` + timeCol + ` AS t,
			COALESCE(sum(requests),0), COALESCE(sum(hits),0), COALESCE(sum(misses),0),
			COALESCE(sum(bytes_sent),0), sum(bandwidth_bps), avg(cpu_pct), avg(ram_pct), avg(disk_pct)
		FROM ` + table + `
		WHERE ` + timeCol + ` >= $1 AND ` + timeCol + ` <= $2 AND zone_id IS NULL
		GROUP BY t ORDER BY t`
	rows, err := st.pool.Query(ctx, q, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SeriesPoint{}
	for rows.Next() {
		var p SeriesPoint
		var bw *float64
		if err := rows.Scan(&p.Time, &p.Requests, &p.Hits, &p.Misses, &p.BytesSent,
			&bw, &p.CPUPct, &p.RAMPct, &p.DiskPct); err != nil {
			return nil, err
		}
		p.BandwidthBps = bw
		p.HitRatio = ratio(p.Hits, p.Misses)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ratio computes hits/(hits+misses), 0 when there is no traffic.
func ratio(hits, misses int64) float64 {
	total := hits + misses
	if total <= 0 {
		return 0
	}
	return float64(hits) / float64(total)
}
