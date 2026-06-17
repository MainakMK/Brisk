// Package auth authenticates agent requests. The Authenticator interface keeps
// the mechanism swappable: today bearer tokens, later mTLS (read the client cert
// from r.TLS.PeerCertificates) — a drop-in replacement, no handler changes.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"brisk-control/internal/store"
	"brisk-control/internal/token"
)

// Authenticator returns the authenticated server's ID, or an error.
type Authenticator interface {
	Authenticate(r *http.Request) (serverID int64, err error)
}

// ErrUnauthorized is returned for missing/invalid/revoked credentials.
var ErrUnauthorized = errors.New("unauthorized")

// TokenAuthenticator validates "Authorization: Bearer brisk_..." tokens.
type TokenAuthenticator struct {
	store *store.Store
}

// NewTokenAuthenticator builds a bearer-token authenticator.
func NewTokenAuthenticator(st *store.Store) *TokenAuthenticator {
	return &TokenAuthenticator{store: st}
}

// Authenticate implements Authenticator using the agent_tokens table.
func (a *TokenAuthenticator) Authenticate(r *http.Request) (int64, error) {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || tok == "" {
		return 0, ErrUnauthorized
	}
	lookup, err := a.store.ActiveTokenByPrefix(r.Context(), token.Prefix(tok))
	if err != nil {
		return 0, ErrUnauthorized // not found / revoked / db error -> 401
	}
	if !token.Verify(tok, lookup.Hash) {
		return 0, ErrUnauthorized
	}
	return lookup.ServerID, nil
}

// --- request context ---

type ctxKey int

const serverIDKey ctxKey = iota

// ServerIDFromContext returns the authenticated server ID set by Middleware.
func ServerIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(serverIDKey).(int64)
	return id, ok
}

// Middleware authenticates the request and attaches the server ID to context.
// On failure it responds 401 with a JSON error and does not call next.
func Middleware(a Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serverID, err := a.Authenticate(r)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
			ctx := context.WithValue(r.Context(), serverIDKey, serverID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
