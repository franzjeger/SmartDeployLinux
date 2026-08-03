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
}

const apiTokenCols = `id, name, token_prefix, user_id, created_at, expires_at, last_used_at, revoked_at`

func scanAPIToken(row pgx.Row) (*APIToken, error) {
	var t APIToken
	if err := row.Scan(&t.ID, &t.Name, &t.Prefix, &t.UserID, &t.CreatedAt,
		&t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) CreateAPIToken(ctx context.Context, in CreateAPITokenInput) (*APIToken, error) {
	return scanAPIToken(s.pool.QueryRow(ctx, `
		INSERT INTO api_tokens (name, token_hash, token_prefix, user_id, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+apiTokenCols,
		in.Name, in.TokenHash, in.TokenPrefix, in.UserID, in.ExpiresAt))
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
// owning user id. ok is false for an unknown, revoked, or expired token.
// This is the single source of truth for token validity.
func (s *Store) AuthenticateAPIToken(ctx context.Context, hash string) (uuid.UUID, bool, error) {
	var uid uuid.UUID
	err := s.pool.QueryRow(ctx, `
		UPDATE api_tokens SET last_used_at = now()
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		RETURNING user_id`, hash).Scan(&uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return uid, true, nil
}

// LoadUserPermissions returns the permission strings granted to a user via
// their roles. Used by the API-token authenticator to build a principal
// (the OIDC path loads the same set inline in ResolverFromPool).
func (s *Store) LoadUserPermissions(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT rp.permission
		FROM user_roles ur
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		WHERE ur.user_id = $1`, userID)
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
