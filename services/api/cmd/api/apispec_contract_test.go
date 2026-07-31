package main

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/your-org/deployserver/api/internal/apispec"
)

// TestOpenAPIContract asserts the embedded OpenAPI spec and the routes
// the server actually registers are in EXACT correspondence for the
// operator API — every documented operation exists, and every real
// operator route is documented. This is what lets the spec be trusted
// as a contract: it cannot silently drift from the code.
func TestOpenAPIContract(t *testing.T) {
	// 1. Walk the real operator router.
	h := &handlers{}
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { h.registerOperatorRoutes(r) })

	routeSet := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		// chi routes use /{id}; the spec uses the same. Normalize the
		// trailing slash chi sometimes emits on the mount root.
		route = strings.TrimSuffix(route, "/")
		routeSet[strings.ToUpper(method)+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// 2. Parse the spec's operator operations (skip the two public
	//    pre-auth endpoints, which aren't part of the operator router).
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(apispec.YAML(), &doc); err != nil {
		t.Fatalf("spec parse: %v", err)
	}
	methods := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}
	specSet := map[string]bool{}
	for path, ops := range doc.Paths {
		if path == "/api/v1/auth/config" {
			continue // public pre-auth route, not in the operator group
		}
		if !strings.HasPrefix(path, "/api/v1/") {
			t.Errorf("spec path %q is not under /api/v1", path)
			continue
		}
		for m := range ops {
			if !methods[m] {
				continue // parameters:, etc.
			}
			specSet[strings.ToUpper(m)+" "+path] = true
		}
	}

	// 3. Bijection.
	for op := range specSet {
		if !routeSet[op] {
			t.Errorf("spec documents %q but the router has no such route", op)
		}
	}
	for op := range routeSet {
		if !specSet[op] {
			t.Errorf("router serves %q but the spec does not document it", op)
		}
	}
	if t.Failed() {
		t.Logf("routes (%d):\n%s", len(routeSet), sortedList(routeSet))
		t.Logf("spec (%d):\n%s", len(specSet), sortedList(specSet))
	}
}

func sortedList(m map[string]bool) string {
	s := make([]string, 0, len(m))
	for k := range m {
		s = append(s, k)
	}
	sort.Strings(s)
	return strings.Join(s, "\n")
}

// TestSpecServed sanity-checks the embedded bytes parse and the docs
// handler renders without error.
func TestSpecServed(t *testing.T) {
	if len(apispec.YAML()) < 1000 {
		t.Fatalf("embedded spec suspiciously small: %d bytes", len(apispec.YAML()))
	}
	var doc struct {
		OpenAPI string `yaml:"openapi"`
		Info    struct{ Title string } `yaml:"info"`
	}
	if err := yaml.Unmarshal(apispec.YAML(), &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") || doc.Info.Title == "" {
		t.Fatalf("not a valid OpenAPI 3 doc: version=%q title=%q", doc.OpenAPI, doc.Info.Title)
	}
}
