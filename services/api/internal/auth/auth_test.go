package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestHasPerm_NoPrincipalAllows(t *testing.T) {
	// In dev mode (no OIDC), everything passes.
	if !HasPerm(context.Background(), "machine.write") {
		t.Fatal("HasPerm with no principal should return true (dev mode)")
	}
}

func TestHasPerm_Wildcard(t *testing.T) {
	p := &principal{Perms: map[string]struct{}{"*": {}}}
	ctx := context.WithValue(context.Background(), ctxKeyPrincipal, p)
	if !HasPerm(ctx, "anything.at.all") {
		t.Fatal("wildcard should grant any perm")
	}
}

func TestHasPerm_SpecificMatch(t *testing.T) {
	p := &principal{Perms: map[string]struct{}{"machine.read": {}}}
	ctx := context.WithValue(context.Background(), ctxKeyPrincipal, p)
	if !HasPerm(ctx, "machine.read") {
		t.Fatal("specific perm should match")
	}
	if HasPerm(ctx, "machine.write") {
		t.Fatal("non-granted perm should not match")
	}
}

func TestUserID_NoPrincipal(t *testing.T) {
	if got := UserID(context.Background()); got != uuid.Nil {
		t.Fatalf("expected Nil UUID, got %v", got)
	}
}

func TestUserID_WithPrincipal(t *testing.T) {
	id := uuid.New()
	p := &principal{UserID: id}
	ctx := context.WithValue(context.Background(), ctxKeyPrincipal, p)
	if got := UserID(ctx); got != id {
		t.Fatalf("expected %v, got %v", id, got)
	}
}
