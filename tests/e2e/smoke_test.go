// End-to-end smoke test of the deploy spine, black-box style: builds
// the real api binary, runs it against a real Postgres, and walks the
// full lifecycle over HTTP exactly as the stick, installer, and
// operator UI would — no internal packages imported.
//
//	operator API:  create image -> profile -> machine
//	redeem (SQL):  auth code + boot token + pending job (the broker's
//	               writes, seeded directly since Headscale isn't here)
//	boot:          GET /internal/render/by-token/<tok>  (dev-mode route)
//	install:       POST /v1/jobs/<id>/events  imaging -> completed
//	operator:      job shows completed with the event trail
//	security:      the token is dead after the job completes
//	capture:       a capture job's user-data runs capture.sh
//
// Gated on DEPLOY_TEST_PG_DSN like the store integration tests.

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const deployFQDN = "deploy.e2e.test"
const edgeWakeToken = "e2e-edge-wake-token"

// hashBootToken mirrors the broker/api scheme: sha256(pepper || 0x00 || token).
func hashBootToken(token, pepper string) string {
	h := sha256.New()
	h.Write([]byte(pepper))
	h.Write([]byte{0})
	h.Write([]byte(token))
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type harness struct {
	base string
	db   *pgx.Conn
	t    *testing.T
}

func startAPI(t *testing.T) *harness {
	t.Helper()
	dsn := os.Getenv("DEPLOY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("DEPLOY_TEST_PG_DSN not set; skipping e2e test")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("bad DSN: %v", err)
	}
	pass, _ := u.User.Password()
	host, port, _ := net.SplitHostPort(u.Host)

	bin := filepath.Join(t.TempDir(), "api")
	build := exec.Command("go", "build", "-o", bin, "./cmd/api")
	build.Dir = "../../services/api"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build api: %v\n%s", err, out)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cmd := exec.Command(bin, "serve")
	cmd.Env = append(os.Environ(),
		"POSTGRES_HOST="+host,
		"POSTGRES_PORT="+port,
		"POSTGRES_USER="+u.User.Username(),
		"POSTGRES_PASSWORD="+pass,
		"POSTGRES_DB="+strings.TrimPrefix(u.Path, "/"),
		"API_LISTEN="+addr,
		"API_PUBLIC_URL=http://"+addr,
		"DEPLOY_FQDN="+deployFQDN,
		"LOG_LEVEL=warn",
		// Force dev-mode paths: no OIDC, no internal mTLS certs.
		"OIDC_ISSUER=", "OIDC_CLIENT_ID=", "INTERNAL_TLS_CERT=/nonexistent",
		"EDGE_WAKE_TOKEN="+edgeWakeToken,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start api: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	base := "http://" + addr
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(base + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("api did not become ready within 30s")
		}
		time.Sleep(200 * time.Millisecond)
	}

	db, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatalf("db connect: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	return &harness{base: base, db: db, t: t}
}

func (h *harness) call(method, path, bearer string, body any) (int, map[string]any) {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.base+path, rdr)
	if err != nil {
		h.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"_raw": string(raw)}
	}
	return resp.StatusCode, out
}

func (h *harness) must(status int, wantStatus int, out map[string]any, what string) map[string]any {
	h.t.Helper()
	if status != wantStatus {
		h.t.Fatalf("%s: status %d (want %d): %v", what, status, wantStatus, out)
	}
	return out
}

// seedRedeem performs the writes the auth-broker would do at redeem
// time: user, auth code, boot token, pending job. Returns the cleartext
// token and job id.
func (h *harness) seedRedeem(machineID, profileID string, kind, captureImageID string) (token, jobID string) {
	h.t.Helper()
	ctx := context.Background()
	token = "e2etok_" + randHex(16)
	var userID, authCodeID string
	if err := h.db.QueryRow(ctx, `
		INSERT INTO users (email) VALUES ($1) RETURNING id`,
		randHex(6)+"@e2e.test").Scan(&userID); err != nil {
		h.t.Fatal(err)
	}
	var capImg *string
	if captureImageID != "" {
		capImg = &captureImageID
	}
	if err := h.db.QueryRow(ctx, `
		INSERT INTO auth_codes (code_hash, machine_id, profile_id, issued_by,
		                        expires_at, kind, capture_image_id)
		VALUES ($1, $2, $3, $4, now() + '1h'::interval, $5, $6)
		RETURNING id`,
		"e2e-"+randHex(8), machineID, profileID, userID, kind, capImg).Scan(&authCodeID); err != nil {
		h.t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		INSERT INTO one_shot_tokens (auth_code_id, token_hash, purpose, expires_at)
		VALUES ($1, $2, 'boot', now() + '1h'::interval) RETURNING id`,
		authCodeID, hashBootToken(token, deployFQDN)).Scan(new(string)); err != nil {
		h.t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		INSERT INTO deployment_jobs (machine_id, profile_id, auth_code_id, state,
		                             kind, capture_image_id)
		VALUES ($1, $2, $3, 'pending', $4, $5) RETURNING id`,
		machineID, profileID, authCodeID, kind, capImg).Scan(&jobID); err != nil {
		h.t.Fatal(err)
	}
	return token, jobID
}

// setupFixtures creates image + profile + machine over the operator API
// and returns their ids.
func (h *harness) setupFixtures(suffix string) (imageID, profileID, machineID string) {
	h.t.Helper()
	status, img := h.call("POST", "/api/v1/images", "", map[string]any{
		"name": "e2e-ubuntu-" + suffix, "os_family": "linux",
		"os_version": "24.04", "arch": "amd64",
		"media": map[string]any{
			"kernel_url": "https://mirror.test/vmlinuz",
			"initrd_url": "https://mirror.test/initrd",
		},
	})
	h.must(status, 201, img, "create image")
	imageID, _ = img["id"].(string)

	status, prof := h.call("POST", "/api/v1/profiles", "", map[string]any{
		"name": "e2e-prof-" + suffix, "image_id": imageID,
	})
	h.must(status, 201, prof, "create profile")
	profileID, _ = prof["id"].(string)

	status, m := h.call("POST", "/api/v1/machines", "", map[string]any{
		"asset_tag": "e2e-" + suffix, "default_profile_id": profileID,
	})
	h.must(status, 201, m, "create machine")
	machineID, _ = m["ID"].(string)

	if imageID == "" || profileID == "" || machineID == "" {
		h.t.Fatalf("fixture ids missing: img=%q prof=%q machine=%q", imageID, profileID, machineID)
	}
	return imageID, profileID, machineID
}

func TestDeploySpineEndToEnd(t *testing.T) {
	h := startAPI(t)
	_, profileID, machineID := h.setupFixtures(randHex(4))

	// --- stick redeems; broker's DB writes seeded directly -----------
	token, jobID := h.seedRedeem(machineID, profileID, "deploy", "")

	// --- machine boots: iPXE chain fetch by token --------------------
	status, render := h.call("GET", "/internal/render/by-token/"+token, "", nil)
	h.must(status, 200, render, "render by token")
	if render["one_shot_token"] != token {
		t.Fatalf("render did not echo the boot token: %v", render["one_shot_token"])
	}
	if render["job_id"] != jobID {
		t.Fatalf("render bound to wrong job: %v vs %v", render["job_id"], jobID)
	}
	imgBlock, _ := render["image"].(map[string]any)
	if imgBlock == nil || imgBlock["kernel_url"] != "https://mirror.test/vmlinuz" {
		t.Fatalf("render image media wrong: %v", render["image"])
	}

	// The boot fetch advanced the job out of pending.
	status, job := h.call("GET", "/api/v1/jobs/"+jobID, "", nil)
	h.must(status, 200, job, "get job after boot")
	if j, _ := job["job"].(map[string]any); j["state"] != "bootstrapped" {
		t.Fatalf("job state after boot: %v", j["state"])
	}

	// --- user-data renders with the phone-home token -----------------
	udReq, _ := http.NewRequest("GET", h.base+"/internal/render/by-token/"+token+"/user-data", nil)
	udResp, err := http.DefaultClient.Do(udReq)
	if err != nil {
		t.Fatal(err)
	}
	ud, _ := io.ReadAll(udResp.Body)
	udResp.Body.Close()
	if udResp.StatusCode != 200 || !strings.Contains(string(ud), "Bearer "+token) {
		t.Fatalf("user-data missing bearer (status %d):\n%s", udResp.StatusCode, ud)
	}

	// --- installer phones home ---------------------------------------
	status, out := h.call("POST", "/v1/jobs/"+jobID+"/events", token,
		map[string]any{"phase": "imaging", "message": "e2e imaging"})
	h.must(status, 202, out, "phone-home imaging")
	// Repeated events must keep working (non-consuming token).
	status, out = h.call("POST", "/v1/jobs/"+jobID+"/events", token,
		map[string]any{"phase": "imaging", "message": "e2e imaging 2"})
	h.must(status, 202, out, "phone-home imaging repeat")
	status, out = h.call("POST", "/v1/jobs/"+jobID+"/events", token,
		map[string]any{"phase": "completed", "message": "e2e done"})
	h.must(status, 202, out, "phone-home completed")

	// --- operator sees the completed job with its event trail --------
	status, job = h.call("GET", "/api/v1/jobs/"+jobID, "", nil)
	h.must(status, 200, job, "get job final")
	j, _ := job["job"].(map[string]any)
	if j["state"] != "completed" {
		t.Fatalf("final state: %v", j["state"])
	}
	events, _ := job["events"].([]any)
	if len(events) < 3 {
		t.Fatalf("expected >=3 events, got %d", len(events))
	}

	// --- the token died with the job ---------------------------------
	status, _ = h.call("GET", "/internal/render/by-token/"+token, "", nil)
	if status != 410 {
		t.Fatalf("token still alive after job completion: %d", status)
	}
	status, _ = h.call("POST", "/v1/jobs/"+jobID+"/events", token,
		map[string]any{"phase": "imaging", "message": "zombie"})
	if status != 401 {
		t.Fatalf("phone-home accepted a dead token: %d", status)
	}

	// --- reporting layer sees the completed job ----------------------
	status, sum := h.call("GET", "/api/v1/reports/summary?since=24h", "", nil)
	h.must(status, 200, sum, "report summary")
	if c, _ := sum["completed"].(float64); c < 1 {
		t.Fatalf("summary missed the completed job: %v", sum)
	}
	csvResp, err := http.Get(h.base + "/api/v1/reports/jobs.csv?since=24h")
	if err != nil {
		t.Fatal(err)
	}
	csvBody, _ := io.ReadAll(csvResp.Body)
	csvResp.Body.Close()
	if csvResp.StatusCode != 200 || !strings.Contains(string(csvBody), jobID) {
		t.Fatalf("csv export missing job (status %d):\n%.500s", csvResp.StatusCode, csvBody)
	}

	// --- observability rode along ------------------------------------
	mResp, err := http.Get(h.base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metrics, _ := io.ReadAll(mResp.Body)
	mResp.Body.Close()
	if !strings.Contains(string(metrics), `http_requests_total{group="phone_home"`) {
		t.Fatal("phone_home metrics missing from /metrics")
	}
}

func TestCaptureJobUserData(t *testing.T) {
	h := startAPI(t)
	imageID, profileID, machineID := h.setupFixtures(randHex(4))

	token, jobID := h.seedRedeem(machineID, profileID, "capture", imageID)

	// The machine chains through the iPXE render first (this advances
	// the job to bootstrapped, which the WinPE-style endpoints require).
	status0, render := h.call("GET", "/internal/render/by-token/"+token, "", nil)
	h.must(status0, 200, render, "render by token (capture)")

	// A capture job's user-data must run capture.sh, not an installer.
	resp, err := http.Get(h.base + "/internal/render/by-token/" + token + "/user-data")
	if err != nil {
		t.Fatal(err)
	}
	ud, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("user-data status %d: %s", resp.StatusCode, ud)
	}
	for _, want := range []string{"capture.sh", "DEPLOY_TOKEN=" + token, "DEPLOY_JOB=" + jobID} {
		if !strings.Contains(string(ud), want) {
			t.Fatalf("capture user-data missing %q:\n%s", want, ud)
		}
	}
	if strings.Contains(string(ud), "autoinstall") {
		t.Fatalf("capture user-data must not run the installer:\n%s", ud)
	}

	// And the token-gated script itself is served.
	status, _ := h.call("GET", "/v1/jobs/"+jobID+"/capture.sh", token, nil)
	if status != 200 {
		t.Fatalf("capture.sh fetch: %d", status)
	}
	// Without the token it is not.
	status, _ = h.call("GET", "/v1/jobs/"+jobID+"/capture.sh", "", nil)
	if status == 200 {
		t.Fatal("capture.sh served without a token")
	}
}

func TestSiteMirrorRewritesMediaURLs(t *testing.T) {
	h := startAPI(t)
	suffix := randHex(4)
	site := "e2e-mirror-" + suffix
	mirror := "http://192.168.99.2:8090"

	// Operator registers the site's edge mirror.
	status, s := h.call("PUT", "/api/v1/sites", "", map[string]any{
		"name": site, "mirror_base_url": mirror,
	})
	h.must(status, 200, s, "upsert site")

	// Fixtures, then re-home the machine to the site.
	_, profileID, machineID := h.setupFixtures(suffix)
	status, m := h.call("PATCH", "/api/v1/machines/"+machineID, "", map[string]any{
		"attributes": map[string]any{"site": site},
	})
	h.must(status, 200, m, "set machine site")

	// Point the image's media at the deploy server's own /static path so
	// the rewrite has something to act on.
	imgs, _ := m["ID"].(string)
	_ = imgs
	token, _ := h.seedRedeem(machineID, profileID, "deploy", "")

	// The fixture image uses third-party mirror.test URLs — those must
	// NOT be rewritten. Verify passthrough first.
	status, render := h.call("GET", "/internal/render/by-token/"+token, "", nil)
	h.must(status, 200, render, "render (third-party media)")
	img, _ := render["image"].(map[string]any)
	if img["kernel_url"] != "https://mirror.test/vmlinuz" {
		t.Fatalf("third-party URL rewritten: %v", img["kernel_url"])
	}

	// Now switch the image media to deploy-server paths and re-render.
	resp, err := http.Get(h.base + "/api/v1/images")
	if err != nil {
		t.Fatal(err)
	}
	var imgsList []map[string]any
	err = json.NewDecoder(resp.Body).Decode(&imgsList)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	imageID := ""
	for _, im := range imgsList {
		if im["name"] == "e2e-ubuntu-"+suffix {
			imageID, _ = im["id"].(string)
		}
	}
	if imageID == "" {
		t.Fatal("fixture image not found")
	}
	status, out := h.call("PATCH", "/api/v1/images/"+imageID, "", map[string]any{
		"media": map[string]any{
			"kernel_url": "https://" + deployFQDN + "/static/linux-24.04/vmlinuz",
			"initrd_url": "https://" + deployFQDN + "/static/linux-24.04/initrd",
		},
	})
	h.must(status, 200, out, "patch image media")

	status, render = h.call("GET", "/internal/render/by-token/"+token, "", nil)
	h.must(status, 200, render, "render (deploy-server media)")
	img, _ = render["image"].(map[string]any)
	if img["kernel_url"] != mirror+"/static/linux-24.04/vmlinuz" {
		t.Fatalf("kernel_url not mirrored: %v", img["kernel_url"])
	}
	if img["initrd_url"] != mirror+"/static/linux-24.04/initrd" {
		t.Fatalf("initrd_url not mirrored: %v", img["initrd_url"])
	}
}

// TestSSELiveStream proves the full push path: a phone-home lands on
// the api, rides Postgres LISTEN/NOTIFY through the event bus, and
// arrives on an open SSE stream — the exact flow the HA tier depends on
// (in HA the POST and the stream are on different replicas; here they
// share one process but the Postgres hop is the same).
func TestSSELiveStream(t *testing.T) {
	h := startAPI(t)
	_, profileID, machineID := h.setupFixtures(randHex(4))
	token, jobID := h.seedRedeem(machineID, profileID, "deploy", "")

	// Boot (advances the job so phone-homes are accepted).
	status, render := h.call("GET", "/internal/render/by-token/"+token, "", nil)
	h.must(status, 200, render, "render")

	// Open the SSE stream.
	req, _ := http.NewRequest("GET", h.base+"/api/v1/jobs/"+jobID+"/events/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stream status %d", resp.StatusCode)
	}

	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	// Wait for the backfill marker, then phone home and expect the event
	// to arrive live over the stream (via the Postgres bus).
	waitFor := func(substr string, timeout time.Duration) {
		t.Helper()
		deadline := time.After(timeout)
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("stream closed while waiting for %q", substr)
				}
				if strings.Contains(line, substr) {
					return
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %q on SSE stream", substr)
			}
		}
	}
	waitFor("synced", 10*time.Second)

	status, out := h.call("POST", "/v1/jobs/"+jobID+"/events", token,
		map[string]any{"phase": "imaging", "message": "sse-live-marker"})
	h.must(status, 202, out, "phone-home during stream")
	waitFor("sse-live-marker", 10*time.Second)
}

func TestLinuxGoldenRestoreUserData(t *testing.T) {
	h := startAPI(t)
	suffix := randHex(4)
	imageID, profileID, machineID := h.setupFixtures(suffix)

	// Turn the fixture image into a golden archive: uploaded version +
	// deploy_method flag (what capture-complete sets automatically).
	ctx := context.Background()
	var blobID string
	if err := h.db.QueryRow(ctx, `
		INSERT INTO blobs (sha256, size_bytes, s3_bucket, s3_key)
		VALUES ($1, 1234, 'images', $2) RETURNING id`,
		randHex(32), "aa/"+randHex(32)+"-golden.tar.zst").Scan(&blobID); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(ctx, `
		INSERT INTO image_versions (image_id, blob_id, version_tag)
		VALUES ($1, $2, 'gold-1') RETURNING id`, imageID, blobID).Scan(new(string)); err != nil {
		t.Fatal(err)
	}
	status, out := h.call("PATCH", "/api/v1/images/"+imageID, "", map[string]any{
		"media": map[string]any{"deploy_method": "golden"},
	})
	h.must(status, 200, out, "flag image golden")

	// Deploy job: user-data must be the RESTORE cloud-init.
	token, jobID := h.seedRedeem(machineID, profileID, "deploy", "")
	status, render := h.call("GET", "/internal/render/by-token/"+token, "", nil)
	h.must(status, 200, render, "render golden deploy")

	resp, err := http.Get(h.base + "/internal/render/by-token/" + token + "/user-data")
	if err != nil {
		t.Fatal(err)
	}
	ud, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"restore.sh", "DEPLOY_ARCHIVE_URL", "DEPLOY_TOKEN=" + token} {
		if !strings.Contains(string(ud), want) {
			t.Fatalf("restore user-data missing %q:\n%s", want, ud)
		}
	}
	if strings.Contains(string(ud), "autoinstall") || strings.Contains(string(ud), "capture.sh") {
		t.Fatalf("wrong branch selected:\n%s", ud)
	}

	// restore.sh is served for the deploy token…
	status, _ = h.call("GET", "/v1/jobs/"+jobID+"/restore.sh", token, nil)
	if status != 200 {
		t.Fatalf("restore.sh fetch: %d", status)
	}

	// Default profile → plain layout exported.
	if !strings.Contains(string(ud), "DEPLOY_LAYOUT=plain") {
		t.Fatalf("default layout missing:\n%s", ud)
	}

	// An LVM profile threads layout vars into the restore environment.
	if _, err := h.db.Exec(ctx, `
		UPDATE deployment_profiles
		SET answer_file_vars = '{"restore_layout":"lvm","restore_vg":"vg_sys","restore_swap":"4G"}'::jsonb
		WHERE id = $1`, profileID); err != nil {
		t.Fatal(err)
	}
	resp, err = http.Get(h.base + "/internal/render/by-token/" + token + "/user-data")
	if err != nil {
		t.Fatal(err)
	}
	udLVM, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(udLVM), "DEPLOY_LAYOUT=lvm DEPLOY_VG=vg_sys DEPLOY_SWAP=4G") {
		t.Fatalf("lvm layout env missing:\n%s", udLVM)
	}

	// …but a CAPTURE job on the same golden image must still capture
	// (branch order: capture wins), and its token must not pull restore.sh.
	capToken, capJobID := h.seedRedeem(machineID, profileID, "capture", imageID)
	status, _ = h.call("GET", "/internal/render/by-token/"+capToken, "", nil)
	if status != 200 {
		t.Fatalf("capture render: %d", status)
	}
	resp, err = http.Get(h.base + "/internal/render/by-token/" + capToken + "/user-data")
	if err != nil {
		t.Fatal(err)
	}
	capUD, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(capUD), "capture.sh") || strings.Contains(string(capUD), "restore.sh") {
		t.Fatalf("capture job did not get capture user-data:\n%s", capUD)
	}
	status, _ = h.call("GET", "/v1/jobs/"+capJobID+"/restore.sh", capToken, nil)
	if status == 200 {
		t.Fatal("capture token pulled restore.sh")
	}
}

func TestUserRoleAdminFlow(t *testing.T) {
	h := startAPI(t)
	ctx := context.Background()

	var userID string
	if err := h.db.QueryRow(ctx, `
		INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"e2e-"+randHex(4)+"@example.com").Scan(&userID); err != nil {
		t.Fatal(err)
	}

	status, out := h.call("POST", "/api/v1/users/"+userID+"/roles", "", map[string]any{"role": "operator"})
	if status != 204 {
		t.Fatalf("grant: %d %v", status, out)
	}
	// Unknown role → 400.
	status, _ = h.call("POST", "/api/v1/users/"+userID+"/roles", "", map[string]any{"role": "warlord"})
	if status != 400 {
		t.Fatalf("unknown role: %d", status)
	}

	// Make this user the ONLY admin, then verify the lockout guard.
	if _, err := h.db.Exec(ctx, `
		DELETE FROM user_roles WHERE role_id = '00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	status, _ = h.call("POST", "/api/v1/users/"+userID+"/roles", "", map[string]any{"role": "admin"})
	if status != 204 {
		t.Fatalf("grant admin: %d", status)
	}
	status, out = h.call("DELETE", "/api/v1/users/"+userID+"/roles/admin", "", nil)
	if status != 409 {
		t.Fatalf("last-admin guard: got %d %v, want 409", status, out)
	}
	// Non-admin roles still revocable.
	status, _ = h.call("DELETE", "/api/v1/users/"+userID+"/roles/operator", "", nil)
	if status != 204 {
		t.Fatalf("revoke operator: %d", status)
	}
}

func TestWakeOnLANQueue(t *testing.T) {
	h := startAPI(t)
	suffix := randHex(4)

	status, m := h.call("POST", "/api/v1/machines", "", map[string]any{
		"asset_tag":   "e2e-wake-" + suffix,
		"mac_primary": "02:e2:e2:00:11:22",
		"attributes":  map[string]any{"site": "e2e-site-" + suffix},
	})
	h.must(status, 201, m, "create machine")
	machineID, _ := m["ID"].(string)

	// Operator queues a wake; site resolved from the machine attribute.
	status, wake := h.call("POST", "/api/v1/machines/"+machineID+"/wake", "", nil)
	h.must(status, 202, wake, "queue wake")
	if wake["site"] != "e2e-site-"+suffix {
		t.Fatalf("site not resolved from attributes: %v", wake["site"])
	}

	// Edge agent drains its site's queue with the shared token.
	status, q := h.call("GET",
		"/v1/edge/wake-queue?site=e2e-site-"+suffix+"&agent=e2e-edge", edgeWakeToken, nil)
	h.must(status, 200, q, "edge queue drain")
	wakes, _ := q["wakes"].([]any)
	if len(wakes) != 1 {
		t.Fatalf("drained %d wakes, want 1: %v", len(wakes), q)
	}
	first, _ := wakes[0].(map[string]any)
	if first["mac"] != "02:e2:e2:00:11:22" {
		t.Fatalf("wrong mac in queue: %v", first)
	}

	// Second drain is empty (one-shot claim); wrong token is a 404.
	status, q = h.call("GET",
		"/v1/edge/wake-queue?site=e2e-site-"+suffix, edgeWakeToken, nil)
	h.must(status, 200, q, "second drain")
	if wakes, _ := q["wakes"].([]any); len(wakes) != 0 {
		t.Fatalf("double delivery: %v", wakes)
	}
	status, _ = h.call("GET", "/v1/edge/wake-queue", "wrong-token", nil)
	if status != 404 {
		t.Fatalf("bad token: got %d, want 404", status)
	}
}
