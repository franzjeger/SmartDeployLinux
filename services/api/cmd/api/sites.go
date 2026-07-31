// Site management: named locations with an optional local image mirror
// (the edge agent's caching blob proxy). See migration 0007.

package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/store"
)

// GET /api/v1/sites
func (h *handlers) listSites(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "machine.read") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	sites, err := h.store.ListSites(r.Context())
	if err != nil {
		http.Error(w, "list sites: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if sites == nil {
		sites = []store.Site{}
	}
	writeJSON(w, http.StatusOK, sites)
}

type upsertSiteReq struct {
	Name          string  `json:"name"`
	MirrorBaseURL *string `json:"mirror_base_url"`
	Description   *string `json:"description"`
}

// PUT /api/v1/sites — upsert by name.
func (h *handlers) upsertSite(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "machine.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req upsertSiteReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if req.MirrorBaseURL != nil && *req.MirrorBaseURL != "" {
		u, err := url.Parse(*req.MirrorBaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			http.Error(w, "mirror_base_url must be an http(s) URL", http.StatusBadRequest)
			return
		}
	}
	site, err := h.store.UpsertSite(r.Context(), req.Name, req.MirrorBaseURL, req.Description)
	if err != nil {
		http.Error(w, "upsert site: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, site)
}

// DELETE /api/v1/sites/{name}
func (h *handlers) deleteSite(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "machine.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := chi.URLParam(r, "name")
	if err := h.store.DeleteSite(r.Context(), name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "delete site: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
