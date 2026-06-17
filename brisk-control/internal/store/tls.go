package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// TLSCert is a control-plane-managed certificate (Phase 3.7 Step 2, Part 3).
// The control plane issues it via lego Bunny DNS-01 and fans the PEM material out
// to edges over the agent-config pull channel. The dashboard only ever sees the
// metadata (issuer/serial/expiry), never FullChain or PrivKey.
type TLSCert struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`    // stable key, e.g. apex "a2zjav.com"
	Domains   string     `json:"domains"` // comma-joined SANs "*.a2zjav.com,a2zjav.com"
	FullChain string     `json:"-"`       // PEM — never serialized to the dashboard
	PrivKey   string     `json:"-"`       // PEM — never serialized to the dashboard
	Issuer    string     `json:"issuer"`
	Serial    string     `json:"serial"`
	Staging   bool       `json:"staging"`
	NotBefore *time.Time `json:"not_before,omitempty"`
	NotAfter  *time.Time `json:"not_after,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

const tlsCertCols = `id, name, domains, fullchain, privkey, issuer, serial,
	staging, not_before, not_after, updated_at`

func scanTLSCert(row pgx.Row) (TLSCert, error) {
	var c TLSCert
	err := row.Scan(&c.ID, &c.Name, &c.Domains, &c.FullChain, &c.PrivKey, &c.Issuer,
		&c.Serial, &c.Staging, &c.NotBefore, &c.NotAfter, &c.UpdatedAt)
	return c, err
}

// GetTLSCert returns the managed cert with the given name, or ErrNotFound.
func (st *Store) GetTLSCert(ctx context.Context, name string) (TLSCert, error) {
	row := st.pool.QueryRow(ctx, `SELECT `+tlsCertCols+` FROM tls_certs WHERE name = $1`, name)
	c, err := scanTLSCert(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return TLSCert{}, ErrNotFound
	}
	return c, err
}

// ListTLSCerts returns all managed certs (ordered by name) — full PEM included,
// for the agent-config fan-out. Callers that surface to humans must strip
// FullChain/PrivKey (the JSON tags already drop them).
func (st *Store) ListTLSCerts(ctx context.Context) ([]TLSCert, error) {
	rows, err := st.pool.Query(ctx, `SELECT `+tlsCertCols+` FROM tls_certs ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TLSCert{}
	for rows.Next() {
		c, err := scanTLSCert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteTLSCert removes the managed cert with the given name (used when a custom
// domain is detached, so the per-domain cert is no longer fanned out to edges).
// Missing is not an error — detach is idempotent.
func (st *Store) DeleteTLSCert(ctx context.Context, name string) error {
	_, err := st.pool.Exec(ctx, `DELETE FROM tls_certs WHERE name = $1`, name)
	return err
}

// UpsertTLSCert inserts or replaces the cert keyed by name (one row per cert
// name; a renewal overwrites in place and bumps updated_at).
func (st *Store) UpsertTLSCert(ctx context.Context, c TLSCert) error {
	_, err := st.pool.Exec(ctx, `
		INSERT INTO tls_certs (name, domains, fullchain, privkey, issuer, serial,
			staging, not_before, not_after, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, now())
		ON CONFLICT (name) DO UPDATE SET
			domains    = EXCLUDED.domains,
			fullchain  = EXCLUDED.fullchain,
			privkey    = EXCLUDED.privkey,
			issuer     = EXCLUDED.issuer,
			serial     = EXCLUDED.serial,
			staging    = EXCLUDED.staging,
			not_before = EXCLUDED.not_before,
			not_after  = EXCLUDED.not_after,
			updated_at = now()`,
		c.Name, c.Domains, c.FullChain, c.PrivKey, c.Issuer, c.Serial,
		c.Staging, c.NotBefore, c.NotAfter)
	return err
}
