// auth subcommands: OIDC device-authorization login for the CLI.
//
//   deployctl auth login    — device-code flow against the deploy
//                             server's IdP; caches the ID token
//   deployctl auth status   — show which token source is active
//   deployctl auth logout   — remove the cached token
//
// The device flow (RFC 8628) suits a CLI: no local listener, no browser
// redirect plumbing — the IdP shows a short code, the operator confirms
// it on any device, and the CLI polls the token endpoint. The resulting
// ID token is cached at ~/.config/deployctl/token (0600) and picked up
// by the client automatically; DEPLOY_API_TOKEN still overrides it.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/your-org/deployserver/deployctl/internal/client"
)

func authMain(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "auth: subcommand required (login|status|logout)")
		os.Exit(2)
	}
	switch args[0] {
	case "login":
		authLogin(args[1:])
	case "status":
		authStatus()
	case "logout":
		authLogout()
	default:
		fmt.Fprintf(os.Stderr, "auth: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

type oidcMeta struct {
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
}

type deviceAuthResp struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

func authLogin(args []string) {
	fs := flag.NewFlagSet("auth login", flag.ExitOnError)
	issuerFlag := fs.String("issuer", "", "OIDC issuer (default: discovered from the deploy server)")
	clientIDFlag := fs.String("client-id", "", "OIDC client id (default: discovered from the deploy server)")
	_ = fs.Parse(args)

	issuer, clientID := *issuerFlag, *clientIDFlag
	if issuer == "" || clientID == "" {
		// Discover from the deploy server's pre-auth config endpoint.
		base := os.Getenv("DEPLOY_API_URL")
		if base == "" {
			fatal(fmt.Errorf("set DEPLOY_API_URL (or pass --issuer and --client-id)"))
		}
		var cfg struct {
			Issuer   string `json:"issuer"`
			ClientID string `json:"client_id"`
			DevMode  bool   `json:"dev_mode"`
		}
		if err := getJSON(base+"/api/v1/auth/config", &cfg); err != nil {
			fatal(fmt.Errorf("discover auth config: %w", err))
		}
		if cfg.DevMode {
			fmt.Println("Server runs in dev mode (no OIDC); no login needed.")
			return
		}
		if issuer == "" {
			issuer = cfg.Issuer
		}
		if clientID == "" {
			clientID = cfg.ClientID
		}
	}
	if issuer == "" || clientID == "" {
		fatal(fmt.Errorf("issuer/client-id unresolved"))
	}

	var meta oidcMeta
	if err := getJSON(strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration", &meta); err != nil {
		fatal(fmt.Errorf("OIDC discovery: %w", err))
	}
	if meta.DeviceAuthorizationEndpoint == "" {
		fatal(fmt.Errorf("IdP does not advertise a device_authorization_endpoint; use DEPLOY_API_TOKEN instead"))
	}

	da, err := postForm[deviceAuthResp](meta.DeviceAuthorizationEndpoint, url.Values{
		"client_id": {clientID},
		"scope":     {"openid email profile"},
	})
	if err != nil {
		fatal(fmt.Errorf("device authorization: %w", err))
	}

	verify := da.VerificationURIComplete
	if verify == "" {
		verify = da.VerificationURI
	}
	fmt.Printf("\nTo sign in, visit:\n\n    %s\n\nand enter code: %s\n\nWaiting for approval", verify, da.UserCode)

	interval := da.Interval
	if interval < 1 {
		interval = 5
	}
	deadline := time.Now().Add(time.Duration(max(da.ExpiresIn, 60)) * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(time.Duration(interval) * time.Second)
		fmt.Print(".")
		tok, err := postForm[struct {
			IDToken     string `json:"id_token"`
			Error       string `json:"error"`
			ExpiresIn   int    `json:"expires_in"`
		}](meta.TokenEndpoint, url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {da.DeviceCode},
			"client_id":   {clientID},
		})
		if err != nil {
			continue
		}
		switch tok.Error {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5
			continue
		case "":
			if tok.IDToken == "" {
				fatal(fmt.Errorf("token response carried no id_token"))
			}
			path, err := client.SaveToken(tok.IDToken)
			if err != nil {
				fatal(fmt.Errorf("save token: %w", err))
			}
			fmt.Printf("\n\nSigned in. Token cached at %s\n", path)
			return
		default:
			fatal(fmt.Errorf("token endpoint: %s", tok.Error))
		}
	}
	fatal(fmt.Errorf("device code expired before approval"))
}

func authStatus() {
	if os.Getenv("DEPLOY_API_TOKEN") != "" {
		fmt.Println("Using DEPLOY_API_TOKEN from the environment (overrides the cache).")
		return
	}
	path, err := client.TokenCachePath()
	if err == nil {
		if _, statErr := os.Stat(path); statErr == nil {
			fmt.Printf("Using cached token at %s (run `deployctl auth login` to refresh).\n", path)
			return
		}
	}
	fmt.Println("Not signed in. Run `deployctl auth login` or set DEPLOY_API_TOKEN.")
}

func authLogout() {
	path, err := client.TokenCachePath()
	if err != nil {
		fatal(err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fatal(err)
	}
	fmt.Println("Signed out (cached token removed).")
}

// --- small HTTP helpers ------------------------------------------------

func getJSON(u string, out any) error {
	resp, err := http.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, u)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func postForm[T any](u string, form url.Values) (T, error) {
	var out T
	resp, err := http.PostForm(u, form)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	// Device-flow token endpoints signal pending via 400 + error field,
	// so decode the body regardless of status.
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
