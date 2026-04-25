// DB layer for the API.
//
// We keep this package a thin wrapper around pgx. Each query is a method
// returning either a row struct or an error; no ORM, no codegen, no
// builder. The schema is small enough that hand-written SQL is the
// easiest thing to audit.

package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	pool     *pgxpool.Pool
	auditLog *slog.Logger // optional fan-out for audit_events writes
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 16
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// SetAuditLogger attaches an optional slog.Logger that receives every
// audit_events insert as a structured log line. Set this at startup to
// fan-out audit records to a file via auditlog.Open.
func (s *Store) SetAuditLogger(l *slog.Logger) { s.auditLog = l }

// --- migrations -------------------------------------------------------

// MigrateUp applies every embedded migration whose filename has not yet
// been recorded in the schema_migrations table. Forward-only; never
// rolls back.
func (s *Store) MigrateUp(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
		    filename text PRIMARY KEY,
		    applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, n := range names {
		var applied bool
		err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename = $1)`, n,
		).Scan(&applied)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + n)
		if err != nil {
			return err
		}
		if _, err := s.pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", n, err)
		}
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO schema_migrations(filename) VALUES ($1)`, n,
		); err != nil {
			return err
		}
	}
	return nil
}

// --- machines ---------------------------------------------------------

type Machine struct {
	ID                uuid.UUID
	AssetTag          *string
	MACPrimary        *string
	UUIDSMBIOS        *uuid.UUID
	Vendor            *string
	Model             *string
	DefaultProfileID  *uuid.UUID
	Attributes        []byte
	CreatedAt         time.Time
}

func (s *Store) GetMachine(ctx context.Context, id uuid.UUID) (*Machine, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, asset_tag, mac_primary::text, uuid_smbios, vendor, model,
		       default_profile_id, attributes, created_at
		FROM machines WHERE id = $1`, id)
	return scanMachine(row)
}

func (s *Store) GetMachineByMAC(ctx context.Context, mac string) (*Machine, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, asset_tag, mac_primary::text, uuid_smbios, vendor, model,
		       default_profile_id, attributes, created_at
		FROM machines WHERE mac_primary = $1::macaddr`, mac)
	return scanMachine(row)
}

type pgScanner interface {
	Scan(dest ...any) error
}

func scanMachine(row pgScanner) (*Machine, error) {
	var m Machine
	if err := row.Scan(&m.ID, &m.AssetTag, &m.MACPrimary, &m.UUIDSMBIOS,
		&m.Vendor, &m.Model, &m.DefaultProfileID, &m.Attributes,
		&m.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// CreateMachine inserts a machine. Caller decides whether to set
// default_profile_id; if nil, the machine has no default and a profile
// must be specified at issue-code time.
func (s *Store) CreateMachine(ctx context.Context, in CreateMachineInput) (*Machine, error) {
	var id uuid.UUID
	row := s.pool.QueryRow(ctx, `
		INSERT INTO machines (asset_tag, mac_primary, uuid_smbios, vendor, model, default_profile_id, attributes)
		VALUES ($1, $2::macaddr, $3, $4, $5, $6, COALESCE($7, '{}'::jsonb))
		RETURNING id`,
		in.AssetTag, in.MACPrimary, in.UUIDSMBIOS, in.Vendor, in.Model,
		in.DefaultProfileID, in.Attributes)
	if err := row.Scan(&id); err != nil {
		return nil, err
	}
	return s.GetMachine(ctx, id)
}

type CreateMachineInput struct {
	AssetTag         *string
	MACPrimary       *string
	UUIDSMBIOS       *uuid.UUID
	Vendor           *string
	Model            *string
	DefaultProfileID *uuid.UUID
	Attributes       []byte
}

func (s *Store) ListMachines(ctx context.Context, limit, offset int) ([]Machine, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, asset_tag, mac_primary::text, uuid_smbios, vendor, model,
		       default_profile_id, attributes, created_at
		FROM machines
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Machine
	for rows.Next() {
		m, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// --- profiles + images -----------------------------------------------

type RenderBundle struct {
	Machine        Machine
	ProfileID      uuid.UUID
	ProfileName    string
	ProfileVars    []byte // raw jsonb
	ImageOSFamily  string
	ImageOSVersion string
	ImageArch      string
	ImageBlobKey   string // s3 key for the kernel/wim
	JobID          *uuid.UUID
	OneShotToken   string // empty unless caller created one
}

// LookupRenderBundle resolves the data needed to render a per-machine
// iPXE script + answer file. If the machine has an active deployment_job,
// returns the profile from that job; otherwise falls back to
// machines.default_profile_id.
//
// Returns ErrNoActiveProfile if neither a job nor a default profile is set.
func (s *Store) LookupRenderBundle(ctx context.Context, machineID uuid.UUID) (*RenderBundle, error) {
	m, err := s.GetMachine(ctx, machineID)
	if err != nil {
		return nil, err
	}

	// Prefer the most recent non-terminal deployment_job.
	var profileID uuid.UUID
	var jobID uuid.UUID
	var jobIDPtr *uuid.UUID

	row := s.pool.QueryRow(ctx, `
		SELECT id, profile_id
		FROM deployment_jobs
		WHERE machine_id = $1
		  AND state IN ('pending','bootstrapped','imaging')
		ORDER BY created_at DESC
		LIMIT 1`, machineID)
	switch err := row.Scan(&jobID, &profileID); {
	case errors.Is(err, pgx.ErrNoRows):
		if m.DefaultProfileID == nil {
			return nil, ErrNoActiveProfile
		}
		profileID = *m.DefaultProfileID
	case err != nil:
		return nil, err
	default:
		jobIDPtr = &jobID
	}

	bundle := &RenderBundle{
		Machine:   *m,
		ProfileID: profileID,
		JobID:     jobIDPtr,
	}

	row = s.pool.QueryRow(ctx, `
		SELECT p.name, p.answer_file_vars,
		       i.os_family, i.os_version, i.arch
		FROM deployment_profiles p
		JOIN images i ON i.id = p.image_id
		WHERE p.id = $1`, profileID)
	if err := row.Scan(&bundle.ProfileName, &bundle.ProfileVars,
		&bundle.ImageOSFamily, &bundle.ImageOSVersion, &bundle.ImageArch); err != nil {
		return nil, err
	}

	// Fetch the latest image_version's blob for this image.
	row = s.pool.QueryRow(ctx, `
		SELECT b.s3_key
		FROM image_versions iv
		JOIN deployment_profiles dp ON dp.image_id = iv.image_id
		JOIN blobs b ON b.id = iv.blob_id
		WHERE dp.id = $1
		ORDER BY iv.created_at DESC
		LIMIT 1`, profileID)
	if err := row.Scan(&bundle.ImageBlobKey); err != nil {
		// Not fatal — the renderer can fall back to a configured default URL
		// for stubbed test images.
		bundle.ImageBlobKey = ""
	}

	return bundle, nil
}

// LookupRenderBundleByToken resolves a hashed boot token to its bound
// machine + active job + profile, but does NOT consume the token.
// Idempotent: usable for the iPXE chain fetch AND the user-data fetch.
//
// Returns ErrTokenInvalid if the token is unknown, expired, or already
// consumed (consumption happens at deployment-job terminal-state
// transition; see TransitionJob).
func (s *Store) LookupRenderBundleByToken(ctx context.Context, tokenHash string) (*RenderBundle, error) {
	var authCodeID, machineID uuid.UUID
	row := s.pool.QueryRow(ctx, `
		SELECT auth_code_id,
		       (SELECT machine_id FROM auth_codes WHERE id = one_shot_tokens.auth_code_id)
		FROM one_shot_tokens
		WHERE token_hash = $1 AND purpose = 'boot'
		  AND consumed_at IS NULL AND expires_at > now()`, tokenHash)
	if err := row.Scan(&authCodeID, &machineID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTokenInvalid
		}
		return nil, err
	}
	return s.LookupRenderBundle(ctx, machineID)
}

// MarkBootTokensConsumedForJob marks every still-active boot token bound
// to the given job's auth_code as consumed. Called at deployment_job
// terminal-state transitions.
func (s *Store) MarkBootTokensConsumedForJob(ctx context.Context, jobID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE one_shot_tokens t
		SET consumed_at = now()
		FROM deployment_jobs j
		WHERE j.id = $1
		  AND t.auth_code_id = j.auth_code_id
		  AND t.purpose = 'boot'
		  AND t.consumed_at IS NULL`, jobID)
	return err
}

// --- deployment events / phone-home -----------------------------------

type DeploymentEventInput struct {
	JobID   uuid.UUID
	Phase   string
	Message string
	Data    []byte
}

func (s *Store) AppendDeploymentEvent(ctx context.Context, in DeploymentEventInput) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO deployment_events (job_id, phase, message, data)
		VALUES ($1, $2, $3, $4)`,
		in.JobID, in.Phase, in.Message, in.Data)
	return err
}

// TransitionJob moves a deployment_jobs row to a new state. Returns
// the canonical state after the transition (which is just `to` on
// success). Validates the transition against a small allowed graph;
// rejecting bogus transitions catches phone-home replay bugs.
func (s *Store) TransitionJob(ctx context.Context, jobID uuid.UUID, to string) error {
	allowed := map[string][]string{
		"pending":      {"bootstrapped", "cancelled"},
		"bootstrapped": {"imaging", "failed", "cancelled"},
		"imaging":      {"post_install", "completed", "failed", "cancelled"},
		"post_install": {"completed", "failed"},
	}
	row := s.pool.QueryRow(ctx,
		`SELECT state::text FROM deployment_jobs WHERE id = $1`, jobID)
	var cur string
	if err := row.Scan(&cur); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	ok := false
	for _, v := range allowed[cur] {
		if v == to {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("invalid transition %s -> %s", cur, to)
	}

	finished := ""
	terminal := false
	if to == "completed" || to == "failed" || to == "cancelled" {
		finished = ", finished_at = now()"
		terminal = true
	}

	q := `UPDATE deployment_jobs SET state = $2::deployment_state` + finished + ` WHERE id = $1`
	if _, err := s.pool.Exec(ctx, q, jobID, to); err != nil {
		return err
	}
	if terminal {
		// Best-effort: revoke any still-live boot tokens. A failure here
		// is logged at caller, but doesn't block the state transition.
		_ = s.MarkBootTokensConsumedForJob(ctx, jobID)
	}
	return nil
}

func (s *Store) GetJobOneShotToken(ctx context.Context, jobID uuid.UUID, purpose string) (uuid.UUID, error) {
	var id uuid.UUID
	row := s.pool.QueryRow(ctx, `
		SELECT t.id FROM one_shot_tokens t
		JOIN deployment_jobs j ON j.auth_code_id = t.auth_code_id
		WHERE j.id = $1 AND t.purpose = $2 AND t.consumed_at IS NULL
		  AND t.expires_at > now()
		ORDER BY t.id DESC LIMIT 1`, jobID, purpose)
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	return id, nil
}

// VerifyOneShotTokenForJob is the non-consuming check used by per-job
// WinPE endpoints (plan, image.wim, drivers.zip, unattend.xml). The
// install needs to fetch these multiple times across the run, so we do
// NOT consume — instead we only check that the token is still valid
// AND the job is in a non-terminal imaging state. Returns the job's
// machine_id and profile_id on success.
func (s *Store) VerifyOneShotTokenForJob(ctx context.Context, tokenHash string, jobID uuid.UUID) (machineID, profileID uuid.UUID, err error) {
	row := s.pool.QueryRow(ctx, `
		SELECT j.machine_id, j.profile_id
		FROM one_shot_tokens t
		JOIN deployment_jobs j ON j.auth_code_id = t.auth_code_id
		WHERE t.token_hash = $1
		  AND t.purpose IN ('boot','phone-home')
		  AND t.consumed_at IS NULL
		  AND t.expires_at > now()
		  AND j.id = $2
		  AND j.state IN ('bootstrapped','imaging')`, tokenHash, jobID)
	if err := row.Scan(&machineID, &profileID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, uuid.Nil, ErrTokenInvalid
		}
		return uuid.Nil, uuid.Nil, err
	}
	return machineID, profileID, nil
}

// VerifyOneShotToken atomically marks a token consumed and returns the
// job_id it was bound to. The caller passes the *hashed* token.
func (s *Store) VerifyOneShotToken(ctx context.Context, tokenHash string, fromIP netip.Addr) (jobID uuid.UUID, err error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE one_shot_tokens
		SET    consumed_at      = now(),
		       consumed_from_ip = $2
		WHERE  token_hash = $1
		  AND  consumed_at IS NULL
		  AND  expires_at > now()
		RETURNING (SELECT id FROM deployment_jobs WHERE auth_code_id = one_shot_tokens.auth_code_id ORDER BY created_at DESC LIMIT 1)`,
		tokenHash, fromIP.String())
	if err := row.Scan(&jobID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrTokenInvalid
		}
		return uuid.Nil, err
	}
	return jobID, nil
}

// --- bootstrap sticks inventory --------------------------------------

type BootstrapStick struct {
	ID            uuid.UUID
	ImageSHA256   string
	Tailnet       string
	DeployURL     string
	CAFingerprint string
	BuiltBy       uuid.UUID
	BuiltAt       time.Time
	Label         *string
	RetiredAt     *time.Time
}

type CreateStickInput struct {
	ImageSHA256   string
	Tailnet       string
	DeployURL     string
	CAFingerprint string
	BuiltBy       uuid.UUID
	Label         *string
}

func (s *Store) CreateBootstrapStick(ctx context.Context, in CreateStickInput) (*BootstrapStick, error) {
	if in.ImageSHA256 == "" || in.Tailnet == "" || in.DeployURL == "" || in.CAFingerprint == "" {
		return nil, errors.New("image_sha256/tailnet/deploy_url/ca_fingerprint required")
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO bootstrap_sticks (image_sha256, tailnet, deploy_url, ca_fingerprint, built_by, label)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		in.ImageSHA256, in.Tailnet, in.DeployURL, in.CAFingerprint, in.BuiltBy, in.Label).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.GetBootstrapStick(ctx, id)
}

func (s *Store) GetBootstrapStick(ctx context.Context, id uuid.UUID) (*BootstrapStick, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, image_sha256, tailnet, deploy_url, ca_fingerprint,
		       built_by, built_at, label, retired_at
		FROM bootstrap_sticks WHERE id = $1`, id)
	var b BootstrapStick
	if err := row.Scan(&b.ID, &b.ImageSHA256, &b.Tailnet, &b.DeployURL,
		&b.CAFingerprint, &b.BuiltBy, &b.BuiltAt, &b.Label, &b.RetiredAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &b, nil
}

// ListBootstrapSticks returns sticks ordered by built_at desc. caFingerprint
// optional — when set, only sticks with that fingerprint are returned (used
// for CA rotation inventory).
func (s *Store) ListBootstrapSticks(ctx context.Context, caFingerprint string, limit int) ([]BootstrapStick, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var rows pgx.Rows
	var err error
	if caFingerprint != "" {
		rows, err = s.pool.Query(ctx, `
			SELECT id, image_sha256, tailnet, deploy_url, ca_fingerprint,
			       built_by, built_at, label, retired_at
			FROM bootstrap_sticks
			WHERE ca_fingerprint = $1
			ORDER BY built_at DESC
			LIMIT $2`, caFingerprint, limit)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT id, image_sha256, tailnet, deploy_url, ca_fingerprint,
			       built_by, built_at, label, retired_at
			FROM bootstrap_sticks
			ORDER BY built_at DESC
			LIMIT $1`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BootstrapStick
	for rows.Next() {
		var b BootstrapStick
		if err := rows.Scan(&b.ID, &b.ImageSHA256, &b.Tailnet, &b.DeployURL,
			&b.CAFingerprint, &b.BuiltBy, &b.BuiltAt, &b.Label, &b.RetiredAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// --- audit -----------------------------------------------------------

type AuditQueryFilters struct {
	Since    time.Duration // 0 = no since filter
	ActionLike string       // SQL LIKE pattern; empty = no filter
	MachineID  *uuid.UUID
	Limit      int
}

type AuditRow struct {
	ID          int64
	At          time.Time
	ActorID     *uuid.UUID
	ActorKind   string
	Action      string
	SubjectID   *uuid.UUID
	SubjectKind *string
	Data        []byte
	SourceIP    *string
}

// QueryAuditEvents runs a filtered audit query. The CLI's
// `deployctl audit query --since X --action Y --machine Z` lands here.
func (s *Store) QueryAuditEvents(ctx context.Context, f AuditQueryFilters) ([]AuditRow, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	q := `SELECT id, at, actor_id, actor_kind, action, subject_id, subject_kind,
	             data, source_ip::text
	      FROM audit_events
	      WHERE 1=1`
	args := []any{}
	if f.Since > 0 {
		q += fmt.Sprintf(" AND at > now() - $%d::interval", len(args)+1)
		args = append(args, fmt.Sprintf("%d seconds", int(f.Since.Seconds())))
	}
	if f.ActionLike != "" {
		q += fmt.Sprintf(" AND action LIKE $%d", len(args)+1)
		args = append(args, f.ActionLike)
	}
	if f.MachineID != nil {
		// Match either subject_id or any "machine_id" key inside data.
		q += fmt.Sprintf(" AND (subject_id = $%d OR data->>'machine_id' = $%d::text)",
			len(args)+1, len(args)+2)
		args = append(args, *f.MachineID, *f.MachineID)
	}
	q += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args)+1)
	args = append(args, f.Limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		if err := rows.Scan(&r.ID, &r.At, &r.ActorID, &r.ActorKind, &r.Action,
			&r.SubjectID, &r.SubjectKind, &r.Data, &r.SourceIP); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetUserByOIDCSub returns the local user row for an OIDC subject claim,
// or ErrNotFound if not yet seen.
func (s *Store) GetUserByOIDCSub(ctx context.Context, sub string) (uuid.UUID, error) {
	var id uuid.UUID
	row := s.pool.QueryRow(ctx, `SELECT id FROM users WHERE oidc_subject = $1`, sub)
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	return id, nil
}

// SeedAdmin upserts a user row by email and assigns the admin role.
// Returns the user id. Used by `api seed-admin` for first-run setup.
func (s *Store) SeedAdmin(ctx context.Context, email string) (uuid.UUID, error) {
	if email == "" {
		return uuid.Nil, errors.New("email required")
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (email)
		VALUES ($1)
		ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id`, email).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		VALUES ($1, '00000000-0000-0000-0000-000000000001')
		ON CONFLICT DO NOTHING`, id); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func (s *Store) Audit(ctx context.Context, ev AuditEvent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_events
		   (actor_id, actor_kind, action, subject_id, subject_kind, data, source_ip)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ev.ActorID, ev.ActorKind, ev.Action, ev.SubjectID, ev.SubjectKind,
		ev.Data, nullIP(ev.SourceIP))
	// Fan-out to file logger regardless of DB outcome — if the DB write
	// failed, the file log is the recovery record.
	if s.auditLog != nil {
		s.auditLog.Info("audit",
			"action", ev.Action,
			"actor_id", ev.ActorID,
			"actor_kind", ev.ActorKind,
			"subject_id", ev.SubjectID,
			"subject_kind", ev.SubjectKind,
			"source_ip", ev.SourceIP.String(),
			"data", string(ev.Data),
			"db_err", err,
		)
	}
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

func nullIP(a netip.Addr) any {
	if !a.IsValid() {
		return nil
	}
	return a.String()
}

// --- worker housekeeping helpers --------------------------------------

// ReapRedeemAttempts deletes redeem_attempts older than `older`.
// Returns rows deleted.
func (s *Store) ReapRedeemAttempts(ctx context.Context, older time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM redeem_attempts WHERE at < now() - $1::interval`,
		older.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// LockExpiredAuthCodes locks auth_codes that have passed their TTL
// without redemption. We don't delete; we lock and let the operator
// see them in the audit/UI.
func (s *Store) LockExpiredAuthCodes(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE auth_codes
		SET locked_at = now()
		WHERE redeemed_at IS NULL
		  AND locked_at IS NULL
		  AND expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// --- errors ----------------------------------------------------------

var (
	ErrNotFound        = errors.New("not found")
	ErrNoActiveProfile = errors.New("no active profile and no default profile")
	ErrTokenInvalid    = errors.New("token invalid or already consumed")
)
