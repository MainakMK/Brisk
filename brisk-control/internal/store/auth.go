package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// --- accounts (human login) ---

// Account is a login identity (admin now; customer in Phase 5).
type Account struct {
	ID           int64
	Name         string
	Role         string
	Email        *string
	PasswordHash *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const accountCols = `id, name, role, email, password_hash, created_at, updated_at`

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Name, &a.Role, &a.Email, &a.PasswordHash, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

// GetAccountByEmail looks up an account by its login email (case-folded by the
// caller). Returns ErrNotFound when absent.
func (st *Store) GetAccountByEmail(ctx context.Context, email string) (Account, error) {
	row := st.pool.QueryRow(ctx, `SELECT `+accountCols+` FROM accounts WHERE email = $1`, email)
	a, err := scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// GetAccountByID returns one account or ErrNotFound.
func (st *Store) GetAccountByID(ctx context.Context, id int64) (Account, error) {
	row := st.pool.QueryRow(ctx, `SELECT `+accountCols+` FROM accounts WHERE id = $1`, id)
	a, err := scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// SetAccountCredentials sets the login email + argon2 password hash for an account
// (used by bootstrap and change-password). email is optional (nil = leave).
func (st *Store) SetAccountCredentials(ctx context.Context, id int64, email *string, passwordHash string) error {
	ct, err := st.pool.Exec(ctx, `
		UPDATE accounts
		   SET email = COALESCE($2, email), password_hash = $3, updated_at = now()
		 WHERE id = $1`, id, email, passwordHash)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- sessions (dashboard cookie) ---

// Session is a server-side login session (the cookie holds the plaintext id; we
// store only its hash). Role is joined from the owning account.
type Session struct {
	AccountID int64
	Role      string
	CSRFHash  string
	ExpiresAt time.Time
}

// CreateSession inserts a session row.
func (st *Store) CreateSession(ctx context.Context, tokenHash, csrfHash string, accountID int64, userAgent string, expiresAt time.Time) error {
	_, err := st.pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, csrf_hash, account_id, user_agent, expires_at)
		VALUES ($1,$2,$3,$4,$5)`, tokenHash, csrfHash, accountID, userAgent, expiresAt)
	return err
}

// GetSession resolves a non-expired session by its token hash (joined with the
// account role). Returns ErrNotFound when missing or expired.
func (st *Store) GetSession(ctx context.Context, tokenHash string) (Session, error) {
	var s Session
	err := st.pool.QueryRow(ctx, `
		SELECT s.account_id, a.role, s.csrf_hash, s.expires_at
		  FROM sessions s JOIN accounts a ON a.id = s.account_id
		 WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash).
		Scan(&s.AccountID, &s.Role, &s.CSRFHash, &s.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return s, err
}

// RotateSession swaps a session's id+csrf+expiry in place (refresh / privilege
// change), keeping the same row. Returns ErrNotFound if the old hash is gone.
func (st *Store) RotateSession(ctx context.Context, oldHash, newHash, newCSRFHash string, expiresAt time.Time) error {
	ct, err := st.pool.Exec(ctx, `
		UPDATE sessions
		   SET token_hash = $2, csrf_hash = $3, expires_at = $4, last_used_at = now()
		 WHERE token_hash = $1`, oldHash, newHash, newCSRFHash, expiresAt)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSession removes a session by token hash (logout). No error if absent.
func (st *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := st.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// DeleteAccountSessions removes all sessions for an account (e.g. after a password
// change, to log out other devices).
func (st *Store) DeleteAccountSessions(ctx context.Context, accountID int64) error {
	_, err := st.pool.Exec(ctx, `DELETE FROM sessions WHERE account_id = $1`, accountID)
	return err
}

// --- admin API tokens (bearer, for scripts/automation) ---

// AdminToken is the metadata view of an admin API token (never carries the secret).
type AdminToken struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// AdminTokenLookup is the auth-time view (hash + owning account/role).
type AdminTokenLookup struct {
	ID        int64
	AccountID int64
	Role      string
	Hash      string
}

// CreateAdminToken stores a new (already-hashed) admin token and returns its id.
func (st *Store) CreateAdminToken(ctx context.Context, accountID int64, name, prefix, tokenHash string) (int64, error) {
	var id int64
	err := st.pool.QueryRow(ctx, `
		INSERT INTO admin_api_tokens (account_id, name, prefix, token_hash)
		VALUES ($1,$2,$3,$4) RETURNING id`, accountID, name, prefix, tokenHash).Scan(&id)
	return id, err
}

// ActiveAdminTokensByPrefix returns non-revoked candidates matching the prefix
// (the authenticator constant-time-verifies the full hash). Joined with role.
func (st *Store) ActiveAdminTokensByPrefix(ctx context.Context, prefix string) ([]AdminTokenLookup, error) {
	rows, err := st.pool.Query(ctx, `
		SELECT t.id, t.account_id, a.role, t.token_hash
		  FROM admin_api_tokens t JOIN accounts a ON a.id = t.account_id
		 WHERE t.prefix = $1 AND t.revoked_at IS NULL`, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminTokenLookup
	for rows.Next() {
		var l AdminTokenLookup
		if err := rows.Scan(&l.ID, &l.AccountID, &l.Role, &l.Hash); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// TouchAdminToken records last use (best-effort; ignore errors at the call site).
func (st *Store) TouchAdminToken(ctx context.Context, id int64) error {
	_, err := st.pool.Exec(ctx, `UPDATE admin_api_tokens SET last_used_at = now() WHERE id = $1`, id)
	return err
}

// ListAdminTokens returns an account's tokens (metadata only), newest first.
func (st *Store) ListAdminTokens(ctx context.Context, accountID int64) ([]AdminToken, error) {
	rows, err := st.pool.Query(ctx, `
		SELECT id, name, prefix, created_at, last_used_at, revoked_at
		  FROM admin_api_tokens WHERE account_id = $1 ORDER BY id DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminToken{}
	for rows.Next() {
		var t AdminToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAdminToken marks a token revoked, scoped to its owning account (so a
// caller can't revoke another tenant's token). ErrNotFound if absent/not owned.
func (st *Store) RevokeAdminToken(ctx context.Context, accountID, id int64) error {
	ct, err := st.pool.Exec(ctx, `
		UPDATE admin_api_tokens SET revoked_at = now()
		 WHERE id = $1 AND account_id = $2 AND revoked_at IS NULL`, id, accountID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAdminToken permanently removes a token row, but ONLY if it is already revoked
// (revoked_at IS NOT NULL). This is the "tidy up the list" action — an active token must be
// revoked first (which immediately stops it working) before it can be deleted. Scoped to the
// owning account. ErrNotFound if absent, not owned, or still active.
func (st *Store) DeleteAdminToken(ctx context.Context, accountID, id int64) error {
	ct, err := st.pool.Exec(ctx, `
		DELETE FROM admin_api_tokens
		 WHERE id = $1 AND account_id = $2 AND revoked_at IS NOT NULL`, id, accountID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
