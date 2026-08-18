package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/store"
)

// DELETE /api/v1/jobs/{id} — remove a single deployment job.
func (h *handlers) deleteJob(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "deployment.create") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := h.store.DeleteJob(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "delete: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/jobs/prune[?days=N] — delete terminal jobs (optionally only
// those older than N days). Returns {"deleted": <count>}.
func (h *handlers) pruneJobs(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "deployment.create") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var olderThan time.Duration
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			olderThan = time.Duration(n) * 24 * time.Hour
		}
	}
	n, err := h.store.PruneTerminalJobs(r.Context(), olderThan)
	if err != nil {
		http.Error(w, "prune: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// pruneJobsLoop periodically removes terminal deployment jobs older than
// JOB_RETENTION_DAYS (default 7) so the jobs list does not grow without
// bound. Set JOB_RETENTION_DAYS=0 to disable.
func pruneJobsLoop(ctx context.Context, st *store.Store) {
	days := 7
	if v := os.Getenv("JOB_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			days = n
		}
	}
	if days <= 0 {
		return
	}
	age := time.Duration(days) * 24 * time.Hour
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := st.PruneTerminalJobs(ctx, age); err != nil {
				slog.Warn("job prune failed", "err", err)
			} else if n > 0 {
				slog.Info("pruned old terminal jobs", "count", n, "older_than_days", days)
			}
			t.Reset(24 * time.Hour)
		}
	}
}
