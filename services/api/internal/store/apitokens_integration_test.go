package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAPITokenLifecycle(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var uid uuid.UUID
	mustScan(t, st, `INSERT INTO users (email) VALUES ('tok@example.com') RETURNING id`, &uid)
	// Grant the operator role so LoadUserPermissions returns something.
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO user_roles (user_id, role_id) VALUES ($1, '00000000-0000-0000-0000-000000000002')`, uid); err != nil {
		t.Fatalf("grant role: %v", err)
	}

	// Create.
	tok, err := st.CreateAPIToken(ctx, CreateAPITokenInput{
		Name: "ci-runner", UserID: uid, TokenHash: "sha256:deadbeef", TokenPrefix: "dpsk_abc1234",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if tok.Name != "ci-runner" || tok.UserID != uid || tok.RevokedAt != nil || tok.LastUsedAt != nil {
		t.Fatalf("unexpected created token: %+v", tok)
	}

	// List.
	list, err := st.ListAPITokens(ctx, uid)
	if err != nil || len(list) != 1 || list[0].ID != tok.ID {
		t.Fatalf("list mismatch: %v / %+v", err, list)
	}

	// Authenticate a live token → owner + last_used stamped.
	got, scope, ok, err := st.AuthenticateAPIToken(ctx, "sha256:deadbeef")
	if err != nil || !ok || got != uid {
		t.Fatalf("authenticate live: got=%v ok=%v err=%v", got, ok, err)
	}
	if len(scope) != 0 {
		t.Fatalf("unscoped token should have empty scope, got %v", scope)
	}
	if perms, err := st.LoadUserPermissions(ctx, uid); err != nil || len(perms) == 0 {
		t.Fatalf("load perms: %v / %v", perms, err)
	}

	// Unknown hash → not ok, no error.
	if _, _, ok, err := st.AuthenticateAPIToken(ctx, "sha256:nope"); err != nil || ok {
		t.Fatalf("authenticate unknown: ok=%v err=%v", ok, err)
	}

	// Revoke, then authentication must fail.
	revoked, err := st.RevokeAPIToken(ctx, tok.ID, uid)
	if err != nil || !revoked {
		t.Fatalf("revoke: revoked=%v err=%v", revoked, err)
	}
	if _, _, ok, _ := st.AuthenticateAPIToken(ctx, "sha256:deadbeef"); ok {
		t.Fatal("revoked token still authenticates")
	}
	// Second revoke is a no-op (already revoked / not found).
	if again, _ := st.RevokeAPIToken(ctx, tok.ID, uid); again {
		t.Fatal("double-revoke reported success")
	}
	// A stranger cannot revoke it either (wrong owner).
	if other, _ := st.RevokeAPIToken(ctx, tok.ID, uuid.New()); other {
		t.Fatal("revoke succeeded for wrong owner")
	}
}

func TestAPITokenExpiry(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var uid uuid.UUID
	mustScan(t, st, `INSERT INTO users (email) VALUES ('exp@example.com') RETURNING id`, &uid)

	past := time.Now().Add(-time.Hour)
	if _, err := st.CreateAPIToken(ctx, CreateAPITokenInput{
		Name: "expired", UserID: uid, TokenHash: "sha256:expired", TokenPrefix: "dpsk_exp1234", ExpiresAt: &past,
	}); err != nil {
		t.Fatalf("create expired: %v", err)
	}
	if _, _, ok, err := st.AuthenticateAPIToken(ctx, "sha256:expired"); err != nil || ok {
		t.Fatalf("expired token authenticated: ok=%v err=%v", ok, err)
	}

	future := time.Now().Add(time.Hour)
	if _, err := st.CreateAPIToken(ctx, CreateAPITokenInput{
		Name: "live", UserID: uid, TokenHash: "sha256:live", TokenPrefix: "dpsk_liv1234", ExpiresAt: &future,
	}); err != nil {
		t.Fatalf("create live: %v", err)
	}
	if _, _, ok, err := st.AuthenticateAPIToken(ctx, "sha256:live"); err != nil || !ok {
		t.Fatalf("live token rejected: ok=%v err=%v", ok, err)
	}
}

func mustScan(t *testing.T, st *Store, sql string, dest ...any) {
	t.Helper()
	if err := st.Pool().QueryRow(context.Background(), sql).Scan(dest...); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
}

func TestAPITokenScoping(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var uid uuid.UUID
	mustScan(t, st, `INSERT INTO users (email) VALUES ('scope@example.com') RETURNING id`, &uid)
	// Owner holds admin (has '*') and operator.
	for _, rid := range []string{
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
	} {
		if _, err := st.Pool().Exec(ctx, `INSERT INTO user_roles (user_id, role_id) VALUES ($1,$2)`, uid, rid); err != nil {
			t.Fatalf("grant %s: %v", rid, err)
		}
	}

	names, err := st.ListUserRoleNames(ctx, uid)
	if err != nil || !sliceHas(names, "admin") || !sliceHas(names, "operator") {
		t.Fatalf("roles: %v (%v)", names, err)
	}

	full, _ := st.LoadUserPermissions(ctx, uid)
	if !sliceHas(full, "*") {
		t.Fatalf("owner should have '*': %v", full)
	}

	// Scoped to operator only → operator perms, never the admin '*'.
	scoped, err := st.LoadUserPermissionsScoped(ctx, uid, []string{"operator"})
	if err != nil {
		t.Fatal(err)
	}
	if sliceHas(scoped, "*") {
		t.Fatalf("operator scope leaked '*': %v", scoped)
	}
	if !sliceHas(scoped, "machine.read") {
		t.Fatalf("operator scope missing machine.read: %v", scoped)
	}

	// A scoped token round-trips its scope through create + authenticate.
	tok, err := st.CreateAPIToken(ctx, CreateAPITokenInput{
		Name: "scoped", UserID: uid, TokenHash: "sha256:scopedtok",
		TokenPrefix: "dpsk_scoped1", ScopeRoles: []string{"operator"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tok.ScopeRoles) != 1 || tok.ScopeRoles[0] != "operator" {
		t.Fatalf("stored scope: %v", tok.ScopeRoles)
	}
	_, gotScope, ok, err := st.AuthenticateAPIToken(ctx, "sha256:scopedtok")
	if err != nil || !ok || len(gotScope) != 1 || gotScope[0] != "operator" {
		t.Fatalf("auth scope: %v ok=%v err=%v", gotScope, ok, err)
	}
}

func sliceHas(ss []string, x string) bool {
	for _, s := range ss {
		if s == x {
			return true
		}
	}
	return false
}
