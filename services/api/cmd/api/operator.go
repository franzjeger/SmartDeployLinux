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

	"github.com/go-chi/chi/v5"
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

// --- profiles CRUD ---------------------------------------------------

type createProfileReq struct {
	Name           string                 `json:"name"`
	ImageID        string                 `json:"image_id"`
	AnswerFileVars map[string]interface{} `json:"answer_file_vars"`
}

func (h *handlers) createProfile(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "*") && !auth.HasPerm(r.Context(), "image.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req createProfileReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	imgID, err := uuid.Parse(req.ImageID)
	if err != nil {
		http.Error(w, "bad image_id", http.StatusBadRequest)
		return
	}
	in := store.CreateProfileInput{Name: req.Name, ImageID: imgID}
	if req.AnswerFileVars != nil {
		in.AnswerFileVars, _ = json.Marshal(req.AnswerFileVars)
	}
	id, err := h.store.CreateProfile(r.Context(), in)
	if err != nil {
		http.Error(w, "create: "+err.Error(), http.StatusBadRequest)
		return
	}
	uid := auth.UserID(r.Context())
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "profile.created",
		SubjectID: &id, SubjectKind: "deployment_profile",
	})
	prof, _ := h.store.GetProfile(r.Context(), id)
	writeJSON(w, http.StatusCreated, prof)
}

type updateProfileReq struct {
	Name           *string                 `json:"name"`
	ImageID        *string                 `json:"image_id"`
	AnswerFileVars *map[string]interface{} `json:"answer_file_vars"`
}

func (h *handlers) updateProfile(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "*") && !auth.HasPerm(r.Context(), "image.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var req updateProfileReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	in := store.UpdateProfileInput{Name: req.Name}
	if req.ImageID != nil {
		imgID, err := uuid.Parse(*req.ImageID)
		if err != nil {
			http.Error(w, "bad image_id", http.StatusBadRequest)
			return
		}
		in.ImageID = &imgID
	}
	if req.AnswerFileVars != nil {
		b, _ := json.Marshal(*req.AnswerFileVars)
		in.AnswerFileVars = b
	}
	if err := h.store.UpdateProfile(r.Context(), id, in); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "update: "+err.Error(), http.StatusBadRequest)
		return
	}
	uid := auth.UserID(r.Context())
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "profile.updated",
		SubjectID: &id, SubjectKind: "deployment_profile",
	})
	prof, _ := h.store.GetProfile(r.Context(), id)
	writeJSON(w, http.StatusOK, prof)
}

func (h *handlers) deleteProfile(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "*") && !auth.HasPerm(r.Context(), "image.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := h.store.DeleteProfile(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	uid := auth.UserID(r.Context())
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "profile.deleted",
		SubjectID: &id, SubjectKind: "deployment_profile",
	})
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/profiles/{id} returns the profile + vars + templates list.
func (h *handlers) getProfile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	prof, err := h.store.GetProfile(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	rawVars, _ := h.store.GetProfileVars(r.Context(), id)
	templates, _ := h.store.ListProfileTemplates(r.Context(), id)
	if templates == nil {
		templates = []store.AnswerFileTemplate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"profile":           prof,
		"answer_file_vars":  json.RawMessage(rawVars),
		"templates":         templates,
	})
}

type upsertTemplateReq struct {
	Kind string `json:"kind"`
	Body string `json:"body"`
}

func (h *handlers) upsertProfileTemplate(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "*") && !auth.HasPerm(r.Context(), "image.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var req upsertTemplateReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	tid, err := h.store.UpsertProfileTemplate(r.Context(), pid, req.Kind, req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	uid := auth.UserID(r.Context())
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "profile.template_upserted",
		SubjectID: &pid, SubjectKind: "deployment_profile",
		Data: []byte(fmt.Sprintf(`{"kind":"%s","template_id":"%s"}`, req.Kind, tid)),
	})
	writeJSON(w, http.StatusOK, map[string]any{"id": tid, "kind": req.Kind})
}

func (h *handlers) deleteProfileTemplate(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "*") && !auth.HasPerm(r.Context(), "image.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	pid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	kind := chi.URLParam(r, "kind")
	if err := h.store.DeleteProfileTemplate(r.Context(), pid, kind); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- images CRUD -----------------------------------------------------

func (h *handlers) listImages(w http.ResponseWriter, r *http.Request) {
	imgs, err := h.store.ListImages(r.Context())
	if err != nil {
		http.Error(w, "list images: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if imgs == nil {
		imgs = []store.Image{}
	}
	writeJSON(w, http.StatusOK, imgs)
}

func (h *handlers) getImage(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	img, err := h.store.GetImage(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, img)
}

type createImageReq struct {
	Name        string                 `json:"name"`
	OSFamily    string                 `json:"os_family"`
	OSVersion   string                 `json:"os_version"`
	Arch        string                 `json:"arch"`
	Description *string                `json:"description"`
	Media       map[string]interface{} `json:"media"`
}

func (h *handlers) createImage(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "*") && !auth.HasPerm(r.Context(), "image.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req createImageReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	in := store.CreateImageInput{
		Name: req.Name, OSFamily: req.OSFamily, OSVersion: req.OSVersion,
		Arch: req.Arch, Description: req.Description,
	}
	if req.Media != nil {
		in.Media, _ = json.Marshal(req.Media)
	}
	id, err := h.store.CreateImage(r.Context(), in)
	if err != nil {
		http.Error(w, "create: "+err.Error(), http.StatusBadRequest)
		return
	}
	uid := auth.UserID(r.Context())
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "image.created",
		SubjectID: &id, SubjectKind: "image",
	})
	img, _ := h.store.GetImage(r.Context(), id)
	writeJSON(w, http.StatusCreated, img)
}

type updateImageReq struct {
	Name        *string                 `json:"name"`
	Description *string                 `json:"description"`
	Media       *map[string]interface{} `json:"media"`
}

func (h *handlers) updateImage(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "*") && !auth.HasPerm(r.Context(), "image.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var req updateImageReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	in := store.UpdateImageInput{Name: req.Name, Description: req.Description}
	if req.Media != nil {
		b, _ := json.Marshal(*req.Media)
		in.Media = b
	}
	if err := h.store.UpdateImage(r.Context(), id, in); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "update: "+err.Error(), http.StatusBadRequest)
		return
	}
	uid := auth.UserID(r.Context())
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "image.updated",
		SubjectID: &id, SubjectKind: "image",
	})
	img, _ := h.store.GetImage(r.Context(), id)
	writeJSON(w, http.StatusOK, img)
}

func (h *handlers) deleteImage(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "*") && !auth.HasPerm(r.Context(), "image.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := h.store.DeleteImage(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	uid := auth.UserID(r.Context())
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "image.deleted",
		SubjectID: &id, SubjectKind: "image",
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- GET /api/v1/jobs ------------------------------------------------

func (h *handlers) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.JobFilter{
		State: q.Get("state"),
		Limit: parseLimit(q.Get("limit"), 200),
	}
	if v := q.Get("machine"); v != "" {
		mid, err := uuid.Parse(v)
		if err != nil {
			http.Error(w, "bad machine uuid", http.StatusBadRequest)
			return
		}
		f.MachineID = &mid
	}
	jobs, err := h.store.ListJobs(r.Context(), f)
	if err != nil {
		http.Error(w, "list jobs: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if jobs == nil {
		jobs = []store.Job{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

// --- GET /api/v1/jobs/{id} -------------------------------------------

func (h *handlers) getJob(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	job, err := h.store.GetJob(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	events, err := h.store.GetJobEvents(r.Context(), id)
	if err != nil {
		http.Error(w, "events: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []store.JobEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job, "events": events})
}

// --- DELETE /api/v1/machines/{id} ------------------------------------

func (h *handlers) deleteMachine(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "machine.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := h.store.DeleteMachine(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	uid := auth.UserID(r.Context())
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "machine.deleted",
		SubjectID: &id, SubjectKind: "machine",
	})
	w.WriteHeader(http.StatusNoContent)
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
