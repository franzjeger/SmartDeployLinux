// Golden-image capture endpoints (job-token authenticated, WinPE-side).
//
//   POST /v1/jobs/{id}/capture-upload   {sha256, size_bytes}
//        → registers the blob, returns a presigned PUT URL
//   POST /v1/jobs/{id}/capture-complete {sha256}
//        → links the uploaded blob as a new version of the job's
//          capture target image and completes the job
//
// Both are gated exactly like the deploy-side WinPE endpoints
// (authJobToken: bearer one-shot token, job state bootstrapped/imaging)
// plus a kind=capture check so a deploy token can't mint image versions.

package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/store"
)

// captureCmdBody is the canonical capture.cmd, embedded like deploy.cmd
// and sync-checked by TestCaptureCmdMatchesCanonical.
//
//go:embed capture.cmd
var captureCmdBody string

// captureShBody is the Linux counterpart (linux/scripts/capture.sh),
// served at GET /v1/jobs/{id}/capture.sh for linux capture jobs.
//
//go:embed capture.sh
var captureShBody string

// GET /v1/jobs/{id}/capture.sh
func (h *handlers) captureScript(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.captureJob(w, r); !ok {
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript")
	fmt.Fprint(w, captureShBody)
}

// captureJob authenticates the request and asserts the job is a capture
// job, returning its capture target.
func (h *handlers) captureJob(w http.ResponseWriter, r *http.Request) (jobID uuid.UUID, jc *store.JobCapture, ok bool) {
	_, _, jobID, tokOK := h.authJobToken(w, r)
	if !tokOK {
		return uuid.Nil, nil, false
	}
	jc, err := h.store.GetJobCapture(r.Context(), jobID)
	if err != nil {
		http.Error(w, "job lookup", http.StatusInternalServerError)
		return uuid.Nil, nil, false
	}
	if jc.Kind != "capture" || jc.ImageID == nil {
		http.Error(w, "not a capture job", http.StatusConflict)
		return uuid.Nil, nil, false
	}
	return jobID, jc, true
}

type captureUploadReq struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// POST /v1/jobs/{id}/capture-upload
func (h *handlers) captureUpload(w http.ResponseWriter, r *http.Request) {
	jobID, _, ok := h.captureJob(w, r)
	if !ok {
		return
	}
	var req captureUploadReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	// certutil emits uppercase hex on some builds; normalize before the
	// format check so the capture script doesn't have to care.
	req.SHA256 = lowerHex(req.SHA256)
	if !sha256Re.MatchString(req.SHA256) {
		http.Error(w, "sha256 must be 64 hex chars", http.StatusBadRequest)
		return
	}
	if req.SizeBytes <= 0 {
		http.Error(w, "size_bytes required", http.StatusBadRequest)
		return
	}
	signer := blobSigner()
	if signer == nil {
		http.Error(w, "object store not configured", http.StatusServiceUnavailable)
		return
	}
	bucket, _ := bucketForRole("images")
	key := req.SHA256[:2] + "/" + req.SHA256 + "-capture"
	blob, err := h.store.CreateBlob(r.Context(), req.SHA256, req.SizeBytes, bucket, key)
	if err != nil {
		http.Error(w, "create blob: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Golden WIMs are large and WinPE links can be slow: 6h validity.
	uploadURL, err := signer.Presign(http.MethodPut, blob.S3Bucket, blob.S3Key, 6*time.Hour)
	if err != nil {
		http.Error(w, "presign: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorKind: "machine", Action: "capture.upload_started",
		SubjectID: &jobID, SubjectKind: "deployment_job",
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"blob_id":    blob.ID.String(),
		"upload_url": uploadURL,
	})
}

type captureCompleteReq struct {
	SHA256 string `json:"sha256"`
}

// POST /v1/jobs/{id}/capture-complete
func (h *handlers) captureComplete(w http.ResponseWriter, r *http.Request) {
	jobID, jc, ok := h.captureJob(w, r)
	if !ok {
		return
	}
	var req captureCompleteReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	blob, err := h.store.GetBlobBySHA(r.Context(), lowerHex(req.SHA256))
	if err != nil {
		http.Error(w, "unknown blob; call capture-upload first", http.StatusConflict)
		return
	}
	tag := jc.VersionTag
	if tag == "" {
		tag = "captured-" + time.Now().UTC().Format("20060102-150405")
	}
	versionID, err := h.store.CreateImageVersion(r.Context(), *jc.ImageID, blob.ID, tag)
	if err != nil {
		http.Error(w, "create version: "+err.Error(), http.StatusConflict)
		return
	}
	_ = h.store.AdvanceJob(r.Context(), jobID, "completed")
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorKind: "machine", Action: "capture.completed",
		SubjectID: jc.ImageID, SubjectKind: "image",
	})
	writeJSON(w, http.StatusOK, map[string]string{
		"version_id": versionID.String(), "version_tag": tag,
	})
}

func lowerHex(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'F' {
			b[i] = c + 32
		}
	}
	return string(b)
}
