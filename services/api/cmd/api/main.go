// API service. Implements the operator-facing REST API and the
// internal /internal/render endpoints that http-boot calls.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/your-org/deployserver/api/internal/auditlog"
	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/store"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "serve")
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(getenv("LOG_LEVEL", "info")),
	}))
	slog.SetDefault(logger)

	switch os.Args[1] {
	case "serve":
		serve()
	case "migrate":
		runMigrate(os.Args[2:])
	case "seed-admin":
		seedAdmin()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		os.Exit(2)
	}
}

func openStore(ctx context.Context) *store.Store {
	dsn := postgresDSN()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		slog.Error("db open", "err", err)
		os.Exit(2)
	}
	return st
}

func runMigrate(args []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st := openStore(ctx)
	defer st.Close()

	if len(args) > 0 && args[0] == "up" {
		// fall through
	}
	if err := st.MigrateUp(ctx); err != nil {
		slog.Error("migrate up", "err", err)
		os.Exit(2)
	}
	slog.Info("migrations applied")
}

func seedAdmin() {
	slog.Info("seed-admin: not yet implemented (Phase 8e)")
}

func serve() {
	listen := getenv("API_LISTEN", ":8080")
	publicURL := getenv("API_PUBLIC_URL", "https://deploy.example.com")
	deployFQDN := getenv("DEPLOY_FQDN", "deploy.example.com")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	st := openStore(ctx)
	defer st.Close()

	// Apply migrations on startup. Idempotent.
	if err := st.MigrateUp(ctx); err != nil {
		slog.Error("startup migrations", "err", err)
		os.Exit(2)
	}

	// Audit log fan-out to file (if configured). SECURITY.md §4 #4.
	auditLogger, auditCloser, err := auditlog.Open(os.Getenv("AUDIT_FILE"))
	if err != nil {
		slog.Error("audit log open", "err", err, "path", os.Getenv("AUDIT_FILE"))
		os.Exit(2)
	}
	defer auditCloser.Close()
	st.SetAuditLogger(auditLogger)
	if p := os.Getenv("AUDIT_FILE"); p != "" {
		slog.Info("audit log file mirror enabled", "path", p)
	}

	verifier, err := auth.NewVerifier(ctx,
		os.Getenv("OIDC_ISSUER"),
		os.Getenv("OIDC_CLIENT_ID"),
	)
	if err != nil {
		// OIDC config is optional in dev; log and proceed unauthenticated.
		slog.Warn("OIDC verifier unavailable; public API will be open", "err", err)
	} else {
		auth.SetUserResolver(auth.ResolverFromPool(st.Pool()))
	}

	h := &handlers{
		store:      st,
		publicURL:  publicURL,
		deployFQDN: deployFQDN,
		verifier:   verifier,
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(slogMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	// Internal endpoints called by http-boot. Not exposed externally.
	r.Route("/internal/render", func(r chi.Router) {
		// Token-based: the bootstrap and PXE chainload paths land here.
		// URL contains a single-use, deployment-bound token; SECURITY.md §4 #1.
		r.Get("/by-token/{token}", h.renderByToken)
		r.Get("/by-token/{token}/user-data", h.renderUserDataByToken)
		r.Get("/by-token/{token}/meta-data", h.renderMetaDataByToken)
		// UUID-based: legacy. Will be retired; see STATUS.md.
		r.Get("/machine/{id}", h.renderMachineByID)
		r.Get("/by-mac/{mac}", h.renderMachineByMAC)
		r.Get("/{id}/user-data", h.renderUserData)
		r.Get("/{id}/meta-data", h.renderMetaData)
	})

	// Phone-home from installers. Auth: one-shot bearer token bound to
	// a deployment_jobs row.
	r.Route("/v1/jobs/{id}/events", func(r chi.Router) {
		r.Post("/", h.appendDeploymentEvent)
	})

	// WinPE per-job endpoints. Auth: Bearer token; state-gated to
	// imaging/bootstrapped via Store.VerifyOneShotTokenForJob.
	// SECURITY.md §4 #8.
	r.Route("/v1/jobs/{id}", func(r chi.Router) {
		r.Get("/deploy.cmd", h.winpeDeployCmd)
		r.Post("/plan", h.winpePlan)
		r.Get("/image.wim", h.winpeImage)
		r.Get("/drivers.zip", h.winpeDrivers)
		r.Get("/unattend.xml", h.winpeUnattend)
	})

	// Public, OIDC-authenticated endpoints.
	r.Route("/api/v1", func(r chi.Router) {
		if h.verifier != nil {
			r.Use(auth.Middleware(h.verifier))
		}
		r.Get("/machines", h.listMachines)
		r.Post("/machines", h.createMachine)
		r.Get("/machines/{id}", h.getMachine)
		r.Get("/audit", h.listAudit)
	})

	srv := &http.Server{
		Addr:              listen,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.Info("api listening", "addr", listen, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(shutdown)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func postgresDSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		os.Getenv("POSTGRES_USER"),
		os.Getenv("POSTGRES_PASSWORD"),
		getenv("POSTGRES_HOST", "postgres"),
		getenv("POSTGRES_PORT", "5432"),
		os.Getenv("POSTGRES_DB"),
	)
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func slogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)
		slog.InfoContext(r.Context(), "http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
