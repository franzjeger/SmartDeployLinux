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

	"github.com/your-org/deployserver/api/internal/tokens"
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
			// Long-lived API tokens carry a fixed prefix and are verified
			// against the database rather than the IdP. This lets headless
			// clients (SDKs, deployctl, CI) authenticate without the OIDC
			// device flow.
			if strings.HasPrefix(tok, tokens.APITokenPrefix) {
				if apiTokenAuth == nil {
					http.Error(w, "api tokens not enabled", http.StatusUnauthorized)
					return
				}
				p, err := apiTokenAuth(r.Context(), tok)
				if err != nil {
					http.Error(w, "invalid api token", http.StatusUnauthorized)
					return
				}
				ctx := context.WithValue(r.Context(), ctxKeyPrincipal, p)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			if v == nil {
				http.Error(w, "token auth not configured", http.StatusUnauthorized)
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

// APITokenAuthenticator verifies a presented long-lived API token and
// returns the principal it authenticates as. It is consulted by
// Middleware for any bearer carrying tokens.APITokenPrefix.
type APITokenAuthenticator func(ctx context.Context, presented string) (*principal, error)

var apiTokenAuth APITokenAuthenticator

// SetAPITokenAuthenticator installs the API-token verification path.
// Without it, API-token bearers are rejected (401).
func SetAPITokenAuthenticator(a APITokenAuthenticator) { apiTokenAuth = a }

// APITokenStore is the slice of the store the API-token authenticator
// needs. *store.Store satisfies it.
type APITokenStore interface {
	// AuthenticateAPIToken returns the owner and the token's role scope
	// (empty = unscoped) for a live token.
	AuthenticateAPIToken(ctx context.Context, hash string) (uuid.UUID, []string, bool, error)
	LoadUserPermissions(ctx context.Context, userID uuid.UUID) ([]string, error)
	LoadUserPermissionsScoped(ctx context.Context, userID uuid.UUID, roleNames []string) ([]string, error)
}

// NewAPITokenAuthenticator builds an authenticator over the given store,
// hashing presented tokens with pepper (which must equal the pepper the
// create handler used — the deploy FQDN). A token authenticates as its
// owning user; an unscoped token carries the owner's full permissions, a
// role-scoped token only the permissions the owner holds via those roles.
func NewAPITokenAuthenticator(ts APITokenStore, pepper []byte) APITokenAuthenticator {
	return func(ctx context.Context, presented string) (*principal, error) {
		uid, scope, ok, err := ts.AuthenticateAPIToken(ctx, tokens.HashAPIToken(presented, pepper))
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("unknown, revoked, or expired api token")
		}
		var perms []string
		if len(scope) == 0 {
			perms, err = ts.LoadUserPermissions(ctx, uid)
		} else {
			perms, err = ts.LoadUserPermissionsScoped(ctx, uid, scope)
		}
		if err != nil {
			return nil, err
		}
		p := &principal{UserID: uid, Perms: make(map[string]struct{}, len(perms))}
		for _, perm := range perms {
			p.Perms[perm] = struct{}{}
		}
		return p, nil
	}
}

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

// Email returns the principal's email claim, or "" when the request has
// no principal (dev mode) or the IdP issued no email. Callers use it to
// show operators who they are signed in as; a UUID tells them nothing.
func Email(ctx context.Context) string {
	p, ok := ctx.Value(ctxKeyPrincipal).(*principal)
	if !ok || p == nil {
		return ""
	}
	return p.Email
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
