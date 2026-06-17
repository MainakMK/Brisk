package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// CreateAgentToken stores a token's prefix + SHA-256 hash for a server.
// The plaintext token is never persisted.
func (st *Store) CreateAgentToken(ctx context.Context, serverID int64, prefix, hash string) error {
	_, err := st.pool.Exec(ctx,
		`INSERT INTO agent_tokens (server_id, token_prefix, token_hash) VALUES ($1, $2, $3)`,
		serverID, prefix, hash)
	return err
}

// TokenLookup is the active-token row needed to verify a presented token.
type TokenLookup struct {
	ID       int64
	ServerID int64
	Hash     string
}

// ActiveTokenByPrefix returns the non-revoked token row for a prefix, or ErrNotFound.
func (st *Store) ActiveTokenByPrefix(ctx context.Context, prefix string) (TokenLookup, error) {
	var t TokenLookup
	err := st.pool.QueryRow(ctx,
		`SELECT id, server_id, token_hash FROM agent_tokens
		 WHERE token_prefix = $1 AND revoked_at IS NULL
		 ORDER BY id DESC LIMIT 1`, prefix).
		Scan(&t.ID, &t.ServerID, &t.Hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenLookup{}, ErrNotFound
	}
	return t, err
}

// RevokeServerTokens revokes all active tokens for a server (used on rotation).
func (st *Store) RevokeServerTokens(ctx context.Context, serverID int64) error {
	_, err := st.pool.Exec(ctx,
		`UPDATE agent_tokens SET revoked_at = now() WHERE server_id = $1 AND revoked_at IS NULL`,
		serverID)
	return err
}
