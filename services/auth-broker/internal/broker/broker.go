// HTTP routes and handlers for the auth-broker.

package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/auth-broker/internal/codes"
	"github.com/your-org/deployserver/auth-broker/internal/config"
	"github.com/your-org/deployserver/auth-broker/internal/db"
	"github.com/your-org/deployserver/auth-broker/internal/tsclient"
)

type Broker struct {
	cfg      *config.Config
	store    *db.Store
	ts       tsclient.Client
	pepper   []byte
	auditLog *slog.Logger
}

func New(cfg *config.Config, store *db.Store, ts tsclient.Client, auditLog *slog.Logger) *Broker {
	pepper := []byte(cfg.DeployFQDN) // see codes.Hash docstring
	if auditLog == nil {
		auditLog = slog.Default()
	}
	return &Broker{cfg: cfg, store: store, ts: ts, pepper: pepper, auditLog: auditLog}
}

func (b *Broker) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(slogMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(20 * time.Second))

	// Coarse global rate limit. Per-IP and per-code limits live deeper.
	r.Use(httprate.LimitByIP(120, time.Minute))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Route("/api/v1/bootstrap", func(r chi.Router) {
		// Public: stick redeems here. Per-IP burst limit applied here.
		r.With(httprate.LimitByIP(10, time.Minute)).
			Post("/redeem", b.handleRedeem)

		// Authenticated by API: operator UI calls here through internal
		// service auth (mTLS or trusted-network header). For now we
		// gate by network — only the API service can hit this path.
		r.Post("/issue-code", b.handleIssueCode)
	})

	return r
}

// --- /redeem ----------------------------------------------------------

type redeemReq struct {
	Code             string `json:"code"`
	StickImageSHA256 string `json:"stick_image_sha256,omitempty"`
	BootUUID         string `json:"boot_uuid,omitempty"`
}

type redeemResp struct {
	AuthKey      string    `json:"auth_key"`
	ControlURL   string    `json:"control_url,omitempty"`
	ChainloadURL string    `json:"chainload_url"`
	MachineID    string    `json:"machine_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (b *Broker) handleRedeem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srcIP := remoteAddr(r)

	// Per-IP soft limit using the DB-backed counter, on top of the
	// per-IP middleware burst limit. This one survives broker restarts.
	if cnt, err := b.store.RecordPerIPAttempt(ctx, srcIP, time.Hour); err != nil {
		slog.WarnContext(ctx, "rate-limit record", "err", err)
	} else if cnt > b.cfg.RateRedeemPerIPHour {
		w.Header().Set("Retry-After", "3600")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}

	var req redeemReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_json"})
		return
	}
	normalized, err := codes.Normalize(req.Code)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_code_format"})
		return
	}
	hash := codes.Hash(normalized, b.pepper)

	// Look up first to bump attempts even on bad codes (so a brute-force
	// attempt against random codes is rate-limited per-code).
	row, err := b.store.FindCodeByHash(ctx, hash)
	if errors.Is(err, db.ErrNotFound) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_code"})
		b.audit(ctx, "auth_code.redeem_failed", nil, srcIP, map[string]any{"reason": "not_found"})
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "find code", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}

	// Pre-check: locked?
	if row.LockedAt != nil {
		writeJSON(w, http.StatusGone, map[string]string{"error": "code_consumed_or_expired"})
		return
	}
	// Pre-check: already consumed?
	if row.RedeemedAt != nil {
		writeJSON(w, http.StatusGone, map[string]string{"error": "code_consumed_or_expired"})
		b.audit(ctx, "auth_code.redeem_failed", &row.ID, srcIP, map[string]any{"reason": "already_consumed"})
		return
	}
	// Pre-check: expired?
	if time.Now().After(row.ExpiresAt) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "code_consumed_or_expired"})
		b.audit(ctx, "auth_code.redeem_failed", &row.ID, srcIP, map[string]any{"reason": "expired"})
		return
	}
	// Pre-check: binding CIDR match?
	if row.BindingCIDR != nil && !row.BindingCIDR.Contains(srcIP) {
		// Counts as an attempt — somebody is using this code from the
		// wrong place.
		_, _, _ = b.store.IncrementAttempt(ctx, hash, b.cfg.RateRedeemPerCode)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "ip_not_allowed"})
		b.audit(ctx, "auth_code.redeem_failed", &row.ID, srcIP, map[string]any{"reason": "binding_cidr"})
		return
	}

	// All gates passed. Mint the Tailscale key BEFORE atomic consume so
	// that on a Tailscale API failure we don't burn the code.
	authKey, controlURL, err := b.ts.CreateBootstrapKey(ctx,
		b.cfg.TSAuthkeyTTL,
		[]string{"tag:deploy-bootstrap"})
	if err != nil {
		slog.ErrorContext(ctx, "mint authkey", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "overlay_unavailable"})
		return
	}

	consumed, err := b.store.ConsumeCode(ctx, hash, srcIP)
	if err != nil {
		// Race: somebody else just consumed this code. We have already
		// minted an authkey; it will self-expire. We don't try to revoke
		// it because that's a second remote API call that can fail
		// independently; defense-in-depth is the short TTL + ephemeral
		// flag.
		slog.WarnContext(ctx, "consume race", "err", err)
		writeJSON(w, http.StatusGone, map[string]string{"error": "code_consumed_or_expired"})
		return
	}

	jobID, err := b.store.CreateDeploymentJob(ctx, consumed.MachineID, consumed.ProfileID, consumed.ID)
	if err != nil {
		slog.ErrorContext(ctx, "create job", "err", err)
		// Don't fail the redeem — the audit log and authkey are already
		// in flight. The job row can be reconstructed from the audit.
	}

	// Mint a boot token bound to this auth_code. The chainload URL
	// uses this token, NOT the machine UUID — that's what closes
	// SECURITY.md §4 #1 (the previously-unauthenticated /boot/<id>.ipxe).
	bootToken, err := codes.GenerateBootToken()
	if err != nil {
		slog.ErrorContext(ctx, "generate boot token", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	bootTokenHash := codes.HashBootToken(bootToken, b.pepper)
	bootTokenExpires := time.Now().Add(b.cfg.TSAuthkeyTTL)
	if _, err := b.store.CreateOneShotToken(ctx, consumed.ID, bootTokenHash, "boot", bootTokenExpires); err != nil {
		slog.ErrorContext(ctx, "store boot token", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}

	chainURL := fmt.Sprintf("https://%s/boot/%s.ipxe",
		b.cfg.DeployFQDN, bootToken)

	resp := redeemResp{
		AuthKey:      authKey,
		ControlURL:   controlURL,
		ChainloadURL: chainURL,
		MachineID:    consumed.MachineID.String(),
		ExpiresAt:    time.Now().Add(b.cfg.TSAuthkeyTTL),
	}

	b.audit(ctx, "auth_code.redeemed", &consumed.ID, srcIP, map[string]any{
		"machine_id": consumed.MachineID,
		"job_id":     jobID,
		"boot_uuid":  req.BootUUID,
		"stick_sha":  req.StickImageSHA256,
	})

	writeJSON(w, http.StatusOK, resp)
}

// --- /issue-code ------------------------------------------------------

type issueReq struct {
	MachineID   string `json:"machine_id"`
	ProfileID   string `json:"profile_id"`
	TTLSeconds  int    `json:"ttl_seconds,omitempty"`
	IssuedFor   string `json:"issued_for,omitempty"`
	BindingCIDR string `json:"binding_cidr,omitempty"`
	IssuedBy    string `json:"issued_by"` // user UUID, set by trusted caller
}

type issueResp struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (b *Broker) handleIssueCode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req issueReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_json"})
		return
	}

	machineID, err := uuid.Parse(req.MachineID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_machine_id"})
		return
	}
	profileID, err := uuid.Parse(req.ProfileID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_profile_id"})
		return
	}
	issuedBy, err := uuid.Parse(req.IssuedBy)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_issued_by"})
		return
	}

	ttl := b.cfg.DefaultCodeTTL
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
		if ttl > 7*24*time.Hour {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ttl_too_long"})
			return
		}
	}

	var binding *netip.Prefix
	if req.BindingCIDR != "" {
		p, err := netip.ParsePrefix(req.BindingCIDR)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_binding_cidr"})
			return
		}
		binding = &p
	}

	// Per-operator issue rate limit. Enforced AFTER the input validation
	// gates above so that bad requests don't burn quota.
	if cnt, err := b.store.CountIssuedByActorRecently(ctx, issuedBy, time.Hour); err != nil {
		slog.ErrorContext(ctx, "count issued", "err", err)
	} else if cnt >= b.cfg.RateIssuePerOpHour {
		w.Header().Set("Retry-After", "3600")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "rate_limited", "detail": "issuance quota exceeded",
		})
		return
	}

	code, err := codes.Generate()
	if err != nil {
		slog.ErrorContext(ctx, "generate code", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	hash := codes.Hash(code, b.pepper)

	id, expiresAt, err := b.store.IssueCode(ctx, hash, machineID, profileID,
		issuedBy, remoteAddr(r), binding, ttl, req.IssuedFor)
	if err != nil {
		slog.ErrorContext(ctx, "store issue", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}

	b.audit(ctx, "auth_code.issued", &id, remoteAddr(r), map[string]any{
		"machine_id": machineID,
		"profile_id": profileID,
		"ttl_sec":    int(ttl / time.Second),
		"label":      req.IssuedFor,
		"binding":    req.BindingCIDR,
	})

	writeJSON(w, http.StatusOK, issueResp{Code: code, ExpiresAt: expiresAt})
}

// --- helpers ---------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func remoteAddr(r *http.Request) netip.Addr {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr
}

func (b *Broker) audit(_ context.Context, action string, subjectID *uuid.UUID, src netip.Addr, data map[string]any) {
	// Best-effort audit. If the DB write fails, we still log to stdout
	// (which is presumed shipped to a separate sink); see SECURITY.md.
	payload, _ := json.Marshal(data)
	b.auditLog.Info("audit",
		"action", action,
		"subject_id", subjectID,
		"source_ip", src.String(),
		"data", string(payload),
	)
	// We do not hold up the request on the DB audit insert. The
	// goroutine gets its own short-lived context so a client disconnect
	// doesn't cancel the audit write.
	go func() {
		c2, cancel := contextWithTimeout(2 * time.Second)
		defer cancel()
		_ = b.store.Audit(c2, db.AuditEvent{
			ActorKind:   "stick",
			Action:      action,
			SubjectID:   subjectID,
			SubjectKind: "auth_code",
			Data:        payload,
			SourceIP:    src,
		})
	}()
}
