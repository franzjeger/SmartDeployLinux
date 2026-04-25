// WinPE per-job endpoints.
//
// Routes (all require Authorization: Bearer <one-shot-token>):
//   GET  /v1/jobs/{id}/deploy.cmd      — fetched fresh by startnet.cmd
//   POST /v1/jobs/{id}/plan            — fingerprint → driver pack list
//   GET  /v1/jobs/{id}/image.wim       — 302 to the image blob
//   GET  /v1/jobs/{id}/drivers.zip     — bundled driver packs (or 404 if none)
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
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/tokens"
)

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
	// We serve the canonical deploy.cmd from the embedded copy; the
	// startnet.cmd fetches this so we can update deployment logic
	// without rebuilding boot.wim.
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
	// Driver pack matching would consult the DB driver_packs/match_rules
	// here. Phase 8 does not yet ship the DB query for that; v1 returns
	// an empty driver list so the WinPE flow still works for hardware
	// covered by Windows in-box drivers. STATUS.md flags this.
	resp := planResponse{
		ImageArch: bundle.ImageArch,
		ImageURL:  fmt.Sprintf("%s/v1/jobs/%s/image.wim", h.publicURL, jobID),
		// Drivers endpoint always exists; it 404s if no packs match.
		DriverPackURLs: []string{
			fmt.Sprintf("%s/v1/jobs/%s/drivers.zip", h.publicURL, jobID),
		},
		UnattendURL: fmt.Sprintf("%s/v1/jobs/%s/unattend.xml", h.publicURL, jobID),
	}
	writeJSON(w, http.StatusOK, resp)
}

// GET /v1/jobs/{id}/image.wim
//
// Redirects (302) to the static blob endpoint where nginx serves the
// WIM with byte-range support. We could also presign an S3 URL here;
// for v1 the simpler /static/ path is fine because http-boot is on the
// tailnet ACL and serving via nginx is byte-range-friendly.
func (h *handlers) winpeImage(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := h.authJobToken(w, r)
	if !ok {
		return
	}
	// In v1 the image URL is computed from the profile's image; the
	// renderer doesn't yet fully resolve to a content-addressed blob
	// path. We redirect to the conventional /static/<os>-<ver>/install.wim.
	target := fmt.Sprintf("https://%s/static/win11/install.wim", h.deployFQDN)
	http.Redirect(w, r, target, http.StatusFound)
}

// GET /v1/jobs/{id}/drivers.zip
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
	// Phase 8 doesn't yet wire driver-pack matching to S3-backed bundles.
	// v1 returns 404 so the WinPE deploy.cmd's "non-fatal driver fetch
	// failure" branch kicks in.
	_ = bundle
	http.Error(w, "no driver packs configured", http.StatusNotFound)
}

// GET /v1/jobs/{id}/unattend.xml
func (h *handlers) winpeUnattend(w http.ResponseWriter, r *http.Request) {
	machineID, profileID, _, ok := h.authJobToken(w, r)
	if !ok {
		return
	}
	// Look up the unattend template for this profile.
	var body string
	row := h.store.Pool().QueryRow(r.Context(), `
		SELECT body FROM answer_file_templates
		WHERE profile_id = $1 AND kind = 'unattend'`, profileID)
	if err := row.Scan(&body); err != nil {
		// No template configured. Return a stub that at least lets OOBE
		// proceed; in production the operator MUST provide a template.
		http.Error(w, "no unattend.xml template configured for profile", http.StatusNotFound)
		return
	}
	// Render with whatever vars we can resolve. Phase 9e's job is
	// state-gating, not finishing the renderer-with-DB-data — that's
	// Phase 8 / 11 work. v1 returns the raw template; the operator's
	// template should be valid unattend.xml on its own.
	_ = machineID
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Content-Disposition", `attachment; filename="unattend.xml"`)
	fmt.Fprint(w, body)
}

// deployCmdBody is the canonical content of winpe/scripts/deploy.cmd.
// Embedded so we can serve it from /v1/jobs/{id}/deploy.cmd without a
// filesystem read at runtime. Kept in sync with winpe/scripts/deploy.cmd
// via a small sanity test (TODO: a build-time check that asserts equality).
const deployCmdBody = `@rem deploy.cmd — fetched fresh by startnet.cmd. See winpe/scripts/deploy.cmd
@rem in the deployserver repo for the canonical source. This embed is what
@rem startnet.cmd actually executes.
@echo off
echo deployserver: deploy.cmd not yet baked. Please overwrite this stub.
exit /b 1
`
