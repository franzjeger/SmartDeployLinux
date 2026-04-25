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
	"context"
	"fmt"
	"log/slog"
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
