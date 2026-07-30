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
	"github.com/your-org/deployserver/api/internal/eventbus"
	"github.com/your-org/deployserver/api/internal/metrics"
	"github.com/your-org/deployserver/api/internal/mtls"
	"github.com/your-org/deployserver/api/internal/store"
	"github.com/your-org/deployserver/api/internal/ui"
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
	// Usage: api seed-admin <email>
	// First-run helper. Idempotent: rerunning with the same email is a
	// no-op. Once the admin has logged in via OIDC at least once, the
	// row's oidc_subject column is populated automatically by the
	// resolver and from then on the admin gets full RBAC perms.
	args := os.Args[2:]
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: api seed-admin <email>")
		os.Exit(2)
	}
	email := args[0]

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st := openStore(ctx)
	defer st.Close()
	if err := st.MigrateUp(ctx); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(2)
	}
	id, err := st.SeedAdmin(ctx, email)
	if err != nil {
		slog.Error("seed admin", "err", err)
		os.Exit(2)
	}
	fmt.Printf("admin user: %s (%s)\n", email, id)
	fmt.Println("On first OIDC login from this email, the user will be linked to its oidc_subject and gain admin perms.")
}

func serve() {
	listen := getenv("API_LISTEN", ":8080")
	internalListen := getenv("API_INTERNAL_LISTEN", ":8443")
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

	reg := metrics.NewRegistry()
	h := &handlers{
		store:      st,
		publicURL:  publicURL,
		deployFQDN: deployFQDN,
		verifier:   verifier,
		bus:        eventbus.New(),
		metrics:    reg,
	}

	// --- public router: phone-home + operator API + healthz -----------
	pub := chi.NewRouter()
	pub.Use(middleware.RequestID)
	pub.Use(middleware.RealIP)
	pub.Use(slogMiddleware)
	pub.Use(middleware.Recoverer)
	// NOTE: middleware.Timeout removed at the router level because it
	// is incompatible with SSE long-lived connections. Each non-SSE
	// route group below applies its own request timeout. See the
	// withTimeout helper.

	pub.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	// Readiness: healthz says the process is up; readyz says it can
	// actually serve (DB reachable). Compose/K8s should gate on readyz.
	pub.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := st.Pool().Ping(ctx); err != nil {
			http.Error(w, "db unreachable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	// Prometheus metrics. Not exposed through Caddy's public routes;
	// scrape it over the internal network.
	pub.Handle("/metrics", reg.Handler())

	// Operator UI — embedded HTML+CSS+JS. Single-page app at /, with
	// static assets under /assets/. ui.Handler() handles both.
	uiHandler := ui.Handler()
	pub.Handle("/", uiHandler)
	pub.Handle("/assets/*", uiHandler)

	// Direct iPXE menu for the catalog — no auth, no DB lookup, just a
	// generated iPXE script listing every catalog entry. Anyone with
	// network access to the api can `chain` to this URL and get a
	// netboot.xyz-style picker.
	pub.Get("/catalog/menu.ipxe", h.catalogMenuIPXE)

	// Pre-auth: the SPA fetches issuer/client_id to run the OIDC
	// code+PKCE flow. Public values only.
	pub.Get("/api/v1/auth/config", h.authConfig)

	// Edge-agent wake queue. Shared-secret bearer (EDGE_WAKE_TOKEN);
	// fails closed when unconfigured.
	pub.With(middleware.Timeout(15 * time.Second)).
		Get("/v1/edge/wake-queue", h.edgeWakeQueue)

	// Phone-home from in-OS installers (cross-WAN; bearer-token auth).
	pub.Route("/v1/jobs/{id}/events", func(r chi.Router) {
		r.Use(middleware.Timeout(30 * time.Second))
		r.Use(reg.Middleware("phone_home"))
		r.Post("/", h.appendDeploymentEvent)
	})

	// SSE stream of events for a job. NO request timeout — the connection
	// stays open as long as the client is reading. The handler manages
	// idle disconnects and ping keepalives internally.
	pub.Route("/api/v1/jobs/{id}/events/stream", func(r chi.Router) {
		if h.verifier != nil {
			// EventSource cannot set an Authorization header; the SPA
			// mirrors its ID token into a SameSite=Strict cookie. The
			// fallback is scoped to this read-only GET route so the
			// rest of the API stays header-only (no CSRF surface).
			r.Use(cookieBearerFallback)
			r.Use(auth.Middleware(h.verifier))
		}
		r.Get("/", h.streamJobEvents)
	})

	// WinPE per-job endpoints. Bearer-token + state-gated.
	pub.Route("/v1/jobs/{id}", func(r chi.Router) {
		r.Use(middleware.Timeout(30 * time.Second))
		r.Use(reg.Middleware("winpe"))
		r.Get("/deploy.cmd", h.winpeDeployCmd)
		r.Post("/plan", h.winpePlan)
		r.Get("/image.wim", h.winpeImage)
		r.Get("/drivers.zip", h.winpeDrivers)
		r.Get("/unattend.xml", h.winpeUnattend)
		r.Get("/capture.sh", h.captureScript)
		r.Post("/capture-upload", h.captureUpload)
		r.Post("/capture-complete", h.captureComplete)
	})

	// Operator API (OIDC-authenticated).
	pub.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.Timeout(30 * time.Second))
		r.Use(reg.Middleware("operator_api"))
		if h.verifier != nil {
			r.Use(auth.Middleware(h.verifier))
		}
		r.Get("/me", h.me)
		r.Get("/machines", h.listMachines)
		r.Post("/machines", h.createMachine)
		r.Get("/machines/{id}", h.getMachine)
		r.Patch("/machines/{id}", h.updateMachine)
		r.Delete("/machines/{id}", h.deleteMachine)
		r.Post("/machines/{id}/wake", h.wakeMachine)
		r.Get("/machines/{id}/wake", h.listWakes)
		r.Get("/profiles", h.listProfiles)
		r.Post("/profiles", h.createProfile)
		r.Get("/profiles/{id}", h.getProfile)
		r.Patch("/profiles/{id}", h.updateProfile)
		r.Delete("/profiles/{id}", h.deleteProfile)
		r.Put("/profiles/{id}/templates", h.upsertProfileTemplate)
		r.Delete("/profiles/{id}/templates/{kind}", h.deleteProfileTemplate)
		r.Get("/images", h.listImages)
		r.Post("/images", h.createImage)
		r.Get("/images/{id}", h.getImage)
		r.Patch("/images/{id}", h.updateImage)
		r.Delete("/images/{id}", h.deleteImage)
		r.Post("/blobs", h.createBlob)
		r.Get("/driver-packs", h.listDriverPacks)
		r.Post("/driver-packs", h.createDriverPack)
		r.Delete("/driver-packs/versions/{id}", h.deleteDriverPackVersion)
		r.Post("/images/{id}/versions", h.createImageVersion)
		r.Get("/images/{id}/versions", h.listImageVersions)
		r.Get("/catalog", h.listCatalog)
		r.Post("/catalog/install", h.installFromCatalog)
		r.Get("/jobs", h.listJobs)
		r.Get("/jobs/{id}", h.getJob)
		r.Post("/jobs/{id}/cancel", h.cancelJob)
		r.Get("/audit", h.queryAudit)
		r.Post("/deployments/issue", h.issueDeployment)
		r.Post("/bootstrap-sticks", h.registerStick)
		r.Get("/bootstrap-sticks", h.listSticks)
		r.Get("/bootstrap-sticks/config", h.stickConfig)
	})

	// --- internal router: render endpoints, called by http-boot only --
	intr := chi.NewRouter()
	intr.Use(middleware.RequestID)
	intr.Use(middleware.RealIP)
	intr.Use(slogMiddleware)
	intr.Use(middleware.Recoverer)
	intr.Use(middleware.Timeout(30 * time.Second))

	// Routes here are relative to /internal — the router is mounted at
	// /internal on whichever listener serves it (mTLS listener or, in
	// dev mode, the public one). Registering absolute /internal/...
	// paths here would double the prefix when mounted.
	intr.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	intr.Route("/render", func(r chi.Router) {
		r.Get("/by-token/{token}", h.renderByToken)
		r.Get("/by-token/{token}/user-data", h.renderUserDataByToken)
		r.Get("/by-token/{token}/meta-data", h.renderMetaDataByToken)
		r.Get("/machine/{id}", h.renderMachineByID)
		r.Get("/by-mac/{mac}", h.renderMachineByMAC)
		r.Get("/{id}/user-data", h.renderUserData)
		r.Get("/{id}/meta-data", h.renderMetaData)
	})

	srv := &http.Server{
		Addr:              listen,
		Handler:           pub,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Internal listener with mTLS, if cert paths are configured. If not
	// configured, fall back to mounting /internal/* on the public listener
	// in plaintext — the dev-mode escape hatch matching the OIDC fallback.
	// SECURITY.md §4 #3.
	var internalSrv *http.Server
	caPath := getenv("INTERNAL_CA_CERT_PATH", "/secrets/internal-ca.pem")
	certPath := getenv("INTERNAL_TLS_CERT", "/secrets/api.pem")
	keyPath := getenv("INTERNAL_TLS_KEY", "/secrets/api-key.pem")
	if _, err := os.Stat(certPath); err == nil {
		bundle, err := mtls.Load(caPath, certPath, keyPath)
		if err != nil {
			slog.Error("mtls load", "err", err)
			os.Exit(2)
		}
		internalRoot := chi.NewRouter()
		internalRoot.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})
		internalRoot.Mount("/internal", intr)
		internalSrv = &http.Server{
			Addr:              internalListen,
			Handler:           internalRoot,
			TLSConfig:         bundle.ServerConfig(),
			ReadHeaderTimeout: 5 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		slog.Info("internal mTLS listener configured", "addr", internalListen)
	} else {
		pub.Mount("/internal", intr)
		slog.Warn("INTERNAL_TLS_CERT not present; /internal/* served plaintext on public listener (dev-mode)")
	}

	go func() {
		slog.Info("api public listening", "addr", listen, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen public", "err", err)
			cancel()
		}
	}()
	if internalSrv != nil {
		go func() {
			slog.Info("api internal listening (mTLS)", "addr", internalListen)
			if err := internalSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("listen internal", "err", err)
				cancel()
			}
		}()
	}

	<-ctx.Done()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(shutdown)
	if internalSrv != nil {
		_ = internalSrv.Shutdown(shutdown)
	}
}

// cookieBearerFallback promotes the SPA's session cookie to an
// Authorization header when none is present. Used only on the SSE route.
func cookieBearerFallback(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if c, err := r.Cookie("deploy_session"); err == nil && c.Value != "" {
				r.Header.Set("Authorization", "Bearer "+c.Value)
			}
		}
		next.ServeHTTP(w, r)
	})
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

// Flush forwards to the wrapped writer if it implements http.Flusher.
// Without this, embedding http.ResponseWriter here hides the Flusher
// interface from `w.(http.Flusher)` assertions in downstream handlers
// (notably the SSE stream handler).
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
