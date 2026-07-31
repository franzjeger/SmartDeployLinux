// Linux golden-image restore: serve restore.sh (token-gated) and build
// the archive URL the rescue environment streams from.
//
// An image counts as a Linux golden archive when its os_family is
// linux, it has a captured/uploaded image_version blob, and its media
// declares `"deploy_method": "golden"` (set automatically by
// capture-complete for Linux targets; removable via PATCH /images/{id}
// to fall back to installer-media deployment).

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/your-org/deployserver/api/internal/store"
)

// restoreShBody is the canonical linux/scripts/restore.sh, embedded and
// sync-checked by TestRestoreShMatchesCanonical.
//
//go:embed restore.sh
var restoreShBody string

// mediaDeployMethod reads media.deploy_method ("" when unset).
func mediaDeployMethod(media []byte) string {
	if len(media) == 0 {
		return ""
	}
	var m struct {
		DeployMethod string `json:"deploy_method"`
	}
	_ = json.Unmarshal(media, &m)
	return m.DeployMethod
}

// isLinuxGolden reports whether the bundle deploys a golden archive
// rather than installer media.
func isLinuxGolden(b *store.RenderBundle) bool {
	return b.ImageOSFamily == "linux" && b.ImageBlobKey != "" &&
		mediaDeployMethod(b.ImageMedia) == "golden"
}

// goldenArchiveURL builds the URL the rescue environment streams the
// archive from: the deploy server's /blobs path (rewritten through the
// machine's site mirror when one exists — the Phase 18 edge cache
// sha-verifies /blobs objects, giving free integrity checking), falling
// back to a presigned S3 GET when no object store route is exposed.
func (h *handlers) goldenArchiveURL(ctx context.Context, b *store.RenderBundle) string {
	base := fmt.Sprintf("https://%s/blobs/%s", h.deployFQDN, b.ImageBlobKey)
	if mirror := h.siteMirrorFor(ctx, &b.Machine); mirror != "" {
		return mirrorRewrite(mirror, h.deployFQDN, base)
	}
	// No mirror: prefer a presigned GET straight to the object store so
	// the flow doesn't depend on nginx having the blob locally.
	if signer := blobSigner(); signer != nil && b.ImageBlobBucket != "" {
		if u, err := signer.Presign(http.MethodGet, b.ImageBlobBucket, b.ImageBlobKey, 6*time.Hour); err == nil {
			return u
		}
	}
	return base
}

// GET /v1/jobs/{id}/restore.sh — job-token gated; deploy jobs whose
// image is a Linux golden archive only (a capture token must not pull
// the restore script and vice versa).
func (h *handlers) restoreScript(w http.ResponseWriter, r *http.Request) {
	machineID, _, jobID, ok := h.authJobToken(w, r)
	if !ok {
		return
	}
	if jc, err := h.store.GetJobCapture(r.Context(), jobID); err != nil || jc.Kind != "deploy" {
		http.Error(w, "not a deploy job", http.StatusConflict)
		return
	}
	bundle, err := h.store.LookupRenderBundle(r.Context(), machineID)
	if err != nil || !isLinuxGolden(bundle) {
		http.Error(w, "job's image is not a linux golden archive", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript")
	fmt.Fprint(w, restoreShBody)
}
