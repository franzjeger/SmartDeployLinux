package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// issueViaBroker is the shared seam between single and bulk issuance;
// verify it forwards the shared secret and returns broker output intact.
func TestIssueViaBroker(t *testing.T) {
	var gotAuth, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("X-Internal-Auth")
		gotCT = r.Header.Get("Content-Type")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["machine_id"] != "m-1" {
			t.Errorf("body: %v", body)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"code":"4A7-K2P","expires_at":"2026-08-01T00:00:00Z"}`))
	}))
	defer srv.Close()
	t.Setenv("DEPLOY_AUTH_BROKER_URL", srv.URL)
	t.Setenv("AUTH_BROKER_ISSUE_SHARED_SECRET", "sekrit")

	h := &handlers{}
	body, _ := json.Marshal(map[string]any{"machine_id": "m-1"})
	status, respBody, _, err := h.issueViaBroker(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 || gotAuth != "sekrit" || gotCT != "application/json" {
		t.Fatalf("status=%d auth=%q ct=%q", status, gotAuth, gotCT)
	}
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(respBody, &out)
	if out.Code != "4A7-K2P" {
		t.Fatalf("code = %q", out.Code)
	}
}
