// ui embeds the static HTML/CSS/JS for the operator UI directly into
// the api binary so the deploy doesn't need a separate ui container.
//
// The UI is intentionally vanilla (no framework, no build step). It
// makes JSON calls to /api/v1/* and renders results client-side.

package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets/*
var assetsFS embed.FS

// Handler returns an http.Handler that serves the UI at /. The root
// path returns index.html; everything else under /assets/ is served
// directly from the embedded FS with the right content types.
func Handler() http.Handler {
	mux := http.NewServeMux()
	sub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err)
	}
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Anything that didn't match /assets/* falls here. We always
		// serve index.html so the UI can do client-side routing.
		body, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, "ui not embedded", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	})
	return mux
}
