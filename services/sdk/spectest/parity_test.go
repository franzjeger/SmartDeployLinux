// Package spectest enforces that the deployserver Go SDK stays in exact
// correspondence with the OpenAPI contract. It lives in its own module so
// the YAML dependency it needs to parse the spec never reaches importers
// of the SDK.
package spectest

import (
	"bytes"
	"os"
	"sort"
	"strings"
	"testing"

	sdk "github.com/your-org/deployserver/sdk"
	"gopkg.in/yaml.v3"
)

// sourceSpecPath is the api module's authoritative spec, relative to this
// package directory in a full monorepo checkout.
const sourceSpecPath = "../../api/internal/apispec/openapi.yaml"

// specMethods are the HTTP verbs the OpenAPI paths object uses as
// operation keys; every other key (parameters, summary, …) is skipped.
var specMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true, "delete": true,
}

// specOperationSet parses the SDK's embedded spec into a set of
// "METHOD path" keys — the same normalization AllOperations uses.
func specOperationSet(t *testing.T) map[string]bool {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(sdk.SpecYAML(), &doc); err != nil {
		t.Fatalf("parse embedded spec: %v", err)
	}
	set := map[string]bool{}
	for path, ops := range doc.Paths {
		if !strings.HasPrefix(path, "/api/v1/") {
			t.Errorf("spec path %q is not under /api/v1", path)
			continue
		}
		for m := range ops {
			if !specMethods[m] {
				continue // parameters:, summary:, …
			}
			set[strings.ToUpper(m)+" "+path] = true
		}
	}
	if len(set) == 0 {
		t.Fatal("spec produced no operations — embed broken?")
	}
	return set
}

// TestOperationParity is the SDK's core quality gate: it asserts the set
// of operations the SDK implements (sdk.AllOperations) is in EXACT
// bijection with the operations documented in the OpenAPI spec. A spec
// endpoint with no SDK method, or an SDK method with no spec endpoint,
// fails here — the guarantee a code generator gives, enforced on a
// hand-written client.
func TestOperationParity(t *testing.T) {
	specSet := specOperationSet(t)

	sdkSet := map[string]bool{}
	for _, op := range sdk.AllOperations {
		key := op.Method + " " + op.Path
		if sdkSet[key] {
			t.Errorf("AllOperations lists %q more than once", key)
		}
		sdkSet[key] = true
	}

	for op := range specSet {
		if !sdkSet[op] {
			t.Errorf("spec documents %q but the SDK implements no such operation", op)
		}
	}
	for op := range sdkSet {
		if !specSet[op] {
			t.Errorf("SDK implements %q but the spec documents no such operation", op)
		}
	}
	if t.Failed() {
		t.Logf("sdk  (%d):\n%s", len(sdkSet), sortedKeys(sdkSet))
		t.Logf("spec (%d):\n%s", len(specSet), sortedKeys(specSet))
	}
}

// TestSpecIsValidOpenAPI3 checks the embedded bytes parse as an OpenAPI 3
// document with a title.
func TestSpecIsValidOpenAPI3(t *testing.T) {
	var doc struct {
		OpenAPI string `yaml:"openapi"`
		Info    struct {
			Title string `yaml:"title"`
		} `yaml:"info"`
	}
	if err := yaml.Unmarshal(sdk.SpecYAML(), &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") || doc.Info.Title == "" {
		t.Fatalf("not a valid OpenAPI 3 doc: version=%q title=%q", doc.OpenAPI, doc.Info.Title)
	}
}

// TestEmbeddedSpecMatchesSource asserts the spec the SDK embeds is
// byte-for-byte identical to the api module's source of truth, so the SDK
// can never be built against a stale contract. When the SDK is vendored
// or checked out on its own the source file is absent — the test skips
// (the in-module parity test still guards SDK↔spec correspondence).
func TestEmbeddedSpecMatchesSource(t *testing.T) {
	src, err := os.ReadFile(sourceSpecPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("source spec %s not present (standalone checkout) — skipping sync check", sourceSpecPath)
		}
		t.Fatalf("read source spec: %v", err)
	}
	if !bytes.Equal(src, sdk.SpecYAML()) {
		t.Errorf("SDK's embedded openapi.yaml is out of sync with %s\n"+
			"refresh it with: make sync-sdk-spec", sourceSpecPath)
		t.Errorf("sizes: embedded=%d source=%d", len(sdk.SpecYAML()), len(src))
	}
}

func sortedKeys(m map[string]bool) string {
	s := make([]string, 0, len(m))
	for k := range m {
		s = append(s, k)
	}
	sort.Strings(s)
	return strings.Join(s, "\n")
}
