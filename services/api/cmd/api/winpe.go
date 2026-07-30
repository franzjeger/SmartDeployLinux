// WinPE per-job endpoints.
//
// Routes (all require Authorization: Bearer <one-shot-token>):
//   GET  /v1/jobs/{id}/deploy.cmd      — fetched fresh by startnet.cmd
//   POST /v1/jobs/{id}/plan            — fingerprint → driver pack list
//   GET  /v1/jobs/{id}/image.wim       — 302 to the image blob
//   GET  /v1/jobs/{id}/drivers.zip     — 302 to the best-matched pack (404 if none)
//   GET  /v1/jobs/{id}/unattend.xml    — rendered unattend
//
// Each call:
//   1. Extracts bearer token, hashes it, calls VerifyOneShotTokenForJob.
//      On invalid: 401. On consumed/expired: 410. On wrong job: 403.
//      State-gated: rejected if job has left imaging (completed, failed,
//      cancelled).  SECURITY.md §4 #8.
//   2. Returns the job-specific artifact.

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/answerfile"
	"github.com/your-org/deployserver/api/internal/driverpack"
	"github.com/your-org/deployserver/api/internal/store"
	"github.com/your-org/deployserver/api/internal/tokens"
)

// deployCmdBody is the canonical deploy.cmd, embedded at build time from
// the same file tracked at winpe/scripts/deploy.cmd (kept in sync by
// TestDeployCmdMatchesCanonical). startnet.cmd fetches this so we can
// update deployment logic without rebuilding boot.wim.
//
//go:embed deploy.cmd
var deployCmdBody string

// authJobToken pulls the bearer, verifies it against the job, and
// returns the machine_id + profile_id on success.
func (h *handlers) authJobToken(w http.ResponseWriter, r *http.Request) (machineID, profileID, jobID uuid.UUID, ok bool) {
	jobIDStr := chi.URLParam(r, "id")
	jid, err := uuid.Parse(jobIDStr)
	if err != nil {
		http.Error(w, "bad job id", http.StatusBadRequest)
		return
	}
	tok := bearerToken(r)
	if tok == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	hash := tokens.HashBootToken(tok, h.bootTokenPepper())
	mID, pID, err := h.store.VerifyOneShotTokenForJob(r.Context(), hash, jid)
	if err != nil {
		// Either the token is bogus, expired/consumed, OR the job has
		// already moved out of imaging state. We don't disambiguate to
		// avoid leaking job-state to an attacker probing endpoints.
		http.Error(w, "token invalid or job not active", http.StatusGone)
		return
	}
	return mID, pID, jid, true
}

// GET /v1/jobs/{id}/deploy.cmd
func (h *handlers) winpeDeployCmd(w http.ResponseWriter, r *http.Request) {
	if _, _, _, ok := h.authJobToken(w, r); !ok {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=us-ascii")
	w.Header().Set("Content-Disposition", `attachment; filename="deploy.cmd"`)
	fmt.Fprint(w, deployCmdBody)
}

// POST /v1/jobs/{id}/plan
//
// Body: hardware fingerprint
//   { "dmi_vendor": "Dell Inc.", "dmi_product": "Latitude 7440",
//     "dmi_baseboard": "0FC2X3",
//     "pci": [{"vid":"8086","did":"1521"}, ...] }
//
// Response: deployment plan
//   { "image_url": "...", "driver_pack_urls": [...],
//     "unattend_url": "...", "image_arch": "amd64" }
type planRequest struct {
	DMIVendor    string `json:"dmi_vendor"`
	DMIProduct   string `json:"dmi_product"`
	DMIBaseboard string `json:"dmi_baseboard"`
	PCI          []struct {
		VID string `json:"vid"`
		DID string `json:"did"`
	} `json:"pci"`
}

type planResponse struct {
	ImageURL       string   `json:"image_url"`
	DriverPackURLs []string `json:"driver_pack_urls"`
	UnattendURL    string   `json:"unattend_url"`
	ImageArch      string   `json:"image_arch"`
}

func (h *handlers) winpePlan(w http.ResponseWriter, r *http.Request) {
	machineID, _, jobID, ok := h.authJobToken(w, r)
	if !ok {
		return
	}
	var req planRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	bundle, err := h.store.LookupRenderBundle(r.Context(), machineID)
	if err != nil {
		http.Error(w, "render lookup: "+err.Error(), http.StatusConflict)
		return
	}

	// Persist the reported fingerprint as machine hardware inventory —
	// SmartDeploy-style auto-populated asset data.
	if inv, err := json.Marshal(req); err == nil {
		if err := h.store.RecordMachineInventory(r.Context(), machineID, inv); err != nil {
			// Inventory is best-effort; the deployment must not fail on it.
			_ = err
		}
	}

	matched, err := h.store.MatchDriverPacks(r.Context(), h.fingerprintFromPlan(req, bundle))
	if err != nil {
		http.Error(w, "driver match: "+err.Error(), http.StatusInternalServerError)
		return
	}
	urls := make([]string, 0, len(matched))
	for _, m := range matched {
		urls = append(urls, fmt.Sprintf("https://%s/blobs/%s", h.deployFQDN, m.BlobKey))
	}

	resp := planResponse{
		ImageArch:      bundle.ImageArch,
		ImageURL:       fmt.Sprintf("%s/v1/jobs/%s/image.wim", h.publicURL, jobID),
		DriverPackURLs: urls,
		UnattendURL:    fmt.Sprintf("%s/v1/jobs/%s/unattend.xml", h.publicURL, jobID),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handlers) fingerprintFromPlan(req planRequest, bundle *store.RenderBundle) driverpack.Fingerprint {
	fp := driverpack.Fingerprint{
		DMIVendor:    req.DMIVendor,
		DMIProduct:   req.DMIProduct,
		DMIBaseboard: req.DMIBaseboard,
		OSFamily:     bundle.ImageOSFamily,
		OSVersion:    bundle.ImageOSVersion,
	}
	for _, p := range req.PCI {
		fp.PCIDevices = append(fp.PCIDevices, driverpack.PCIID{VID: p.VID, DID: p.DID})
	}
	return fp
}

// GET /v1/jobs/{id}/image.wim
//
// Redirects (302) to the blob nginx serves with byte-range support:
// the operator-set media.wim_url when present, otherwise the /static
// convention derived from the image's os family+version.
func (h *handlers) winpeImage(w http.ResponseWriter, r *http.Request) {
	machineID, _, _, ok := h.authJobToken(w, r)
	if !ok {
		return
	}
	bundle, err := h.store.LookupRenderBundle(r.Context(), machineID)
	if err != nil {
		http.Error(w, "render lookup", http.StatusConflict)
		return
	}
	resp := h.bundleToResp(bundle)
	if resp.Image.WimURL == "" {
		http.Error(w, "no install.wim configured for image", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, resp.Image.WimURL, http.StatusFound)
}

// GET /v1/jobs/{id}/drivers.zip
//
// Redirects to the most specific matched driver pack for the hardware
// last reported via /plan (stored in machines.attributes.hardware).
// 404 when no pack matches, which deploy.cmd treats as non-fatal.
func (h *handlers) winpeDrivers(w http.ResponseWriter, r *http.Request) {
	machineID, _, _, ok := h.authJobToken(w, r)
	if !ok {
		return
	}
	bundle, err := h.store.LookupRenderBundle(r.Context(), machineID)
	if err != nil {
		http.Error(w, "render lookup", http.StatusConflict)
		return
	}
	var req planRequest
	if len(bundle.Machine.Attributes) > 0 {
		var attrs struct {
			Hardware planRequest `json:"hardware"`
		}
		_ = json.Unmarshal(bundle.Machine.Attributes, &attrs)
		req = attrs.Hardware
	}
	matched, err := h.store.MatchDriverPacks(r.Context(), h.fingerprintFromPlan(req, bundle))
	if err != nil {
		http.Error(w, "driver match", http.StatusInternalServerError)
		return
	}
	if len(matched) == 0 {
		http.Error(w, "no driver packs matched", http.StatusNotFound)
		return
	}
	http.Redirect(w, r,
		fmt.Sprintf("https://%s/blobs/%s", h.deployFQDN, matched[0].BlobKey),
		http.StatusFound)
}

// GET /v1/jobs/{id}/unattend.xml
func (h *handlers) winpeUnattend(w http.ResponseWriter, r *http.Request) {
	machineID, profileID, _, ok := h.authJobToken(w, r)
	if !ok {
		return
	}
	var body string
	row := h.store.Pool().QueryRow(r.Context(), `
		SELECT body FROM answer_file_templates
		WHERE profile_id = $1 AND kind = 'unattend'`, profileID)
	if err := row.Scan(&body); err != nil {
		http.Error(w, "no unattend.xml template configured for profile", http.StatusNotFound)
		return
	}

	bundle, err := h.store.LookupRenderBundle(r.Context(), machineID)
	if err != nil {
		http.Error(w, "render lookup", http.StatusConflict)
		return
	}
	var in answerfile.UnattendInput
	in.Machine.ID = bundle.Machine.ID.String()
	if bundle.Machine.AssetTag != nil {
		in.Machine.AssetTag = *bundle.Machine.AssetTag
	}
	if bundle.Machine.MACPrimary != nil {
		in.Machine.MAC = *bundle.Machine.MACPrimary
	}
	in.Profile.Name = bundle.ProfileName
	if len(bundle.ProfileVars) > 0 {
		_ = json.Unmarshal(bundle.ProfileVars, &in.Profile.Vars)
	}
	in.Image.Arch = bundle.ImageArch

	rendered, err := answerfile.Render(body, in)
	if err != nil {
		http.Error(w, "unattend render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Disposition", `attachment; filename="unattend.xml"`)
	_, _ = w.Write(rendered)
}
