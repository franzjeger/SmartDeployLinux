// Integration tests against a real Postgres. Skipped when
// DEPLOY_TEST_PG_DSN is unset (e.g. in plain `go test ./...`); enabled
// in CI by setting the env var to a reachable Postgres.

package store

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DEPLOY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("DEPLOY_TEST_PG_DSN not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.MigrateUp(ctx); err != nil {
		st.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Start every test in a clean slate. Order matters: rows that
	// reference others must be deleted first. machines references
	// deployment_profiles via default_profile_id, so machines goes
	// before deployment_profiles.
	for _, tbl := range []string{
		"audit_events", "deployment_events", "deployment_jobs",
		"one_shot_tokens", "auth_codes", "redeem_attempts",
		"answer_file_templates",
		"driver_match_rules", "driver_pack_versions", "driver_packs",
		"machines",
		"image_versions", "deployment_profiles", "images", "blobs",
	} {
		if _, err := st.Pool().Exec(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("clean %s: %v", tbl, err)
		}
	}
	return st
}

func TestMigrateUp_Idempotent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.MigrateUp(ctx); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	if err := st.MigrateUp(ctx); err != nil {
		t.Fatalf("third migrate: %v", err)
	}
}

func TestCreateAndGetMachine(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	tag := "lab-01"
	mac := "aa:bb:cc:dd:ee:ff"
	in := CreateMachineInput{
		AssetTag:   &tag,
		MACPrimary: &mac,
		Vendor:     strPtr("Dell"),
		Model:      strPtr("Latitude 7440"),
		Attributes: []byte(`{"site":"hq"}`),
	}
	m, err := st.CreateMachine(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if m.AssetTag == nil || *m.AssetTag != "lab-01" {
		t.Fatalf("asset_tag wrong: %+v", m.AssetTag)
	}

	got, err := st.GetMachine(ctx, m.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != m.ID {
		t.Fatalf("round-trip ID mismatch")
	}

	got2, err := st.GetMachineByMAC(ctx, mac)
	if err != nil {
		t.Fatalf("get by mac: %v", err)
	}
	if got2.ID != m.ID {
		t.Fatalf("by-mac mismatch")
	}
}

func TestLookupRenderBundle_FallsBackToDefaultProfile(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// Seed: blob → image → image_version → deployment_profile.
	var blobID, imageID, profileID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO blobs (sha256, size_bytes, s3_bucket, s3_key)
		VALUES ('abc', 100, 'images', 'win11/install.wim')
		RETURNING id`).Scan(&blobID))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO images (name, os_family, os_version, arch)
		VALUES ('win11', 'windows', '11', 'amd64')
		RETURNING id`).Scan(&imageID))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO image_versions (image_id, version_tag, blob_id)
		VALUES ($1, '1.0', $2) RETURNING id`,
		imageID, blobID).Scan(new(uuid.UUID)))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO deployment_profiles (name, image_id, answer_file_vars)
		VALUES ('win11-baseline', $1, '{"hello":"world"}'::jsonb)
		RETURNING id`, imageID).Scan(&profileID))

	mTag := "labwin"
	m, err := st.CreateMachine(ctx, CreateMachineInput{
		AssetTag:         &mTag,
		DefaultProfileID: &profileID,
	})
	if err != nil {
		t.Fatalf("create machine: %v", err)
	}

	bundle, err := st.LookupRenderBundle(ctx, m.ID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if bundle.ProfileID != profileID {
		t.Fatalf("expected fallback to default profile")
	}
	if bundle.ImageOSFamily != "windows" {
		t.Fatalf("os family: %s", bundle.ImageOSFamily)
	}
	if bundle.ImageBlobKey != "win11/install.wim" {
		t.Fatalf("blob key: %s", bundle.ImageBlobKey)
	}
	var vars map[string]any
	if err := json.Unmarshal(bundle.ProfileVars, &vars); err != nil {
		t.Fatalf("unmarshal vars: %v", err)
	}
	if vars["hello"] != "world" {
		t.Fatalf("vars not preserved: %+v", vars)
	}
	if bundle.JobID != nil {
		t.Fatalf("expected nil JobID when no active job")
	}
}

func TestLookupRenderBundle_PrefersActiveJob(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var defaultProfile, jobProfile uuid.UUID
	mustImageProfile(t, st, "default-img", "default-prof", &defaultProfile)
	mustImageProfile(t, st, "active-img", "active-prof", &jobProfile)

	tag := "lab-02"
	m, err := st.CreateMachine(ctx, CreateMachineInput{
		AssetTag:         &tag,
		DefaultProfileID: &defaultProfile,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Active job pointing at jobProfile.
	var jobID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO deployment_jobs (machine_id, profile_id, state)
		VALUES ($1, $2, 'imaging') RETURNING id`,
		m.ID, jobProfile).Scan(&jobID))

	bundle, err := st.LookupRenderBundle(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.ProfileID != jobProfile {
		t.Fatalf("expected active job profile, got %s vs %s",
			bundle.ProfileID, jobProfile)
	}
	if bundle.JobID == nil || *bundle.JobID != jobID {
		t.Fatalf("expected job id in bundle")
	}
}

func TestTransitionJob_HappyAndInvalid(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var profile uuid.UUID
	mustImageProfile(t, st, "img", "prof", &profile)
	tag := "lab-tr"
	m, err := st.CreateMachine(ctx, CreateMachineInput{
		AssetTag: &tag, DefaultProfileID: &profile,
	})
	if err != nil {
		t.Fatal(err)
	}

	var jobID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO deployment_jobs (machine_id, profile_id, state)
		VALUES ($1, $2, 'pending') RETURNING id`,
		m.ID, profile).Scan(&jobID))

	if err := st.TransitionJob(ctx, jobID, "bootstrapped"); err != nil {
		t.Fatalf("pending→bootstrapped: %v", err)
	}
	if err := st.TransitionJob(ctx, jobID, "imaging"); err != nil {
		t.Fatalf("bootstrapped→imaging: %v", err)
	}
	if err := st.TransitionJob(ctx, jobID, "pending"); err == nil {
		t.Fatal("imaging→pending should be rejected")
	}
	if err := st.TransitionJob(ctx, jobID, "completed"); err != nil {
		t.Fatalf("imaging→completed: %v", err)
	}
}

func TestVerifyOneShotToken_AtomicConsume(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// Set up auth_code + token.
	var profile uuid.UUID
	mustImageProfile(t, st, "img", "prof", &profile)
	tag := "lab-tok"
	m, err := st.CreateMachine(ctx, CreateMachineInput{
		AssetTag: &tag, DefaultProfileID: &profile,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Need a user row for auth_codes.issued_by FK.
	var userID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO users (email) VALUES ('admin@example.com')
		RETURNING id`).Scan(&userID))

	var authCodeID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO auth_codes (code_hash, machine_id, profile_id, issued_by, expires_at)
		VALUES ('h', $1, $2, $3, now() + '1h'::interval)
		RETURNING id`, m.ID, profile, userID).Scan(&authCodeID))

	var jobID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO deployment_jobs (machine_id, profile_id, auth_code_id, state)
		VALUES ($1, $2, $3, 'bootstrapped') RETURNING id`,
		m.ID, profile, authCodeID).Scan(&jobID))

	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO one_shot_tokens (auth_code_id, token_hash, purpose, expires_at)
		VALUES ($1, 'tokhash', 'phone-home', now() + '1h'::interval)
		RETURNING id`, authCodeID).Scan(new(uuid.UUID)))

	ip, _ := netip.ParseAddr("203.0.113.5")

	got, err := st.VerifyOneShotToken(ctx, "tokhash", ip)
	if err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if got != jobID {
		t.Fatalf("returned job id mismatch: %s vs %s", got, jobID)
	}

	// Replay must fail.
	if _, err := st.VerifyOneShotToken(ctx, "tokhash", ip); err == nil {
		t.Fatal("expected error on replay")
	}
}

func TestAppendDeploymentEvent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var profile uuid.UUID
	mustImageProfile(t, st, "img", "prof", &profile)
	tag := "lab-ev"
	m, err := st.CreateMachine(ctx, CreateMachineInput{
		AssetTag: &tag, DefaultProfileID: &profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	var jobID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO deployment_jobs (machine_id, profile_id, state)
		VALUES ($1, $2, 'imaging') RETURNING id`,
		m.ID, profile).Scan(&jobID))

	for _, p := range []string{"imaging", "imaging", "completed"} {
		err := st.AppendDeploymentEvent(ctx, DeploymentEventInput{
			JobID: jobID, Phase: p, Message: "tick", Data: []byte(`{}`),
		})
		if err != nil {
			t.Fatalf("append %s: %v", p, err)
		}
	}
	var n int
	must(t, st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM deployment_events WHERE job_id = $1`, jobID,
	).Scan(&n))
	if n != 3 {
		t.Fatalf("expected 3 events, got %d", n)
	}
}

func TestLookupRenderBundleByToken_AndConsume(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var profile uuid.UUID
	mustImageProfile(t, st, "img-tok", "prof-tok", &profile)
	tag := "lab-tokpath"
	m, err := st.CreateMachine(ctx, CreateMachineInput{
		AssetTag: &tag, DefaultProfileID: &profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	var userID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO users (email) VALUES ('btok@example.com')
		RETURNING id`).Scan(&userID))

	var authCodeID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO auth_codes (code_hash, machine_id, profile_id, issued_by, expires_at)
		VALUES ('codehash', $1, $2, $3, now() + '1h'::interval)
		RETURNING id`, m.ID, profile, userID).Scan(&authCodeID))

	// Boot token
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO one_shot_tokens (auth_code_id, token_hash, purpose, expires_at)
		VALUES ($1, 'sha256:boothash', 'boot', now() + '1h'::interval)
		RETURNING id`, authCodeID).Scan(new(uuid.UUID)))

	var jobID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO deployment_jobs (machine_id, profile_id, auth_code_id, state)
		VALUES ($1, $2, $3, 'imaging') RETURNING id`,
		m.ID, profile, authCodeID).Scan(&jobID))

	// Render lookup must succeed (token not yet consumed).
	bundle, err := st.LookupRenderBundleByToken(ctx, "sha256:boothash")
	if err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	if bundle.Machine.ID != m.ID {
		t.Fatalf("wrong machine: %s vs %s", bundle.Machine.ID, m.ID)
	}
	// Idempotent: second lookup also succeeds (it's the user-data fetch).
	if _, err := st.LookupRenderBundleByToken(ctx, "sha256:boothash"); err != nil {
		t.Fatalf("second lookup: %v", err)
	}

	// Terminal transition consumes boot tokens.
	if err := st.TransitionJob(ctx, jobID, "completed"); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if _, err := st.LookupRenderBundleByToken(ctx, "sha256:boothash"); err == nil {
		t.Fatal("expected token to be consumed after terminal transition")
	}
}

func TestReapAndLock(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// Insert old + new redeem_attempts.
	for _, hrs := range []string{"2", "1", "0"} {
		_, err := st.Pool().Exec(ctx, `
			INSERT INTO redeem_attempts (source_ip, at)
			VALUES ('203.0.113.1', now() - ($1 || ' hours')::interval)`, hrs)
		must(t, err)
	}
	n, err := st.ReapRedeemAttempts(ctx, 90*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 reaped (>90min), got %d", n)
	}

	// Insert an expired auth_code and lock it.
	var profile uuid.UUID
	mustImageProfile(t, st, "img-lock", "prof-lock", &profile)
	tag := "lab-lock"
	m, err := st.CreateMachine(ctx, CreateMachineInput{
		AssetTag: &tag, DefaultProfileID: &profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	var userID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO users (email) VALUES ('lock@example.com')
		RETURNING id`).Scan(&userID))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO auth_codes (code_hash, machine_id, profile_id, issued_by, expires_at)
		VALUES ('expired1', $1, $2, $3, now() - '1m'::interval)
		RETURNING id`, m.ID, profile, userID).Scan(new(uuid.UUID)))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO auth_codes (code_hash, machine_id, profile_id, issued_by, expires_at)
		VALUES ('alive', $1, $2, $3, now() + '1h'::interval)
		RETURNING id`, m.ID, profile, userID).Scan(new(uuid.UUID)))

	locked, err := st.LockExpiredAuthCodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if locked != 1 {
		t.Fatalf("expected 1 locked, got %d", locked)
	}
}

// --- helpers ----------------------------------------------------------

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func strPtr(s string) *string { return &s }

func mustImageProfile(t *testing.T, st *Store, imgName, profName string, profileOut *uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var blobID, imageID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO blobs (sha256, size_bytes, s3_bucket, s3_key)
		VALUES ($1, 1, 'images', $2)
		RETURNING id`, randSHA(t), imgName+"/file").Scan(&blobID))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO images (name, os_family, os_version, arch)
		VALUES ($1, 'linux', '24.04', 'amd64')
		RETURNING id`, imgName).Scan(&imageID))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO image_versions (image_id, version_tag, blob_id)
		VALUES ($1, '1.0', $2) RETURNING id`,
		imageID, blobID).Scan(new(uuid.UUID)))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO deployment_profiles (name, image_id)
		VALUES ($1, $2) RETURNING id`,
		profName, imageID).Scan(profileOut))
}

func randSHA(t *testing.T) string {
	t.Helper()
	id := uuid.New().String()
	return strings.ReplaceAll(id, "-", "")
}
