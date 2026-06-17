package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// AssignZone maps a zone to a server (idempotent). Returns ErrNotFound if the
// server or zone does not exist (FK violation).
func (st *Store) AssignZone(ctx context.Context, serverID, zoneID int64) error {
	_, err := st.pool.Exec(ctx,
		`INSERT INTO server_zones (server_id, zone_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		serverID, zoneID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
		return ErrNotFound
	}
	return err
}

// UnassignZone removes a zone↔server mapping; ErrNotFound if it did not exist.
func (st *Store) UnassignZone(ctx context.Context, serverID, zoneID int64) error {
	ct, err := st.pool.Exec(ctx,
		`DELETE FROM server_zones WHERE server_id = $1 AND zone_id = $2`, serverID, zoneID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListZoneServers returns the servers that serve a zone — the inverse lookup
// (Phase 4 Step 6 backlog; replaces unioning every server's /zones client-side).
func (st *Store) ListZoneServers(ctx context.Context, zoneID int64) ([]Server, error) {
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
		srv, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

// ListServerZones returns the zones assigned to a server (ordered by zone id for
// deterministic output), each with its cache rules attached.
func (st *Store) ListServerZones(ctx context.Context, serverID int64) ([]Zone, error) {
	rows, err := st.pool.Query(ctx, `
		SELECT `+zoneCols+`
		FROM zones z
		JOIN server_zones sz ON sz.zone_id = z.id
		WHERE sz.server_id = $1
		ORDER BY z.id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	zones := []Zone{}
	for rows.Next() {
		z, err := scanZone(rows)
		if err != nil {
			return nil, err
		}
		zones = append(zones, z)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Attach rules per zone (deterministic order from ListRules).
	for i := range zones {
		rules, err := st.ListRules(ctx, zones[i].ID)
		if err != nil {
			return nil, err
		}
		zones[i].Rules = rules
	}
	return zones, nil
}
