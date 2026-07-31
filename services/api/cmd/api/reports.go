// Reporting endpoints (operator-facing, perm job.read):
//
//   GET /api/v1/reports/summary?since=7d
//   GET /api/v1/reports/daily?days=14
//   GET /api/v1/reports/by-profile?since=7d
//   GET /api/v1/reports/by-site?since=7d
//   GET /api/v1/reports/jobs.csv?since=30d      (flat export)

package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/store"
)

// parseSince accepts "24h", "7d", "30d" style windows (Go durations plus
// a d suffix), clamped to [1h, 365d]. Default 7d.
func parseSince(q string) time.Duration {
	const def = 7 * 24 * time.Hour
	if q == "" {
		return def
	}
	var dur time.Duration
	if strings.HasSuffix(q, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(q, "d"))
		if err != nil {
			return def
		}
		dur = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		dur, err = time.ParseDuration(q)
		if err != nil {
			return def
		}
	}
	if dur < time.Hour {
		return time.Hour
	}
	if dur > 365*24*time.Hour {
		return 365 * 24 * time.Hour
	}
	return dur
}

func (h *handlers) reportGate(w http.ResponseWriter, r *http.Request) bool {
	if !auth.HasPerm(r.Context(), "job.read") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func (h *handlers) reportSummary(w http.ResponseWriter, r *http.Request) {
	if !h.reportGate(w, r) {
		return
	}
	s, err := h.store.GetReportSummary(r.Context(), parseSince(r.URL.Query().Get("since")))
	if err != nil {
		http.Error(w, "summary: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *handlers) reportDaily(w http.ResponseWriter, r *http.Request) {
	if !h.reportGate(w, r) {
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	d, err := h.store.GetReportDaily(r.Context(), days)
	if err != nil {
		http.Error(w, "daily: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if d == nil {
		d = []store.ReportDay{}
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *handlers) reportByProfile(w http.ResponseWriter, r *http.Request) {
	if !h.reportGate(w, r) {
		return
	}
	g, err := h.store.GetReportByProfile(r.Context(), parseSince(r.URL.Query().Get("since")))
	if err != nil {
		http.Error(w, "by-profile: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if g == nil {
		g = []store.ReportGroup{}
	}
	writeJSON(w, http.StatusOK, g)
}

func (h *handlers) reportBySite(w http.ResponseWriter, r *http.Request) {
	if !h.reportGate(w, r) {
		return
	}
	g, err := h.store.GetReportBySite(r.Context(), parseSince(r.URL.Query().Get("since")))
	if err != nil {
		http.Error(w, "by-site: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if g == nil {
		g = []store.ReportGroup{}
	}
	writeJSON(w, http.StatusOK, g)
}

func (h *handlers) reportJobsCSV(w http.ResponseWriter, r *http.Request) {
	if !h.reportGate(w, r) {
		return
	}
	since := parseSince(r.URL.Query().Get("since"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="deployments-%s.csv"`, time.Now().UTC().Format("2006-01-02")))
	if err := h.store.StreamJobsCSV(r.Context(), w, since); err != nil {
		// Headers are gone; the truncated CSV is the best signal left.
		_, _ = fmt.Fprintf(w, "\n# export failed: %s\n", err)
	}
}
