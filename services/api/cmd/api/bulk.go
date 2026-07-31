// Bulk deployments: issue a deployment code for many machines in one
// call, optionally queueing wake-on-LAN for each.
//
//   POST /api/v1/deployments/bulk
//     {machine_ids: [...], profile_id, ttl_seconds?, wake?}
//   → 200 {"results": [{machine_id, code?, expires_at?, wake_id?, error?}]}
//
// Always 200 with per-row outcomes: partial failure is an expected
// state for a 50-machine rollout, and the caller needs every code that
// WAS issued regardless of the rows that failed. Machines are issued
// sequentially — the broker enforces per-operator rate limits, and
// hammering it in parallel would only trip them faster.

package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/store"
)

const bulkDeployMax = 100

type bulkDeployReq struct {
	MachineIDs []string `json:"machine_ids"`
	ProfileID  string   `json:"profile_id"`
	TTLSeconds int      `json:"ttl_seconds,omitempty"`
	IssuedFor  string   `json:"issued_for,omitempty"`
	Wake       bool     `json:"wake,omitempty"`
}

type bulkDeployResult struct {
	MachineID string `json:"machine_id"`
	Code      string `json:"code,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	WakeID    string `json:"wake_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

// POST /api/v1/deployments/bulk
func (h *handlers) bulkDeploy(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "deployment.create") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	uid := auth.UserID(r.Context())
	if uid == uuid.Nil {
		admin, err := h.store.FirstAdminUserID(r.Context())
		if err != nil {
			http.Error(w, "no admin user; run `api seed-admin <email>` first", http.StatusUnauthorized)
			return
		}
		uid = admin
	}

	var req bulkDeployReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.ProfileID == "" || len(req.MachineIDs) == 0 {
		http.Error(w, "profile_id and machine_ids required", http.StatusBadRequest)
		return
	}

	// Dedupe while preserving order.
	seen := map[string]bool{}
	ids := make([]string, 0, len(req.MachineIDs))
	for _, id := range req.MachineIDs {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) > bulkDeployMax {
		http.Error(w, "too many machines (max 100 per call)", http.StatusBadRequest)
		return
	}

	label := req.IssuedFor
	if label == "" {
		label = "bulk " + time.Now().UTC().Format("2006-01-02 15:04")
	}

	results := make([]bulkDeployResult, 0, len(ids))
	var issued, failed int
	for _, idStr := range ids {
		res := bulkDeployResult{MachineID: idStr}
		machineID, err := uuid.Parse(idStr)
		if err != nil {
			res.Error = "bad machine id"
			results = append(results, res)
			failed++
			continue
		}
		body, _ := json.Marshal(map[string]any{
			"machine_id":  idStr,
			"profile_id":  req.ProfileID,
			"ttl_seconds": req.TTLSeconds,
			"issued_for":  label,
			"issued_by":   uid.String(),
		})
		status, respBody, _, err := h.issueViaBroker(r.Context(), body)
		if err != nil {
			res.Error = "broker unreachable: " + err.Error()
			results = append(results, res)
			failed++
			continue
		}
		var brokerResp struct {
			Code      string `json:"code"`
			ExpiresAt string `json:"expires_at"`
			Error     string `json:"error"`
		}
		_ = json.Unmarshal(respBody, &brokerResp)
		if status != http.StatusOK || brokerResp.Code == "" {
			res.Error = nonEmpty(brokerResp.Error, "issue failed")
			results = append(results, res)
			failed++
			continue
		}
		res.Code = brokerResp.Code
		res.ExpiresAt = brokerResp.ExpiresAt
		issued++

		if req.Wake {
			m, err := h.store.GetMachine(r.Context(), machineID)
			switch {
			case err != nil:
				res.Error = "wake skipped: machine lookup failed"
			case m.MACPrimary == nil || *m.MACPrimary == "":
				res.Error = "wake skipped: no primary MAC"
			default:
				wakeID, err := h.store.CreateWakeRequest(r.Context(),
					machineID, *m.MACPrimary, machineSite(m), time.Time{}, &uid)
				if err != nil {
					res.Error = "wake skipped: " + err.Error()
				} else {
					res.WakeID = wakeID.String()
				}
			}
		}
		results = append(results, res)
	}

	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "deployment.bulk_issued",
		Data: mustJSON(map[string]any{
			"requested": len(ids), "issued": issued, "failed": failed,
			"profile_id": req.ProfileID, "wake": req.Wake,
		}),
	})
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
