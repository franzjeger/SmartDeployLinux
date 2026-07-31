// Package sdk is the typed Go client for the deployserver operator API.
//
// Its surface is derived from — and kept in exact correspondence with —
// the OpenAPI 3.1 spec the server publishes at /api/openapi.yaml: a
// parity test asserts every documented operation has exactly one SDK
// method and vice versa, so the SDK cannot silently drift from the API.
//
// Runtime dependencies: the standard library only.
//
//	c, _ := sdk.New(sdk.Options{BaseURL: "https://deploy.example.com", Token: tok})
//	machines, err := c.ListMachines(ctx)
//
// Auth: pass Options.Token (an OIDC ID token from `deployctl auth
// login`, or a service bearer). When empty, New falls back to
// DEPLOY_API_TOKEN and then DEPLOY_API_URL from the environment.
package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Options configures a Client. BaseURL is required (or DEPLOY_API_URL).
type Options struct {
	BaseURL string
	Token   string
	// HTTP lets callers supply their own client (timeouts, proxies,
	// InsecureSkipVerify for dev). Defaults to a 30s-timeout client.
	HTTP *http.Client
}

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func New(o Options) (*Client, error) {
	base := strings.TrimRight(o.BaseURL, "/")
	if base == "" {
		return nil, errors.New("sdk: BaseURL required")
	}
	hc := o.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: base, token: o.Token, http: hc}, nil
}

// APIError carries a non-2xx response. The Message is the server's
// error body (or its `error` JSON field) so failures are actionable.
type APIError struct {
	Status  int
	Method  string
	Path    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("deployserver %s %s: %d: %s", e.Method, e.Path, e.Status, e.Message)
}

// IsNotFound reports whether err is a 404 APIError.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// IsForbidden reports whether err is a 403 (missing RBAC permission).
func IsForbidden(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusForbidden
}

// fill substitutes {name} placeholders in an operation path.
func fill(path string, args map[string]string) string {
	for k, v := range args {
		path = strings.ReplaceAll(path, "{"+k+"}", url.PathEscape(v))
	}
	return path
}

// do executes op, filling path args and query, JSON-encoding body (when
// non-nil), and decoding a JSON response into out (when non-nil).
func (c *Client) do(ctx context.Context, op Operation, args map[string]string, query url.Values, body, out any) error {
	raw, err := c.doRaw(ctx, op, args, query, body)
	if err != nil {
		return err
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("sdk: decode %s response: %w", op.ID, err)
	}
	return nil
}

// doRaw is do without JSON decoding — used for CSV and other non-JSON
// responses. Returns the response body on success.
func (c *Client) doRaw(ctx context.Context, op Operation, args map[string]string, query url.Values, body any) ([]byte, error) {
	p := fill(op.Path, args)
	u := c.baseURL + p
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, op.Method, u, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sdk: %s %s: %w", op.Method, p, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode/100 != 2 {
		return nil, &APIError{Status: resp.StatusCode, Method: op.Method, Path: p, Message: errorMessage(raw)}
	}
	return raw, nil
}

// errorMessage extracts a human message from an error body: the JSON
// `error` field when present, else the trimmed raw text.
func errorMessage(raw []byte) string {
	var j struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &j) == nil && j.Error != "" {
		return j.Error
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func sinceQuery(since string) url.Values {
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	return q
}
