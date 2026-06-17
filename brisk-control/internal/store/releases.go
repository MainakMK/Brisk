package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Release is one signed agent binary the fleet can self-update to (migration 00030).
// The binary bytes are NOT carried on the Release struct (they're ~20MB) — fetch them
// separately via GetReleaseBinary only when an agent downloads.
type Release struct {
	Version    string    `json:"version"`
	SHA256     string    `json:"sha256"`
	Signature  string    `json:"signature,omitempty"`
	SignedBy   string    `json:"signed_by"`
	SizeBytes  int64     `json:"size_bytes"`
	Notes      string    `json:"notes"`
	UploadedBy string    `json:"uploaded_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

const releaseCols = `version, sha256, signature, signed_by, size_bytes, notes, uploaded_by, created_at`

func scanRelease(row pgx.Row) (Release, error) {
	var r Release
	err := row.Scan(&r.Version, &r.SHA256, &r.Signature, &r.SignedBy, &r.SizeBytes, &r.Notes, &r.UploadedBy, &r.CreatedAt)
	return r, err
}

// InsertRelease stores a release. The caller MUST have recomputed sha256 and verified the
// signature against a trusted key BEFORE calling this (the store does not trust its inputs).
// size_bytes is derived from the binary, not the caller.
func (st *Store) InsertRelease(ctx context.Context, r Release, binary []byte) error {
	_, err := st.pool.Exec(ctx,
		`INSERT INTO agent_releases (version, bin_data, sha256, signature, signed_by, size_bytes, notes, uploaded_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		r.Version, binary, r.SHA256, r.Signature, r.SignedBy, len(binary), r.Notes, r.UploadedBy)
	return err
}

// ListReleases returns release metadata (no binary), newest first.
func (st *Store) ListReleases(ctx context.Context) ([]Release, error) {
	rows, err := st.pool.Query(ctx, `SELECT `+releaseCols+` FROM agent_releases ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Release{}
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestRelease returns the newest release. Returns pgx.ErrNoRows when there are none.
func (st *Store) LatestRelease(ctx context.Context) (Release, error) {
	return scanRelease(st.pool.QueryRow(ctx,
		`SELECT `+releaseCols+` FROM agent_releases ORDER BY created_at DESC LIMIT 1`))
}

// GetReleaseMeta returns metadata for one version.
func (st *Store) GetReleaseMeta(ctx context.Context, version string) (Release, error) {
	return scanRelease(st.pool.QueryRow(ctx,
		`SELECT `+releaseCols+` FROM agent_releases WHERE version=$1`, version))
}

// GetReleaseBinary streams the binary bytes + sha256 for one version (for the agent download path).
func (st *Store) GetReleaseBinary(ctx context.Context, version string) ([]byte, string, error) {
	var bin []byte
	var sha string
	err := st.pool.QueryRow(ctx, `SELECT bin_data, sha256 FROM agent_releases WHERE version=$1`, version).Scan(&bin, &sha)
	return bin, sha, err
}

// DeleteRelease removes a release row entirely.
func (st *Store) DeleteRelease(ctx context.Context, version string) error {
	_, err := st.pool.Exec(ctx, `DELETE FROM agent_releases WHERE version=$1`, version)
	return err
}

// PruneReleases keeps the newest `keep` releases plus any version in `protect` (those currently
// running on an edge), and deletes the rest. Protects the fleet from losing a binary it may still
// need to roll back to.
func (st *Store) PruneReleases(ctx context.Context, keep int, protect []string) error {
	if protect == nil {
		protect = []string{}
	}
	_, err := st.pool.Exec(ctx, `
		DELETE FROM agent_releases WHERE version IN (
		  SELECT version FROM agent_releases
		  WHERE version <> ALL($2)
		  ORDER BY created_at DESC
		  OFFSET $1
		)`, keep, protect)
	return err
}
