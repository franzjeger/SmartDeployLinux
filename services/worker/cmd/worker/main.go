// worker runs background jobs for the deployserver:
//
//   - reaper: deletes stale rows from redeem_attempts (>24h)
//   - locker: marks expired auth_codes as locked
//   - cleaner: TODO Phase 9 — revokes orphaned ephemeral tailnet nodes
//             whose deployment_jobs is in a terminal state
//
// The work is small enough that a single goroutine on a 30-second tick
// is sufficient. As deployment volume grows, split each duty into its
// own queue-driven job.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := openPool(ctx)
	if err != nil {
		slog.Error("db open", "err", err)
		os.Exit(2)
	}
	defer pool.Close()

	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()

	slog.Info("worker started")
	// Run once immediately on startup, then on every tick.
	runOnce(ctx, pool)

	for {
		select {
		case <-ctx.Done():
			slog.Info("worker exiting")
			return
		case <-tick.C:
			runOnce(ctx, pool)
		}
	}
}

func runOnce(ctx context.Context, pool *pgxpool.Pool) {
	if n, err := reapRedeemAttempts(ctx, pool, 24*time.Hour); err != nil {
		slog.Error("reap redeem_attempts", "err", err)
	} else if n > 0 {
		slog.Info("reaped redeem_attempts", "count", n)
	}
	if n, err := lockExpiredCodes(ctx, pool); err != nil {
		slog.Error("lock expired codes", "err", err)
	} else if n > 0 {
		slog.Info("locked expired codes", "count", n)
	}
	if n, err := reapOneShotTokens(ctx, pool, 24*time.Hour); err != nil {
		slog.Error("reap one_shot_tokens", "err", err)
	} else if n > 0 {
		slog.Info("reaped one_shot_tokens", "count", n)
	}
	if n, err := reapWakeRequests(ctx, pool, 7*24*time.Hour); err != nil {
		slog.Error("reap wake_requests", "err", err)
	} else if n > 0 {
		slog.Info("reaped wake_requests", "count", n)
	}
	if url := os.Getenv("NOTIFY_WEBHOOK_URL"); url != "" {
		if n, err := notifyTerminalJobs(ctx, pool, url); err != nil {
			slog.Error("notify terminal jobs", "err", err)
		} else if n > 0 {
			slog.Info("job notifications sent", "count", n)
		}
	}
}

// notifyTerminalJobs claims newly-terminal jobs (FOR UPDATE SKIP LOCKED,
// safe with multiple workers) and POSTs a webhook per job. Rows are
// marked notified BEFORE the POST — at-most-once semantics: a failed
// delivery is logged and lost rather than retried forever; the UI/SSE
// remains the authoritative record. The 1-hour window means enabling
// NOTIFY_WEBHOOK_URL doesn't replay history. The payload's `text` field
// makes the same body Slack-incoming-webhook compatible.
func notifyTerminalJobs(ctx context.Context, pool *pgxpool.Pool, webhookURL string) (int64, error) {
	rows, err := pool.Query(ctx, `
		UPDATE deployment_jobs j
		SET notified_at = now()
		FROM (
			SELECT dj.id FROM deployment_jobs dj
			WHERE dj.state IN ('completed','failed','cancelled')
			  AND dj.notified_at IS NULL
			  AND dj.finished_at > now() - interval '1 hour'
			ORDER BY dj.finished_at
			LIMIT 20
			FOR UPDATE SKIP LOCKED
		) due
		WHERE j.id = due.id
		RETURNING j.id, j.machine_id,
		          (SELECT asset_tag FROM machines m WHERE m.id = j.machine_id),
		          j.kind, j.state::text, j.finished_at`)
	if err != nil {
		return 0, err
	}
	type notif struct {
		jobID, machineID           string
		assetTag                   *string
		kind, state                string
		finishedAt                 time.Time
	}
	var due []notif
	for rows.Next() {
		var n notif
		if err := rows.Scan(&n.jobID, &n.machineID, &n.assetTag, &n.kind, &n.state, &n.finishedAt); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	var sent int64
	for _, n := range due {
		machine := n.machineID
		if n.assetTag != nil && *n.assetTag != "" {
			machine = *n.assetTag
		}
		payload, _ := json.Marshal(map[string]any{
			"event":      "job.terminal",
			"job_id":     n.jobID,
			"machine_id": n.machineID,
			"kind":       n.kind,
			"state":      n.state,
			"at":         n.finishedAt.UTC().Format(time.RFC3339),
			"text": fmt.Sprintf("%s job %.8s for machine %s %s",
				n.kind, n.jobID, machine, n.state),
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
		if err != nil {
			slog.Error("notify build request", "err", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if tok := os.Getenv("NOTIFY_WEBHOOK_TOKEN"); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := client.Do(req)
		if err != nil {
			slog.Error("notify post", "job", n.jobID, "err", err)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			slog.Error("notify post status", "job", n.jobID, "status", resp.StatusCode)
			continue
		}
		sent++
	}
	return sent, nil
}

// reapWakeRequests deletes claimed wake requests older than `older`.
func reapWakeRequests(ctx context.Context, pool *pgxpool.Pool, older time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx, `
		DELETE FROM wake_requests
		WHERE claimed_at IS NOT NULL AND claimed_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(older.Seconds())))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// reapOneShotTokens deletes boot/phone-home tokens that were consumed or
// expired more than `older` ago, so the table doesn't grow unbounded.
func reapOneShotTokens(ctx context.Context, pool *pgxpool.Pool, older time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx, `
		DELETE FROM one_shot_tokens
		WHERE (consumed_at IS NOT NULL AND consumed_at < now() - $1::interval)
		   OR (expires_at < now() - $1::interval)`,
		fmt.Sprintf("%d seconds", int(older.Seconds())))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func reapRedeemAttempts(ctx context.Context, pool *pgxpool.Pool, older time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx, `
		DELETE FROM redeem_attempts WHERE at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(older.Seconds())))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func lockExpiredCodes(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE auth_codes SET locked_at = now()
		WHERE redeemed_at IS NULL AND locked_at IS NULL
		  AND expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func openPool(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		os.Getenv("POSTGRES_USER"),
		os.Getenv("POSTGRES_PASSWORD"),
		envOr("POSTGRES_HOST", "postgres"),
		envOr("POSTGRES_PORT", "5432"),
		os.Getenv("POSTGRES_DB"),
	)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
