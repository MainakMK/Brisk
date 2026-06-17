// Package identity is the tenant-aware RBAC core (Phase 3.7 Step 3).
//
// Every authorization decision flows through ONE chokepoint here:
//
//	actor (account + role)  ->  action  ->  resource (account-scoped)
//
// Design rule from multi-tenant SaaS practice: never just ask "is this user an
// admin?" — ask "is this user allowed to act on THIS account's resources?". The
// tenant-scoping check is implemented NOW, even though only admin accounts exist,
// so the Phase-5 customer portal slots in without a refactor and can't leak across
// tenants (a missing scope check is one bug from a data leak).
package identity

import (
	"context"
	"errors"
)

// Roles. Stored in accounts.role.
const (
	RoleAdmin    = "admin"
	RoleCustomer = "customer"
)

// Identity is the resolved caller, attached to the request context by the auth
// middleware (from a session cookie or an admin bearer token).
type Identity struct {
	AccountID int64
	Role      string
	Method    string // "session" | "token" — for audit/debug, not authorization
}

// IsAdmin reports whether the caller has the admin role (full access today).
func (i Identity) IsAdmin() bool { return i.Role == RoleAdmin }

// ErrForbidden is returned by the authorization helpers when a caller is
// authenticated but not permitted (maps to HTTP 403).
var ErrForbidden = errors.New("forbidden")

// CanAccessAccount is THE tenant-scoping primitive: admin may touch any account;
// a customer may only touch resources owned by its own account.
func (i Identity) CanAccessAccount(resourceAccountID int64) bool {
	if i.Role == RoleAdmin {
		return true
	}
	return i.AccountID == resourceAccountID
}

// Authorize is the single chokepoint handlers call before acting on an
// account-scoped resource. Returns nil when allowed, ErrForbidden otherwise.
// (The "action" dimension is implicit today — admin=all, customer=own — and grows
// here, not in scattered per-handler checks, when the portal lands.)
func Authorize(i Identity, resourceAccountID int64) error {
	if i.CanAccessAccount(resourceAccountID) {
		return nil
	}
	return ErrForbidden
}

// RequireAdmin guards infrastructure resources that are admin-only regardless of
// account (servers, DNS, health, network stats). Customers never touch these.
func RequireAdmin(i Identity) error {
	if i.IsAdmin() {
		return nil
	}
	return ErrForbidden
}

// --- request context ---

type ctxKey int

const idKey ctxKey = iota

// NewContext returns a context carrying the resolved identity.
func NewContext(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, idKey, id)
}

// FromContext returns the identity attached by the auth middleware.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(idKey).(Identity)
	return id, ok
}
