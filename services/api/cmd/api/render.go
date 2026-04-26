// Render endpoints serve the http-boot service's per-machine queries.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/store"
	"github.com/your-org/deployserver/api/internal/tokens"
)

type handlers struct {
	store      *store.Store
	publicURL  string
	deployFQDN string
	verifier   *auth.Verifier
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
	writeJSON(w, http.StatusOK, h.bundleToResp(bundle))
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
	writeJSON(w, http.StatusOK, h.bundleToResp(bundle))
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
	writeJSON(w, http.StatusOK, h.bundleToResp(bundle))
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
	host := bundle.Machine.ID.String()
	if bundle.Machine.AssetTag != nil {
		host = *bundle.Machine.AssetTag
	}
	jobID := ""
	if bundle.JobID != nil {
		jobID = bundle.JobID.String()
	}
	w.Header().Set("Content-Type", "text/cloud-config")
	fmt.Fprintf(w, ubuntuUserDataTpl, host, h.publicURL, jobID)
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

const ubuntuUserDataTpl = `#cloud-config
autoinstall:
  version: 1
  identity:
    hostname: %s
    username: ubuntu
    password: "$6$rounds=4096$REPLACEME"
  ssh:
    install-server: yes
  storage:
    layout:
      name: lvm
  packages:
    - openssh-server
    - python3-minimal
  late-commands:
    - curtin in-target -- bash -c 'curl -sf %s/v1/jobs/%s/events -X POST -H "Authorization: Bearer ONESHOT" -H "Content-Type: application/json" --data "{\"phase\":\"completed\",\"message\":\"autoinstall finished\"}" || true'
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
	host := bundle.Machine.ID.String()
	if bundle.Machine.AssetTag != nil {
		host = *bundle.Machine.AssetTag
	}
	jobID := ""
	if bundle.JobID != nil {
		jobID = bundle.JobID.String()
	}
	w.Header().Set("Content-Type", "text/cloud-config")
	fmt.Fprintf(w, ubuntuUserDataTpl, host, h.publicURL, jobID)
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
	tokHash := hashToken(tok)

	srcIP, _ := readSourceIP(r)
	verifiedJob, err := h.store.VerifyOneShotToken(r.Context(), tokHash, srcIP)
	if errors.Is(err, store.ErrTokenInvalid) {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if verifiedJob != jobID {
		http.Error(w, "token does not match job", http.StatusForbidden)
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

	// Map phase to deployment_jobs state if it's a state transition.
	target := mapPhaseToState(req.Phase)
	if target != "" {
		_ = h.store.TransitionJob(r.Context(), jobID, target)
	}
	w.WriteHeader(http.StatusAccepted)
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
	_ = json.NewEncoder(w).Encode(v)
}

func bearerToken(r *http.Request) string {
	v := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(v) > len(prefix) && v[:len(prefix)] == prefix {
		return v[len(prefix):]
	}
	// Fallback: query string ?token=... (legacy WinPE path; logs leak).
	return r.URL.Query().Get("token")
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func readSourceIP(r *http.Request) (a netipAddrLike, err error) {
	host := r.RemoteAddr
	if i := indexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	return parseIP(host), nil
}

// netip.Addr indirection (we only need .IsValid + String() + raw stringly
// path; the Store helper uses it directly).
type netipAddrLike = netipAddr
