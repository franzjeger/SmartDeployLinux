package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthConfig(t *testing.T) {
	t.Setenv("OIDC_ISSUER", "https://idp.example.com")
	t.Setenv("OIDC_CLIENT_ID", "deploy-ui")
	h := &handlers{} // verifier nil → dev_mode true
	w := httptest.NewRecorder()
	h.authConfig(w, httptest.NewRequest("GET", "/api/v1/auth/config", nil))
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["issuer"] != "https://idp.example.com" || out["client_id"] != "deploy-ui" {
		t.Fatalf("bad config: %v", out)
	}
	if out["dev_mode"] != true {
		t.Fatalf("expected dev_mode true without verifier: %v", out)
	}
}

func TestStickConfig(t *testing.T) {
	t.Setenv("HEADSCALE_URL", "https://hs.example.com")
	t.Setenv("DEPLOY_CA_CERT_PATH", "/nonexistent/deploy-ca.pem")
	h := &handlers{deployFQDN: "deploy.example.com"}

	// Without a tailnet anywhere: 400.
	w := httptest.NewRecorder()
	h.stickConfig(w, httptest.NewRequest("GET", "/api/v1/bootstrap-sticks/config", nil))
	if w.Code != 400 {
		t.Fatalf("expected 400 without tailnet, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	h.stickConfig(w, httptest.NewRequest("GET",
		"/api/v1/bootstrap-sticks/config?tailnet=acme.hs.example.com", nil))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var out struct {
		ConfigJSON       string `json:"config_json"`
		MakeStickCommand string `json:"make_stick_command"`
		CAPEM            string `json:"ca_pem"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"deploy_url": "https://deploy.example.com"`,
		`"tailnet": "acme.hs.example.com"`,
		`"control_url": "https://hs.example.com"`,
	} {
		if !strings.Contains(out.ConfigJSON, want) {
			t.Fatalf("config_json missing %q:\n%s", want, out.ConfigJSON)
		}
	}
	for _, want := range []string{
		"--tailnet     acme.hs.example.com",
		"--deploy-url  https://deploy.example.com",
		"--control-url https://hs.example.com",
	} {
		if !strings.Contains(out.MakeStickCommand, want) {
			t.Fatalf("command missing %q:\n%s", want, out.MakeStickCommand)
		}
	}
	if out.CAPEM != "" {
		t.Fatal("ca_pem should be empty when the file is absent")
	}
}

func TestBucketForRole(t *testing.T) {
	t.Setenv("S3_BUCKET_IMAGES", "imgs")
	if b, ok := bucketForRole(""); !ok || b != "imgs" {
		t.Fatalf("default role: %s %v", b, ok)
	}
	if _, ok := bucketForRole("nonsense"); ok {
		t.Fatal("bad role accepted")
	}
}
