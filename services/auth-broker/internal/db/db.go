// Thin Postgres wrapper using pgx. We deliberately do not use sqlx or
// go-orm here; the queries are few, focused, and security-critical.
// Plain SQL is the easiest thing to audit.

package db

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 8
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

// AuthCode is the projection of the auth_codes row that the broker needs.
type AuthCode struct {
	ID            uuid.UUID
	MachineID     uuid.UUID
	ProfileID     uuid.UUID
	BindingCIDR   *netip.Prefix
	ExpiresAt     time.Time
	Attempts      int
	LockedAt      *time.Time
	RedeemedAt    *time.Time
}

var ErrNotFound = errors.New("not found")
var ErrLocked = errors.New("code locked due to too many attempts")
var ErrExpired = errors.New("code expired")
var ErrAlreadyConsumed = errors.New("code already consumed")

// FindAndCheckCode looks up a code by its argon2id hash and returns its
// row plus the result of the row-level checks that don't need atomicity
// with the consume step. The atomic consume happens in ConsumeCode.
func (s *Store) FindCodeByHash(ctx context.Context, hash string) (*AuthCode, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, machine_id, profile_id, binding_cidr, expires_at,
		       attempts, locked_at, redeemed_at
		FROM auth_codes
		WHERE code_hash = $1`, hash)

	var c AuthCode
	var binding *string
	if err := row.Scan(&c.ID, &c.MachineID, &c.ProfileID, &binding,
		&c.ExpiresAt, &c.Attempts, &c.LockedAt, &c.RedeemedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if binding != nil {
		p, err := netip.ParsePrefix(*binding)
		if err == nil {
			c.BindingCIDR = &p
		}
	}
	return &c, nil
}

// ConsumeCode atomically marks a code as redeemed. Returns ErrAlreadyConsumed
// if a concurrent redemption beat us to it; ErrExpired or ErrLocked otherwise.
// On success, returns the updated row.
func (s *Store) ConsumeCode(ctx context.Context, hash string, fromIP netip.Addr) (*AuthCode, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE auth_codes
		SET    redeemed_at      = now(),
		       redeemed_from_ip = $2
		WHERE  code_hash = $1
		  AND  redeemed_at IS NULL
		  AND  locked_at   IS NULL
		  AND  expires_at  > now()
		RETURNING id, machine_id, profile_id, binding_cidr, expires_at,
		          attempts, locked_at, redeemed_at`, hash, fromIP.String())

	var c AuthCode
	var binding *string
	if err := row.Scan(&c.ID, &c.MachineID, &c.ProfileID, &binding,
		&c.ExpiresAt, &c.Attempts, &c.LockedAt, &c.RedeemedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Disambiguate by re-reading.
			cur, qerr := s.FindCodeByHash(ctx, hash)
			if qerr != nil {
				return nil, qerr
			}
			switch {
			case cur.LockedAt != nil:
				return nil, ErrLocked
			case cur.RedeemedAt != nil:
				return nil, ErrAlreadyConsumed
			case time.Now().After(cur.ExpiresAt):
				return nil, ErrExpired
			default:
				return nil, errors.New("consume failed for unknown reason")
			}
		}
		return nil, err
	}
	return &c, nil
}

// IncrementAttempt bumps the attempts counter and locks if cap reached.
func (s *Store) IncrementAttempt(ctx context.Context, hash string, capAttempts int) (attemptsAfter int, locked bool, err error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE auth_codes
		SET    attempts  = attempts + 1,
		       locked_at = CASE WHEN attempts + 1 >= $2
		                        THEN now() ELSE locked_at END
		WHERE  code_hash = $1
		RETURNING attempts, locked_at IS NOT NULL`, hash, capAttempts)
	if err = row.Scan(&attemptsAfter, &locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, ErrNotFound
		}
		return 0, false, err
	}
	return
}

// Audit appends a row to audit_events. Failures here are logged but not
// returned to the caller — we'd rather complete a successful redeem than
// fail a deploy because the audit table is briefly unavailable. The
// mirrored file/syslog sink (configured separately) is the authoritative
// audit log; this DB row is the queryable index.
func (s *Store) Audit(ctx context.Context, ev AuditEvent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_events
		   (actor_id, actor_kind, action, subject_id, subject_kind, data, source_ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ev.ActorID, ev.ActorKind, ev.Action, ev.SubjectID, ev.SubjectKind,
		ev.Data, ev.SourceIP.String())
	return err
}

type AuditEvent struct {
	ActorID     *uuid.UUID
	ActorKind   string
	Action      string
	SubjectID   *uuid.UUID
	SubjectKind string
	Data        []byte
	SourceIP    netip.Addr
}

// CreateOneShotToken stores a hashed one-shot token bound to an auth_code.
func (s *Store) CreateOneShotToken(ctx context.Context, authCodeID uuid.UUID, tokenHash, purpose string, expiresAt time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO one_shot_tokens (auth_code_id, token_hash, purpose, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, authCodeID, tokenHash, purpose, expiresAt).Scan(&id)
	return id, err
}

// CreateDeploymentJob inserts a new deployment_jobs row in state 'bootstrapped'.
func (s *Store) CreateDeploymentJob(ctx context.Context, machineID, profileID, authCodeID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO deployment_jobs (machine_id, profile_id, auth_code_id, state, started_at)
		VALUES ($1, $2, $3, 'bootstrapped', now())
		RETURNING id`, machineID, profileID, authCodeID).Scan(&id)
	return id, err
}

// CountIssuedByActorRecently returns how many auth_codes the given user
// has issued within the lookback window. Used to enforce a per-operator
// rate limit at issue-code time so a compromised admin account can't
// mint a stack of codes for arbitrary machines without tripping the
// limit. SECURITY.md §4 #7.
func (s *Store) CountIssuedByActorRecently(ctx context.Context, actor uuid.UUID, window time.Duration) (int, error) {
	var n int
	row := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM auth_codes
		WHERE issued_by = $1 AND issued_at > now() - $2::interval`,
		actor, fmt.Sprintf("%d seconds", int(window.Seconds())))
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// IssueCode inserts a row. The cleartext code never enters this function;
// caller hashes it first.
func (s *Store) IssueCode(ctx context.Context, codeHash string, machineID, profileID, issuedBy uuid.UUID, issuedFrom netip.Addr, bindingCIDR *netip.Prefix, ttl time.Duration, label string) (uuid.UUID, time.Time, error) {
	var id uuid.UUID
	var expiresAt time.Time
	var binding *string
	if bindingCIDR != nil {
		s := bindingCIDR.String()
		binding = &s
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO auth_codes
		   (code_hash, machine_id, profile_id, issued_by, issued_from_ip,
		    binding_cidr, expires_at, label)
		VALUES ($1, $2, $3, $4, $5, $6, now() + $7::interval, $8)
		RETURNING id, expires_at`,
		codeHash, machineID, profileID, issuedBy, issuedFrom.String(),
		binding, ttl.String(), label).Scan(&id, &expiresAt)
	return id, expiresAt, err
}

// RecordPerIPAttempt is a coarse rate-limit gate. It returns the attempt
// count for the given source IP within the window.
//
// Implementation: we use a Postgres advisory mechanism — a small table
// that we insert into and reap. This avoids needing a separate
// cache/store and survives a broker restart.
func (s *Store) RecordPerIPAttempt(ctx context.Context, ip netip.Addr, window time.Duration) (count int, err error) {
	row := s.pool.QueryRow(ctx, `
		WITH inserted AS (
		  INSERT INTO redeem_attempts (source_ip, at) VALUES ($1, now())
		  RETURNING at
		)
		SELECT count(*) FROM redeem_attempts
		WHERE source_ip = $1 AND at > now() - $2::interval`,
		ip.String(), window.String())
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
