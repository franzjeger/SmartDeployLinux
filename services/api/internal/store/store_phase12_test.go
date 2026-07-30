// Integration tests for the phone-home hardening + driver pack matching
// added after Phase 11. Same DEPLOY_TEST_PG_DSN gating as
// store_integration_test.go.

package store

import (
	"context"
	"encoding/json"
	"testing"

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
