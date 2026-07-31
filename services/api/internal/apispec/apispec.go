// Package apispec embeds and serves the OpenAPI 3.1 contract for the
// operator API. The spec is a hand-written source of truth; the
// contract test in cmd/api asserts it stays in exact correspondence
// with the routes the server actually registers.
package apispec

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.yaml
var openAPIYAML []byte

// YAML returns the raw spec bytes.
func YAML() []byte { return openAPIYAML }

// Handler serves the spec as application/yaml.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(openAPIYAML)
	})
}
