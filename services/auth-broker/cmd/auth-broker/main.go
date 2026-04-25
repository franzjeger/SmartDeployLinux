// auth-broker is the only unauthenticated, internet-reachable service in
// deployserver. It mediates between a freshly-booted USB stick (which has
// no credentials beyond a pinned TLS root CA and a typed code) and the
// Headscale/Tailscale control plane.
//
// Threat-critical surface. See docs/SECURITY.md.

package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/your-org/deployserver/auth-broker/internal/auditlog"
	"github.com/your-org/deployserver/auth-broker/internal/broker"
	"github.com/your-org/deployserver/auth-broker/internal/config"
	"github.com/your-org/deployserver/auth-broker/internal/db"
	"github.com/your-org/deployserver/auth-broker/internal/tsclient"
)

var version = "dev"

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT, syscall.SIGTERM,
	)
	defer cancel()

	store, err := db.Open(ctx, cfg.PostgresDSN())
	if err != nil {
		slog.Error("db open", "err", err)
		os.Exit(2)
	}
	defer store.Close()

	tsc, err := tsclient.New(cfg)
	if err != nil {
		slog.Error("tsclient", "err", err)
		os.Exit(2)
	}

	auditLogger, auditCloser, err := auditlog.Open(cfg.AuditFile)
	if err != nil {
		slog.Error("audit log open", "err", err, "path", cfg.AuditFile)
		os.Exit(2)
	}
	defer auditCloser.Close()
	if cfg.AuditFile != "" {
		slog.Info("audit log file mirror enabled", "path", cfg.AuditFile)
	}

	b := broker.New(cfg, store, tsc, auditLogger)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           b.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.Info("auth-broker listening", "addr", cfg.Listen, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(shutdownCtx)
}
