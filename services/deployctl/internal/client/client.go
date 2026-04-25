// Tiny HTTP client wrapper. Reads DEPLOY_API_URL and DEPLOY_API_TOKEN
// from env. The token is an OIDC ID token (from `deployctl auth login`,
// not yet implemented) or a static service token if your deployment
// uses one.

package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New() (*Client, error) {
	base := os.Getenv("DEPLOY_API_URL")
	if base == "" {
		return nil, fmt.Errorf("DEPLOY_API_URL not set (e.g. https://deploy.example.com)")
	}
	tok := os.Getenv("DEPLOY_API_TOKEN")
	tlsCfg := &tls.Config{}
	if os.Getenv("DEPLOY_API_INSECURE_SKIP_VERIFY") == "1" {
		tlsCfg.InsecureSkipVerify = true
	}
	return &Client{
		BaseURL: base,
		Token:   tok,
		HTTP: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		},
	}, nil
}

func (c *Client) Do(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("call %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: %d: %s", method, path, resp.StatusCode, string(b))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
