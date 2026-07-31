// Render endpoints serve the http-boot service's per-machine queries.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"text/template"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/eventbus"
	"github.com/your-org/deployserver/api/internal/metrics"
	"github.com/your-org/deployserver/api/internal/store"
	"github.com/your-org/deployserver/api/internal/tokens"
)

// eventBus abstracts the SSE fan-out: the in-process eventbus.Bus in
// tests/single-replica dev, pgbus.Bus (LISTEN/NOTIFY-backed) in serve.
type eventBus interface {
	Publish(eventbus.Event)
	Subscribe(jobID uuid.UUID) (<-chan eventbus.Event, func())
}

type handlers struct {
	store      *store.Store
	publicURL  string
	deployFQDN string
	verifier   *auth.Verifier
	bus        eventBus
	metrics    *metrics.Registry
}

type renderResp struct {
	Machine struct {
		ID       string `json:"id"`
		AssetTag string `json:"asset_tag"`
		MAC      string `json:"mac_primary"`
	} `json:"machine"`
	Profile struct {
		ID   string                 `json:"id"`
		Name string                 `json:"name"`
		Vars map[string]interface{} `json:"vars"`
	} `json:"profile"`
	Image struct {
		OSFamily   string `json:"os_family"`
		OSVersion  string `json:"os_version"`
		Arch       string `json:"arch"`
		KernelURL  string `json:"kernel_url,omitempty"`
		InitrdURL  string `json:"initrd_url,omitempty"`
		WimbootURL string `json:"wimboot_url,omitempty"`
		BootWimURL string `json:"bootwim_url,omitempty"`
		WimURL     string `json:"wim_url,omitempty"`
	} `json:"image"`
	OneShotToken string `json:"one_shot_token,omitempty"`
	JobID        string `json:"job_id,omitempty"`
}

// machineSite reads the machine's site attribute ("" when unset).
func machineSite(m *store.Machine) string {
	if len(m.Attributes) == 0 {
		return ""
	}
	var attrs struct {
		Site string `json:"site"`
	}
	_ = json.Unmarshal(m.Attributes, &attrs)
	return attrs.Site
}

// mirrorRewrite points a media URL at a site's local mirror when the
// URL is served by this deploy server's blob paths (/static, /blobs).
// Third-party URLs (public distro mirrors etc.) pass through untouched.
func mirrorRewrite(mirror, deployFQDN, rawURL string) string {
	if mirror == "" || rawURL == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host != deployFQDN {
		return rawURL
	}
	if !strings.HasPrefix(u.Path, "/static/") && !strings.HasPrefix(u.Path, "/blobs/") {
		return rawURL
	}
	return strings.TrimRight(mirror, "/") + u.Path
}

// siteMirrorFor resolves the machine's site to its mirror base URL.
// Best-effort: on lookup failure the machine just fetches from origin.
func (h *handlers) siteMirrorFor(ctx context.Context, m *store.Machine) string {
	mirror, err := h.store.SiteMirror(ctx, machineSite(m))
	if err != nil {
		return ""
	}
	return mirror
}

func (h *handlers) bundleToResp(b *store.RenderBundle) renderResp {
	var r renderResp
	r.Machine.ID = b.Machine.ID.String()
	if b.Machine.AssetTag != nil {
		r.Machine.AssetTag = *b.Machine.AssetTag
	}
	if b.Machine.MACPrimary != nil {
		r.Machine.MAC = *b.Machine.MACPrimary
	}
	r.Profile.ID = b.ProfileID.String()
	r.Profile.Name = b.ProfileName
	if len(b.ProfileVars) > 0 {
		_ = json.Unmarshal(b.ProfileVars, &r.Profile.Vars)
	}
	r.Image.OSFamily = b.ImageOSFamily
	r.Image.OSVersion = b.ImageOSVersion
	r.Image.Arch = b.ImageArch

	// Pull URLs from images.media — operator-controlled per-image
	// metadata. If the operator hasn't set them, fall back to the
	// /static convention so the http-boot service can serve from a
	// local mirror if configured.
	var media struct {
		KernelURL   string `json:"kernel_url"`
		InitrdURL   string `json:"initrd_url"`
		WimbootURL  string `json:"wimboot_url"`
		BootWimURL  string `json:"bootwim_url"`
		WimURL      string `json:"wim_url"`
		KernelArgs  string `json:"kernel_args"`
	}
	if len(b.ImageMedia) > 0 {
		_ = json.Unmarshal(b.ImageMedia, &media)
	}

	staticBase := fmt.Sprintf("https://%s/static", h.deployFQDN)
	switch b.ImageOSFamily {
	case "linux":
		r.Image.KernelURL = nonEmpty(media.KernelURL,
			fmt.Sprintf("%s/%s-%s/vmlinuz", staticBase, b.ImageOSFamily, b.ImageOSVersion))
		r.Image.InitrdURL = nonEmpty(media.InitrdURL,
			fmt.Sprintf("%s/%s-%s/initrd", staticBase, b.ImageOSFamily, b.ImageOSVersion))
	case "windows":
		r.Image.WimbootURL = nonEmpty(media.WimbootURL, fmt.Sprintf("%s/wimboot/wimboot", staticBase))
		r.Image.BootWimURL = nonEmpty(media.BootWimURL, fmt.Sprintf("%s/winpe/boot.wim", staticBase))
		r.Image.WimURL     = nonEmpty(media.WimURL, fmt.Sprintf("%s/win/install.wim", staticBase))
	}
	if b.JobID != nil {
		r.JobID = b.JobID.String()
	}
	return r
}

// bundleToRespForSite is bundleToResp plus media-URL rewriting to the
// machine's site mirror (the edge box's caching blob proxy), when one
// is configured. This is what turns a 40-machine site into one WAN
// image transfer instead of forty.
func (h *handlers) bundleToRespForSite(ctx context.Context, b *store.RenderBundle) renderResp {
	r := h.bundleToResp(b)
	mirror := h.siteMirrorFor(ctx, &b.Machine)
	if mirror == "" {
		return r
	}
	for _, p := range []*string{
		&r.Image.KernelURL, &r.Image.InitrdURL,
		&r.Image.WimbootURL, &r.Image.BootWimURL, &r.Image.WimURL,
	} {
		*p = mirrorRewrite(mirror, h.deployFQDN, *p)
	}
	return r
}

func nonEmpty(a, b string) string { if a != "" { return a }; return b }

func (h *handlers) renderMachineByID(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	bundle, err := h.store.LookupRenderBundle(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "machine not found", http.StatusNotFound)
		return
	case errors.Is(err, store.ErrNoActiveProfile):
		http.Error(w, "no active profile for machine", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, h.bundleToRespForSite(r.Context(), bundle))
}

func (h *handlers) renderMachineByMAC(w http.ResponseWriter, r *http.Request) {
	mac := chi.URLParam(r, "mac")
	m, err := h.store.GetMachineByMAC(r.Context(), mac)
	if err != nil {
		http.Error(w, "machine not found", http.StatusNotFound)
		return
	}
	bundle, err := h.store.LookupRenderBundle(r.Context(), m.ID)
	if err != nil {
		http.Error(w, "no active profile", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, h.bundleToRespForSite(r.Context(), bundle))
}

// renderByToken is the path the bootstrap stick (and any LAN PXE chainload
// over the same scheme) hits. URL: /internal/render/by-token/{token}
// Returns the same bundle as renderMachineByID but the lookup is gated
// by an unguessable, single-deployment-bound token.
func (h *handlers) renderByToken(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	if len(tok) < 16 {
		http.Error(w, "bad token", http.StatusBadRequest)
		return
	}
	hash := tokens.HashBootToken(tok, h.bootTokenPepper())
	bundle, err := h.store.LookupRenderBundleByToken(r.Context(), hash)
	if errors.Is(err, store.ErrTokenInvalid) {
		http.Error(w, "token invalid or consumed", http.StatusGone)
		return
	}
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	// The stick just fetched its boot script: the deployment is under way.
	if bundle.JobID != nil {
		_ = h.store.AdvanceJob(r.Context(), *bundle.JobID, "bootstrapped")
	}
	resp := h.bundleToRespForSite(r.Context(), bundle)
	// Echo the raw token back so http-boot can template it into the
	// nocloud datasource URL and the WinPE bearer. The token already
	// authenticated this request, so this is not an escalation.
	resp.OneShotToken = tok
	writeJSON(w, http.StatusOK, resp)
}

func (h *handlers) renderUserDataByToken(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	if len(tok) < 16 {
		http.Error(w, "bad token", http.StatusBadRequest)
		return
	}
	hash := tokens.HashBootToken(tok, h.bootTokenPepper())
	bundle, err := h.store.LookupRenderBundleByToken(r.Context(), hash)
	if err != nil {
		http.Error(w, "token invalid or consumed", http.StatusGone)
		return
	}
	h.writeUserData(w, r, bundle, tok)
}

// writeUserData renders the Linux answer file for a machine. If the
// profile has an operator-authored template (kind autoinstall or
// cloud-init), that template is rendered with the same context as the
// unattend path; otherwise a built-in Ubuntu autoinstall fallback is
// used. The one-shot token is injected so phone-home authenticates.
func (h *handlers) writeUserData(w http.ResponseWriter, r *http.Request, bundle *store.RenderBundle, tok string) {
	host := bundle.Machine.ID.String()
	if bundle.Machine.AssetTag != nil {
		host = *bundle.Machine.AssetTag
	}
	jobID := ""
	if bundle.JobID != nil {
		jobID = bundle.JobID.String()
	}

	// Branch order matters and is covered by tests:
	//   1. capture job        → capture cloud-init (archive the machine)
	//   2. linux golden image → restore cloud-init (untar the archive)
	//   3. otherwise          → installer answer file (autoinstall etc.)
	// Capture must win even when the boot profile's image is itself a
	// golden archive — a capture job must never restore.
	isCapture := false
	if bundle.JobID != nil {
		if jc, err := h.store.GetJobCapture(r.Context(), *bundle.JobID); err == nil && jc.Kind == "capture" {
			isCapture = true
		}
	}
	if isCapture {
		w.Header().Set("Content-Type", "text/cloud-config")
		fmt.Fprintf(w, linuxCaptureUserDataTpl, h.publicURL, jobID, tok, h.publicURL, jobID)
		return
	}
	if isLinuxGolden(bundle) {
		archiveURL := h.goldenArchiveURL(r.Context(), bundle)
		w.Header().Set("Content-Type", "text/cloud-config")
		fmt.Fprintf(w, linuxRestoreUserDataTpl,
			h.publicURL, jobID, tok, archiveURL, host, h.publicURL, jobID)
		return
	}

	ctxData := userDataContext{
		Hostname:  host,
		PublicURL: h.publicURL,
		JobID:     jobID,
		Token:     tok,
	}
	ctxData.Machine.ID = bundle.Machine.ID.String()
	ctxData.Machine.AssetTag = host
	if len(bundle.ProfileVars) > 0 {
		_ = json.Unmarshal(bundle.ProfileVars, &ctxData.Vars)
	}

	tplBody := h.profileTemplate(r, bundle.ProfileID.String(), "autoinstall")
	if tplBody == "" {
		tplBody = h.profileTemplate(r, bundle.ProfileID.String(), "cloud-init")
	}
	if tplBody == "" {
		tplBody = ubuntuUserDataTpl
	}

	t, err := template.New("user-data").Option("missingkey=zero").Parse(tplBody)
	if err != nil {
		http.Error(w, "template parse: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctxData); err != nil {
		http.Error(w, "template render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/cloud-config")
	_, _ = w.Write(buf.Bytes())
}

type userDataContext struct {
	Hostname  string
	PublicURL string
	JobID     string
	Token     string
	Machine   struct {
		ID       string
		AssetTag string
	}
	Vars map[string]any
}

// profileTemplate loads an answer-file template body for a profile, or
// "" when none is configured.
func (h *handlers) profileTemplate(r *http.Request, profileID, kind string) string {
	var body string
	row := h.store.Pool().QueryRow(r.Context(), `
		SELECT body FROM answer_file_templates
		WHERE profile_id = $1 AND kind = $2`, profileID, kind)
	if err := row.Scan(&body); err != nil {
		return ""
	}
	return body
}

func (h *handlers) renderMetaDataByToken(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	if len(tok) < 16 {
		http.Error(w, "bad token", http.StatusBadRequest)
		return
	}
	hash := tokens.HashBootToken(tok, h.bootTokenPepper())
	bundle, err := h.store.LookupRenderBundleByToken(r.Context(), hash)
	if err != nil {
		http.Error(w, "token invalid or consumed", http.StatusGone)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	fmt.Fprintf(w, "instance-id: %s\nlocal-hostname: deployed-%s\n",
		bundle.Machine.ID, bundle.Machine.ID)
}

// bootTokenPepper returns the deploy FQDN as the pepper used to hash
// boot tokens. Both auth-broker and api use this, and they MUST agree.
func (h *handlers) bootTokenPepper() []byte {
	return []byte(h.deployFQDN)
}

// ubuntuUserDataTpl is the built-in fallback when the profile has no
// autoinstall / cloud-init template. The account ships with a locked
// password (SSH-key or console recovery only) unless the profile sets
// vars.password_hash to a crypt(3) SHA-512 hash. Progress phone-homes
// carry the deployment's one-shot bearer token.
const ubuntuUserDataTpl = `#cloud-config
autoinstall:
  version: 1
  identity:
    hostname: {{.Hostname}}
    username: {{if .Vars.username}}{{.Vars.username}}{{else}}ubuntu{{end}}
    password: "{{if .Vars.password_hash}}{{.Vars.password_hash}}{{else}}!{{end}}"
  ssh:
    install-server: yes
{{- if .Vars.ssh_authorized_key}}
    authorized-keys:
      - "{{.Vars.ssh_authorized_key}}"
{{- end}}
  storage:
    layout:
      name: lvm
  packages:
    - openssh-server
    - python3-minimal
  early-commands:
    - 'curl -sf {{.PublicURL}}/v1/jobs/{{.JobID}}/events -X POST -H "Authorization: Bearer {{.Token}}" -H "Content-Type: application/json" --data "{\"phase\":\"imaging\",\"message\":\"autoinstall started\"}" || true'
  late-commands:
    - 'curtin in-target -- bash -c true || true'
    - 'curl -sf {{.PublicURL}}/v1/jobs/{{.JobID}}/events -X POST -H "Authorization: Bearer {{.Token}}" -H "Content-Type: application/json" --data "{\"phase\":\"completed\",\"message\":\"autoinstall finished\"}" || true'
`

// linuxRestoreUserDataTpl boots a live environment straight into the
// golden-image restore script. Args: publicURL, jobID, token,
// archiveURL, hostname, publicURL, jobID.
const linuxRestoreUserDataTpl = `#cloud-config
runcmd:
  - |
    export DEPLOY_API=%s DEPLOY_JOB=%s DEPLOY_TOKEN=%s
    export DEPLOY_ARCHIVE_URL='%s' DEPLOY_HOSTNAME=%s
    curl -sf -H "Authorization: Bearer $DEPLOY_TOKEN" \
      %s/v1/jobs/%s/restore.sh -o /run/restore.sh \
      && sh /run/restore.sh
`

// linuxCaptureUserDataTpl boots a live environment straight into the
// capture script. Args: publicURL, jobID, token, publicURL, jobID.
const linuxCaptureUserDataTpl = `#cloud-config
runcmd:
  - |
    export DEPLOY_API=%s DEPLOY_JOB=%s DEPLOY_TOKEN=%s
    curl -sf -H "Authorization: Bearer $DEPLOY_TOKEN" \
      %s/v1/jobs/%s/capture.sh -o /run/capture.sh \
      && sh /run/capture.sh
`

func (h *handlers) renderUserData(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	bundle, err := h.store.LookupRenderBundle(r.Context(), id)
	if err != nil {
		http.Error(w, "no active profile", http.StatusConflict)
		return
	}
	// Legacy path (LAN PXE by machine id): no boot token exists, so the
	// phone-home lines render with an empty bearer and are ignored by
	// the API. The token-based path is the supported one.
	h.writeUserData(w, r, bundle, "")
}

func (h *handlers) renderMetaData(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	w.Header().Set("Content-Type", "application/yaml")
	fmt.Fprintf(w, "instance-id: %s\nlocal-hostname: deployed-%s\n", idStr, idStr)
}

// --- machines CRUD (operator) ----------------------------------------

func (h *handlers) listMachines(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "machine.read") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	machines, err := h.store.ListMachines(r.Context(), 100, 0)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, machines)
}

type createMachineReq struct {
	AssetTag         *string                `json:"asset_tag"`
	MACPrimary       *string                `json:"mac_primary"`
	UUIDSMBIOS       *string                `json:"uuid_smbios"`
	Vendor           *string                `json:"vendor"`
	Model            *string                `json:"model"`
	DefaultProfileID *string                `json:"default_profile_id"`
	Attributes       map[string]any         `json:"attributes"`
}

func (h *handlers) createMachine(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "machine.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req createMachineReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	in := store.CreateMachineInput{
		AssetTag: req.AssetTag, MACPrimary: req.MACPrimary,
		Vendor: req.Vendor, Model: req.Model,
	}
	if req.UUIDSMBIOS != nil {
		u, err := uuid.Parse(*req.UUIDSMBIOS)
		if err != nil {
			http.Error(w, "bad uuid_smbios", http.StatusBadRequest)
			return
		}
		in.UUIDSMBIOS = &u
	}
	if req.DefaultProfileID != nil {
		u, err := uuid.Parse(*req.DefaultProfileID)
		if err != nil {
			http.Error(w, "bad default_profile_id", http.StatusBadRequest)
			return
		}
		in.DefaultProfileID = &u
	}
	if req.Attributes != nil {
		b, _ := json.Marshal(req.Attributes)
		in.Attributes = b
	}
	m, err := h.store.CreateMachine(r.Context(), in)
	if err != nil {
		http.Error(w, "create failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	if u := auth.UserID(r.Context()); u != uuid.Nil {
		_ = h.store.Audit(r.Context(), store.AuditEvent{
			ActorID: &u, ActorKind: "user", Action: "machine.created",
			SubjectID: &m.ID, SubjectKind: "machine",
		})
	}
	writeJSON(w, 201, m)
}

type updateMachineReq struct {
	AssetTag         *string        `json:"asset_tag"`
	MACPrimary       *string        `json:"mac_primary"`
	Vendor           *string        `json:"vendor"`
	Model            *string        `json:"model"`
	DefaultProfileID *string        `json:"default_profile_id"` // "" clears it
	Attributes       map[string]any `json:"attributes"`
}

// PATCH /api/v1/machines/{id} — partial update; omitted fields keep
// their value, an empty default_profile_id clears the profile.
func (h *handlers) updateMachine(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "machine.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var req updateMachineReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	in := store.UpdateMachineInput{
		AssetTag: req.AssetTag, MACPrimary: req.MACPrimary,
		Vendor: req.Vendor, Model: req.Model,
	}
	if req.DefaultProfileID != nil {
		if *req.DefaultProfileID == "" {
			in.ClearDefaultProfile = true
		} else {
			u, err := uuid.Parse(*req.DefaultProfileID)
			if err != nil {
				http.Error(w, "bad default_profile_id", http.StatusBadRequest)
				return
			}
			in.DefaultProfileID = &u
		}
	}
	if req.Attributes != nil {
		b, _ := json.Marshal(req.Attributes)
		in.Attributes = b
	}
	m, err := h.store.UpdateMachine(r.Context(), id, in)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "update failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	if u := auth.UserID(r.Context()); u != uuid.Nil {
		_ = h.store.Audit(r.Context(), store.AuditEvent{
			ActorID: &u, ActorKind: "user", Action: "machine.updated",
			SubjectID: &id, SubjectKind: "machine",
		})
	}
	writeJSON(w, 200, m)
}

func (h *handlers) getMachine(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "machine.read") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	m, err := h.store.GetMachine(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	writeJSON(w, 200, m)
}

// --- phone-home --------------------------------------------------------

type appendEventReq struct {
	Phase   string         `json:"phase"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

func (h *handlers) appendDeploymentEvent(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	jobID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	tok := bearerToken(r)
	if tok == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	// Must match the scheme used by auth-broker when the token was
	// minted (peppered HashBootToken) or verification can never succeed.
	tokHash := tokens.HashBootToken(tok, h.bootTokenPepper())

	// Non-consuming check: an install phones home many times over its
	// lifetime (imaging, post_install, completed). Tokens are revoked by
	// TransitionJob when the job reaches a terminal state.
	if _, _, err := h.store.VerifyPhoneHomeToken(r.Context(), tokHash, jobID); err != nil {
		if errors.Is(err, store.ErrTokenInvalid) {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}

	var req appendEventReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	data, _ := json.Marshal(req.Data)
	if err := h.store.AppendDeploymentEvent(r.Context(), store.DeploymentEventInput{
		JobID: jobID, Phase: req.Phase, Message: req.Message, Data: data,
	}); err != nil {
		http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
		return
	}

	target := mapPhaseToState(req.Phase)
	if target != "" {
		// AdvanceJob walks intermediate states (e.g. pending →
		// bootstrapped → imaging) so an installer that reports
		// "imaging" first doesn't hit an invalid-transition error.
		_ = h.store.AdvanceJob(r.Context(), jobID, target)
	}

	// Push to SSE subscribers so the UI doesn't have to wait for poll.
	if h.bus != nil {
		h.bus.Publish(eventbus.Event{
			JobID: jobID, Phase: req.Phase, Message: req.Message,
			State: target, At: time.Now().UTC(),
		})
	}
	w.WriteHeader(http.StatusAccepted)
}

// streamJobEvents serves /api/v1/jobs/{id}/events/stream as Server-Sent
// Events. The client first gets a backfill of all existing events,
// then a continuous stream of new events until disconnect or 5 min idle.
//
// Headers honor X-Accel-Buffering: no so nginx doesn't buffer the
// stream; tailscale serve passes through correctly.
func (h *handlers) streamJobEvents(w http.ResponseWriter, r *http.Request) {
	jobID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	// Existence check (also acts as a permission gate).
	if _, err := h.store.GetJob(r.Context(), jobID); err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(evType string, payload any) bool {
		buf, _ := json.Marshal(payload)
		var line string
		if evType != "" {
			line = "event: " + evType + "\n"
		}
		line += "data: " + string(buf) + "\n\n"
		if _, err := io.WriteString(w, line); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	// Backfill.
	existing, _ := h.store.GetJobEvents(r.Context(), jobID)
	for _, e := range existing {
		if !send("event", map[string]any{
			"id": e.ID, "phase": e.Phase, "message": e.Message, "at": e.At,
		}) {
			return
		}
	}
	send("synced", map[string]any{"backfill_count": len(existing)})

	// Subscribe + relay.
	sub, cancel := h.bus.Subscribe(jobID)
	defer cancel()

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	idleAfter := time.NewTimer(5 * time.Minute)
	defer idleAfter.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-idleAfter.C:
			send("timeout", map[string]any{"reason": "idle"})
			return
		case ev, ok := <-sub:
			if !ok {
				return
			}
			if !send("event", map[string]any{
				"phase": ev.Phase, "message": ev.Message,
				"state": ev.State, "at": ev.At,
			}) {
				return
			}
			if !idleAfter.Stop() {
				select {
				case <-idleAfter.C:
				default:
				}
			}
			idleAfter.Reset(5 * time.Minute)
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func mapPhaseToState(phase string) string {
	switch phase {
	case "imaging":
		return "imaging"
	case "post_install":
		return "post_install"
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	}
	return ""
}

// --- helpers -----------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	// No HTML entity escaping: responses carry presigned URLs whose &
	// separators must survive naive shell-side JSON extraction
	// (capture.sh parses upload_url with sed).
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func bearerToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(v) > len(prefix) && v[:len(prefix)] == prefix {
		return v[len(prefix):]
	}
	// No query-string fallback: ?token=... leaks via access logs.
	// SECURITY.md §4 #2.
	return ""
}

func readSourceIP(r *http.Request) (a netipAddrLike, err error) {
	host := r.RemoteAddr
	if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = h
	}
	return parseIP(host), nil
}

// netip.Addr indirection (we only need .IsValid + String() + raw stringly
// path; the Store helper uses it directly).
type netipAddrLike = netipAddr
