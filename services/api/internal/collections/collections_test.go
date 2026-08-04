package collections

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/your-org/deployserver/api/internal/apispec"
)

// repoRoot is the repository root relative to this package directory
// (services/api/internal/collections). The committed collections live
// under repoRoot/clients.
const repoRoot = "../../../.."

var httpVerbs = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}

// specOperationSet returns the set of "METHOD /path" the spec documents.
func specOperationSet(t *testing.T) map[string]bool {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(apispec.YAML(), &doc); err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for path, ops := range doc.Paths {
		for m := range ops {
			if httpVerbs[m] {
				set[strings.ToUpper(m)+" "+path] = true
			}
		}
	}
	return set
}

// TestOperationParity asserts both collections cover exactly the spec's
// operations — the same generated-quality guarantee the SDKs carry.
func TestOperationParity(t *testing.T) {
	spec := specOperationSet(t)
	files, err := Generate(apispec.YAML())
	if err != nil {
		t.Fatal(err)
	}

	// Postman: walk folders -> requests, reconstruct METHOD /path.
	var col struct {
		Item []struct {
			Item []struct {
				Request struct {
					Method string `json:"method"`
					URL    struct {
						Path []string `json:"path"`
					} `json:"url"`
				} `json:"request"`
			} `json:"item"`
		} `json:"item"`
	}
	if err := json.Unmarshal(files["clients/postman/deployserver.postman_collection.json"], &col); err != nil {
		t.Fatalf("postman parse: %v", err)
	}
	pm := map[string]bool{}
	for _, folder := range col.Item {
		for _, req := range folder.Item {
			var segs []string
			for _, s := range req.Request.URL.Path {
				if strings.HasPrefix(s, ":") {
					segs = append(segs, "{"+strings.TrimPrefix(s, ":")+"}")
				} else {
					segs = append(segs, s)
				}
			}
			pm[req.Request.Method+" /"+strings.Join(segs, "/")] = true
		}
	}
	assertBijection(t, "postman", spec, pm)

	// Bruno: one .bru request file per operation.
	bru := 0
	for p := range files {
		if strings.HasPrefix(p, "clients/bruno/") && strings.HasSuffix(p, ".bru") &&
			!strings.Contains(p, "/environments/") {
			bru++
		}
	}
	if bru != len(spec) {
		t.Errorf("bruno has %d request files, spec has %d operations", bru, len(spec))
	}
}

func assertBijection(t *testing.T, name string, spec, got map[string]bool) {
	t.Helper()
	for op := range spec {
		if !got[op] {
			t.Errorf("%s: spec documents %q but the collection omits it", name, op)
		}
	}
	for op := range got {
		if !spec[op] {
			t.Errorf("%s: collection has %q but the spec does not document it", name, op)
		}
	}
}

// TestGeneratedUpToDate asserts every committed file is byte-identical to
// what the generator produces from the current spec, and that there are no
// orphaned committed files. This is what makes the collections trustworthy
// — they cannot silently drift. Regenerate with `make collections`.
func TestGeneratedUpToDate(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoRoot, "clients")); os.IsNotExist(err) {
		t.Skip("clients/ not present (standalone checkout) — skipping sync check")
	}
	files, err := Generate(apispec.YAML())
	if err != nil {
		t.Fatal(err)
	}

	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Errorf("missing committed file %s (run: make collections)", rel)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("%s is out of date — run: make collections", rel)
		}
	}

	// No orphans: every committed collection file must be one we generate.
	generated := map[string]bool{}
	for rel := range files {
		generated[filepath.FromSlash(rel)] = true
	}
	for _, dir := range []string{"clients/postman", "clients/bruno"} {
		root := filepath.Join(repoRoot, dir)
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(repoRoot, p)
			if !generated[rel] {
				t.Errorf("orphaned committed file %s (not produced by the generator; run: make collections)", rel)
			}
			return nil
		})
	}
}

// TestPostmanStructure sanity-checks the collection is a valid v2.1
// document with collection-level bearer auth and the expected variables.
func TestPostmanStructure(t *testing.T) {
	files, err := Generate(apispec.YAML())
	if err != nil {
		t.Fatal(err)
	}
	var col struct {
		Info struct {
			Name   string `json:"name"`
			Schema string `json:"schema"`
		} `json:"info"`
		Auth struct {
			Type string `json:"type"`
		} `json:"auth"`
		Variable []struct {
			Key string `json:"key"`
		} `json:"variable"`
	}
	if err := json.Unmarshal(files["clients/postman/deployserver.postman_collection.json"], &col); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(col.Info.Schema, "v2.1.0") {
		t.Errorf("not a v2.1 collection: %q", col.Info.Schema)
	}
	if col.Auth.Type != "bearer" {
		t.Errorf("collection auth is %q, want bearer", col.Auth.Type)
	}
	keys := map[string]bool{}
	for _, v := range col.Variable {
		keys[v.Key] = true
	}
	for _, want := range []string{"baseUrl", "token"} {
		if !keys[want] {
			t.Errorf("collection variable %q missing", want)
		}
	}
}

// TestDeterministic: generating twice yields identical bytes for every
// file (no map-iteration nondeterminism leaks into the output).
func TestDeterministic(t *testing.T) {
	a, err := Generate(apispec.YAML())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(apispec.YAML())
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("file count differs: %d vs %d", len(a), len(b))
	}
	paths := make([]string, 0, len(a))
	for p := range a {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if string(a[p]) != string(b[p]) {
			t.Errorf("non-deterministic output for %s", p)
		}
	}
}
