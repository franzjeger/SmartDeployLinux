// Driver-pack management endpoints (operator-facing).
//
// Upload flow mirrors image ingest: register the zip via POST
// /api/v1/blobs (role=drivers), PUT the bytes to the presigned URL,
// then POST /api/v1/driver-packs to bind it with match rules. The
// matcher (store.MatchDriverPacks) picks packs up immediately.

package main

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/store"
)

var validRuleTypes = map[string]bool{
	"pci-vid-did": true, "dmi-product": true, "dmi-baseboard": true,
	"dmi-vendor": true, "os-version": true,
}

type createDriverPackReq struct {
	Vendor     string             `json:"vendor"`
	Model      string             `json:"model"`
	OSFamily   string             `json:"os_family"`
	OSVersion  string             `json:"os_version"`
	VersionTag string             `json:"version_tag"`
	BlobID     string             `json:"blob_id"`
	Rules      []store.DriverRule `json:"rules"`
}

// POST /api/v1/driver-packs
func (h *handlers) createDriverPack(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "image.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req createDriverPackReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.Vendor == "" || req.Model == "" {
		http.Error(w, "vendor and model required", http.StatusBadRequest)
		return
	}
	if req.OSFamily != "windows" && req.OSFamily != "linux" {
		http.Error(w, "os_family must be windows or linux", http.StatusBadRequest)
		return
	}
	if len(req.Rules) == 0 {
		http.Error(w, "at least one match rule required (a pack no hardware selects is dead weight)", http.StatusBadRequest)
		return
	}
	for _, rule := range req.Rules {
		if !validRuleTypes[rule.Type] || rule.Value == "" {
			http.Error(w, "bad rule: type must be one of pci-vid-did/dmi-product/dmi-baseboard/dmi-vendor/os-version with a value", http.StatusBadRequest)
			return
		}
	}
	blobID, err := uuid.Parse(req.BlobID)
	if err != nil {
		http.Error(w, "bad blob_id", http.StatusBadRequest)
		return
	}
	tag := req.VersionTag
	if tag == "" {
		tag = "v1"
	}
	versionID, err := h.store.CreateDriverPackVersion(r.Context(),
		req.Vendor, req.Model, req.OSFamily, req.OSVersion, tag, blobID, req.Rules)
	if err != nil {
		http.Error(w, "create driver pack: "+err.Error(), http.StatusBadRequest)
		return
	}
	if u := auth.UserID(r.Context()); u != uuid.Nil {
		_ = h.store.Audit(r.Context(), store.AuditEvent{
			ActorID: &u, ActorKind: "user", Action: "driverpack.created",
			SubjectID: &versionID, SubjectKind: "driver_pack_version",
		})
	}
	writeJSON(w, http.StatusCreated, map[string]string{"version_id": versionID.String()})
}

// GET /api/v1/driver-packs
func (h *handlers) listDriverPacks(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "driverpack.read") && !auth.HasPerm(r.Context(), "image.read") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	packs, err := h.store.ListDriverPacks(r.Context())
	if err != nil {
		http.Error(w, "list: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if packs == nil {
		packs = []store.DriverPackRow{}
	}
	writeJSON(w, http.StatusOK, packs)
}

// DELETE /api/v1/driver-packs/versions/{id}
func (h *handlers) deleteDriverPackVersion(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "image.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := h.store.DeleteDriverPackVersion(r.Context(), id); err != nil {
		if err == store.ErrNotFound {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "delete: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if u := auth.UserID(r.Context()); u != uuid.Nil {
		_ = h.store.Audit(r.Context(), store.AuditEvent{
			ActorID: &u, ActorKind: "user", Action: "driverpack.version_deleted",
			SubjectID: &id, SubjectKind: "driver_pack_version",
		})
	}
	w.WriteHeader(http.StatusNoContent)
}
