package main

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/your-org/deployserver/deployctl/internal/client"
	sdk "github.com/your-org/deployserver/sdk"
)

// newSDK builds the typed SDK client from the same environment and token
// cache the rest of deployctl uses (DEPLOY_API_URL, DEPLOY_API_TOKEN, the
// auth-login cache, DEPLOY_API_INSECURE_SKIP_VERIFY). The machines
// commands ride on the published SDK instead of hand-rolled request code
// — this is the project dogfooding its own client: if a machines endpoint
// or model drifts from the OpenAPI spec, the regenerated SDK stops
// compiling here.
func newSDK() (*sdk.Client, error) {
	base := os.Getenv("DEPLOY_API_URL")
	if base == "" {
		return nil, fmt.Errorf("DEPLOY_API_URL not set (e.g. https://deploy.example.com)")
	}
	// Precedence matches internal/client.New: explicit env var, then the
	// auth-login token cache.
	tok := os.Getenv("DEPLOY_API_TOKEN")
	if tok == "" {
		if path, err := client.TokenCachePath(); err == nil {
			if b, err := os.ReadFile(path); err == nil {
				tok = strings.TrimSpace(string(b))
			}
		}
	}
	tlsCfg := &tls.Config{}
	if os.Getenv("DEPLOY_API_INSECURE_SKIP_VERIFY") == "1" {
		tlsCfg.InsecureSkipVerify = true
	}
	hc := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	return sdk.New(sdk.Options{BaseURL: base, Token: tok, HTTP: hc})
}
