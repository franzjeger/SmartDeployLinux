// Integration test for job-completion notifications. Gated on
// DEPLOY_TEST_PG_DSN like the api store tests.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DEPLOY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("DEPLOY_TEST_PG_DSN not set; skipping integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// The api's migrations must already be applied (the api test suite
	// runs first in CI; locally run `api migrate up`). Sanity-check.
	var ok bool
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		 WHERE table_name='deployment_jobs' AND column_name='notified_at')`).Scan(&ok); err != nil || !ok {
		t.Skip("deployment_jobs.notified_at missing; apply migrations first")
	}
	return pool
}

// seedJob inserts a machine+profile chain and a job in the given state.
func seedJob(t *testing.T, pool *pgxpool.Pool, state string, finishedAgo time.Duration) string {
	t.Helper()
	ctx := context.Background()
	var userID, blobID, imageID, profileID, machineID, jobID string
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO users (email) VALUES (md5(random()::text) || '@n.test') RETURNING id`).Scan(&userID))
	must(pool.QueryRow(ctx, `INSERT INTO blobs (sha256, size_bytes, s3_bucket, s3_key)
		VALUES (md5(random()::text) || md5(random()::text), 1, 'images', md5(random()::text)) RETURNING id`).Scan(&blobID))
	must(pool.QueryRow(ctx, `INSERT INTO images (name, os_family, os_version, arch)
		VALUES ('ntest-' || md5(random()::text), 'linux', '24.04', 'amd64') RETURNING id`).Scan(&imageID))
	must(pool.QueryRow(ctx, `INSERT INTO deployment_profiles (name, image_id)
		VALUES ('ntest-' || md5(random()::text), $1) RETURNING id`, imageID).Scan(&profileID))
	must(pool.QueryRow(ctx, `INSERT INTO machines (asset_tag)
		VALUES ('ntest-' || md5(random()::text)) RETURNING id`).Scan(&machineID))
	terminal := state == "completed" || state == "failed" || state == "cancelled"
	if terminal {
		must(pool.QueryRow(ctx, `
			INSERT INTO deployment_jobs (machine_id, profile_id, state, finished_at)
			VALUES ($1, $2, $3::deployment_state, now() - $4::interval) RETURNING id`,
			machineID, profileID, state, finishedAgo.String()).Scan(&jobID))
	} else {
		must(pool.QueryRow(ctx, `
			INSERT INTO deployment_jobs (machine_id, profile_id, state)
			VALUES ($1, $2, $3::deployment_state) RETURNING id`,
			machineID, profileID, state).Scan(&jobID))
	}
	return jobID
}

func TestNotifyTerminalJobs(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var received atomic.Int64
	var lastPayload atomic.Value
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		lastPayload.Store(p)
		received.Add(1)
		w.WriteHeader(200)
	}))
	defer hook.Close()

	fresh := seedJob(t, pool, "completed", time.Minute)
	_ = seedJob(t, pool, "imaging", 0)               // non-terminal: excluded
	stale := seedJob(t, pool, "failed", 3*time.Hour) // older than 1h window: excluded

	n, err := notifyTerminalJobs(ctx, pool, hook.URL)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("sent %d, want >=1", n)
	}
	// The fresh job was delivered with the right shape.
	p, _ := lastPayload.Load().(map[string]any)
	if p["event"] != "job.terminal" || p["state"] == "" || p["text"] == "" {
		t.Fatalf("payload: %v", p)
	}

	// Second call: nothing new (claims are one-shot).
	before := received.Load()
	if _, err := notifyTerminalJobs(ctx, pool, hook.URL); err != nil {
		t.Fatal(err)
	}
	if received.Load() != before {
		t.Fatal("double delivery")
	}

	// The fresh job is marked; the stale one is untouched (outside the
	// 1h backfill window, so enabling webhooks doesn't replay history).
	var notified bool
	if err := pool.QueryRow(ctx,
		`SELECT notified_at IS NOT NULL FROM deployment_jobs WHERE id = $1`, fresh).Scan(&notified); err != nil || !notified {
		t.Fatalf("fresh job not marked: %v %v", notified, err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT notified_at IS NULL FROM deployment_jobs WHERE id = $1`, stale).Scan(&notified); err != nil || !notified {
		t.Fatalf("stale job was claimed: %v %v", notified, err)
	}
}
