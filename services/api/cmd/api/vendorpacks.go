// Vendor driver-pack fetching (docs/DRIVERPACK_VENDOR_FETCH.md, #18).
//
// GET  /api/v1/vendor-driverpacks?q=...   search the vendor catalogs
// POST /api/v1/vendor-driverpacks/fetch   queue a download+ingest job
// GET  /api/v1/vendor-driverpacks/jobs    recent jobs with status
//
// The fetch itself runs inside this process (startVendorFetchRunner),
// not the worker: packs are 0.5-2 GiB streams into the blob store, and
// everything the pipeline needs — store, S3 signing, the driverpack
// rule model — already lives here. The queue is a table, so jobs
// survive restarts; a stale-heartbeat claim reclaims interrupted ones.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/your-org/deployserver/api/internal/auth"
	"github.com/your-org/deployserver/api/internal/s3sign"
	"github.com/your-org/deployserver/api/internal/store"
	"github.com/your-org/deployserver/api/internal/vendorcatalog"
)

var vendorCat = &vendorcatalog.Client{}

func (h *handlers) searchVendorPacks(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "driverpack.read") && !auth.HasPerm(r.Context(), "*") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	entries, err := vendorCat.Entries(r.Context())
	if err != nil {
		http.Error(w, "vendor catalog: "+err.Error(), http.StatusBadGateway)
		return
	}
	res := vendorcatalog.Search(entries, r.URL.Query().Get("q"))
	if len(res) > 100 {
		res = res[:100]
	}
	writeJSON(w, http.StatusOK, res)
}

type vendorFetchReq struct {
	URL string `json:"url"` // must be an entry URL from the search response
}

func (h *handlers) fetchVendorPack(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "driverpack.write") && !auth.HasPerm(r.Context(), "*") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if internalBlobSigner() == nil {
		http.Error(w, "blob store not configured (enable minio / set S3_* env)", http.StatusServiceUnavailable)
		return
	}
	var req vendorFetchReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	// The URL is a lookup key into the live catalog, not a free-form
	// input: the server only ever downloads addresses it read from the
	// vendor's own catalog. Accepting arbitrary URLs here would turn
	// this endpoint into "make my server download and store anything".
	entries, err := vendorCat.Entries(r.Context())
	if err != nil {
		http.Error(w, "vendor catalog: "+err.Error(), http.StatusBadGateway)
		return
	}
	var entry *vendorcatalog.Entry
	for i := range entries {
		if entries[i].URL == req.URL {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		http.Error(w, "url not in vendor catalog", http.StatusBadRequest)
		return
	}
	id, err := h.store.CreateVendorFetchJob(r.Context(), store.VendorFetchJob{
		Vendor: entry.Vendor, Model: entry.Model, MTypes: entry.Types,
		OSFamily: entry.OSFamily, OSVersion: entry.OSVersion,
		URL: entry.URL, ExpectedSHA: entry.SHA256,
	}, auth.UserID(r.Context()))
	if err != nil {
		http.Error(w, "queue: "+err.Error(), http.StatusInternalServerError)
		return
	}
	uid := auth.UserID(r.Context())
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorID: &uid, ActorKind: "user", Action: "driverpack.vendor_fetch_queued",
		SubjectID: &id, SubjectKind: "vendor_fetch_job",
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": id})
}

func (h *handlers) listVendorFetchJobs(w http.ResponseWriter, r *http.Request) {
	if !auth.HasPerm(r.Context(), "driverpack.read") && !auth.HasPerm(r.Context(), "*") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	jobs, err := h.store.ListVendorFetchJobs(r.Context(), parseLimit(r.URL.Query().Get("limit"), 50))
	if err != nil {
		http.Error(w, "list: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

// --- the runner --------------------------------------------------------

// internalBlobSigner signs against S3_ENDPOINT (the in-cluster address),
// unlike ingest's blobSigner which prefers S3_PUBLIC_ENDPOINT for
// operator browsers. This process talks to MinIO directly.
func internalBlobSigner() *s3sign.Signer {
	if os.Getenv("S3_ENDPOINT") == "" || os.Getenv("S3_ACCESS_KEY") == "" {
		return nil
	}
	return &s3sign.Signer{
		Endpoint:  os.Getenv("S3_ENDPOINT"),
		Region:    getenv("S3_REGION", "us-east-1"),
		AccessKey: os.Getenv("S3_ACCESS_KEY"),
		SecretKey: os.Getenv("S3_SECRET_KEY"),
	}
}

func startVendorFetchRunner(ctx context.Context, st *store.Store) {
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				job, err := st.ClaimVendorFetchJob(ctx)
				if err != nil {
					slog.Error("vendor fetch claim", "err", err)
					continue
				}
				if job == nil {
					continue
				}
				runVendorFetch(ctx, st, job)
			}
		}
	}()
}

func runVendorFetch(ctx context.Context, st *store.Store, job *store.VendorFetchJob) {
	log := slog.With("job", job.ID, "model", job.Model, "os", job.OSVersion)
	log.Info("vendor fetch started", "url", job.URL)

	fail := func(stage string, err error) {
		log.Error("vendor fetch failed", "stage", stage, "err", err)
		_ = st.FailVendorFetchJob(context.WithoutCancel(ctx), job.ID, stage+": "+err.Error())
	}

	signer := internalBlobSigner()
	if signer == nil {
		fail("config", fmt.Errorf("blob store not configured"))
		return
	}

	// Download stream from the vendor CDN.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, job.URL, nil)
	if err != nil {
		fail("request", err)
		return
	}
	resp, err := (&http.Client{Timeout: 45 * time.Minute}).Do(req)
	if err != nil {
		fail("download", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail("download", fmt.Errorf("HTTP %d from vendor", resp.StatusCode))
		return
	}
	if resp.ContentLength <= 0 {
		// The presigned PUT needs a length; every vendor CDN sends one.
		fail("download", fmt.Errorf("vendor response has no Content-Length"))
		return
	}

	// Stream vendor→MinIO, hashing on the way through. Nothing touches
	// disk and memory stays flat regardless of pack size.
	bucket := getenv("S3_BUCKET_DRIVERS", "drivers")
	key := fmt.Sprintf("vendor/%s/%s", strings.ToLower(job.Vendor), path.Base(job.URL))
	putURL, err := signer.Presign(http.MethodPut, bucket, key, 2*time.Hour)
	if err != nil {
		fail("presign", err)
		return
	}
	hasher := sha256.New()
	stopHeartbeat := make(chan struct{})
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-t.C:
				_ = st.HeartbeatVendorFetchJob(ctx, job.ID)
			}
		}
	}()
	defer close(stopHeartbeat)

	put, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, io.TeeReader(resp.Body, hasher))
	if err != nil {
		fail("upload request", err)
		return
	}
	put.ContentLength = resp.ContentLength
	putResp, err := (&http.Client{Timeout: 45 * time.Minute}).Do(put)
	if err != nil {
		fail("upload", err)
		return
	}
	io.Copy(io.Discard, putResp.Body)
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		fail("upload", fmt.Errorf("HTTP %d from object store", putResp.StatusCode))
		return
	}

	// Verify against the catalog's checksum before anything references
	// the object. A mismatch means a corrupted or tampered download; the
	// object is best-effort deleted and the job fails loudly.
	gotSHA := hex.EncodeToString(hasher.Sum(nil))
	if gotSHA != job.ExpectedSHA {
		if delURL, derr := signer.Presign(http.MethodDelete, bucket, key, time.Minute); derr == nil {
			if dreq, derr := http.NewRequest(http.MethodDelete, delURL, nil); derr == nil {
				if dresp, derr := http.DefaultClient.Do(dreq); derr == nil {
					dresp.Body.Close()
				}
			}
		}
		fail("verify", fmt.Errorf("sha256 mismatch: catalog %s, downloaded %s", job.ExpectedSHA, gotSHA))
		return
	}

	// Blob + pack version + match rules, atomically on the pack side.
	blob, err := st.CreateBlob(ctx, gotSHA, resp.ContentLength, bucket, key)
	if err != nil {
		fail("blob", err)
		return
	}
	rules := []store.DriverRule{}
	for _, t := range job.MTypes {
		rules = append(rules, store.DriverRule{Type: "dmi-product-prefix", Value: t})
	}
	if len(rules) == 0 {
		// No machine types in the catalog entry — fall back to exact
		// model match so the pack is at least selectable.
		rules = append(rules, store.DriverRule{Type: "dmi-product", Value: job.Model})
	}
	// version_tag is unique per pack (vendor+model); the OS version is
	// exactly that granularity — one Lenovo pack per model per OS build.
	versionTag := job.OSVersion
	pvID, err := st.CreateDriverPackVersion(ctx, job.Vendor, job.Model,
		job.OSFamily, job.OSVersion, versionTag, blob.ID, rules)
	if err != nil {
		fail("pack version", err)
		return
	}
	if err := st.CompleteVendorFetchJob(context.WithoutCancel(ctx), job.ID, pvID, resp.ContentLength); err != nil {
		log.Error("vendor fetch: completion update", "err", err)
		return
	}
	log.Info("vendor fetch completed", "pack_version", pvID, "bytes", resp.ContentLength)
}

