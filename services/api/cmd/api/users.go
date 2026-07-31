// User & role administration.
//
//   GET    /api/v1/users               (user.read)
//   GET    /api/v1/roles               (user.read)
//   POST   /api/v1/users/{id}/roles    {role} (user.write — admin-only in practice)
//   DELETE /api/v1/users/{id}/roles/{role}    (user.write)
//
// Lockout guard: revoking the admin role is refused when it would leave
// zero admins. Count-based (not "not yourself") so it also covers an
// admin removing the only other admin's role by mistake, and works in
// dev mode where the caller has no identity.

package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/store"
)

// GET /api/v1/users
func (h *handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "user.read") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	users, err := h.store.ListUsersWithRoles(r.Context(), 200, 0)
	if err != nil {
		http.Error(w, "list users: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if users == nil {
		users = []store.UserWithRoles{}
	}
	writeJSON(w, http.StatusOK, users)
}

// GET /api/v1/roles
func (h *handlers) listRoles(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "user.read") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	roles, err := h.store.ListRoles(r.Context())
	if err != nil {
		http.Error(w, "list roles: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if roles == nil {
		roles = []store.RoleWithPerms{}
	}
	writeJSON(w, http.StatusOK, roles)
}

type grantRoleReq struct {
	Role string `json:"role"`
}

// POST /api/v1/users/{id}/roles
func (h *handlers) grantUserRole(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "user.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var req grantRoleReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Role == "" {
		http.Error(w, "role required", http.StatusBadRequest)
		return
	}
	if err := h.store.GrantRole(r.Context(), userID, req.Role); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "unknown role", http.StatusBadRequest)
			return
		}
		http.Error(w, "grant: "+err.Error(), http.StatusBadRequest)
		return
	}
	if u := auth.UserID(r.Context()); u != uuid.Nil {
		_ = h.store.Audit(r.Context(), store.AuditEvent{
			ActorID: &u, ActorKind: "user", Action: "user.role_granted",
			SubjectID: &userID, SubjectKind: "user",
			Data: mustJSON(map[string]string{"role": req.Role}),
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/users/{id}/roles/{role}
func (h *handlers) revokeUserRole(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "user.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	role := chi.URLParam(r, "role")

	if role == "admin" {
		others, err := h.store.CountOtherAdmins(r.Context(), userID)
		if err != nil {
			http.Error(w, "admin count: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if others == 0 {
			http.Error(w, "cannot remove the last admin", http.StatusConflict)
			return
		}
	}
	if err := h.store.RevokeRole(r.Context(), userID, role); err != nil {
		http.Error(w, "revoke: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if u := auth.UserID(r.Context()); u != uuid.Nil {
		_ = h.store.Audit(r.Context(), store.AuditEvent{
			ActorID: &u, ActorKind: "user", Action: "user.role_revoked",
			SubjectID: &userID, SubjectKind: "user",
			Data: mustJSON(map[string]string{"role": role}),
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
