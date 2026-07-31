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
	"encoding/json"
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

	"github.com/your-org/deployserver/api/internal/driverpack"
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
	ID               uuid.UUID
	AssetTag         *string
	MACPrimary       *string
	UUIDSMBIOS       *uuid.UUID
	Vendor           *string
	Model            *string
	DefaultProfileID *uuid.UUID
	// RawMessage (not []byte) so API responses carry the attributes as a
	// JSON object instead of base64 — the UI reads site + hardware
	// inventory out of it.
	Attributes json.RawMessage
	CreatedAt  time.Time
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

// UpdateMachineInput carries partial updates: nil means "leave as is".
// ClearDefaultProfile distinguishes "unset the profile" from "no change"
// (both would otherwise be a nil pointer).
type UpdateMachineInput struct {
	AssetTag            *string
	MACPrimary          *string
	Vendor              *string
	Model               *string
	DefaultProfileID    *uuid.UUID
	ClearDefaultProfile bool
	Attributes          []byte // replaces attributes wholesale when non-nil
}

func (s *Store) UpdateMachine(ctx context.Context, id uuid.UUID, in UpdateMachineInput) (*Machine, error) {
	var profileArg any
	if in.DefaultProfileID != nil {
		profileArg = *in.DefaultProfileID
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE machines SET
			asset_tag          = COALESCE($2, asset_tag),
			mac_primary        = COALESCE($3::macaddr, mac_primary),
			vendor             = COALESCE($4, vendor),
			model              = COALESCE($5, model),
			default_profile_id = CASE WHEN $7 THEN NULL
			                          ELSE COALESCE($6::uuid, default_profile_id) END,
			attributes         = COALESCE($8::jsonb, attributes)
		WHERE id = $1`,
		id, in.AssetTag, in.MACPrimary, in.Vendor, in.Model,
		profileArg, in.ClearDefaultProfile, in.Attributes)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.GetMachine(ctx, id)
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
	ImageID        uuid.UUID
	ImageOSFamily  string
	ImageOSVersion string
	ImageArch      string
	ImageBlobKey    string // s3 key for the newest image_version blob
	ImageBlobBucket string
	ImageBlobSHA    string
	ImageMedia      []byte // raw jsonb of images.media
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
		       i.id, i.os_family, i.os_version, i.arch, i.media
		FROM deployment_profiles p
		JOIN images i ON i.id = p.image_id
		WHERE p.id = $1`, profileID)
	if err := row.Scan(&bundle.ProfileName, &bundle.ProfileVars,
		&bundle.ImageID, &bundle.ImageOSFamily, &bundle.ImageOSVersion, &bundle.ImageArch,
		&bundle.ImageMedia); err != nil {
		return nil, err
	}

	// Fetch the latest image_version's blob for this image.
	row = s.pool.QueryRow(ctx, `
		SELECT b.s3_key, b.s3_bucket, b.sha256
		FROM image_versions iv
		JOIN deployment_profiles dp ON dp.image_id = iv.image_id
		JOIN blobs b ON b.id = iv.blob_id
		WHERE dp.id = $1
		ORDER BY iv.created_at DESC
		LIMIT 1`, profileID)
	if err := row.Scan(&bundle.ImageBlobKey, &bundle.ImageBlobBucket, &bundle.ImageBlobSHA); err != nil {
		// Not fatal — the renderer can fall back to a configured default URL
		// for stubbed test images.
		bundle.ImageBlobKey, bundle.ImageBlobBucket, bundle.ImageBlobSHA = "", "", ""
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

// --- profiles --------------------------------------------------------

type Profile struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	ImageID      uuid.UUID `json:"image_id"`
	ImageName    string    `json:"image_name"`
	OSFamily     string    `json:"os_family"`
	OSVersion    string    `json:"os_version"`
	CreatedAt    time.Time `json:"created_at"`
}

func (s *Store) ListProfiles(ctx context.Context) ([]Profile, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.name, p.image_id, i.name, i.os_family, i.os_version, p.created_at
		FROM deployment_profiles p
		JOIN images i ON i.id = p.image_id
		ORDER BY p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Profile
	for rows.Next() {
		var p Profile
		if err := rows.Scan(&p.ID, &p.Name, &p.ImageID, &p.ImageName,
			&p.OSFamily, &p.OSVersion, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- jobs (deployment_jobs queries used by the UI) -------------------

type Job struct {
	ID          uuid.UUID  `json:"id"`
	MachineID   uuid.UUID  `json:"machine_id"`
	MachineTag  *string    `json:"machine_asset_tag"`
	ProfileID   uuid.UUID  `json:"profile_id"`
	ProfileName string     `json:"profile_name"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

type JobFilter struct {
	State     string     // empty = any
	MachineID *uuid.UUID // nil = any
	Limit     int
}

func (s *Store) ListJobs(ctx context.Context, f JobFilter) ([]Job, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	q := `
		SELECT j.id, j.machine_id, m.asset_tag, j.profile_id, p.name,
		       j.state::text, j.created_at, j.started_at, j.finished_at
		FROM deployment_jobs j
		JOIN machines m ON m.id = j.machine_id
		JOIN deployment_profiles p ON p.id = j.profile_id
		WHERE 1=1`
	args := []any{}
	if f.State != "" {
		q += fmt.Sprintf(" AND j.state = $%d::deployment_state", len(args)+1)
		args = append(args, f.State)
	}
	if f.MachineID != nil {
		q += fmt.Sprintf(" AND j.machine_id = $%d", len(args)+1)
		args = append(args, *f.MachineID)
	}
	q += fmt.Sprintf(" ORDER BY j.created_at DESC LIMIT $%d", len(args)+1)
	args = append(args, f.Limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.MachineID, &j.MachineTag, &j.ProfileID,
			&j.ProfileName, &j.State, &j.CreatedAt, &j.StartedAt, &j.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

type JobEvent struct {
	ID      int64     `json:"id"`
	Phase   string    `json:"phase"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

func (s *Store) GetJob(ctx context.Context, id uuid.UUID) (*Job, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT j.id, j.machine_id, m.asset_tag, j.profile_id, p.name,
		       j.state::text, j.created_at, j.started_at, j.finished_at
		FROM deployment_jobs j
		JOIN machines m ON m.id = j.machine_id
		JOIN deployment_profiles p ON p.id = j.profile_id
		WHERE j.id = $1`, id)
	var j Job
	if err := row.Scan(&j.ID, &j.MachineID, &j.MachineTag, &j.ProfileID,
		&j.ProfileName, &j.State, &j.CreatedAt, &j.StartedAt, &j.FinishedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &j, nil
}

func (s *Store) GetJobEvents(ctx context.Context, jobID uuid.UUID) ([]JobEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, phase, message, at FROM deployment_events
		WHERE job_id = $1 ORDER BY id ASC`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobEvent
	for rows.Next() {
		var e JobEvent
		if err := rows.Scan(&e.ID, &e.Phase, &e.Message, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- images ----------------------------------------------------------

type Image struct {
	ID            uuid.UUID       `json:"id"`
	Name          string          `json:"name"`
	OSFamily      string          `json:"os_family"`
	OSVersion     string          `json:"os_version"`
	Arch          string          `json:"arch"`
	Description   *string         `json:"description"`
	CreatedAt     time.Time       `json:"created_at"`
	VersionsCount int             `json:"versions_count"`
	Media         json.RawMessage `json:"media"`
}

func (s *Store) ListImages(ctx context.Context) ([]Image, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.name, i.os_family, i.os_version, i.arch, i.description,
		       i.created_at, i.media,
		       (SELECT count(*) FROM image_versions iv WHERE iv.image_id = i.id)
		FROM images i ORDER BY i.os_family, i.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Image
	for rows.Next() {
		var i Image
		var media []byte
		if err := rows.Scan(&i.ID, &i.Name, &i.OSFamily, &i.OSVersion,
			&i.Arch, &i.Description, &i.CreatedAt, &media, &i.VersionsCount); err != nil {
			return nil, err
		}
		if len(media) > 0 {
			i.Media = json.RawMessage(media)
		} else {
			i.Media = json.RawMessage(`{}`)
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) GetImage(ctx context.Context, id uuid.UUID) (*Image, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT i.id, i.name, i.os_family, i.os_version, i.arch, i.description,
		       i.created_at, i.media,
		       (SELECT count(*) FROM image_versions iv WHERE iv.image_id = i.id)
		FROM images i WHERE i.id = $1`, id)
	var i Image
	var media []byte
	if err := row.Scan(&i.ID, &i.Name, &i.OSFamily, &i.OSVersion,
		&i.Arch, &i.Description, &i.CreatedAt, &media, &i.VersionsCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if len(media) > 0 {
		i.Media = json.RawMessage(media)
	} else {
		i.Media = json.RawMessage(`{}`)
	}
	return &i, nil
}

type CreateImageInput struct {
	Name        string
	OSFamily    string
	OSVersion   string
	Arch        string
	Description *string
	Media       []byte // JSONB; pass nil for `{}`
}

func (s *Store) CreateImage(ctx context.Context, in CreateImageInput) (uuid.UUID, error) {
	if in.Name == "" || in.OSFamily == "" || in.OSVersion == "" || in.Arch == "" {
		return uuid.Nil, errors.New("name, os_family, os_version, arch all required")
	}
	if len(in.Media) == 0 {
		in.Media = []byte(`{}`)
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO images (name, os_family, os_version, arch, description, media)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		RETURNING id`,
		in.Name, in.OSFamily, in.OSVersion, in.Arch, in.Description, in.Media).Scan(&id)
	return id, err
}

type UpdateImageInput struct {
	Name        *string
	Description *string
	Media       []byte // pass nil to leave; pass `{}` to clear
}

func (s *Store) UpdateImage(ctx context.Context, id uuid.UUID, in UpdateImageInput) error {
	sets := []string{}
	args := []any{id}
	if in.Name != nil {
		sets = append(sets, fmt.Sprintf("name = $%d", len(args)+1))
		args = append(args, *in.Name)
	}
	if in.Description != nil {
		sets = append(sets, fmt.Sprintf("description = $%d", len(args)+1))
		args = append(args, *in.Description)
	}
	if in.Media != nil {
		sets = append(sets, fmt.Sprintf("media = $%d::jsonb", len(args)+1))
		args = append(args, in.Media)
	}
	if len(sets) == 0 {
		return nil
	}
	tag, err := s.pool.Exec(ctx,
		"UPDATE images SET "+strings.Join(sets, ", ")+" WHERE id = $1", args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteImage(ctx context.Context, id uuid.UUID) error {
	// Refuse if any deployment_profile references this image.
	var refs int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM deployment_profiles WHERE image_id = $1`, id).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return fmt.Errorf("image is used by %d profile(s); reassign them first", refs)
	}
	// Delete image_versions cascade is set up via FK; blobs are
	// content-addressed and may be shared, so we don't delete them here.
	tag, err := s.pool.Exec(ctx, `DELETE FROM images WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- machine delete --------------------------------------------------

func (s *Store) DeleteMachine(ctx context.Context, id uuid.UUID) error {
	// Refuse if there are non-terminal deployment_jobs — that's
	// catching the operator deleting a machine mid-deploy.
	var pending int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM deployment_jobs
		WHERE machine_id = $1 AND state IN ('pending','bootstrapped','imaging','post_install')`,
		id).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return fmt.Errorf("machine has %d non-terminal deployment job(s); cancel them first", pending)
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM machines WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetProfile returns a single profile, including its image join.
func (s *Store) GetProfile(ctx context.Context, id uuid.UUID) (*Profile, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT p.id, p.name, p.image_id, i.name, i.os_family, i.os_version, p.created_at
		FROM deployment_profiles p
		JOIN images i ON i.id = p.image_id
		WHERE p.id = $1`, id)
	var p Profile
	if err := row.Scan(&p.ID, &p.Name, &p.ImageID, &p.ImageName,
		&p.OSFamily, &p.OSVersion, &p.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// GetProfileVars returns the raw JSONB answer_file_vars for a profile.
func (s *Store) GetProfileVars(ctx context.Context, id uuid.UUID) ([]byte, error) {
	var vars []byte
	row := s.pool.QueryRow(ctx, `SELECT answer_file_vars FROM deployment_profiles WHERE id = $1`, id)
	if err := row.Scan(&vars); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return vars, nil
}

type CreateProfileInput struct {
	Name           string
	ImageID        uuid.UUID
	AnswerFileVars []byte // JSONB
}

func (s *Store) CreateProfile(ctx context.Context, in CreateProfileInput) (uuid.UUID, error) {
	if in.Name == "" {
		return uuid.Nil, errors.New("name required")
	}
	if len(in.AnswerFileVars) == 0 {
		in.AnswerFileVars = []byte(`{}`)
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO deployment_profiles (name, image_id, answer_file_vars)
		VALUES ($1, $2, $3::jsonb)
		RETURNING id`, in.Name, in.ImageID, in.AnswerFileVars).Scan(&id)
	return id, err
}

type UpdateProfileInput struct {
	Name           *string
	ImageID        *uuid.UUID
	AnswerFileVars []byte // pass nil to leave unchanged; pass `{}` to clear
}

func (s *Store) UpdateProfile(ctx context.Context, id uuid.UUID, in UpdateProfileInput) error {
	// Build a dynamic UPDATE so callers can patch any subset.
	sets := []string{}
	args := []any{id}
	if in.Name != nil {
		sets = append(sets, fmt.Sprintf("name = $%d", len(args)+1))
		args = append(args, *in.Name)
	}
	if in.ImageID != nil {
		sets = append(sets, fmt.Sprintf("image_id = $%d", len(args)+1))
		args = append(args, *in.ImageID)
	}
	if in.AnswerFileVars != nil {
		sets = append(sets, fmt.Sprintf("answer_file_vars = $%d::jsonb", len(args)+1))
		args = append(args, in.AnswerFileVars)
	}
	if len(sets) == 0 {
		return nil
	}
	q := "UPDATE deployment_profiles SET " + strings.Join(sets, ", ") + " WHERE id = $1"
	tag, err := s.pool.Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteProfile(ctx context.Context, id uuid.UUID) error {
	// Refuse if any non-terminal job references this profile.
	var pending int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM deployment_jobs
		WHERE profile_id = $1 AND state IN ('pending','bootstrapped','imaging','post_install')`,
		id).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return fmt.Errorf("profile has %d non-terminal job(s); cancel them first", pending)
	}
	// Refuse if any machine has this as default_profile_id.
	var refs int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM machines WHERE default_profile_id = $1`, id).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return fmt.Errorf("profile is default for %d machine(s); reassign them first", refs)
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM deployment_profiles WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- answer file templates -------------------------------------------

type AnswerFileTemplate struct {
	ID        uuid.UUID `json:"id"`
	ProfileID uuid.UUID `json:"profile_id"`
	Kind      string    `json:"kind"` // unattend|autoinstall|kickstart|preseed|ignition|cloud-init
	Body      string    `json:"body"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) ListProfileTemplates(ctx context.Context, profileID uuid.UUID) ([]AnswerFileTemplate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, profile_id, kind, body, updated_at
		FROM answer_file_templates
		WHERE profile_id = $1
		ORDER BY kind`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AnswerFileTemplate
	for rows.Next() {
		var t AnswerFileTemplate
		if err := rows.Scan(&t.ID, &t.ProfileID, &t.Kind, &t.Body, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpsertProfileTemplate inserts or replaces the template body for the given
// (profile_id, kind) pair. Returns the row id.
func (s *Store) UpsertProfileTemplate(ctx context.Context, profileID uuid.UUID, kind, body string) (uuid.UUID, error) {
	allowed := map[string]bool{
		"unattend": true, "autoinstall": true, "kickstart": true,
		"preseed": true, "ignition": true, "cloud-init": true,
	}
	if !allowed[kind] {
		return uuid.Nil, fmt.Errorf("unknown template kind %q", kind)
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO answer_file_templates (profile_id, kind, body)
		VALUES ($1, $2, $3)
		ON CONFLICT (profile_id, kind) DO UPDATE
		   SET body = EXCLUDED.body, updated_at = now()
		RETURNING id`, profileID, kind, body).Scan(&id)
	return id, err
}

func (s *Store) DeleteProfileTemplate(ctx context.Context, profileID uuid.UUID, kind string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM answer_file_templates WHERE profile_id = $1 AND kind = $2`,
		profileID, kind)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// FirstAdminUserID returns the user_id of any user with the admin role,
// or uuid.Nil if no admin exists yet. Used by the api in dev-mode (no
// OIDC) to attribute issuance actions to the seeded admin.
func (s *Store) FirstAdminUserID(ctx context.Context) (uuid.UUID, error) {
	var id uuid.UUID
	row := s.pool.QueryRow(ctx, `
		SELECT user_id FROM user_roles
		WHERE role_id = '00000000-0000-0000-0000-000000000001'
		ORDER BY user_id LIMIT 1`)
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	return id, nil
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

// ReapExpiredOneShotTokens deletes one_shot_tokens that were consumed or
// expired more than `older` ago. Keeps the table from growing unbounded.
func (s *Store) ReapExpiredOneShotTokens(ctx context.Context, older time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM one_shot_tokens
		WHERE (consumed_at IS NOT NULL AND consumed_at < now() - $1::interval)
		   OR (expires_at < now() - $1::interval)`,
		older.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// --- phone-home / job progression -------------------------------------

// VerifyPhoneHomeToken checks — without consuming — that the hashed
// token is live and bound to the given job, and that the job is not in
// a terminal state. Installers phone home repeatedly across the whole
// deployment, so the token stays valid until TransitionJob revokes it
// at completion.
func (s *Store) VerifyPhoneHomeToken(ctx context.Context, tokenHash string, jobID uuid.UUID) (machineID, profileID uuid.UUID, err error) {
	row := s.pool.QueryRow(ctx, `
		SELECT j.machine_id, j.profile_id
		FROM one_shot_tokens t
		JOIN deployment_jobs j ON j.auth_code_id = t.auth_code_id
		WHERE t.token_hash = $1
		  AND t.purpose IN ('boot','phone-home')
		  AND t.consumed_at IS NULL
		  AND t.expires_at > now()
		  AND j.id = $2
		  AND j.state IN ('pending','bootstrapped','imaging','post_install')`,
		tokenHash, jobID)
	if err := row.Scan(&machineID, &profileID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, uuid.Nil, ErrTokenInvalid
		}
		return uuid.Nil, uuid.Nil, err
	}
	return machineID, profileID, nil
}

// jobStatePath is the happy-path ordering of deployment states.
var jobStatePath = []string{"pending", "bootstrapped", "imaging", "post_install", "completed"}

// AdvanceJob moves a job to `to`, applying any intermediate happy-path
// transitions required to get there (e.g. an installer whose first
// report is "imaging" while the job still sits in "pending"). "failed"
// and "cancelled" are reachable from any non-terminal state via
// TransitionJob's graph. A no-op if the job is already at or past `to`.
func (s *Store) AdvanceJob(ctx context.Context, jobID uuid.UUID, to string) error {
	if to == "failed" || to == "cancelled" {
		return s.TransitionJob(ctx, jobID, to)
	}
	targetIdx := -1
	for i, st := range jobStatePath {
		if st == to {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return fmt.Errorf("unknown target state %q", to)
	}
	var cur string
	row := s.pool.QueryRow(ctx, `SELECT state::text FROM deployment_jobs WHERE id = $1`, jobID)
	if err := row.Scan(&cur); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	curIdx := -1
	for i, st := range jobStatePath {
		if st == cur {
			curIdx = i
			break
		}
	}
	if curIdx < 0 {
		return fmt.Errorf("job in terminal state %q", cur)
	}
	for i := curIdx + 1; i <= targetIdx; i++ {
		if err := s.TransitionJob(ctx, jobID, jobStatePath[i]); err != nil {
			return err
		}
	}
	return nil
}

// --- driver packs -----------------------------------------------------

// MatchedDriverPack is a driver pack version selected for a machine,
// with the blob key needed to build a download URL.
type MatchedDriverPack struct {
	VersionID  uuid.UUID `json:"version_id"`
	Vendor     string    `json:"vendor"`
	Model      string    `json:"model"`
	VersionTag string    `json:"version_tag"`
	BlobKey    string    `json:"blob_key"`
}

// MatchDriverPacks loads every driver pack version (with rules and blob
// key) for the given OS family and runs the driverpack matcher over the
// fingerprint. Results come back most-specific-first.
func (s *Store) MatchDriverPacks(ctx context.Context, fp driverpack.Fingerprint) ([]MatchedDriverPack, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT pv.id, pv.pack_id, p.vendor, p.model, p.os_family, p.os_version,
		       pv.version_tag, b.s3_key,
		       COALESCE(r.match_type, ''), COALESCE(r.match_value, '')
		FROM driver_pack_versions pv
		JOIN driver_packs p ON p.id = pv.pack_id
		JOIN blobs b ON b.id = pv.blob_id
		LEFT JOIN driver_match_rules r ON r.pack_version_id = pv.id
		WHERE lower(p.os_family) = lower($1)
		ORDER BY pv.id`, fp.OSFamily)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type cand struct {
		pv      driverpack.PackVersion
		blobKey string
	}
	byID := map[uuid.UUID]*cand{}
	var order []uuid.UUID
	for rows.Next() {
		var id, packID uuid.UUID
		var vendor, model, osFamily, osVersion, versionTag, blobKey, rType, rValue string
		if err := rows.Scan(&id, &packID, &vendor, &model, &osFamily, &osVersion,
			&versionTag, &blobKey, &rType, &rValue); err != nil {
			return nil, err
		}
		c, ok := byID[id]
		if !ok {
			c = &cand{pv: driverpack.PackVersion{
				ID: id, PackID: packID, Vendor: vendor, Model: model,
				OSFamily: osFamily, OSVersion: osVersion, VersionTag: versionTag,
			}, blobKey: blobKey}
			byID[id] = c
			order = append(order, id)
		}
		if rType != "" {
			c.pv.Rules = append(c.pv.Rules, driverpack.Rule{Type: rType, Value: rValue})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	candidates := make([]driverpack.PackVersion, 0, len(order))
	for _, id := range order {
		candidates = append(candidates, byID[id].pv)
	}
	selected := driverpack.Select(fp, candidates)
	out := make([]MatchedDriverPack, 0, len(selected))
	for _, pv := range selected {
		out = append(out, MatchedDriverPack{
			VersionID: pv.ID, Vendor: pv.Vendor, Model: pv.Model,
			VersionTag: pv.VersionTag, BlobKey: byID[pv.ID].blobKey,
		})
	}
	return out, nil
}

// RecordMachineInventory merges a hardware fingerprint reported by the
// installer into machines.attributes and updates vendor/model if they
// were unset. This gives operators auto-populated hardware inventory
// from the first deployment onward.
func (s *Store) RecordMachineInventory(ctx context.Context, machineID uuid.UUID, inventory []byte) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE machines
		SET attributes = attributes || jsonb_build_object('hardware', $2::jsonb),
		    vendor = COALESCE(vendor, $2::jsonb->>'dmi_vendor'),
		    model  = COALESCE(model,  $2::jsonb->>'dmi_product')
		WHERE id = $1`, machineID, inventory)
	return err
}

// --- blobs / image versions (ingest) -----------------------------------

type Blob struct {
	ID        uuid.UUID `json:"id"`
	SHA256    string    `json:"sha256"`
	SizeBytes int64     `json:"size_bytes"`
	S3Bucket  string    `json:"s3_bucket"`
	S3Key     string    `json:"s3_key"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateBlob registers an object about to be uploaded. Idempotent on
// sha256: re-registering the same content returns the existing row, so
// a retried upload doesn't strand duplicates.
func (s *Store) CreateBlob(ctx context.Context, sha256hex string, sizeBytes int64, bucket, key string) (*Blob, error) {
	b := &Blob{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO blobs (sha256, size_bytes, s3_bucket, s3_key)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (sha256) DO UPDATE SET sha256 = EXCLUDED.sha256
		RETURNING id, sha256, size_bytes, s3_bucket, s3_key, created_at`,
		sha256hex, sizeBytes, bucket, key).
		Scan(&b.ID, &b.SHA256, &b.SizeBytes, &b.S3Bucket, &b.S3Key, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return b, nil
}

type ImageVersion struct {
	ID         uuid.UUID `json:"id"`
	ImageID    uuid.UUID `json:"image_id"`
	VersionTag string    `json:"version_tag"`
	BlobID     uuid.UUID `json:"blob_id"`
	BlobKey    string    `json:"blob_key"`
	BlobSHA256 string    `json:"blob_sha256"`
	SizeBytes  int64     `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
}

// CreateImageVersion links an uploaded blob to an image as a new version.
func (s *Store) CreateImageVersion(ctx context.Context, imageID, blobID uuid.UUID, versionTag string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO image_versions (image_id, blob_id, version_tag)
		VALUES ($1, $2, $3)
		RETURNING id`, imageID, blobID, versionTag).Scan(&id)
	return id, err
}

func (s *Store) ListImageVersions(ctx context.Context, imageID uuid.UUID) ([]ImageVersion, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT iv.id, iv.image_id, iv.version_tag, iv.blob_id,
		       b.s3_key, b.sha256, b.size_bytes, iv.created_at
		FROM image_versions iv
		JOIN blobs b ON b.id = iv.blob_id
		WHERE iv.image_id = $1
		ORDER BY iv.created_at DESC`, imageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImageVersion
	for rows.Next() {
		var v ImageVersion
		if err := rows.Scan(&v.ID, &v.ImageID, &v.VersionTag, &v.BlobID,
			&v.BlobKey, &v.BlobSHA256, &v.SizeBytes, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// --- capture jobs ------------------------------------------------------

type JobCapture struct {
	Kind       string
	ImageID    *uuid.UUID
	VersionTag string
}

// GetJobCapture returns the job's kind and (for capture jobs) the target
// image + version tag.
func (s *Store) GetJobCapture(ctx context.Context, jobID uuid.UUID) (*JobCapture, error) {
	jc := &JobCapture{}
	var tag *string
	row := s.pool.QueryRow(ctx, `
		SELECT kind, capture_image_id, capture_version_tag
		FROM deployment_jobs WHERE id = $1`, jobID)
	if err := row.Scan(&jc.Kind, &jc.ImageID, &tag); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if tag != nil {
		jc.VersionTag = *tag
	}
	return jc, nil
}

// GetBlobBySHA looks a blob up by its content hash.
func (s *Store) GetBlobBySHA(ctx context.Context, sha256hex string) (*Blob, error) {
	b := &Blob{}
	row := s.pool.QueryRow(ctx, `
		SELECT id, sha256, size_bytes, s3_bucket, s3_key, created_at
		FROM blobs WHERE sha256 = $1`, sha256hex)
	if err := row.Scan(&b.ID, &b.SHA256, &b.SizeBytes, &b.S3Bucket, &b.S3Key, &b.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return b, nil
}

// --- driver pack management -------------------------------------------

type DriverPackRow struct {
	PackID     uuid.UUID       `json:"pack_id"`
	Vendor     string          `json:"vendor"`
	Model      string          `json:"model"`
	OSFamily   string          `json:"os_family"`
	OSVersion  string          `json:"os_version"`
	VersionID  uuid.UUID       `json:"version_id"`
	VersionTag string          `json:"version_tag"`
	BlobSHA256 string          `json:"blob_sha256"`
	SizeBytes  int64           `json:"size_bytes"`
	Rules      []DriverRule    `json:"rules"`
	CreatedAt  time.Time       `json:"created_at"`
}

type DriverRule struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// CreateDriverPackVersion upserts the pack identity, adds a version
// backed by the blob, and attaches its match rules — atomically.
func (s *Store) CreateDriverPackVersion(ctx context.Context, vendor, model, osFamily, osVersion, versionTag string, blobID uuid.UUID, rules []DriverRule) (uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var packID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO driver_packs (vendor, model, os_family, os_version)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (vendor, model, os_family, os_version)
		DO UPDATE SET vendor = EXCLUDED.vendor
		RETURNING id`, vendor, model, osFamily, osVersion).Scan(&packID); err != nil {
		return uuid.Nil, err
	}
	var versionID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO driver_pack_versions (pack_id, version_tag, blob_id)
		VALUES ($1, $2, $3)
		RETURNING id`, packID, versionTag, blobID).Scan(&versionID); err != nil {
		return uuid.Nil, err
	}
	for _, r := range rules {
		if _, err := tx.Exec(ctx, `
			INSERT INTO driver_match_rules (pack_version_id, match_type, match_value)
			VALUES ($1, $2, $3)`, versionID, r.Type, r.Value); err != nil {
			return uuid.Nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return versionID, nil
}

// ListDriverPacks returns every pack version with its rules, newest first.
func (s *Store) ListDriverPacks(ctx context.Context) ([]DriverPackRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.vendor, p.model, p.os_family, p.os_version,
		       pv.id, pv.version_tag, b.sha256, b.size_bytes, pv.created_at,
		       COALESCE(r.match_type, ''), COALESCE(r.match_value, '')
		FROM driver_pack_versions pv
		JOIN driver_packs p ON p.id = pv.pack_id
		JOIN blobs b ON b.id = pv.blob_id
		LEFT JOIN driver_match_rules r ON r.pack_version_id = pv.id
		ORDER BY pv.created_at DESC, pv.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byVersion := map[uuid.UUID]*DriverPackRow{}
	var order []uuid.UUID
	for rows.Next() {
		var row DriverPackRow
		var rType, rValue string
		if err := rows.Scan(&row.PackID, &row.Vendor, &row.Model, &row.OSFamily,
			&row.OSVersion, &row.VersionID, &row.VersionTag, &row.BlobSHA256,
			&row.SizeBytes, &row.CreatedAt, &rType, &rValue); err != nil {
			return nil, err
		}
		existing, ok := byVersion[row.VersionID]
		if !ok {
			row.Rules = []DriverRule{}
			byVersion[row.VersionID] = &row
			order = append(order, row.VersionID)
			existing = &row
		}
		if rType != "" {
			existing.Rules = append(existing.Rules, DriverRule{Type: rType, Value: rValue})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]DriverPackRow, 0, len(order))
	for _, id := range order {
		out = append(out, *byVersion[id])
	}
	return out, nil
}

// DeleteDriverPackVersion removes a version (rules cascade). The pack
// row is left even if empty — it's identity, not payload.
func (s *Store) DeleteDriverPackVersion(ctx context.Context, versionID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM driver_pack_versions WHERE id = $1`, versionID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- wake-on-LAN ------------------------------------------------------

type WakeRequest struct {
	ID          uuid.UUID  `json:"id"`
	MachineID   uuid.UUID  `json:"machine_id"`
	MAC         string     `json:"mac"`
	Site        string     `json:"site"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	ClaimedAt   *time.Time `json:"claimed_at"`
	ClaimedBy   *string    `json:"claimed_by"`
	CreatedAt   time.Time  `json:"created_at"`
}

// CreateWakeRequest queues a wake for the machine. scheduledAt zero
// means "as soon as an agent polls".
func (s *Store) CreateWakeRequest(ctx context.Context, machineID uuid.UUID, mac, site string, scheduledAt time.Time, requestedBy *uuid.UUID) (uuid.UUID, error) {
	if site == "" {
		site = "default"
	}
	if scheduledAt.IsZero() {
		scheduledAt = time.Now().UTC()
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO wake_requests (machine_id, mac, site, scheduled_at, requested_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, machineID, mac, site, scheduledAt, requestedBy).Scan(&id)
	return id, err
}

// ClaimDueWakeRequests atomically claims every unclaimed request for the
// site whose schedule has arrived, and returns them. Each request is
// delivered to exactly one agent (the UPDATE is the claim), so two
// agents polling the same site don't double-wake — though WoL itself is
// idempotent, duplicate storms across large sites are not free.
func (s *Store) ClaimDueWakeRequests(ctx context.Context, site, agent string, limit int) ([]WakeRequest, error) {
	if site == "" {
		site = "default"
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		UPDATE wake_requests w
		SET claimed_at = now(), claimed_by = $2
		FROM (
			SELECT id FROM wake_requests
			WHERE site = $1 AND claimed_at IS NULL AND scheduled_at <= now()
			ORDER BY scheduled_at
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		) due
		WHERE w.id = due.id
		RETURNING w.id, w.machine_id, w.mac::text, w.site, w.scheduled_at,
		          w.claimed_at, w.claimed_by, w.created_at`,
		site, agent, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WakeRequest
	for rows.Next() {
		var w WakeRequest
		if err := rows.Scan(&w.ID, &w.MachineID, &w.MAC, &w.Site,
			&w.ScheduledAt, &w.ClaimedAt, &w.ClaimedBy, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ListWakeRequests returns a machine's wake history, newest first.
func (s *Store) ListWakeRequests(ctx context.Context, machineID uuid.UUID, limit int) ([]WakeRequest, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, machine_id, mac::text, site, scheduled_at, claimed_at, claimed_by, created_at
		FROM wake_requests WHERE machine_id = $1
		ORDER BY created_at DESC LIMIT $2`, machineID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WakeRequest
	for rows.Next() {
		var w WakeRequest
		if err := rows.Scan(&w.ID, &w.MachineID, &w.MAC, &w.Site,
			&w.ScheduledAt, &w.ClaimedAt, &w.ClaimedBy, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ReapWakeRequests deletes claimed requests older than `older`.
func (s *Store) ReapWakeRequests(ctx context.Context, older time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM wake_requests
		WHERE claimed_at IS NOT NULL AND claimed_at < now() - $1::interval`,
		older.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// --- sites ------------------------------------------------------------

type Site struct {
	Name          string    `json:"name"`
	MirrorBaseURL *string   `json:"mirror_base_url"`
	Description   *string   `json:"description"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Store) UpsertSite(ctx context.Context, name string, mirrorBaseURL, description *string) (*Site, error) {
	site := &Site{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO sites (name, mirror_base_url, description)
		VALUES ($1, $2, $3)
		ON CONFLICT (name) DO UPDATE
		   SET mirror_base_url = EXCLUDED.mirror_base_url,
		       description     = EXCLUDED.description
		RETURNING name, mirror_base_url, description, created_at`,
		name, mirrorBaseURL, description).
		Scan(&site.Name, &site.MirrorBaseURL, &site.Description, &site.CreatedAt)
	if err != nil {
		return nil, err
	}
	return site, nil
}

func (s *Store) ListSites(ctx context.Context) ([]Site, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT name, mirror_base_url, description, created_at
		FROM sites ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var site Site
		if err := rows.Scan(&site.Name, &site.MirrorBaseURL, &site.Description, &site.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, site)
	}
	return out, rows.Err()
}

func (s *Store) DeleteSite(ctx context.Context, name string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sites WHERE name = $1`, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SiteMirror returns the mirror base URL for a site name, or "" when
// the site is unknown or has no mirror configured.
func (s *Store) SiteMirror(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	var mirror *string
	err := s.pool.QueryRow(ctx,
		`SELECT mirror_base_url FROM sites WHERE name = $1`, name).Scan(&mirror)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if mirror == nil {
		return "", nil
	}
	return *mirror, nil
}

// --- user administration ----------------------------------------------

type UserWithRoles struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	OIDCLinked bool     `json:"oidc_linked"`
	Roles     []string  `json:"roles"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) ListUsersWithRoles(ctx context.Context, limit, offset int) ([]UserWithRoles, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.email::text, u.oidc_subject IS NOT NULL,
		       COALESCE(array_agg(r.name ORDER BY r.name) FILTER (WHERE r.name IS NOT NULL), '{}'),
		       u.created_at
		FROM users u
		LEFT JOIN user_roles ur ON ur.user_id = u.id
		LEFT JOIN roles r ON r.id = ur.role_id
		GROUP BY u.id
		ORDER BY u.created_at
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserWithRoles
	for rows.Next() {
		var u UserWithRoles
		if err := rows.Scan(&u.ID, &u.Email, &u.OIDCLinked, &u.Roles, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type RoleWithPerms struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

func (s *Store) ListRoles(ctx context.Context) ([]RoleWithPerms, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.name,
		       COALESCE(array_agg(rp.permission ORDER BY rp.permission)
		                FILTER (WHERE rp.permission IS NOT NULL), '{}')
		FROM roles r
		LEFT JOIN role_permissions rp ON rp.role_id = r.id
		GROUP BY r.id
		ORDER BY r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoleWithPerms
	for rows.Next() {
		var role RoleWithPerms
		if err := rows.Scan(&role.Name, &role.Permissions); err != nil {
			return nil, err
		}
		out = append(out, role)
	}
	return out, rows.Err()
}

// GrantRole is idempotent. ErrNotFound when the role name is unknown.
func (s *Store) GrantRole(ctx context.Context, userID uuid.UUID, roleName string) error {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		SELECT $1, id FROM roles WHERE name = $2
		ON CONFLICT DO NOTHING`, userID, roleName)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Either the role doesn't exist or the grant already existed;
		// disambiguate so unknown role names are a caller error.
		var exists bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM roles WHERE name = $1)`, roleName).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

func (s *Store) RevokeRole(ctx context.Context, userID uuid.UUID, roleName string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM user_roles
		WHERE user_id = $1 AND role_id = (SELECT id FROM roles WHERE name = $2)`,
		userID, roleName)
	return err
}

// CountOtherAdmins counts users other than excludeUser holding the
// admin role. Used by the last-admin lockout guard.
func (s *Store) CountOtherAdmins(ctx context.Context, excludeUser uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE r.name = 'admin' AND ur.user_id <> $1`, excludeUser).Scan(&n)
	return n, err
}

// --- errors ----------------------------------------------------------

var (
	ErrNotFound        = errors.New("not found")
	ErrNoActiveProfile = errors.New("no active profile and no default profile")
	ErrTokenInvalid    = errors.New("token invalid or already consumed")
)
