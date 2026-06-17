package store

import (
	"context"
	"time"
)

// ProvisionLog is one line of provisioning output for a server.
type ProvisionLog struct {
	ID      int64     `json:"id"`
	Ts      time.Time `json:"ts"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// AddProvisionLog appends a provisioning log line.
func (st *Store) AddProvisionLog(ctx context.Context, serverID int64, level, message string) error {
	_, err := st.pool.Exec(ctx,
		`INSERT INTO provision_logs (server_id, level, message) VALUES ($1, $2, $3)`,
		serverID, level, message)
	return err
}

// ListProvisionLogs returns a server's provisioning log lines in order.
func (st *Store) ListProvisionLogs(ctx context.Context, serverID int64) ([]ProvisionLog, error) {
	rows, err := st.pool.Query(ctx,
		`SELECT id, ts, level, message FROM provision_logs WHERE server_id = $1 ORDER BY id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProvisionLog{}
	for rows.Next() {
		var l ProvisionLog
		if err := rows.Scan(&l.ID, &l.Ts, &l.Level, &l.Message); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetCPIdentity returns the control plane's SSH keypair PEMs (empty if unset).
func (st *Store) GetCPIdentity(ctx context.Context) (priv, pub string, err error) {
	var p, q *string
	err = st.pool.QueryRow(ctx,
		`SELECT ssh_private_key, ssh_public_key FROM control_plane WHERE id = 1`).Scan(&p, &q)
	if err != nil {
		return "", "", err
	}
	if p != nil {
		priv = *p
	}
	if q != nil {
		pub = *q
	}
	return priv, pub, nil
}

// SaveCPIdentity stores the control plane's SSH keypair.
func (st *Store) SaveCPIdentity(ctx context.Context, priv, pub string) error {
	_, err := st.pool.Exec(ctx,
		`UPDATE control_plane SET ssh_private_key = $1, ssh_public_key = $2 WHERE id = 1`, priv, pub)
	return err
}
