// OIDC verification + RBAC for the operator-facing API.
//
// Verifier is a thin wrapper around go-oidc's IDTokenVerifier. Middleware
// extracts the bearer token, verifies it, looks up the local user row
// (creating it on first sight), loads the user's role permissions, and
// stuffs all of that into the request context.
//
// HasPerm and UserID are the helpers handlers use to gate access.

package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
)

type Verifier struct {
	v        *oidc.IDTokenVerifier
	clientID string
	provider *oidc.Provider
}

func NewVerifier(ctx context.Context, issuer, clientID string) (*Verifier, error) {
	if issuer == "" || clientID == "" {
		return nil, errors.New("issuer and clientID required")
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	return &Verifier{
		v:        provider.Verifier(&oidc.Config{ClientID: clientID}),
		clientID: clientID,
		provider: provider,
	}, nil
}

type principal struct {
	UserID uuid.UUID
	Email  string
	Sub    string
	Perms  map[string]struct{}
}

type ctxKey int

const ctxKeyPrincipal ctxKey = 1

// Middleware verifies the bearer ID token, builds a principal, and puts
// it into the request context. Without an ID token we 401.
//
// Note: in v1 we accept ID tokens directly. In a more conservative
// deployment, the operator UI exchanges the OIDC code for a session
// cookie and the API never sees the upstream token; that path is
// deferred to the UI work.
func Middleware(v *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := bearer(r)
			if tok == "" {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			idTok, err := v.v.Verify(r.Context(), tok)
			if err != nil {
				http.Error(w, "invalid token: "+err.Error(), http.StatusUnauthorized)
				return
			}
			var claims struct {
				Email string `json:"email"`
				Sub   string `json:"sub"`
			}
			if err := idTok.Claims(&claims); err != nil {
				http.Error(w, "claims: "+err.Error(), http.StatusUnauthorized)
				return
			}
			// User row resolution + RBAC happens via UserResolver injected
			// at construction time; if not set we fall back to a
			// permissive principal with a stable per-token UUID.
			p := &principal{
				Email: claims.Email,
				Sub:   claims.Sub,
				Perms: map[string]struct{}{},
			}
			if resolver := globalResolver; resolver != nil {
				if err := resolver(r.Context(), p); err != nil {
					http.Error(w, "user resolve: "+err.Error(), http.StatusInternalServerError)
					return
				}
			}
			ctx := context.WithValue(r.Context(), ctxKeyPrincipal, p)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserResolver is called by Middleware to upsert the local users row,
// load roles, and populate principal.Perms. Set with SetUserResolver
// at startup; if unset, principals carry no perms.
type UserResolver func(ctx context.Context, p *principal) error

var globalResolver UserResolver

func SetUserResolver(r UserResolver) { globalResolver = r }

// HasPerm returns true if the request principal carries the named
// permission, OR carries the wildcard "*". With OIDC unconfigured
// (no Verifier), all permission checks pass — operators are expected
// to enforce policy at the network layer in that case (the
// "deployment behind a private VPC" pattern). This is a deliberate
// dev-mode escape hatch.
func HasPerm(ctx context.Context, perm string) bool {
	p, ok := ctx.Value(ctxKeyPrincipal).(*principal)
	if !ok || p == nil {
		return true
	}
	if _, ok := p.Perms["*"]; ok {
		return true
	}
	_, ok = p.Perms[perm]
	return ok
}

// UserID returns the principal's local user UUID, or uuid.Nil if the
// request has no principal (dev mode).
func UserID(ctx context.Context) uuid.UUID {
	p, ok := ctx.Value(ctxKeyPrincipal).(*principal)
	if !ok || p == nil {
		return uuid.Nil
	}
	return p.UserID
}

// PrincipalForResolver exposes the principal struct to the resolver.
// Resolver may write to UserID and Perms.
func PrincipalForResolver(ctx context.Context) *principal {
	p, _ := ctx.Value(ctxKeyPrincipal).(*principal)
	return p
}

func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if strings.HasPrefix(v, "Bearer ") {
		return strings.TrimPrefix(v, "Bearer ")
	}
	if c, err := r.Cookie("deploy_session"); err == nil {
		return c.Value
	}
	return ""
}

