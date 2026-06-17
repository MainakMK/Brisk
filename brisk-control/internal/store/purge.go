package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// PurgeJob is one recorded purge request and its fan-out status.
type PurgeJob struct {
	ID          int64      `json:"id"`
	AccountID   int64      `json:"account_id"`
	ZoneID      *int64     `json:"zone_id,omitempty"`
	Hostname    string     `json:"hostname,omitempty"` // zone hostname snapshot (survives zone delete)
	Type        string     `json:"type"`
	Target      string     `json:"target"`
	Status      string     `json:"status"`
	EdgesTotal  int32      `json:"edges_total"`
	EdgesDone   int32      `json:"edges_done"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

const purgeJobCols = `id, account_id, zone_id, hostname, type, target, status,
	edges_total, edges_done, created_at, completed_at`

func scanPurgeJob(row pgx.Row) (PurgeJob, error) {
	var j PurgeJob
	err := row.Scan(&j.ID, &j.AccountID, &j.ZoneID, &j.Hostname, &j.Type, &j.Target, &j.Status,
		&j.EdgesTotal, &j.EdgesDone, &j.CreatedAt, &j.CompletedAt)
	return j, err
}

// CreatePurgeJob inserts a pending purge job and returns it. zoneID is nil for
// purge-all. edgesTotal is the number of edges the purge will be published to;
// status starts 'done' immediately when edgesTotal is 0 (nothing to wait for).
func (st *Store) CreatePurgeJob(ctx context.Context, zoneID *int64, typ, target string, edgesTotal int) (PurgeJob, error) {
	status := "pending"
	if edgesTotal == 0 {
		status = "done"
	}
	// Snapshot the zone hostname onto the row so purge history stays meaningful
	// after the zone is deleted (FK is now ON DELETE SET NULL — migration 00019).
	row := st.pool.QueryRow(ctx, `
		INSERT INTO purge_jobs (zone_id, hostname, type, target, status, edges_total,
			completed_at)
		VALUES ($1, COALESCE((SELECT cdn_hostname FROM zones WHERE id = $1), ''),
			$2, $3, $4, $5, CASE WHEN $5 = 0 THEN now() ELSE NULL END)
		RETURNING `+purgeJobCols,
		zoneID, typ, target, status, edgesTotal)
	return scanPurgeJob(row)
}

// ListPurgeJobs returns recent purge jobs, newest first. If zoneID is non-nil it
// filters to that zone; limit caps the result (default 100).
func (st *Store) ListPurgeJobs(ctx context.Context, zoneID *int64, limit int) ([]PurgeJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var (
		rows pgx.Rows
		err  error
	)
	if zoneID != nil {
		rows, err = st.pool.Query(ctx, `SELECT `+purgeJobCols+`
			FROM purge_jobs WHERE zone_id = $1 ORDER BY created_at DESC LIMIT $2`, *zoneID, limit)
	} else {
		rows, err = st.pool.Query(ctx, `SELECT `+purgeJobCols+`
			FROM purge_jobs ORDER BY created_at DESC LIMIT $1`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PurgeJob{}
	for rows.Next() {
		j, err := scanPurgeJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// MarkPurgeJobEdgeDone increments edges_done for a job; when all edges have
// reported it flips status to 'done' and stamps completed_at. Returns the
// updated job, or ErrNotFound if the job id does not exist.
func (st *Store) MarkPurgeJobEdgeDone(ctx context.Context, jobID int64) (PurgeJob, error) {
	row := st.pool.QueryRow(ctx, `
		UPDATE purge_jobs SET
			edges_done = LEAST(edges_done + 1, edges_total),
			status = CASE WHEN edges_done + 1 >= edges_total THEN 'done' ELSE 'partial' END,
			completed_at = CASE WHEN edges_done + 1 >= edges_total THEN now() ELSE completed_at END
		WHERE id = $1
		RETURNING `+purgeJobCols, jobID)
	j, err := scanPurgeJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PurgeJob{}, ErrNotFound
	}
	return j, err
}

// ServersForZone returns the servers (edges) currently serving a zone.
func (st *Store) ServersForZone(ctx context.Context, zoneID int64) ([]Server, error) {
	rows, err := st.pool.Query(ctx, `
		SELECT `+serverCols+`
		FROM servers s
		JOIN server_zones sz ON sz.server_id = s.id
		WHERE sz.zone_id = $1
		ORDER BY s.id`, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Server{}
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
