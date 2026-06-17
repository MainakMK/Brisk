package identity

import (
	"errors"
	"testing"
)

func TestAuthorizeTenantScoping(t *testing.T) {
	admin := Identity{AccountID: 1, Role: RoleAdmin}
	custA := Identity{AccountID: 7, Role: RoleCustomer}

	cases := []struct {
		name        string
		actor       Identity
		resourceAcc int64
		wantErr     bool
	}{
		{"admin sees own", admin, 1, false},
		{"admin sees other tenant", admin, 99, false}, // admin = full access
		{"customer sees own", custA, 7, false},
		{"customer BLOCKED cross-tenant", custA, 8, true}, // the portal-safety check
		{"customer BLOCKED on account 1", custA, 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Authorize(c.actor, c.resourceAcc)
			if c.wantErr && !errors.Is(err, ErrForbidden) {
				t.Fatalf("want ErrForbidden, got %v", err)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("want allowed, got %v", err)
			}
		})
	}
}

func TestRequireAdmin(t *testing.T) {
	if err := RequireAdmin(Identity{Role: RoleAdmin}); err != nil {
		t.Fatalf("admin should pass RequireAdmin: %v", err)
	}
	if err := RequireAdmin(Identity{Role: RoleCustomer}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("customer should be forbidden on admin-only resource, got %v", err)
	}
}
