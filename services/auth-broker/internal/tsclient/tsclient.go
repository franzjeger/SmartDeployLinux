// tsclient abstracts over Headscale and Tailscale-SaaS for the one
// operation the broker performs: mint a single-use, ephemeral, tagged
// auth key.
//
// The two backends have similar but not identical APIs. We keep the
// interface narrow on purpose; we don't want this to balloon into a
// general Tailscale client.

package tsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/your-org/deployserver/auth-broker/internal/config"
)

type Client interface {
	// CreateBootstrapKey mints an ephemeral, single-use, tagged auth key.
	// Returns the key string the stick should pass to `tailscale up
	// --authkey=...`, plus the control-plane URL the stick should use as
	// `--login-server` (Headscale only; empty for Tailscale SaaS).
	CreateBootstrapKey(ctx context.Context, ttl time.Duration, tags []string) (key string, controlURL string, err error)
}

func New(cfg *config.Config) (Client, error) {
	switch {
	case cfg.HeadscaleURL != "":
		return &headscale{
			base:    cfg.HeadscaleURL,
			apiKey:  cfg.HeadscaleAPIKey,
			user:    cfg.HeadscaleUser,
			http:    &http.Client{Timeout: 10 * time.Second},
		}, nil
	case cfg.TailscaleAPIKey != "":
		return &tailscale{
			tailnet: cfg.TailscaleTailnet,
			apiKey:  cfg.TailscaleAPIKey,
			http:    &http.Client{Timeout: 10 * time.Second},
		}, nil
	}
	return nil, errors.New("no overlay backend configured")
}

// --- Headscale ---------------------------------------------------------

type headscale struct {
	base   string
	apiKey string
	user   string
	http   *http.Client
}

// Headscale 0.26+ exposes /api/v1/preauthkey. Body shape:
// {
//   "user":      "<userID or username>",
//   "reusable":  false,
//   "ephemeral": true,
//   "expiration": "2026-04-25T18:30:00Z",
//   "aclTags":   ["tag:deploy-bootstrap"]
// }
type hsCreatePreauthKeyRequest struct {
	User       string    `json:"user"`
	Reusable   bool      `json:"reusable"`
	Ephemeral  bool      `json:"ephemeral"`
	Expiration time.Time `json:"expiration"`
	ACLTags    []string  `json:"aclTags"`
}

type hsCreatePreauthKeyResponse struct {
	PreAuthKey struct {
		Key string `json:"key"`
	} `json:"preAuthKey"`
}

func (h *headscale) CreateBootstrapKey(ctx context.Context, ttl time.Duration, tags []string) (string, string, error) {
	body, err := json.Marshal(hsCreatePreauthKeyRequest{
		User:       h.user,
		Reusable:   false,
		Ephemeral:  true,
		Expiration: time.Now().Add(ttl).UTC(),
		ACLTags:    tags,
	})
	if err != nil {
		return "", "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		h.base+"/api/v1/preauthkey", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+h.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("headscale call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", "", fmt.Errorf("headscale %d: %s", resp.StatusCode, string(b))
	}

	var out hsCreatePreauthKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", err
	}
	if out.PreAuthKey.Key == "" {
		return "", "", errors.New("headscale returned empty key")
	}
	return out.PreAuthKey.Key, h.base, nil
}

// --- Tailscale SaaS ----------------------------------------------------

type tailscale struct {
	tailnet string
	apiKey  string
	http    *http.Client
}

type tsCreateAuthKeyRequest struct {
	Capabilities struct {
		Devices struct {
			Create struct {
				Reusable      bool     `json:"reusable"`
				Ephemeral     bool     `json:"ephemeral"`
				Preauthorized bool     `json:"preauthorized"`
				Tags          []string `json:"tags"`
			} `json:"create"`
		} `json:"devices"`
	} `json:"capabilities"`
	ExpirySeconds int64  `json:"expirySeconds"`
	Description   string `json:"description"`
}

type tsCreateAuthKeyResponse struct {
	Key string `json:"key"`
}

func (t *tailscale) CreateBootstrapKey(ctx context.Context, ttl time.Duration, tags []string) (string, string, error) {
	var req tsCreateAuthKeyRequest
	req.Capabilities.Devices.Create.Reusable = false
	req.Capabilities.Devices.Create.Ephemeral = true
	req.Capabilities.Devices.Create.Preauthorized = true
	req.Capabilities.Devices.Create.Tags = tags
	req.ExpirySeconds = int64(ttl / time.Second)
	req.Description = "deployserver bootstrap"

	body, err := json.Marshal(req)
	if err != nil {
		return "", "", err
	}

	url := fmt.Sprintf("https://api.tailscale.com/api/v2/tailnet/%s/keys", t.tailnet)
	hreq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	hreq.SetBasicAuth(t.apiKey, "")
	hreq.Header.Set("Content-Type", "application/json")

	resp, err := t.http.Do(hreq)
	if err != nil {
		return "", "", fmt.Errorf("tailscale call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", "", fmt.Errorf("tailscale %d: %s", resp.StatusCode, string(b))
	}

	var out tsCreateAuthKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", err
	}
	return out.Key, "", nil
}
