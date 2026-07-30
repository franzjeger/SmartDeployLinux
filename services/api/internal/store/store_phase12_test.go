// Integration tests for the phone-home hardening + driver pack matching
// added after Phase 11. Same DEPLOY_TEST_PG_DSN gating as
// store_integration_test.go.

package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/driverpack"
)

// fixtureJob creates machine/profile/auth_code/token/job and returns ids.
func fixtureJob(t *testing.T, st *Store, tokenHash, state string) (machineID, profileID, jobID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var profile uuid.UUID
	mustImageProfile(t, st, "img-"+tokenHash, "prof-"+tokenHash, &profile)
	tag := "lab-" + tokenHash
	m, err := st.CreateMachine(ctx, CreateMachineInput{
		AssetTag: &tag, DefaultProfileID: &profile,
	})
	must(t, err)
	var userID uuid.UUID
	// users are not truncated between runs (role seeds live there), so
	// the email must be unique per invocation.
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO users (email) VALUES ($1) RETURNING id`,
		uuid.NewString()+"@example.com").Scan(&userID))
	var authCodeID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO auth_codes (code_hash, machine_id, profile_id, issued_by, expires_at)
		VALUES ($1, $2, $3, $4, now() + '1h'::interval)
		RETURNING id`, "ch-"+tokenHash, m.ID, profile, userID).Scan(&authCodeID))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO one_shot_tokens (auth_code_id, token_hash, purpose, expires_at)
		VALUES ($1, $2, 'boot', now() + '1h'::interval)
		RETURNING id`, authCodeID, tokenHash).Scan(new(uuid.UUID)))
	var jid uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO deployment_jobs (machine_id, profile_id, auth_code_id, state)
		VALUES ($1, $2, $3, $4::deployment_state) RETURNING id`,
		m.ID, profile, authCodeID, state).Scan(&jid))
	return m.ID, profile, jid
}

func TestVerifyPhoneHomeToken_RepeatableAcrossPhases(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	_, _, jobID := fixtureJob(t, st, "sha256:ph1", "pending")

	// Valid repeatedly, without consuming, across state changes.
	for _, state := range []string{"pending", "bootstrapped", "imaging", "post_install"} {
		must(t, st.Pool().QueryRow(ctx,
			`UPDATE deployment_jobs SET state=$2::deployment_state WHERE id=$1 RETURNING id`,
			jobID, state).Scan(new(uuid.UUID)))
		for i := 0; i < 2; i++ {
			if _, _, err := st.VerifyPhoneHomeToken(ctx, "sha256:ph1", jobID); err != nil {
				t.Fatalf("state %s call %d: %v", state, i, err)
			}
		}
	}
	// Wrong job → rejected.
	if _, _, err := st.VerifyPhoneHomeToken(ctx, "sha256:ph1", uuid.New()); err == nil {
		t.Fatal("expected rejection for wrong job")
	}
	// Terminal state → rejected (and token revoked by TransitionJob).
	must(t, st.TransitionJob(ctx, jobID, "completed"))
	if _, _, err := st.VerifyPhoneHomeToken(ctx, "sha256:ph1", jobID); err == nil {
		t.Fatal("expected rejection after terminal state")
	}
}

func TestAdvanceJob_WalksIntermediateStates(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	_, _, jobID := fixtureJob(t, st, "sha256:adv1", "pending")

	// pending → imaging must pass through bootstrapped.
	must(t, st.AdvanceJob(ctx, jobID, "imaging"))
	var state string
	must(t, st.Pool().QueryRow(ctx,
		`SELECT state::text FROM deployment_jobs WHERE id=$1`, jobID).Scan(&state))
	if state != "imaging" {
		t.Fatalf("state = %s, want imaging", state)
	}
	// Advancing to an earlier-or-equal state is a no-op, not an error.
	must(t, st.AdvanceJob(ctx, jobID, "bootstrapped"))
	must(t, st.Pool().QueryRow(ctx,
		`SELECT state::text FROM deployment_jobs WHERE id=$1`, jobID).Scan(&state))
	if state != "imaging" {
		t.Fatalf("no-op advance changed state to %s", state)
	}
	// failed reachable directly.
	must(t, st.AdvanceJob(ctx, jobID, "failed"))
	must(t, st.Pool().QueryRow(ctx,
		`SELECT state::text FROM deployment_jobs WHERE id=$1`, jobID).Scan(&state))
	if state != "failed" {
		t.Fatalf("state = %s, want failed", state)
	}
}

func TestMatchDriverPacks_DBQueryAndPriority(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var blobA, blobB uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO blobs (sha256, size_bytes, s3_bucket, s3_key)
		VALUES ($1, 1, 'drivers', 'drivers/dell-7440.zip') RETURNING id`, randSHA(t)).Scan(&blobA))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO blobs (sha256, size_bytes, s3_bucket, s3_key)
		VALUES ($1, 1, 'drivers', 'drivers/dell-generic.zip') RETURNING id`, randSHA(t)).Scan(&blobB))

	var packA, packB uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO driver_packs (vendor, model, os_family, os_version)
		VALUES ('Dell', 'Latitude 7440', 'windows', '11') RETURNING id`).Scan(&packA))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO driver_packs (vendor, model, os_family, os_version)
		VALUES ('Dell', 'generic', 'windows', '11') RETURNING id`).Scan(&packB))

	var verA, verB uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO driver_pack_versions (pack_id, version_tag, blob_id)
		VALUES ($1, 'v1', $2) RETURNING id`, packA, blobA).Scan(&verA))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO driver_pack_versions (pack_id, version_tag, blob_id)
		VALUES ($1, 'v1', $2) RETURNING id`, packB, blobB).Scan(&verB))

	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO driver_match_rules (pack_version_id, match_type, match_value)
		VALUES ($1, 'dmi-product', 'Latitude 7440') RETURNING id`, verA).Scan(new(uuid.UUID)))
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO driver_match_rules (pack_version_id, match_type, match_value)
		VALUES ($1, 'dmi-vendor', 'Dell Inc.') RETURNING id`, verB).Scan(new(uuid.UUID)))

	matched, err := st.MatchDriverPacks(ctx, driverpack.Fingerprint{
		DMIVendor:  "Dell Inc.",
		DMIProduct: "Latitude 7440",
		OSFamily:   "windows",
		OSVersion:  "11",
	})
	must(t, err)
	if len(matched) != 2 {
		t.Fatalf("matched %d packs, want 2: %+v", len(matched), matched)
	}
	// dmi-product beats dmi-vendor.
	if matched[0].BlobKey != "drivers/dell-7440.zip" {
		t.Fatalf("wrong priority order: %+v", matched)
	}

	// Linux fingerprint matches nothing.
	none, err := st.MatchDriverPacks(ctx, driverpack.Fingerprint{
		DMIVendor: "Dell Inc.", OSFamily: "linux", OSVersion: "24.04",
	})
	must(t, err)
	if len(none) != 0 {
		t.Fatalf("expected no linux matches, got %+v", none)
	}
}

func TestRecordMachineInventory_MergesAttributes(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	tag := "lab-inv"
	m, err := st.CreateMachine(ctx, CreateMachineInput{
		AssetTag: &tag, Attributes: []byte(`{"site":"hq"}`),
	})
	must(t, err)

	must(t, st.RecordMachineInventory(ctx, m.ID,
		[]byte(`{"dmi_vendor":"Dell Inc.","dmi_product":"Latitude 7440"}`)))

	got, err := st.GetMachine(ctx, m.ID)
	must(t, err)
	if got.Vendor == nil || *got.Vendor != "Dell Inc." {
		t.Fatalf("vendor not backfilled: %+v", got.Vendor)
	}
	if got.Model == nil || *got.Model != "Latitude 7440" {
		t.Fatalf("model not backfilled: %+v", got.Model)
	}
	var attrs map[string]any
	must(t, json.Unmarshal(got.Attributes, &attrs))
	if attrs["site"] != "hq" {
		t.Fatalf("original attribute lost: %v", attrs)
	}
	hw, ok := attrs["hardware"].(map[string]any)
	if !ok || hw["dmi_product"] != "Latitude 7440" {
		t.Fatalf("hardware inventory not merged: %v", attrs)
	}
}

func TestCaptureJobKindAndTarget(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	_, _, jobID := fixtureJob(t, st, "sha256:cap1", "bootstrapped")

	// Default kind is deploy.
	jc, err := st.GetJobCapture(ctx, jobID)
	must(t, err)
	if jc.Kind != "deploy" || jc.ImageID != nil {
		t.Fatalf("default job capture: %+v", jc)
	}

	// Flag it as a capture job (as the broker does from the auth code).
	var imageID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO images (name, os_family, os_version, arch)
		VALUES ('golden-cap', 'windows', '11', 'amd64') RETURNING id`).Scan(&imageID))
	must(t, st.Pool().QueryRow(ctx, `
		UPDATE deployment_jobs
		SET kind = 'capture', capture_image_id = $2, capture_version_tag = '24H2-v1'
		WHERE id = $1 RETURNING id`, jobID, imageID).Scan(new(uuid.UUID)))

	jc, err = st.GetJobCapture(ctx, jobID)
	must(t, err)
	if jc.Kind != "capture" || jc.ImageID == nil || *jc.ImageID != imageID || jc.VersionTag != "24H2-v1" {
		t.Fatalf("capture target: %+v", jc)
	}
}

func TestDriverPackVersionCRUD(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	sha := randSHA(t)
	blob, err := st.CreateBlob(ctx, sha, 4096, "drivers", "cd/"+sha+"-pack.zip")
	must(t, err)

	rules := []DriverRule{
		{Type: "dmi-product", Value: "Latitude 7440"},
		{Type: "pci-vid-did", Value: "8086:1521"},
	}
	vID, err := st.CreateDriverPackVersion(ctx,
		"Dell Inc.", "Latitude 7440", "windows", "11", "v1", blob.ID, rules)
	must(t, err)

	// Same pack identity + new tag reuses the driver_packs row.
	_, err = st.CreateDriverPackVersion(ctx,
		"Dell Inc.", "Latitude 7440", "windows", "11", "v2", blob.ID, rules[:1])
	must(t, err)
	var packCount int
	must(t, st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM driver_packs WHERE vendor = 'Dell Inc.'`).Scan(&packCount))
	if packCount != 1 {
		t.Fatalf("pack identity duplicated: %d rows", packCount)
	}

	packs, err := st.ListDriverPacks(ctx)
	must(t, err)
	if len(packs) != 2 {
		t.Fatalf("listed %d versions, want 2", len(packs))
	}
	var v1 *DriverPackRow
	for i := range packs {
		if packs[i].VersionID == vID {
			v1 = &packs[i]
		}
	}
	if v1 == nil || len(v1.Rules) != 2 || v1.SizeBytes != 4096 {
		t.Fatalf("v1 row wrong: %+v", v1)
	}

	// The matcher sees the ingested pack immediately.
	matched, err := st.MatchDriverPacks(ctx, driverpack.Fingerprint{
		DMIProduct: "Latitude 7440", OSFamily: "windows", OSVersion: "11",
	})
	must(t, err)
	if len(matched) != 2 {
		t.Fatalf("matcher found %d, want 2", len(matched))
	}

	must(t, st.DeleteDriverPackVersion(ctx, vID))
	if err := st.DeleteDriverPackVersion(ctx, vID); err != ErrNotFound {
		t.Fatalf("second delete: %v", err)
	}
}

func TestWakeRequestQueue(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	tag := "lab-wake"
	mac := "aa:bb:cc:00:11:22"
	m, err := st.CreateMachine(ctx, CreateMachineInput{AssetTag: &tag, MACPrimary: &mac})
	must(t, err)

	// One due now, one scheduled in the future, one for another site.
	_, err = st.CreateWakeRequest(ctx, m.ID, mac, "oslo", time.Time{}, nil)
	must(t, err)
	_, err = st.CreateWakeRequest(ctx, m.ID, mac, "oslo", time.Now().Add(time.Hour), nil)
	must(t, err)
	_, err = st.CreateWakeRequest(ctx, m.ID, mac, "bergen", time.Time{}, nil)
	must(t, err)

	claimed, err := st.ClaimDueWakeRequests(ctx, "oslo", "edge-1", 50)
	must(t, err)
	if len(claimed) != 1 {
		t.Fatalf("claimed %d for oslo, want 1 (future + other-site excluded)", len(claimed))
	}
	if claimed[0].MAC != mac || claimed[0].ClaimedBy == nil || *claimed[0].ClaimedBy != "edge-1" {
		t.Fatalf("claim row wrong: %+v", claimed[0])
	}

	// A second poll gets nothing — the claim is one-shot.
	again, err := st.ClaimDueWakeRequests(ctx, "oslo", "edge-2", 50)
	must(t, err)
	if len(again) != 0 {
		t.Fatalf("double-claimed: %+v", again)
	}

	// History shows all three.
	hist, err := st.ListWakeRequests(ctx, m.ID, 20)
	must(t, err)
	if len(hist) != 3 {
		t.Fatalf("history %d rows, want 3", len(hist))
	}

	// Reap removes only old claimed rows; nothing here is old enough.
	n, err := st.ReapWakeRequests(ctx, time.Hour)
	must(t, err)
	if n != 0 {
		t.Fatalf("reaped %d fresh rows", n)
	}
}

func TestUpdateMachinePartial(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var profile uuid.UUID
	mustImageProfile(t, st, "img-upd", "prof-upd", &profile)
	tag := "lab-upd"
	m, err := st.CreateMachine(ctx, CreateMachineInput{
		AssetTag: &tag, DefaultProfileID: &profile,
		Attributes: []byte(`{"site":"oslo"}`),
	})
	must(t, err)

	// Partial update: only the model changes; everything else survives.
	model := "Latitude 7440"
	got, err := st.UpdateMachine(ctx, m.ID, UpdateMachineInput{Model: &model})
	must(t, err)
	if got.Model == nil || *got.Model != model {
		t.Fatalf("model not set: %+v", got.Model)
	}
	if got.AssetTag == nil || *got.AssetTag != tag {
		t.Fatalf("asset tag lost: %+v", got.AssetTag)
	}
	if got.DefaultProfileID == nil || *got.DefaultProfileID != profile {
		t.Fatalf("default profile lost: %+v", got.DefaultProfileID)
	}

	// Attributes replacement + clearing the default profile.
	got, err = st.UpdateMachine(ctx, m.ID, UpdateMachineInput{
		ClearDefaultProfile: true,
		Attributes:          []byte(`{"site":"bergen"}`),
	})
	must(t, err)
	if got.DefaultProfileID != nil {
		t.Fatalf("default profile not cleared: %v", got.DefaultProfileID)
	}
	var attrs map[string]any
	must(t, json.Unmarshal(got.Attributes, &attrs))
	if attrs["site"] != "bergen" {
		t.Fatalf("attributes not replaced: %v", attrs)
	}

	// Unknown machine → ErrNotFound.
	if _, err := st.UpdateMachine(ctx, uuid.New(), UpdateMachineInput{Model: &model}); err != ErrNotFound {
		t.Fatalf("unknown machine: %v", err)
	}
}

func TestBlobAndImageVersionIngest(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	sha := randSHA(t)
	b1, err := st.CreateBlob(ctx, sha, 1234, "images", "ab/"+sha+"-install.wim")
	must(t, err)
	// Idempotent on sha256: re-registering returns the same row.
	b2, err := st.CreateBlob(ctx, sha, 1234, "images", "different/key")
	must(t, err)
	if b1.ID != b2.ID || b2.S3Key != b1.S3Key {
		t.Fatalf("re-register not idempotent: %+v vs %+v", b1, b2)
	}

	var imageID uuid.UUID
	must(t, st.Pool().QueryRow(ctx, `
		INSERT INTO images (name, os_family, os_version, arch)
		VALUES ('win11-ingest', 'windows', '11', 'amd64') RETURNING id`).Scan(&imageID))

	vID, err := st.CreateImageVersion(ctx, imageID, b1.ID, "24H2-v1")
	must(t, err)
	if vID == uuid.Nil {
		t.Fatal("nil version id")
	}
	// Duplicate tag on the same image must be rejected (unique constraint).
	if _, err := st.CreateImageVersion(ctx, imageID, b1.ID, "24H2-v1"); err == nil {
		t.Fatal("duplicate version tag accepted")
	}

	vs, err := st.ListImageVersions(ctx, imageID)
	must(t, err)
	if len(vs) != 1 || vs[0].BlobSHA256 != sha || vs[0].SizeBytes != 1234 {
		t.Fatalf("bad version list: %+v", vs)
	}
}
