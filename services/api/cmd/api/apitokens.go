package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/store"
	"github.com/your-org/deployserver/api/internal/tokens"
)

type createAPITokenReq struct {
	Name          string   `json:"name"`
	ExpiresInDays *int     `json:"expires_in_days,omitempty"`
	Roles         []string `json:"roles,omitempty"`
}

// createdAPIToken is the create response. It embeds the stored record and
// adds the plaintext secret, which is returned exactly once and never
// again — the server keeps only its hash.
type createdAPIToken struct {
	store.APIToken
	Token string `json:"token"`
}

// POST /api/v1/api-tokens
func (h *handlers) createAPIToken(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "apitoken.write") && !auth.HasPerm(r.Context(), "*") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	uid := auth.UserID(r.Context())
	if uid == uuid.Nil {
		http.Error(w, "no authenticated principal", http.StatusUnauthorized)
		return
	}
	var req createAPITokenReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	var expires *time.Time
	if req.ExpiresInDays != nil {
		if *req.ExpiresInDays <= 0 {
			http.Error(w, "expires_in_days must be positive", http.StatusBadRequest)
			return
		}
		t := time.Now().Add(time.Duration(*req.ExpiresInDays) * 24 * time.Hour)
		expires = &t
	}
	// Optional role-scoping: the token may be limited to a subset of the
	// owner's roles. Reject any requested role the owner does not hold —
	// that is the invariant that keeps a token from ever escalating.
	scope, err := validateTokenScope(r.Context(), h, uid, req.Roles)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	secret, err := newTokenSecret()
	if err != nil {
		http.Error(w, "token gen: "+err.Error(), http.StatusInternalServerError)
		return
	}
	full := tokens.APITokenPrefix + secret
	rec, err := h.store.CreateAPIToken(r.Context(), store.CreateAPITokenInput{
		Name:        req.Name,
		UserID:      uid,
		TokenHash:   tokens.HashAPIToken(full, []byte(h.deployFQDN)),
		TokenPrefix: full[:12],
		ExpiresAt:   expires,
		ScopeRoles:  scope,
	})
	if err != nil {
		http.Error(w, "create token: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "api_token.created",
		SubjectID: &rec.ID, SubjectKind: "api_token",
	})
	writeJSON(w, http.StatusCreated, createdAPIToken{APIToken: *rec, Token: full})
}

// GET /api/v1/api-tokens
func (h *handlers) listAPITokens(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "apitoken.read") && !auth.HasPerm(r.Context(), "*") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	uid := auth.UserID(r.Context())
	if uid == uuid.Nil {
		http.Error(w, "no authenticated principal", http.StatusUnauthorized)
		return
	}
	toks, err := h.store.ListAPITokens(r.Context(), uid)
	if err != nil {
		http.Error(w, "list: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if toks == nil {
		toks = []store.APIToken{}
	}
	writeJSON(w, http.StatusOK, toks)
}

// DELETE /api/v1/api-tokens/{id}
func (h *handlers) revokeAPIToken(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "apitoken.write") && !auth.HasPerm(r.Context(), "*") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	uid := auth.UserID(r.Context())
	if uid == uuid.Nil {
		http.Error(w, "no authenticated principal", http.StatusUnauthorized)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	ok, err := h.store.RevokeAPIToken(r.Context(), id, uid)
	if err != nil {
		http.Error(w, "revoke: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "api_token.revoked",
		SubjectID: &id, SubjectKind: "api_token",
	})
	w.WriteHeader(http.StatusNoContent)
}

// validateTokenScope normalizes the requested role scope and verifies it
// is a subset of the owner's roles. An empty request yields an empty
// scope (unscoped = full owner permissions). Duplicates are collapsed.
func validateTokenScope(ctx context.Context, h *handlers, uid uuid.UUID, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	owned, err := h.store.ListUserRoleNames(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("load roles: %w", err)
	}
	ownedSet := make(map[string]struct{}, len(owned))
	for _, r := range owned {
		ownedSet[r] = struct{}{}
	}
	seen := make(map[string]struct{}, len(requested))
	scope := make([]string, 0, len(requested))
	for _, r := range requested {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if _, ok := ownedSet[r]; !ok {
			return nil, fmt.Errorf("cannot scope token to role %q: you do not hold it", r)
		}
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		scope = append(scope, r)
	}
	return scope, nil
}

// newTokenSecret returns 32 bytes of CSPRNG entropy as unpadded base64url.
func newTokenSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
