// Image / blob ingest endpoints.
//
// Upload flow (SmartDeploy "import image", minus the wizard):
//   1. POST /api/v1/blobs {sha256, size_bytes, filename, role}
//      → registers the blob and returns a presigned PUT URL for the
//        object store. The operator (UI or deployctl) uploads the bytes
//        directly to S3/MinIO — the API never proxies image payloads.
//   2. POST /api/v1/images/{id}/versions {blob_id, version_tag}
//      → links the uploaded blob as a new version of the image. The
//        render pipeline picks the newest version automatically
//        (LookupRenderBundle orders by created_at DESC).

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/s3sign"
	"github.com/your-org/deployserver/api/internal/store"
)

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// blobSigner builds the presigner from env. Returns nil when the object
// store is not configured (supported deploy mode; ingest endpoints then
// respond 503 with a clear message).
func blobSigner() *s3sign.Signer {
	endpoint := getenv("S3_PUBLIC_ENDPOINT", os.Getenv("S3_ENDPOINT"))
	if endpoint == "" || os.Getenv("S3_ACCESS_KEY") == "" {
		return nil
	}
	return &s3sign.Signer{
		Endpoint:  endpoint,
		Region:    getenv("S3_REGION", "us-east-1"),
		AccessKey: os.Getenv("S3_ACCESS_KEY"),
		SecretKey: os.Getenv("S3_SECRET_KEY"),
	}
}

func bucketForRole(role string) (string, bool) {
	switch role {
	case "images", "":
		return getenv("S3_BUCKET_IMAGES", "images"), true
	case "drivers":
		return getenv("S3_BUCKET_DRIVERS", "drivers"), true
	case "blobs":
		return getenv("S3_BUCKET_BLOBS", "blobs"), true
	}
	return "", false
}

type createBlobReq struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	Filename  string `json:"filename"`
	Role      string `json:"role"` // images (default) | drivers | blobs
}

type createBlobResp struct {
	BlobID    string `json:"blob_id"`
	Bucket    string `json:"bucket"`
	Key       string `json:"key"`
	UploadURL string `json:"upload_url"`
	ExpiresIn int    `json:"expires_in_seconds"`
}

// POST /api/v1/blobs
func (h *handlers) createBlob(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "image.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req createBlobReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if !sha256Re.MatchString(req.SHA256) {
		http.Error(w, "sha256 must be 64 lowercase hex chars", http.StatusBadRequest)
		return
	}
	if req.SizeBytes <= 0 {
		http.Error(w, "size_bytes required", http.StatusBadRequest)
		return
	}
	bucket, ok := bucketForRole(req.Role)
	if !ok {
		http.Error(w, "role must be images, drivers, or blobs", http.StatusBadRequest)
		return
	}
	signer := blobSigner()
	if signer == nil {
		http.Error(w, "object store not configured (S3_ENDPOINT/S3_ACCESS_KEY)", http.StatusServiceUnavailable)
		return
	}

	// Content-addressed key with the operator's filename kept for
	// readability: <sha256-prefix>/<sha256>-<basename>.
	base := path.Base(req.Filename)
	if base == "" || base == "." || base == "/" {
		base = "blob"
	}
	key := fmt.Sprintf("%s/%s-%s", req.SHA256[:2], req.SHA256, base)

	blob, err := h.store.CreateBlob(r.Context(), req.SHA256, req.SizeBytes, bucket, key)
	if err != nil {
		http.Error(w, "create blob: "+err.Error(), http.StatusInternalServerError)
		return
	}
	const expiry = time.Hour
	uploadURL, err := signer.Presign(http.MethodPut, blob.S3Bucket, blob.S3Key, expiry)
	if err != nil {
		http.Error(w, "presign: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if u := auth.UserID(r.Context()); u != uuid.Nil {
		_ = h.store.Audit(r.Context(), store.AuditEvent{
			ActorID: &u, ActorKind: "user", Action: "blob.registered",
			SubjectID: &blob.ID, SubjectKind: "blob",
		})
	}
	writeJSON(w, http.StatusCreated, createBlobResp{
		BlobID: blob.ID.String(), Bucket: blob.S3Bucket, Key: blob.S3Key,
		UploadURL: uploadURL, ExpiresIn: int(expiry.Seconds()),
	})
}

type createImageVersionReq struct {
	BlobID     string `json:"blob_id"`
	VersionTag string `json:"version_tag"`
}

// POST /api/v1/images/{id}/versions
func (h *handlers) createImageVersion(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "image.write") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	imageID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad image id", http.StatusBadRequest)
		return
	}
	var req createImageVersionReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	blobID, err := uuid.Parse(req.BlobID)
	if err != nil {
		http.Error(w, "bad blob_id", http.StatusBadRequest)
		return
	}
	tag := req.VersionTag
	if tag == "" {
		tag = time.Now().UTC().Format("20060102-150405")
	}
	id, err := h.store.CreateImageVersion(r.Context(), imageID, blobID, tag)
	if err != nil {
		http.Error(w, "create version: "+err.Error(), http.StatusBadRequest)
		return
	}
	if u := auth.UserID(r.Context()); u != uuid.Nil {
		_ = h.store.Audit(r.Context(), store.AuditEvent{
			ActorID: &u, ActorKind: "user", Action: "image.version_added",
			SubjectID: &imageID, SubjectKind: "image",
		})
	}
	writeJSON(w, http.StatusCreated, map[string]string{
		"version_id": id.String(), "version_tag": tag,
	})
}

// GET /api/v1/images/{id}/versions
func (h *handlers) listImageVersions(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "image.read") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	imageID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "bad image id", http.StatusBadRequest)
		return
	}
	vs, err := h.store.ListImageVersions(r.Context(), imageID)
	if err != nil {
		http.Error(w, "list versions: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if vs == nil {
		vs = []store.ImageVersion{}
	}
	writeJSON(w, http.StatusOK, vs)
}
