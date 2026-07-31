package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/your-org/deployserver/deployctl/internal/client"
)

// fakeIdP implements just enough OIDC for the device flow: discovery,
// device authorization, and a token endpoint that reports pending once
// before issuing the token.
func fakeIdP(t *testing.T) *httptest.Server {
	t.Helper()
	var polls atomic.Int64
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"device_authorization_endpoint": srv.URL + "/device",
			"token_endpoint":                srv.URL + "/token",
		})
	})
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("client_id") != "deployctl-test" {
			t.Errorf("client_id = %q", r.FormValue("client_id"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "dev-123",
			"user_code":        "ABCD-EFGH",
			"verification_uri": srv.URL + "/verify",
			"expires_in":       60,
			"interval":         1,
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("device_code") != "dev-123" {
			t.Errorf("device_code = %q", r.FormValue("device_code"))
		}
		if polls.Add(1) == 1 {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": "idtok-xyz"})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAuthLogin_DeviceFlow(t *testing.T) {
	idp := fakeIdP(t)
	// Redirect the token cache into a temp dir.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))

	// Deploy server serving the pre-auth config that points at the IdP.
	deploy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/config" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"issuer":%q,"client_id":"deployctl-test","dev_mode":false}`, idp.URL)
	}))
	defer deploy.Close()
	t.Setenv("DEPLOY_API_URL", deploy.URL)

	authLogin(nil)

	path, err := client.TokenCachePath()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("token not cached: %v", err)
	}
	if string(b) != "idtok-xyz" {
		t.Fatalf("cached token = %q", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v, want 0600", fi.Mode().Perm())
	}

	// The client picks the cached token up automatically.
	t.Setenv("DEPLOY_API_TOKEN", "")
	c, err := client.New()
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "idtok-xyz" {
		t.Fatalf("client token = %q", c.Token)
	}
}
