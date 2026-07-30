package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("DEPLOY_API_URL", srv.URL)
	t.Setenv("DEPLOY_API_TOKEN", "tok-123")
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDo_SendsAuthAndDecodes(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok-123" {
			t.Errorf("Authorization = %q", got)
		}
		if r.Method != "POST" || r.URL.Path != "/api/v1/machines" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["asset_tag"] != "lab-9" {
			t.Errorf("body = %v", body)
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"ID":"m-1"}`))
	})
	var out struct {
		ID string `json:"ID"`
	}
	if err := c.Do("POST", "/api/v1/machines", map[string]any{"asset_tag": "lab-9"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "m-1" {
		t.Fatalf("out = %+v", out)
	}
}

func TestDo_SurfacesErrorBody(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "machine has no primary MAC", http.StatusConflict)
	})
	err := c.Do("POST", "/api/v1/machines/x/wake", map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected error on 409")
	}
	for _, want := range []string{"409", "no primary MAC"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestNew_RequiresBaseURL(t *testing.T) {
	t.Setenv("DEPLOY_API_URL", "")
	if _, err := New(); err == nil {
		t.Fatal("expected error without DEPLOY_API_URL")
	}
}
