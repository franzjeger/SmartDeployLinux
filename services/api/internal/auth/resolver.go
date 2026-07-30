// User resolver: maps an OIDC claim set to a local users row + role
// permissions. Wired up at API startup.

package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ResolverFromPool returns a UserResolver that upserts users by oidc_subject
// and loads their permissions via the role_permissions join.
func ResolverFromPool(pool *pgxpool.Pool) UserResolver {
	return func(ctx context.Context, p *principal) error {
		if p.Sub == "" {
			return fmt.Errorf("missing sub claim")
		}

		// Two-step upsert. First try to match an existing row by
		// oidc_subject (returning users). If none, insert — and on an
		// email collision, adopt the pre-seeded row (seed-admin creates
		// users by email with a NULL oidc_subject; the first OIDC login
		// from that email must link, not 500 on the unique index).
		var id uuid.UUID
		err := pool.QueryRow(ctx, `
			UPDATE users SET email = $2
			WHERE oidc_subject = $1
			RETURNING id`, p.Sub, p.Email).Scan(&id)
		if err != nil {
			err = pool.QueryRow(ctx, `
				INSERT INTO users (oidc_subject, email)
				VALUES ($1, $2)
				ON CONFLICT (email) DO UPDATE
				   SET oidc_subject = COALESCE(users.oidc_subject, EXCLUDED.oidc_subject)
				RETURNING id`, p.Sub, p.Email).Scan(&id)
		}
		if err != nil {
			return fmt.Errorf("upsert user: %w", err)
		}
		p.UserID = id

		// Load perms.
		rows, err := pool.Query(ctx, `
			SELECT rp.permission
			FROM user_roles ur
			JOIN role_permissions rp ON rp.role_id = ur.role_id
			WHERE ur.user_id = $1`, id)
		if err != nil {
			return fmt.Errorf("load perms: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var perm string
			if err := rows.Scan(&perm); err != nil {
				return err
			}
			p.Perms[perm] = struct{}{}
		}
		return rows.Err()
	}
}
