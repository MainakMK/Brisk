package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// CustomDomain lifecycle states (Phase 4 Step 2). Stored in custom_domains.status.
const (
	CustomDomainPendingDNS = "pending_dns" // added; waiting for the customer CNAME
	CustomDomainVerifying  = "verifying"   // DNS being (re)checked before any ACME
	CustomDomainIssuing    = "issuing"     // DNS verified; ACME HTTP-01 in flight
	CustomDomainActive     = "active"      // cert issued + fanned out; served via SNI
	CustomDomainRenewing   = "renewing"    // transient during an in-margin renewal
	CustomDomainFailed     = "failed"      // verify/issue/renew failed or CNAME removed
)

// CustomDomain is a customer-supplied domain attached to a zone (Phase 4 Step 2).
// The per-domain cert material lives in tls_certs keyed by name = CertName (= Domain);
// this row carries only lifecycle metadata (never key/chain).
type CustomDomain struct {
	ID             int64      `json:"id"`
	ZoneID         int64      `json:"zone_id"`
	AccountID      int64      `json:"account_id"`
	Domain         string     `json:"domain"`
	Status         string     `json:"status"`
	VerifyMethod   string     `json:"verify_method"`
	CertName       string     `json:"cert_name"`
	LastError      string     `json:"last_error"`
	AttemptCount   int32      `json:"attempt_count"`
	LastVerifiedAt *time.Time `json:"last_verified_at,omitempty"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

const customDomainCols = `id, zone_id, account_id, domain, status, verify_method,
	cert_name, last_error, attempt_count, last_verified_at, next_attempt_at,
	created_at, updated_at`

// cd-alias-qualified column list for JOIN queries (kept in lockstep with customDomainCols).
const customDomainColsCD = `cd.id, cd.zone_id, cd.account_id, cd.domain, cd.status, cd.verify_method,
	cd.cert_name, cd.last_error, cd.attempt_count, cd.last_verified_at, cd.next_attempt_at,
	cd.created_at, cd.updated_at`

func scanCustomDomain(row pgx.Row) (CustomDomain, error) {
	var c CustomDomain
	err := row.Scan(&c.ID, &c.ZoneID, &c.AccountID, &c.Domain, &c.Status, &c.VerifyMethod,
		&c.CertName, &c.LastError, &c.AttemptCount, &c.LastVerifiedAt, &c.NextAttemptAt,
		&c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// CreateCustomDomain inserts a domain in pending_dns. A duplicate domain returns a
// unique-violation error (IsUniqueViolation -> 409) — a domain can't belong to two
// zones. domain must already be lowercased/trimmed by the caller.
func (st *Store) CreateCustomDomain(ctx context.Context, zoneID, accountID int64, domain string) (CustomDomain, error) {
	row := st.pool.QueryRow(ctx, `
		INSERT INTO custom_domains (zone_id, account_id, domain, cert_name)
		VALUES ($1,$2,$3,$3)
		RETURNING `+customDomainCols,
		zoneID, accountID, domain)
	return scanCustomDomain(row)
}

// GetCustomDomain returns one custom domain or ErrNotFound.
func (st *Store) GetCustomDomain(ctx context.Context, id int64) (CustomDomain, error) {
	row := st.pool.QueryRow(ctx, `SELECT `+customDomainCols+` FROM custom_domains WHERE id = $1`, id)
	c, err := scanCustomDomain(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return CustomDomain{}, ErrNotFound
	}
	return c, err
}

// ListCustomDomainsByZone returns the domains attached to a zone (newest first).
func (st *Store) ListCustomDomainsByZone(ctx context.Context, zoneID int64) ([]CustomDomain, error) {
	return st.queryCustomDomains(ctx,
		`SELECT `+customDomainCols+` FROM custom_domains WHERE zone_id = $1 ORDER BY id DESC`, zoneID)
}

// ListAllCustomDomains returns every custom domain (admin/ops visibility + the
// lifecycle manager's scan source), ordered by id.
func (st *Store) ListAllCustomDomains(ctx context.Context) ([]CustomDomain, error) {
	return st.queryCustomDomains(ctx, `SELECT `+customDomainCols+` FROM custom_domains ORDER BY id`)
}

// ListActiveCustomDomainsForServer returns the ACTIVE custom domains whose parent
// zone is assigned to serverID — the set the agent renders as per-domain SNI
// vhosts. Ordered by zone id then domain for deterministic config + ETag.
func (st *Store) ListActiveCustomDomainsForServer(ctx context.Context, serverID int64) ([]CustomDomain, error) {
	return st.queryCustomDomains(ctx, `
		SELECT `+customDomainColsCD+`
		FROM custom_domains cd
		JOIN server_zones sz ON sz.zone_id = cd.zone_id
		WHERE sz.server_id = $1 AND cd.status = 'active'
		ORDER BY cd.zone_id, cd.domain`, serverID)
}

func (st *Store) queryCustomDomains(ctx context.Context, sql string, args ...any) ([]CustomDomain, error) {
	rows, err := st.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CustomDomain{}
	for rows.Next() {
		c, err := scanCustomDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetCustomDomainStatus updates status + last_error (e.g. pending_dns->verifying,
// ->issuing, ->renewing). Does not touch the backoff/verify timestamps.
func (st *Store) SetCustomDomainStatus(ctx context.Context, id int64, status, lastError string) error {
	ct, err := st.pool.Exec(ctx, `
		UPDATE custom_domains
		SET status = $2, last_error = $3, updated_at = now()
		WHERE id = $1`, id, status, lastError)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkCustomDomainVerified records a successful DNS verification: clears the error
// + backoff and stamps last_verified_at. Status is set by the caller separately.
func (st *Store) MarkCustomDomainVerified(ctx context.Context, id int64) error {
	_, err := st.pool.Exec(ctx, `
		UPDATE custom_domains
		SET last_verified_at = now(), attempt_count = 0, last_error = '',
		    next_attempt_at = NULL, updated_at = now()
		WHERE id = $1`, id)
	return err
}

// SetCustomDomainActive marks a domain active after a successful issue/renew:
// records cert_name, stamps last_verified_at, clears the error + backoff.
func (st *Store) SetCustomDomainActive(ctx context.Context, id int64, certName string) error {
	_, err := st.pool.Exec(ctx, `
		UPDATE custom_domains
		SET status = 'active', cert_name = $2, last_error = '', attempt_count = 0,
		    last_verified_at = now(), next_attempt_at = NULL, updated_at = now()
		WHERE id = $1`, id, certName)
	return err
}

// MarkCustomDomainFailure records a verify/issue/renew failure: sets status +
// last_error, bumps attempt_count, and gates the next attempt (exponential backoff).
func (st *Store) MarkCustomDomainFailure(ctx context.Context, id int64, status, lastError string, nextAttemptAt time.Time) error {
	_, err := st.pool.Exec(ctx, `
		UPDATE custom_domains
		SET status = $2, last_error = $3, attempt_count = attempt_count + 1,
		    next_attempt_at = $4, updated_at = now()
		WHERE id = $1`, id, status, lastError, nextAttemptAt)
	return err
}

// DeleteCustomDomain removes a domain (detach); ErrNotFound if absent. The parent
// zone's config_version must be bumped separately so edges drop the vhost.
func (st *Store) DeleteCustomDomain(ctx context.Context, id int64) error {
	ct, err := st.pool.Exec(ctx, `DELETE FROM custom_domains WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// BumpZoneConfigVersion increments a zone's config_version so the agent-config
// ETag changes and assigned edges re-pull — used when a custom domain becomes
// active or is detached (add/remove a per-domain vhost) without a zone edit.
func (st *Store) BumpZoneConfigVersion(ctx context.Context, zoneID int64) error {
	_, err := st.pool.Exec(ctx, `
		UPDATE zones SET config_version = config_version + 1, updated_at = now()
		WHERE id = $1`, zoneID)
	return err
}
