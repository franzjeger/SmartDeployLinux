package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/tokens"
)

// fakeAPITokenStore is an in-memory APITokenStore for the middleware test.
type fakeAPITokenStore struct {
	byHash map[string]uuid.UUID
	perms  map[uuid.UUID][]string
}

func (f *fakeAPITokenStore) AuthenticateAPIToken(_ context.Context, hash string) (uuid.UUID, bool, error) {
	uid, ok := f.byHash[hash]
	return uid, ok, nil
}

func (f *fakeAPITokenStore) LoadUserPermissions(_ context.Context, userID uuid.UUID) ([]string, error) {
	return f.perms[userID], nil
}

func TestAPITokenMiddleware(t *testing.T) {
	uid := uuid.New()
	pepper := []byte("deploy.example.com")
	secret := tokens.APITokenPrefix + "s3cr3t"
	fake := &fakeAPITokenStore{
		byHash: map[string]uuid.UUID{tokens.HashAPIToken(secret, pepper): uid},
		perms:  map[uuid.UUID][]string{uid: {"machine.read"}},
	}
	SetAPITokenAuthenticator(NewAPITokenAuthenticator(fake, pepper))
	t.Cleanup(func() { SetAPITokenAuthenticator(nil) })

	// A handler that reports the resolved principal.
	var gotUID uuid.UUID
	var gotPerm bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUID = UserID(r.Context())
		gotPerm = HasPerm(r.Context(), "machine.read")
		w.WriteHeader(http.StatusOK)
	})
	// Verifier is nil: the API-token path must work without OIDC configured.
	h := Middleware(nil)(next)

	do := func(authz string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/machines", nil)
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Valid API token → 200, principal carries the owner + perms.
	if rec := do("Bearer " + secret); rec.Code != http.StatusOK {
		t.Fatalf("valid token: got %d", rec.Code)
	}
	if gotUID != uid || !gotPerm {
		t.Fatalf("principal not set: uid=%v perm=%v", gotUID, gotPerm)
	}

	// Unknown API token → 401.
	if rec := do("Bearer " + tokens.APITokenPrefix + "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: got %d, want 401", rec.Code)
	}

	// A non-API-token bearer with no verifier → 401 (not an API token, and
	// OIDC is unconfigured).
	if rec := do("Bearer some.jwt.token"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("jwt with nil verifier: got %d, want 401", rec.Code)
	}

	// Missing bearer → 401.
	if rec := do(""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: got %d, want 401", rec.Code)
	}
}

// When no authenticator is installed, API-token bearers are rejected.
func TestAPITokenMiddleware_Disabled(t *testing.T) {
	SetAPITokenAuthenticator(nil)
	h := Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokens.APITokenPrefix+"x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("disabled: got %d, want 401", rec.Code)
	}
}

var _ APITokenStore = (*fakeAPITokenStore)(nil)
