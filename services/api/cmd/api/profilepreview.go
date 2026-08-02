// Profile template preview (POST /api/v1/profiles/{id}/preview).
//
// Answer-file templates used to have exactly one validation path: boot a
// real machine and watch the install fail. This renders a profile's
// template with a synthetic machine context — the same context the real
// render uses — and checks the result is well-formed YAML, so a typo is
// caught in the editor instead of on hardware.

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"text/template"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/your-org/deployserver/api/internal/auth"
)

type profilePreviewReq struct {
	Kind string `json:"kind"` // autoinstall | cloud-init; default autoinstall
	// Body, when set, is previewed INSTEAD of the stored template — so
	// the editor can validate unsaved changes.
	Body *string `json:"body"`
}

type profilePreviewResp struct {
	Rendered  string `json:"rendered"`
	YAMLValid bool   `json:"yaml_valid"`
	YAMLError string `json:"yaml_error,omitempty"`
	// Fallback is true when no template exists for this kind and the
	// built-in Ubuntu autoinstall default was previewed instead.
	Fallback bool `json:"fallback"`
}

func (h *handlers) previewProfileTemplate(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "profile.read") && !auth.HasPerm(r.Context(), "*") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	profileID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var req profilePreviewReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	kind := req.Kind
	if kind == "" {
		kind = "autoinstall"
	}
	if kind != "autoinstall" && kind != "cloud-init" {
		http.Error(w, "kind must be autoinstall or cloud-init", http.StatusBadRequest)
		return
	}

	if _, err := h.store.GetProfile(r.Context(), profileID); err != nil {
		http.Error(w, "profile not found", http.StatusNotFound)
		return
	}
	vars, _ := h.store.GetProfileVars(r.Context(), profileID)

	resp := profilePreviewResp{}
	tplBody := ""
	if req.Body != nil {
		tplBody = *req.Body
	} else {
		tplBody = h.profileTemplate(r, profileID.String(), kind)
	}
	if tplBody == "" {
		tplBody = ubuntuUserDataTpl
		resp.Fallback = true
	}

	// Same context shape as the real boot-time render, with obviously
	// synthetic values so nobody mistakes a preview for a live install.
	ctxData := userDataContext{
		Hostname:  "preview-host",
		PublicURL: h.publicURL,
		JobID:     "00000000-0000-0000-0000-00000000cafe",
		Token:     "tok_preview_not_redeemable",
	}
	ctxData.Machine.ID = "00000000-0000-0000-0000-00000000beef"
	ctxData.Machine.AssetTag = "preview-host"
	if len(vars) > 0 {
		_ = json.Unmarshal(vars, &ctxData.Vars)
	}

	t, err := template.New("preview").Option("missingkey=zero").Parse(tplBody)
	if err != nil {
		writeJSON(w, http.StatusOK, profilePreviewResp{
			YAMLValid: false, YAMLError: "template parse: " + err.Error(), Fallback: resp.Fallback,
		})
		return
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctxData); err != nil {
		writeJSON(w, http.StatusOK, profilePreviewResp{
			YAMLValid: false, YAMLError: "template render: " + err.Error(), Fallback: resp.Fallback,
		})
		return
	}
	resp.Rendered = buf.String()

	var parsed any
	if err := yaml.Unmarshal(buf.Bytes(), &parsed); err != nil {
		resp.YAMLValid = false
		resp.YAMLError = err.Error()
	} else {
		resp.YAMLValid = true
	}
	writeJSON(w, http.StatusOK, resp)
}
