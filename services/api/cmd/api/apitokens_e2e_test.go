package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/store"
	"github.com/your-org/deployserver/api/internal/tokens"
)

// TestAPITokenHTTPRoundTrip drives the whole vertical over real HTTP: the
// create handler mints + hashes a token, and the auth middleware then
// authenticates that very token against the DB. If the create pepper and
// the middleware pepper ever diverged, step 2 would 401 — so this is the
// test that pins them together.
func TestAPITokenHTTPRoundTrip(t *testing.T) {
	dsn := os.Getenv("DEPLOY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("DEPLOY_TEST_PG_DSN not set; skipping integration test")
	}
	const fqdn = "deploy.example.com"
	pepper := []byte(fqdn)

	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.MigrateUp(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, tbl := range []string{"api_tokens", "user_roles", "users"} {
		if _, err := st.Pool().Exec(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("clean %s: %v", tbl, err)
		}
	}

	// A user with the operator role (grants apitoken.read/write).
	var uid uuid.UUID
	if err := st.Pool().QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('e2e@example.com') RETURNING id`).Scan(&uid); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO user_roles (user_id, role_id) VALUES ($1,'00000000-0000-0000-0000-000000000002')`, uid); err != nil {
		t.Fatalf("grant role: %v", err)
	}

	// A bootstrap token to authenticate the very first request.
	seed := tokens.APITokenPrefix + "seed-secret-value"
	if _, err := st.CreateAPIToken(ctx, store.CreateAPITokenInput{
		Name: "seed", UserID: uid, TokenHash: tokens.HashAPIToken(seed, pepper), TokenPrefix: seed[:12],
	}); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	// Wire the same authenticator main.go wires, and the operator router.
	auth.SetAPITokenAuthenticator(auth.NewAPITokenAuthenticator(st, pepper))
	t.Cleanup(func() { auth.SetAPITokenAuthenticator(nil) })
	h := &handlers{store: st, deployFQDN: fqdn}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(auth.Middleware(nil)) // nil verifier: only the API-token path is exercised
		h.registerOperatorRoutes(r)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	call := func(method, path, bearer, body string) (*http.Response, string) {
		var rdr *strings.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		} else {
			rdr = strings.NewReader("")
		}
		req, _ := http.NewRequest(method, srv.URL+path, rdr)
		req.Header.Set("Authorization", "Bearer "+bearer)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		buf := make([]byte, 8192)
		n, _ := resp.Body.Read(buf)
		resp.Body.Close()
		return resp, string(buf[:n])
	}

	// 1. Create a new token, authenticated by the seed token.
	resp, body := call(http.MethodPost, "/api/v1/api-tokens", seed, `{"name":"ci","expires_in_days":30}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got %d: %s", resp.StatusCode, body)
	}
	var created struct {
		ID        string     `json:"id"`
		Name      string     `json:"name"`
		Prefix    string     `json:"prefix"`
		Token     string     `json:"token"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode create: %v (%s)", err, body)
	}
	if created.Token == "" || !strings.HasPrefix(created.Token, tokens.APITokenPrefix) ||
		created.Name != "ci" || created.ExpiresAt == nil {
		t.Fatalf("unexpected create body: %+v", created)
	}

	// 1b. Role-scoping: scoping to a role the owner lacks is rejected...
	resp, body = call(http.MethodPost, "/api/v1/api-tokens", seed, `{"name":"bad","roles":["admin"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("scope to unheld role should 400, got %d: %s", resp.StatusCode, body)
	}
	// ...and scoping to a held role succeeds and is echoed back.
	resp, body = call(http.MethodPost, "/api/v1/api-tokens", seed, `{"name":"scoped","roles":["operator"]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("scoped create: got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"scope_roles":["operator"]`) {
		t.Fatalf("scope_roles not echoed in create response: %s", body)
	}

	// 2. The freshly minted token must itself authenticate (pepper match).
	resp, body = call(http.MethodGet, "/api/v1/api-tokens", created.Token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list with new token: got %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, created.Prefix) || strings.Contains(body, created.Token) {
		t.Fatalf("list should show prefix but never the secret: %s", body)
	}

	// 3. Revoke the new token (using the seed token), then it must 401.
	resp, body = call(http.MethodDelete, "/api/v1/api-tokens/"+created.ID, seed, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: got %d: %s", resp.StatusCode, body)
	}
	resp, _ = call(http.MethodGet, "/api/v1/api-tokens", created.Token, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token still works: got %d", resp.StatusCode)
	}
}
