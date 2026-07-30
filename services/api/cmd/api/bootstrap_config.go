// Auth bootstrap for the SPA + stick-config generator.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/your-org/deployserver/api/internal/auth"
)

// GET /api/v1/auth/config — public (pre-auth) endpoint the SPA calls to
// discover how to log in. No secrets: issuer + client_id are public
// values in the OIDC code+PKCE flow.
func (h *handlers) authConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":    os.Getenv("OIDC_ISSUER"),
		"client_id": os.Getenv("OIDC_CLIENT_ID"),
		"dev_mode":  h.verifier == nil,
	})
}

// GET /api/v1/bootstrap-sticks/config?tailnet=...
//
// Returns everything make-stick.sh needs for this deployment: the
// rendered config.json content, the CA PEM the stick must pin (when the
// server has it on disk), and a copy-pasteable command. Building the
// image itself requires losetup/root, so it stays on the operator's
// workstation — but this endpoint removes every hand-typed parameter.
func (h *handlers) stickConfig(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "stick.write") && !auth.HasPerm(r.Context(), "*") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	tailnet := r.URL.Query().Get("tailnet")
	if tailnet == "" {
		tailnet = os.Getenv("DEPLOY_TAILNET")
	}
	if tailnet == "" {
		http.Error(w, "tailnet required (query param or DEPLOY_TAILNET env)", http.StatusBadRequest)
		return
	}
	deployURL := "https://" + h.deployFQDN
	controlURL := os.Getenv("HEADSCALE_URL")

	cfg := map[string]string{
		"deploy_url":   deployURL,
		"tailnet":      tailnet,
		"control_url":  controlURL,
		"image_sha256": "@@IMAGE_SHA256@@", // filled by make-stick.sh
		"version":      version,
	}
	cfgJSON, _ := json.MarshalIndent(cfg, "", "  ")

	caPEM := ""
	caPath := getenv("DEPLOY_CA_CERT_PATH", "/secrets/deploy-ca.pem")
	if b, err := os.ReadFile(caPath); err == nil && strings.Contains(string(b), "BEGIN CERTIFICATE") {
		caPEM = string(b)
	}

	cmd := fmt.Sprintf(`./bootstrap/make-stick.sh \
    --output      deploy-bootstrap-%s.img \
    --tailnet     %s \
    --deploy-url  %s \
    --ca-cert     deploy-ca.pem`,
		strings.SplitN(h.deployFQDN, ".", 2)[0], tailnet, deployURL)
	if controlURL != "" {
		cmd += " \\\n    --control-url " + controlURL
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"config_json":        string(cfgJSON),
		"ca_pem":             caPEM,
		"make_stick_command": cmd,
		"deploy_url":         deployURL,
		"control_url":        controlURL,
		"tailnet":            tailnet,
	})
}
