package sdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests use the standard library only, keeping the SDK module
// dependency-free. The spec-parity and spec-sync checks (which need a
// YAML parser) live in ./spectest so that dependency never reaches
// importers.

// TestOperationsWellFormed catches copy-paste slips in the operation
// table: blank fields, duplicate IDs, non-upper verbs, missing prefix.
func TestOperationsWellFormed(t *testing.T) {
	seenID := map[string]bool{}
	for _, op := range AllOperations {
		if op.ID == "" || op.Method == "" || op.Path == "" {
			t.Errorf("operation has empty field: %+v", op)
		}
		if op.Method != strings.ToUpper(op.Method) {
			t.Errorf("operation %s method %q is not upper-case", op.ID, op.Method)
		}
		if !strings.HasPrefix(op.Path, "/api/v1/") {
			t.Errorf("operation %s path %q is not under /api/v1/", op.ID, op.Path)
		}
		if seenID[op.ID] {
			t.Errorf("duplicate operation ID %q", op.ID)
		}
		seenID[op.ID] = true
	}
	if len(AllOperations) == 0 {
		t.Fatal("AllOperations is empty")
	}
}

// TestSpecYAMLPlausible sanity-checks the public accessor without a YAML
// parser (the full parse lives in ./spectest).
func TestSpecYAMLPlausible(t *testing.T) {
	b := SpecYAML()
	if len(b) < 1000 {
		t.Fatalf("SpecYAML() suspiciously small: %d bytes", len(b))
	}
	if !strings.HasPrefix(string(b), "openapi: 3.") {
		t.Fatalf("SpecYAML() does not start with an OpenAPI 3 header: %.20q", b)
	}
}

// --- behavior tests ---------------------------------------------------

// capturedRequest records what the client sent, for assertions.
type capturedRequest struct {
	Method string
	Path   string
	Query  string
	Auth   string
	Accept string
	CType  string
	Body   string
}

// newTestClient returns a Client pointed at an httptest server whose
// handler is fn. The last request seen is stored in *cap.
func newTestClient(t *testing.T, cap *capturedRequest, fn http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*cap = capturedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Auth:   r.Header.Get("Authorization"),
			Accept: r.Header.Get("Accept"),
			CType:  r.Header.Get("Content-Type"),
			Body:   string(body),
		}
		fn(w, r)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, Token: "tok-123"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

func TestNewValidatesBaseURL(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected error for empty BaseURL")
	}
	c, err := New(Options{BaseURL: "https://x.example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != "https://x.example.com" {
		t.Fatalf("trailing slash not trimmed: %q", c.baseURL)
	}
}

func TestListMachinesRequestShape(t *testing.T) {
	var cap capturedRequest
	c, _ := newTestClient(t, &cap, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"ID":"m1","AssetTag":"A-1","CreatedAt":"2026-01-02T03:04:05Z"}]`)
	})
	got, err := c.ListMachines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cap.Method != "GET" || cap.Path != "/api/v1/machines" {
		t.Fatalf("wrong request line: %s %s", cap.Method, cap.Path)
	}
	if cap.Auth != "Bearer tok-123" {
		t.Fatalf("auth header not set: %q", cap.Auth)
	}
	if cap.Accept != "application/json" {
		t.Fatalf("accept header not set: %q", cap.Accept)
	}
	if len(got) != 1 || got[0].ID != "m1" || got[0].AssetTag == nil || *got[0].AssetTag != "A-1" {
		t.Fatalf("decode mismatch: %+v", got)
	}
}

func TestPathParamIsEscaped(t *testing.T) {
	var cap capturedRequest
	c, _ := newTestClient(t, &cap, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ID":"weird/id"}`)
	})
	if _, err := c.GetMachine(context.Background(), "weird/id"); err != nil {
		t.Fatal(err)
	}
	// The server sees the decoded path; the escaping is what makes it
	// arrive as a single path segment rather than two.
	if cap.Path != "/api/v1/machines/weird/id" {
		t.Fatalf("unexpected decoded path: %q", cap.Path)
	}
}

func TestCreateMachineSendsJSONBody(t *testing.T) {
	var cap capturedRequest
	c, _ := newTestClient(t, &cap, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ID":"new"}`)
	})
	tag := "asset-9"
	out, err := c.CreateMachine(context.Background(), CreateMachineInput{AssetTag: &tag})
	if err != nil {
		t.Fatal(err)
	}
	if cap.Method != "POST" || cap.CType != "application/json" {
		t.Fatalf("wrong method/content-type: %s %s", cap.Method, cap.CType)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(cap.Body), &sent); err != nil {
		t.Fatalf("body not JSON: %v (%q)", err, cap.Body)
	}
	if sent["asset_tag"] != "asset-9" {
		t.Fatalf("body mismatch: %v", sent)
	}
	if out.ID != "new" {
		t.Fatalf("response decode: %+v", out)
	}
}

func TestNotFoundClassified(t *testing.T) {
	var cap capturedRequest
	c, _ := newTestClient(t, &cap, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"machine not found"}`)
	})
	_, err := c.GetMachine(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound false for: %v", err)
	}
	if IsForbidden(err) {
		t.Fatal("IsForbidden should be false")
	}
	if !strings.Contains(err.Error(), "machine not found") {
		t.Fatalf("message not surfaced: %v", err)
	}
}

func TestForbiddenClassified(t *testing.T) {
	var cap capturedRequest
	c, _ := newTestClient(t, &cap, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"missing permission machines.write"}`)
	})
	err := c.DeleteMachine(context.Background(), "m1")
	if !IsForbidden(err) {
		t.Fatalf("IsForbidden false for: %v", err)
	}
}

func TestListJobsQueryParams(t *testing.T) {
	var cap capturedRequest
	c, _ := newTestClient(t, &cap, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[]`)
	})
	_, err := c.ListJobs(context.Background(), JobFilter{State: "running", MachineID: "m1", Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	q := cap.Query
	for _, want := range []string{"state=running", "machine=m1", "limit=25"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q missing %q", q, want)
		}
	}
}

func TestBulkDeployUnwrapsResults(t *testing.T) {
	var cap capturedRequest
	c, _ := newTestClient(t, &cap, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[{"machine_id":"m1","code":"A1B2C3"},{"machine_id":"m2","error":"no route"}]}`)
	})
	res, err := c.BulkDeploy(context.Background(), BulkDeployInput{MachineIDs: []string{"m1", "m2"}, ProfileID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Code != "A1B2C3" || res[1].Error != "no route" {
		t.Fatalf("unwrap mismatch: %+v", res)
	}
}

func TestReportJobsCSVReturnsRawBytes(t *testing.T) {
	var cap capturedRequest
	csv := "id,state\nj1,completed\nj2,failed\n"
	c, _ := newTestClient(t, &cap, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = io.WriteString(w, csv)
	})
	got, err := c.ReportJobsCSV(context.Background(), "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != csv {
		t.Fatalf("csv mismatch: %q", string(got))
	}
	if !strings.Contains(cap.Query, "since=") {
		t.Fatalf("since not sent: %q", cap.Query)
	}
}

func TestNoAuthHeaderWhenTokenEmpty(t *testing.T) {
	var cap capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.Auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"issuer":"x","client_id":"y","dev_mode":true}`)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL}) // no token
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AuthConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cap.Auth != "" {
		t.Fatalf("unexpected auth header: %q", cap.Auth)
	}
}

func TestErrorMessageFallsBackToRawText(t *testing.T) {
	var cap capturedRequest
	c, _ := newTestClient(t, &cap, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "upstream exploded")
	})
	err := c.DeleteMachine(context.Background(), "m1")
	if err == nil || !strings.Contains(err.Error(), "upstream exploded") {
		t.Fatalf("raw text not surfaced: %v", err)
	}
}
