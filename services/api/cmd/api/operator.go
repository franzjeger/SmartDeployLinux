// Operator-facing endpoints used by deployctl: deployments/issue,
// bootstrap-sticks register/list, audit query with filters.
//
// All require OIDC auth via the chi router's /api/v1 group.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/store"
)

// --- POST /api/v1/deployments/issue -----------------------------------
//
// Operator issues a deployment code for a machine. We do not implement
// the issuance ourselves: we proxy to the auth-broker (which holds the
// rate-limit logic, code generator, and writes the auth_codes row) and
// inject `issued_by` from the verified OIDC principal so the operator
// can't claim someone else's identity.

type issueReq struct {
	MachineID   string `json:"machine_id"`
	ProfileID   string `json:"profile_id"`
	TTLSeconds  int    `json:"ttl_seconds,omitempty"`
	IssuedFor   string `json:"issued_for,omitempty"`
	BindingCIDR string `json:"binding_cidr,omitempty"`
}

func (h *handlers) issueDeployment(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "deployment.create") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	uid := auth.UserID(r.Context())
	if uid == uuid.Nil {
		// Dev mode (no OIDC): fall back to the first seeded admin so the
		// UI works without an IdP. The operator running `api seed-admin
		// <email>` chose this account; using it here is the documented
		// dev-mode behaviour.
		admin, err := h.store.FirstAdminUserID(r.Context())
		if err != nil {
			http.Error(w, "no admin user; run `api seed-admin <email>` first", http.StatusUnauthorized)
			return
		}
		uid = admin
	}

	var req issueReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.MachineID == "" || req.ProfileID == "" {
		http.Error(w, "machine_id and profile_id required", http.StatusBadRequest)
		return
	}

	// Forward to broker. Inject issued_by server-side.
	body := map[string]any{
		"machine_id":   req.MachineID,
		"profile_id":   req.ProfileID,
		"ttl_seconds":  req.TTLSeconds,
		"issued_for":   req.IssuedFor,
		"binding_cidr": req.BindingCIDR,
		"issued_by":    uid.String(),
	}
	bs, _ := json.Marshal(body)

	brokerURL := getenv("DEPLOY_AUTH_BROKER_URL", "http://auth-broker:8081")
	resp, err := http.Post(brokerURL+"/api/v1/bootstrap/issue-code",
		"application/json", bytes.NewReader(bs))
	if err != nil {
		http.Error(w, "broker unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Pass status + body through unchanged.
	for k, vs := range resp.Header {
		if k == "Content-Length" {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// --- POST /api/v1/bootstrap-sticks (register) -------------------------

type registerStickReq struct {
	ImageSHA256   string  `json:"image_sha256"`
	Tailnet       string  `json:"tailnet"`
	DeployURL     string  `json:"deploy_url"`
	CAFingerprint string  `json:"ca_fingerprint"`
	Label         *string `json:"label,omitempty"`
}

func (h *handlers) registerStick(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "stick.write") && !auth.HasPerm(r.Context(), "*") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	uid := auth.UserID(r.Context())
	if uid == uuid.Nil {
		http.Error(w, "no authenticated principal", http.StatusUnauthorized)
		return
	}
	var req registerStickReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	stick, err := h.store.CreateBootstrapStick(r.Context(), store.CreateStickInput{
		ImageSHA256:   req.ImageSHA256,
		Tailnet:       req.Tailnet,
		DeployURL:     req.DeployURL,
		CAFingerprint: req.CAFingerprint,
		BuiltBy:       uid,
		Label:         req.Label,
	})
	if err != nil {
		http.Error(w, "create stick: "+err.Error(), http.StatusBadRequest)
		return
	}
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "bootstrap_stick.registered",
		SubjectID: &stick.ID, SubjectKind: "bootstrap_stick",
	})
	writeJSON(w, http.StatusCreated, stick)
}

// --- GET /api/v1/bootstrap-sticks?ca_fingerprint=... ------------------

func (h *handlers) listSticks(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "stick.read") && !auth.HasPerm(r.Context(), "*") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	caFP := r.URL.Query().Get("ca_fingerprint")
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	sticks, err := h.store.ListBootstrapSticks(r.Context(), caFP, limit)
	if err != nil {
		http.Error(w, "list: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, sticks)
}

// --- GET /api/v1/audit?since=&action=&machine= ------------------------

func (h *handlers) queryAudit(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "audit.read") && !auth.HasPerm(r.Context(), "*") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	f := store.AuditQueryFilters{
		Limit: parseLimit(q.Get("limit"), 200),
	}
	if v := q.Get("since"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			http.Error(w, "bad since (use 24h, 7d, etc.): "+err.Error(), http.StatusBadRequest)
			return
		}
		f.Since = d
	}
	if v := q.Get("action"); v != "" {
		// Convert "auth_code" → SQL LIKE "auth_code%" so prefix matching
		// works without the operator typing the wildcard.
		if !strings.ContainsAny(v, "%_") {
			v = v + "%"
		}
		f.ActionLike = v
	}
	if v := q.Get("machine"); v != "" {
		mid, err := uuid.Parse(v)
		if err != nil {
			http.Error(w, "bad machine uuid: "+err.Error(), http.StatusBadRequest)
			return
		}
		f.MachineID = &mid
	}
	rows, err := h.store.QueryAuditEvents(r.Context(), f)
	if err != nil {
		http.Error(w, "query: "+err.Error(), http.StatusInternalServerError)
		return
	}
	type respRow struct {
		ID          int64           `json:"id"`
		At          time.Time       `json:"at"`
		ActorID     *uuid.UUID      `json:"actor_id"`
		ActorKind   string          `json:"actor_kind"`
		Action      string          `json:"action"`
		SubjectID   *uuid.UUID      `json:"subject_id"`
		SubjectKind *string         `json:"subject_kind"`
		Data        json.RawMessage `json:"data"`
		SourceIP    *string         `json:"source_ip"`
	}
	out := make([]respRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, respRow{
			ID: r.ID, At: r.At, ActorID: r.ActorID, ActorKind: r.ActorKind,
			Action: r.Action, SubjectID: r.SubjectID, SubjectKind: r.SubjectKind,
			Data: r.Data, SourceIP: r.SourceIP,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func parseLimit(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// --- GET /api/v1/profiles --------------------------------------------

func (h *handlers) listProfiles(w http.ResponseWriter, r *http.Request) {
	profs, err := h.store.ListProfiles(r.Context())
	if err != nil {
		http.Error(w, "list profiles: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if profs == nil {
		profs = []store.Profile{}
	}
	writeJSON(w, http.StatusOK, profs)
}

// --- GET /api/v1/me --------------------------------------------------
//
// Returns the calling principal. In dev mode (no OIDC), reports
// dev_mode=true so the UI can show the warning banner.

func (h *handlers) me(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	dev := h.verifier == nil
	out := map[string]any{
		"user_id":  uid,
		"dev_mode": dev,
	}
	writeJSON(w, http.StatusOK, out)
}

// Suppress unused-import errors when handler bodies grow/shrink.
var _ = errors.New
var _ = url.QueryEscape
var _ = fmt.Sprintf
