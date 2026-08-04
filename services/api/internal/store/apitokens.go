package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// APIToken is a long-lived personal access token. The secret itself is
// never stored or returned after creation — only its peppered hash and a
// short display prefix.
type APIToken struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	UserID     uuid.UUID  `json:"user_id"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	// ScopeRoles restricts the token to the owner's permissions held via
	// these roles. Empty = the owner's full permissions.
	ScopeRoles []string `json:"scope_roles"`
}

// CreateAPITokenInput carries the pre-hashed token for storage. The caller
// (the handler) generates the secret, hashes it, and keeps the plaintext
// only long enough to return it once to the client.
type CreateAPITokenInput struct {
	Name        string
	UserID      uuid.UUID
	TokenHash   string
	TokenPrefix string
	ExpiresAt   *time.Time
	// ScopeRoles, when non-empty, limits the token to these roles. The
	// handler must have verified they are a subset of the owner's roles.
	ScopeRoles []string
}

const apiTokenCols = `id, name, token_prefix, user_id, created_at, expires_at, last_used_at, revoked_at, scope_roles`

func scanAPIToken(row pgx.Row) (*APIToken, error) {
	var t APIToken
	if err := row.Scan(&t.ID, &t.Name, &t.Prefix, &t.UserID, &t.CreatedAt,
		&t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt, &t.ScopeRoles); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) CreateAPIToken(ctx context.Context, in CreateAPITokenInput) (*APIToken, error) {
	scope := in.ScopeRoles
	if scope == nil {
		scope = []string{}
	}
	return scanAPIToken(s.pool.QueryRow(ctx, `
		INSERT INTO api_tokens (name, token_hash, token_prefix, user_id, expires_at, scope_roles)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+apiTokenCols,
		in.Name, in.TokenHash, in.TokenPrefix, in.UserID, in.ExpiresAt, scope))
}

// ListAPITokens returns a user's tokens, newest first. The secret is never
// included; revoked and expired tokens are still listed (with their
// revoked_at / expires_at set) so the owner can audit them.
func (s *Store) ListAPITokens(ctx context.Context, userID uuid.UUID) ([]APIToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+apiTokenCols+`
		FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// RevokeAPIToken revokes a token the given user owns. It returns false if
// no live token with that id belongs to the user (already revoked, wrong
// owner, or unknown id) — so the handler can 404 without leaking whether
// the id exists under another account.
func (s *Store) RevokeAPIToken(ctx context.Context, id, userID uuid.UUID) (bool, error) {
	var got uuid.UUID
	err := s.pool.QueryRow(ctx, `
		UPDATE api_tokens SET revoked_at = now()
		WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL
		RETURNING id`, id, userID).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// AuthenticateAPIToken looks up a presented token by its hash and, if it
// is live (not revoked, not expired), stamps last_used_at and returns the
// owning user id plus the token's role scope (empty = unscoped). ok is
// false for an unknown, revoked, or expired token. This is the single
// source of truth for token validity.
func (s *Store) AuthenticateAPIToken(ctx context.Context, hash string) (uuid.UUID, []string, bool, error) {
	var uid uuid.UUID
	var scope []string
	err := s.pool.QueryRow(ctx, `
		UPDATE api_tokens SET last_used_at = now()
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		RETURNING user_id, scope_roles`, hash).Scan(&uid, &scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil, false, nil
	}
	if err != nil {
		return uuid.Nil, nil, false, err
	}
	return uid, scope, true, nil
}

// LoadUserPermissions returns the permission strings granted to a user via
// their roles. Used by the API-token authenticator to build a principal
// (the OIDC path loads the same set inline in ResolverFromPool).
func (s *Store) LoadUserPermissions(ctx context.Context, userID uuid.UUID) ([]string, error) {
	return s.scanPerms(ctx, `
		SELECT rp.permission
		FROM user_roles ur
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		WHERE ur.user_id = $1`, userID)
}

// LoadUserPermissionsScoped returns only the permissions a user holds via
// the named roles — the effective permission set of a role-scoped token.
// A scoped role the owner no longer has simply contributes nothing, so the
// token can never carry more than the owner currently has.
func (s *Store) LoadUserPermissionsScoped(ctx context.Context, userID uuid.UUID, roleNames []string) ([]string, error) {
	return s.scanPerms(ctx, `
		SELECT rp.permission
		FROM user_roles ur
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1 AND r.name = ANY($2)`, userID, roleNames)
}

func (s *Store) scanPerms(ctx context.Context, sql string, args ...any) ([]string, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var perms []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		perms = append(perms, p)
	}
	return perms, rows.Err()
}

// ListUserRoleNames returns the names of the roles a user holds. Used by
// the create handler to validate that a requested token scope is a subset
// of the owner's roles.
func (s *Store) ListUserRoleNames(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.name FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}
