package broker

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"time"
)

// requireInternalSecret gates internal-only endpoints (issue-code) on
// the shared secret configured via AUTH_BROKER_ISSUE_SHARED_SECRET.
// Constant-time comparison; 404 (not 401/403) on mismatch so probes
// can't distinguish "wrong secret" from "no such route". When no secret
// is configured we allow the call but log a warning once per request —
// the dev-mode escape hatch, consistent with the API's OIDC fallback.
func (b *Broker) requireInternalSecret(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := b.cfg.IssueSharedSecret
		if secret == "" {
			slog.WarnContext(r.Context(),
				"AUTH_BROKER_ISSUE_SHARED_SECRET not set; /issue-code open to internal network (dev-mode)")
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("X-Internal-Auth")
		if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
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

// contextWithTimeout is a tiny helper because the audit goroutine is
// detached from the request context (we don't want a client disconnect
// to cancel the audit insert).
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
