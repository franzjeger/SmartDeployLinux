// Wake-on-LAN endpoints.
//
// Operator side:
//   POST /api/v1/machines/{id}/wake  {at?: RFC3339, site?: string}
//   GET  /api/v1/machines/{id}/wake  — request history
//
// Edge side (drained by edge agents on the target LAN):
//   GET /v1/edge/wake-queue?site=S&agent=NAME
//
// The edge endpoint is authenticated by the EDGE_WAKE_TOKEN shared
// secret. It FAILS CLOSED: with no token configured the endpoint is
// disabled — waking machines is an outward-facing action, so there is
// no dev-mode escape hatch here.

package main

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/store"
)

type wakeReq struct {
	At   string `json:"at,omitempty"`   // RFC3339; empty = now
	Site string `json:"site,omitempty"` // overrides machines.attributes.site
}

// POST /api/v1/machines/{id}/wake
func (h *handlers) wakeMachine(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "deployment.create") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var req wakeReq
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
	}
	m, err := h.store.GetMachine(r.Context(), id)
	if err != nil {
		http.Error(w, "machine not found", http.StatusNotFound)
		return
	}
	if m.MACPrimary == nil || *m.MACPrimary == "" {
		http.Error(w, "machine has no primary MAC; wake-on-LAN needs one", http.StatusConflict)
		return
	}

	var at time.Time
	if req.At != "" {
		at, err = time.Parse(time.RFC3339, req.At)
		if err != nil {
			http.Error(w, "at must be RFC3339", http.StatusBadRequest)
			return
		}
	}
	site := req.Site
	if site == "" && len(m.Attributes) > 0 {
		var attrs struct {
			Site string `json:"site"`
		}
		_ = json.Unmarshal(m.Attributes, &attrs)
		site = attrs.Site
	}

	var by *uuid.UUID
	if u := auth.UserID(r.Context()); u != uuid.Nil {
		by = &u
	}
	wakeID, err := h.store.CreateWakeRequest(r.Context(), id, *m.MACPrimary, site, at, by)
	if err != nil {
		http.Error(w, "queue wake: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if by != nil {
		_ = h.store.Audit(r.Context(), store.AuditEvent{
			ActorID: by, ActorKind: "user", Action: "machine.wake_queued",
			SubjectID: &id, SubjectKind: "machine",
		})
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"wake_id": wakeID.String(),
		"site":    nonEmpty(site, "default"),
	})
}

// GET /api/v1/machines/{id}/wake
func (h *handlers) listWakes(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "machine.read") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	wakes, err := h.store.ListWakeRequests(r.Context(), id, 20)
	if err != nil {
		http.Error(w, "list wakes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if wakes == nil {
		wakes = []store.WakeRequest{}
	}
	writeJSON(w, http.StatusOK, wakes)
}

// GET /v1/edge/wake-queue?site=S&agent=NAME
func (h *handlers) edgeWakeQueue(w http.ResponseWriter, r *http.Request) {
	token := os.Getenv("EDGE_WAKE_TOKEN")
	if token == "" {
		// Fail closed: no token configured means the feature is off.
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(token)) != 1 {
		http.NotFound(w, r)
		return
	}
	site := r.URL.Query().Get("site")
	agent := r.URL.Query().Get("agent")
	if agent == "" {
		agent = "edge"
	}
	claimed, err := h.store.ClaimDueWakeRequests(r.Context(), site, agent, 50)
	if err != nil {
		http.Error(w, "claim: "+err.Error(), http.StatusInternalServerError)
		return
	}
	type item struct {
		WakeID    string `json:"wake_id"`
		MachineID string `json:"machine_id"`
		MAC       string `json:"mac"`
	}
	out := make([]item, 0, len(claimed))
	for _, c := range claimed {
		out = append(out, item{
			WakeID: c.ID.String(), MachineID: c.MachineID.String(), MAC: c.MAC,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"wakes": out})
}
