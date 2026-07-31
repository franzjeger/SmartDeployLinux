# Implementation status

Honest tracker of what's delivered vs. designed. Updated end of every phase.

## What's verified in this checkout

| Verification | Result |
|---|---|
| `go build ./...` on all 6 Go services | clean |
| `go vet ./...` on all 6 Go services | clean |
| Unit tests (codes 13, auditlog 4, answerfile 5, auth 5, driverpack 9, tokens 2) | **38/38 pass** |
| Integration tests (store vs Postgres 16, 9 tests including new token path) | **9/9 pass** |
| `bash -n` / `sh -n` on all 8 bootstrap shell scripts | clean |
| Migration `0001_init.sql` applied to Postgres 16 | 21 tables created |
| Migration runner via `embed.FS` | idempotent, schema_migrations tracked |
| `deployctl --help` smoke-runs | clean |

What's NOT verified (requires sandbox-external resources):

- The bootstrap image has not been built or booted. Requires
  buildroot-2025.02 toolchain fetch + iPXE source + Tailscale + shim
  + grub binaries (some pinned-SHA-required), plus root for
  `losetup`/`mkfs`, plus `/dev/kvm` for nested-KVM e2e. See
  `docs/RUNBOOK_A_USB.md §"One-time setup"`.
- The auth-broker has not been started against a live Headscale; only
  unit-tested.
- WinPE pipeline has not been run end-to-end. Requires a Windows host
  with the Windows ADK + WinPE add-on. The PowerShell `build-boot-wim.ps1`
  is documented but not delivered.

## Phase 1 — Scope + risks
**Done.** Conversation transcript holds the writeup.

## Phase 2 — Architecture
**Done.** `docs/ARCHITECTURE.md` is the locked design contract.

## Phase 3 — Skeleton + root files
**Done.** `Makefile`, `docker-compose.yml`, `.env.example`,
`README.md` are all real and parameterized.

**Deferred:** `docker-bake.hcl`, `docker-compose.ha.yml`,
`docker-compose.observability.yml`. CI integration of `gosec`,
`govulncheck`, `trivy` referenced in Makefile not yet wired up.

## Phase 4 — USB bootstrap → Linux deploy spine
**Done within sandbox limits.** All code in place; not booted (see
top of doc for what would be required).

| Component | State |
|---|---|
| `docs/BOOTSTRAP.md` spec | done |
| `ipxe/` Makefile + embed.script + general.h patch + cert dir | done |
| Tailscale ACL JSON template | done |
| `bootstrap/Makefile` | done |
| `bootstrap/scripts/assemble-image.sh` | done |
| `bootstrap/buildroot/configs/bootstrap_slim_defconfig` | done |
| `bootstrap/buildroot/board/kernel-config-slim` | done |
| `bootstrap/buildroot/board/post-build.sh` | done |
| `bootstrap/external/fetch.sh` (shim/grub/tailscale fetcher) | done; **needs SHAs pinned** by user before first run |
| `bootstrap/keys/gen-mok.sh` (one-time MOK key generator) | done |
| `bootstrap/overlay/` (init script, deploy-bootstrap, exec-ipxe, config template) | done |
| `bootstrap/make-stick.sh` | done |
| `services/auth-broker/` Go service (issue, redeem, Headscale + Tailscale-SaaS clients, rate-limit, audit) | done |
| `services/auth-broker/internal/codes/codes_test.go` (9 tests) | done; **all passing** |
| `services/api/internal/migrations/0001_init.sql` (21 tables) | done; **applies clean to PG 16** |
| `services/api/cmd/api/{main,render}.go` | sketch (returns Ubuntu stub for the boot flow; Phase 8 replaces with DB) |
| `services/http-boot/` (nginx + Go renderer + Caddyfile front proxy) | done |

### Phase-4 known issues to fix when running

1. `services/api/cmd/api/render.go` returns hardcoded stub data; needs
   DB-backed handler in Phase 8.
2. `redeem_attempts` table grows without reaping; the worker's reap
   job is Phase 8.
3. UEFI `efibootmgr -c` + BootNext path needs real-hardware
   validation across firmware vendors.
4. **Security gap (also in `docs/SECURITY.md §4`):** `/boot/<id>.ipxe`
   is currently unauthenticated; should require a one-shot bootstrap
   token issued by `/redeem`. Phase 9.

## Phase 5 — Windows deploy
**Done within sandbox limits.**

| Component | State |
|---|---|
| `services/api/internal/driverpack/` matcher (Go) | done |
| `services/api/internal/driverpack/match_test.go` (9 tests) | done; **all passing** |
| `services/api/internal/answerfile/unattend.go` renderer | done |
| `services/api/internal/answerfile/unattend_test.go` (5 tests) | done; **all passing** |
| `winpe/scripts/startnet.cmd` | done |
| `winpe/scripts/deploy.cmd` | done |
| `docs/WINDOWS.md` | done |

### Phase-5 known issues

1. `winpe/scripts/build-boot-wim.ps1` is referenced in WINDOWS.md but
   not delivered (requires Windows ADK to test).
2. `deploy.cmd`'s PCI VID/DID parsing loop is incomplete; v1 ships
   DMI-only matching from WinPE, deferring PCI-from-WinPE to v2 with
   a Go helper injected into the boot.wim.
3. `/v1/winpe/*` endpoints token-bound but token is in query string
   (logs leak). Move to Authorization header in Phase 9.
4. ARM64 Windows: documented, not validated.

## Phase 6 — LAN PXE
**Done.**

| Component | State |
|---|---|
| `services/tftp/Dockerfile` | done |
| `services/tftp/dnsmasq.conf` (proxyDHCP+TFTP, per-arch) | done |
| `services/tftp/entrypoint.sh` (env-templated) | done |
| `docs/RUNBOOK_B_PXE.md` | done |

### Phase-6 known issues

1. iPXE binaries not built into the image; user runs
   `make build-ipxe && cp ipxe/build/* services/tftp/tftproot/` first.
2. Secure Boot via shim chain documented; not automated. v1 expects
   user disables Secure Boot in lab/DC, or accepts MOK enrollment.

## Phase 7 — Edge agent
**Done.**

| Component | State |
|---|---|
| `services/edge-agent/cmd/edge-agent/main.go` (Go) | done; **compiles clean** |
| `services/edge-agent/Dockerfile` (multi-arch) | done |
| `services/edge-agent/entrypoint.sh` | done |
| `docs/RUNBOOK_C_EDGE.md` | done |

### Phase-7 known issues

1. Code-redemption TUI in edge-agent reads from stdin (operator types
   into `docker exec`); not a friendly first-boot UX. A small HTTP UI
   on a local-only port would be better; deferred.

## Phase 8 — UI + API polish, RBAC, audit, stick generator UI
**Backend done; UI + CLI not started.**

| Component | State |
|---|---|
| Phase 8a: API DB layer (`internal/store`) | done |
| Phase 8a: embed.FS migrations runner + `api migrate up` subcommand | done; idempotent |
| Render endpoints DB-backed (was stub) | done |
| Machine create/list/get + audit list | done |
| Phase 8b: phone-home `/v1/jobs/{id}/events` with one-shot token verify | done |
| Phase 8c: worker (reap redeem_attempts, lock expired auth_codes) | done; compiles clean |
| Phase 8d: OIDC verifier + middleware + user resolver + RBAC perm checks | done |
| Store integration tests against Postgres 16 (8 tests) | passing |
| Auth unit tests (5 tests) | passing |
| SvelteKit UI | **NOT STARTED** |
| `deployctl` CLI | **scaffold done**: `machines list/create/get`, `deployments issue`, `audit query`, `bootstrap-sticks register/list`. No tests yet. |
| `seed-admin` subcommand | **stub** |
| Stick generator endpoint that wraps `make-stick.sh` | **NOT STARTED** |
| Code issuance via API (operator UI) | broker handler exists; CLI calls `/api/v1/bootstrap/issue-code` |

### Phase-8 known issues

1. The phone-home endpoint accepts `?token=` query string in addition
   to `Authorization: Bearer` for the legacy WinPE path. Logged in
   `docs/SECURITY.md §4 #2` for closure in Phase 9.
2. OIDC user-resolver is wired but only via `SetUserResolver(...)` at
   API startup; if OIDC is unconfigured, all permission checks pass
   (dev-mode escape hatch). Documented in `auth.HasPerm` docstring.
3. Worker has no tests yet (Phase 10).

## Phase 9 — Security hardening
**8 of 8 SECURITY.md §4 gaps closed.**

| Gap | State |
|---|---|
| #1 `/boot/<id>.ipxe` unauthenticated | ✅ Closed (token-bound `/boot/<token>.ipxe`; Phase 9a) |
| #2 token in query string for WinPE | ✅ Closed (`Authorization: Bearer`; Phase 9c) |
| #3 mTLS between services | ✅ Closed (api↔http-boot; Phase 9f) |
| #4 audit log mirror to file/syslog | ✅ Closed (`auditlog.Open` fan-out; Phase 9b) |
| #5 Headscale API key rotation | ✅ Closed (`scripts/rotate-headscale-key.sh` + OPERATIONS.md procedure; Phase 9g) |
| #6 MOK private key HSM handling | ✅ Closed (YubiKey + PKCS#11 + sbsign documented in SECURITY.md §4 #6; Phase 9h) |
| #7 per-operator issue rate limit | ✅ Closed (Phase 9d) |
| #8 plan/wim fetch replay protection | ✅ Closed (per-job state-gated WinPE endpoints; Phase 9e) |

### Phase-9 net new code

| Component | Tests |
|---|---|
| `auth-broker/internal/codes` boot-token generator + hash | 4 unit tests |
| `auth-broker/internal/auditlog` | 4 unit tests |
| `api/internal/auditlog` | mirror of broker package |
| `api/internal/tokens` boot-token hash (must match broker) | 2 unit tests |
| `api/internal/store` `LookupRenderBundleByToken` + `MarkBootTokensConsumedForJob` | 1 integration test |
| `api/cmd/api` token-route handlers `renderByToken`, `renderUserDataByToken`, `renderMetaDataByToken` | wired |
| `auth-broker` per-operator issue rate limit + DB counter | wired |
| http-boot nginx + render Go for `/boot/<token>.ipxe` | wired |
| WinPE `startnet.cmd` + `deploy.cmd` Authorization header | wired |

## Phase 10 — Tests + CI
**Partial.** Unit tests delivered for the security-critical primitives
(codes — 9 tests, driverpack — 9 tests, answerfile — 5 tests). End-to-end
nested-KVM harness scaffolded in `tests/e2e/` but not authored.

## Phase 12 — End-to-end correctness + hardening pass

**Done.** A full-code audit found and fixed defects that made the deploy
spine non-functional end-to-end, plus wired in the dormant Phase-5
libraries. All verified by unit + Postgres-backed integration tests.

### Correctness fixes (deploy spine)

| Defect | Fix |
|---|---|
| Phone-home hashed tokens with plain sha256 while broker mints peppered hashes → every event 401'd | `appendDeploymentEvent` now uses `tokens.HashBootToken` |
| Phone-home *consumed* the one-shot token on first event, breaking all later events | new non-consuming `store.VerifyPhoneHomeToken`, job-bound and terminal-state-gated |
| First "imaging" report hit an invalid pending→imaging transition | new `store.AdvanceJob` walks intermediate happy-path states; iPXE fetch also advances pending→bootstrapped |
| `OneShotToken` never populated in render responses → WinPE booted with empty bearer | `renderByToken` echoes the authenticated token |
| Linux nocloud URL used the machine UUID (renders 410 at the token route) | templates now use `/boot/<token>/` |
| iPXE templates used `html/template` (& → `&amp;`) and `\` line continuations (not iPXE syntax) | `text/template`, single-line commands; golden tests assert bootability |
| Served `deploy.cmd` was a stub that `exit /b 1` | real script embedded via `go:embed`, sync-checked against `winpe/scripts/deploy.cmd` by test |
| `image.wim` hardcoded to `/static/win11/install.wim` | resolved from the profile image's media / static convention |
| Driver matcher + unattend renderer were dead code | wired: `store.MatchDriverPacks` (DB rules → `driverpack.Select`), `/plan` + `/drivers.zip` return matched packs, `/unattend.xml` renders through `answerfile.Render` |
| `/plan` discarded the reported DMI/PCI fingerprint | persisted via `store.RecordMachineInventory` into `machines.attributes.hardware` (+ vendor/model backfill) |
| Seeded admin could never link on first OIDC login (unique-email collision → 500) | resolver does a two-step upsert that adopts the seeded row |
| Broker minted tokens after a failed job insert (orphaned deployments) | redeem now fails loudly if `CreateDeploymentJob` errors |
| API `readSourceIP` split IPv6 on the first `:` | uses `net.SplitHostPort` |
| compose/Caddyfile referenced a nonexistent `ui` service, wrong `http-boot-internal` hostname, mismatched `httpboot.pem` names, no caddy secrets mount | fixed; stack now describes only services that exist |

### Security hardening

- `?token=` query-string fallback removed for good (SECURITY.md §4 #2).
- Broker `/issue-code` now requires `X-Internal-Auth` shared secret
  (`AUTH_BROKER_ISSUE_SHARED_SECRET`, constant-time compare, 404 on
  mismatch); the api injects it when proxying issuance.
- RBAC checks added to previously-open read endpoints (profiles, images,
  jobs, catalog); migration `0004` seeds `job.read` for operator/viewer.
- Caddy → http-boot proxy now verifies the internal CA instead of
  `tls_insecure_skip_verify`.

### New capability

- `POST /api/v1/jobs/{id}/cancel` + UI cancel button (revokes boot tokens).
- `/readyz` (DB-gated readiness) and `/metrics` (Prometheus text format,
  dependency-free `internal/metrics`) on the api.
- Worker reaps consumed/expired `one_shot_tokens` (>24h).
- Built-in autoinstall user-data now supports per-profile overrides
  (`autoinstall`/`cloud-init` templates) and profile vars
  (`username`, `password_hash`, `ssh_authorized_key`); ships a locked
  password by default instead of the old `REPLACEME` literal.

### New tests

- `cmd/api`: bearer/query-string, IPv6 source IP, phase mapping,
  user-data token injection, deploy.cmd sync (5 tests).
- `http-boot/render`: iPXE bootability + token-datasource goldens (3).
- `auth-broker/broker`: issue-code secret middleware (2).
- `api/internal/metrics`: exposition + middleware (2).
- Store integration (Postgres): phone-home token repeatability,
  AdvanceJob, driver-pack matching via DB rules, inventory merge (4);
  test harness now cleans `users`/`bootstrap_sticks` so the suite is
  rerunnable against a persistent database.

## Phase 13 — Image ingest, stick-config generator, UI login

**Done.** The three "NOT STARTED" items from Phase 8's backlog.

### Image ingest (SmartDeploy "import image")

- `internal/s3sign`: dependency-free AWS SigV4 presigner (validated
  against the AWS-documented signature vector; works with MinIO path
  style and S3 virtual-host style).
- `POST /api/v1/blobs` registers a blob (idempotent on sha256) and
  returns a presigned PUT URL — image bytes go straight to the object
  store, never through the API.
- `POST/GET /api/v1/images/{id}/versions` links uploaded blobs as image
  versions; the render pipeline already picks the newest version.
- UI: image detail gains a Versions panel with chunked in-browser
  SHA-256 (incremental FIPS 180-4, verified against node's crypto —
  multi-GB WIMs hash without loading into memory), progress, direct
  presigned upload, and version linking.
- New env: `S3_PUBLIC_ENDPOINT` (host the operator's browser can reach),
  `S3_REGION`.

### Stick-config generator

- `GET /api/v1/bootstrap-sticks/config?tailnet=...` returns the rendered
  stick `config.json`, the pinned CA PEM (when `DEPLOY_CA_CERT_PATH`
  exists), and a copy-pasteable `make-stick.sh` command. Image assembly
  stays on the operator workstation (losetup needs root) but no
  parameter is hand-typed anymore.
- UI: "Generate stick config" on the Sticks page (modal + copy button).
- New env: `DEPLOY_TAILNET`, `DEPLOY_CA_CERT_PATH`.

### UI OIDC login (closes "UI works only in dev mode")

- `GET /api/v1/auth/config` (pre-auth) exposes issuer + client_id.
- SPA implements the authorization-code + PKCE flow in vanilla JS
  (S256 challenge, state check, sessionStorage ID token), attaches
  `Authorization: Bearer` to every API call, auto-restarts login on 401,
  and shows a log-out link.
- SSE: `EventSource` can't set headers, so the SPA mirrors the token
  into a `SameSite=Strict` cookie and the server promotes it to a
  bearer **only on the read-only SSE route** (`cookieBearerFallback`) —
  the rest of the API stays header-only (no CSRF surface).

### Tests

- s3sign: AWS known-answer vector, key escaping, determinism (3).
- auth-config + stick-config handlers, bucket role mapping (3).
- Store integration: blob idempotency, version linking + dup-tag
  rejection, version listing (1 test, 3 assertions ↑).
- JS SHA-256 validated against node crypto across block-boundary sizes
  and chunked input (build-time check, not committed as a test runner).

## Phase 14 — Golden-image capture, driver-pack ingest, deployctl upload

**Done.** The SmartDeploy-core capture workflow plus the remaining
ingest surfaces.

### Golden-image capture (WinPE)

- Migration `0005`: `kind` (`deploy`/`capture`) + capture target columns
  on `auth_codes` and `deployment_jobs`. Intent is set at code issuance
  and copied onto the job at redeem (broker `INSERT…SELECT`), so the
  stick/redeem/boot path is unchanged.
- `winpe/scripts/capture.cmd` (embedded + sync-tested like deploy.cmd):
  finds the offline sysprepped Windows volume, captures it with
  `DISM /Capture-Image /Compress:max`, hashes with certutil, registers
  the blob via `POST /v1/jobs/{id}/capture-upload` (presigned PUT, 6h),
  uploads with curl, finalizes via `POST /v1/jobs/{id}/capture-complete`
  — which links the blob as a new version of the target image and
  completes the job. Same bearer-token gating as deploy; a deploy token
  cannot reach the capture endpoints (kind check).
- `GET /v1/jobs/{id}/deploy.cmd` now serves capture.cmd for capture
  jobs — one boot.wim, server decides the script.
- Issue path: `POST /api/v1/deployments/issue` accepts
  `kind=capture` + `capture_image_id` + `capture_version_tag`
  (requires `image.write` on top of `deployment.create`).
- UI: "Capture golden image" on the machine page — pick target image,
  boot profile, version tag → 6-char capture code, tracked like any job.

### Driver-pack ingest

- `store.CreateDriverPackVersion` (atomic pack-upsert + version +
  rules), `ListDriverPacks`, `DeleteDriverPackVersion`.
- `GET/POST /api/v1/driver-packs`, `DELETE
  /api/v1/driver-packs/versions/{id}`; rule types validated; requires
  ≥1 match rule.
- UI: new **Driver packs** page — list with rules, delete, and an
  add-pack modal that hashes + uploads the .zip via the blob flow and
  binds match rules. Packs are matched by `/plan` immediately.

### deployctl

- `deployctl images list` and `deployctl images upload --image <id>
  --file install.wim [--tag v]`: streaming SHA-256, presigned PUT with
  no client timeout, version linking. The headless twin of the UI panel.

### Tests

- capture.cmd embed sync + content assertions, lowerHex (2).
- Store integration: capture kind/target roundtrip, driver-pack CRUD +
  matcher pickup + pack-identity dedup (2).
- Broker capture-copy `INSERT…SELECT` validated against the live
  Postgres 16 schema.

## Phase 15 — Linux capture + end-to-end smoke harness

**Done.** Capture now covers both OS families, and the `tests/e2e/`
harness promised since Phase 10 exists and runs in CI.

### Linux golden-image capture

- `linux/scripts/capture.sh` (embedded + sync-tested): finds the
  installed root filesystem, tars it (`--numeric-owner --xattrs --acls
  --one-file-system`, volatile paths pruned incl. machine-id and SSH
  host keys) with zstd/gzip to auto-discovered scratch space, then uses
  the same OS-agnostic capture-upload / capture-complete endpoints as
  WinPE.
- Served token-gated at `GET /v1/jobs/{id}/capture.sh`.
- Capture jobs on the Linux path get a **capture cloud-init** instead of
  an installer answer file — the live environment fetches and runs
  capture.sh rather than overwriting the machine it's supposed to
  archive.
- `writeJSON` no longer HTML-escapes (`&` in presigned URLs must survive
  capture.sh's sed-based JSON extraction).
- UI capture modal now offers Linux images too.

### End-to-end smoke harness (`tests/e2e/`)

Black-box: builds the real api binary, runs it against real Postgres,
and walks the full lifecycle over HTTP — operator creates
image/profile/machine, the broker's redeem writes are seeded via SQL,
then: iPXE render by token (echoes token, advances job), user-data with
bearer, repeated phone-homes, completed job with event trail, token
dead after completion, `/metrics` populated. A second test verifies
capture-job user-data runs capture.sh (and that the script is
token-gated). Wired into `make test-e2e` and a new CI job.

**The harness immediately caught a real bug:** in dev mode the internal
router was mounted at `/internal` while its routes already carried the
`/internal/...` prefix, so every render endpoint 404'd at
`/internal/internal/...`. The mTLS listener masked it. Routes are now
prefix-relative and mounted consistently on both listeners.

## Phase 16 — Wake-on-LAN scheduling via the edge agent

**Done.** The API can't reach remote L2 segments, so wakes are queued
centrally and broadcast by the edge agent that fronts the machine's LAN.

- Migration `0006`: `wake_requests` (machine, MAC snapshot, site,
  schedule, one-shot claim columns; partial index on due rows).
- Store: `CreateWakeRequest`, `ClaimDueWakeRequests` (atomic
  `UPDATE … FOR UPDATE SKIP LOCKED` — each request delivered to exactly
  one agent), `ListWakeRequests`, `ReapWakeRequests` (worker reaps
  claimed rows >7 days).
- API: `POST /api/v1/machines/{id}/wake` (`{at?, site?}`; site defaults
  to the machine's `attributes.site`; requires a primary MAC),
  `GET …/wake` history, and the edge drain
  `GET /v1/edge/wake-queue?site=&agent=` — shared-secret bearer
  (`EDGE_WAKE_TOKEN`), constant-time compare, **fails closed** when
  unconfigured (no dev-mode escape for an outward-facing action).
- Edge agent: WoL poller goroutine alongside dnsmasq (15s interval,
  no-op without `EDGE_WAKE_TOKEN`); magic-packet builder (6×0xFF +
  16×MAC), 3-packet burst to UDP :9 on `LAN_BROADCAST` or the limited
  broadcast. First tests in the edge-agent module.
- UI: Wake button on the machine page with optional schedule time and
  site override.
- Tests: magic-packet unit tests (2), wake-queue store integration test
  (due/future/site filtering, one-shot claim, history, reap), and an
  e2e test covering operator-queue → edge-drain → double-drain-empty →
  bad-token-404.

## Phase 17 — Machine management polish

**Done.**

- `PATCH /api/v1/machines/{id}` (was missing entirely): partial update
  of asset tag, MAC, vendor/model, attributes; `default_profile_id: ""`
  clears the profile vs. omitted = unchanged
  (`store.UpdateMachine` with `ClearDefaultProfile`).
- `Machine.Attributes` switched `[]byte` → `json.RawMessage` so API
  responses carry attributes as a JSON object (was base64) — unlocks
  site + hardware inventory in the UI.
- Machine page: Edit modal (incl. site + default profile), a Hardware
  inventory card rendering the DMI/PCI fingerprint recorded by `/plan`,
  and a Wake-request history card (queued vs. sent-by-agent).
- `deployctl machines wake <id> [--at] [--site]`.
- Tests: first deployctl tests (httptest-backed client: auth header,
  JSON round-trip, error-body surfacing, env validation) and a store
  integration test for partial update / attribute replacement /
  profile clearing.

## Phase 18 — Site-local image distribution (multicast-class)

**Done.** One WAN image transfer per site, N LAN deliveries.

**Design decision:** true UDP multicast (WDS-style) needs a reliable
multicast protocol plus custom receivers inside WinPE/installer
environments — fragile across switches/firmware and unverifiable here.
The edge box already fronts each remote LAN, so the same goal is met
with a verifying, caching HTTP mirror: the WAN leg happens once, the
LAN leg is unicast HTTP that every client already speaks and modern
switch fabrics handle at line rate. True multicast on the LAN leg
(uftp) can slot behind the same mirror later without touching the API.

- Migration `0007`: `sites` (name, mirror_base_url, description).
- Store: `UpsertSite` / `ListSites` / `DeleteSite` / `SiteMirror`.
- API: `GET/PUT /api/v1/sites`, `DELETE /api/v1/sites/{name}`
  (URL-validated); render pipeline (`bundleToRespForSite`) rewrites
  `/static/*` and `/blobs/*` media URLs to the machine's site mirror —
  applied on the iPXE render, WinPE `image.wim` redirect, `/plan`
  driver-pack URLs, and `drivers.zip`. Third-party URLs pass through.
- Edge agent: `EDGE_CACHE_LISTEN` starts the mirror
  (`EDGE_CACHE_DIR`, `EDGE_CACHE_MAX_BYTES`, default 50 GiB):
  fill-once semantics (concurrent PXE storm → one upstream fetch),
  byte-range serving from cache, **sha256 verification of
  content-addressed /blobs objects during fill** (tampered or corrupted
  WAN transfers are never served), path-escape rejection, oldest-first
  eviction under the size cap.
- UI: Sites page (list / add / edit / delete with mirror URL).
- Tests: 5 cache tests (fill-once under 20 concurrent clients, ranges,
  sha verification incl. tamper rejection, path escape, eviction),
  mirrorRewrite units, sites store integration, and an e2e test proving
  a machine homed to a mirrored site renders mirrored URLs while
  third-party URLs pass through.

## Phase 19 — Golden restore, user admin, bulk deploy, notifications, HA, sec-scan

**Done.** Architecture designed by a dedicated planning pass; six
features landed together. Migrations `0008` (user.read seed) and `0009`
(deployment_jobs.notified_at + partial index).

### 1. Linux golden-image restore (closes the Linux imaging loop)
- An image deploys as a golden archive when os_family=linux, it has an
  uploaded/captured version blob, and media declares
  `"deploy_method": "golden"` — set automatically by capture-complete
  for Linux targets, removable via `PATCH /images/{id}` to fall back to
  installer media.
- `linux/scripts/restore.sh` (embedded + sync-tested): pick largest
  fixed disk, GPT-partition (512 MiB ESP + ext4 root; v1 UEFI-only),
  stream-untar the archive (zstd/gzip), regenerate identity (hostname,
  machine-id, SSH host keys, fstab with new UUIDs), `grub-install
  --removable` + config rebuild, phone home, reboot.
- Archive URL prefers the site mirror (`/blobs` path — the Phase 18
  edge cache sha-verifies it), else a 6 h presigned S3 GET.
- `GET /v1/jobs/{id}/restore.sh` job-token gated; refuses capture jobs
  and non-golden images. `writeUserData` branch order (tested e2e):
  capture → golden restore → installer; capture always wins.

### 2. User & role management
- `GET /api/v1/users` (+roles), `GET /api/v1/roles`,
  `POST /users/{id}/roles`, `DELETE /users/{id}/roles/{role}`;
  `user.read` seeded for operator, `user.write` admin-only via `*`.
- Count-based last-admin lockout guard (409), which also covers
  removing the only other admin and works identity-less in dev mode.
- Users UI page: role chips, grant dropdown, revoke, role reference.

### 3. Bulk deployments
- `POST /api/v1/deployments/bulk {machine_ids≤100, profile_id, ttl,
  wake}` → per-machine results (code or error), sequential issuance
  (respects broker per-operator rate limits), optional WoL queueing
  per machine, single audit event with counts. `issueViaBroker` is the
  shared seam with single issuance.
- Machines UI: multi-select checkboxes → bulk modal → codes table with
  copy-all.

### 4. Job notifications
- Worker claims newly-terminal jobs (`FOR UPDATE SKIP LOCKED`,
  1 h backfill window so enabling doesn't replay history) and POSTs a
  webhook (`NOTIFY_WEBHOOK_URL`, optional bearer). Mark-before-POST:
  at-most-once; UI/SSE stays the record. Payload's `text` field is
  Slack-incoming-webhook compatible as-is.

### 5. HA tier
- `docker-compose.ha.yml` (standalone): external Postgres/S3, one-shot
  `api-migrate` gate, api ×2 replicas health-checked on `/readyz`,
  `Caddyfile.ha` round-robin with health probing. Known gap documented:
  the in-memory SSE bus doesn't fan out across replicas (backfill +
  polling mask it; LISTEN/NOTIFY is the future fix). CI validates the
  file with `docker compose config`.

### 6. CI security scanning
- govulncheck blocking + gosec high-severity advisory, per module (the
  old root-level Makefile invocation could never work across separate
  modules — fixed, and it now covers all six services). Trivy still
  deferred (needs built images).

### Tests
- restore.sh sync + content, `isLinuxGolden` matrix, media parsing (3).
- `issueViaBroker` stub-broker test (secret header, passthrough).
- Store integration: role grant/revoke/idempotency/unknown-role,
  CountOtherAdmins, RenderBundle blob bucket/sha (2).
- Worker integration (first worker tests): one-shot claim, payload
  shape, non-terminal + stale exclusion.
- e2e: golden-restore user-data + capture-wins branch order +
  restore.sh gating; user admin flow incl. last-admin 409 (now 6
  scenarios total).

## Phase 20 — Postgres LISTEN/NOTIFY event bus

**Done.** Closes the one HA gap documented in Phase 19: SSE pushes now
fan out across api replicas.

- `internal/pgbus`: wraps the in-process eventbus. Publish →
  `pg_notify('deploy_events', json)`; every replica runs a listener
  (dedicated pooled connection, capped-backoff reconnect) that fans
  received notifications out locally. The publisher does NOT
  short-circuit its own bus — its listener delivers like everyone
  else's, so all replicas share one ordering and nothing arrives twice.
  Notify failure falls back to local delivery (single-replica dev
  degrades gracefully). Oversized messages truncated under the
  pg_notify payload cap. Semantics stay at-most-once/drop-on-overrun —
  SSE backfill + UI polling remain the recovery path, unchanged.
- Handlers depend on a small `eventBus` interface; `serve` wires
  `pgbus.Bus`, tests keep the in-process bus.
- HA compose + OPERATIONS notes updated: the documented gap is gone; no
  sticky sessions needed.
- Tests: 4 pgbus integration tests against real Postgres (two bus
  instances = two replicas: cross-instance delivery, no double delivery
  on the publisher, job scoping, oversize truncation) and a new e2e
  scenario streaming a live phone-home over SSE through the Postgres
  hop (7 e2e scenarios total).

## Phase 21 — BIOS + LVM layouts in Linux golden restore

**Done.** Removes the two v1 restrictions on Phase 19's restore path.

- **Boot mode auto-detected on the target**: UEFI (ESP + GRUB
  x86_64-efi `--removable`) when `/sys/firmware/efi` exists; legacy
  BIOS otherwise (GPT with a 2 MiB BIOS boot partition + GRUB
  i386-pc). No configuration needed — the same golden image deploys to
  either firmware.
- **Layout from profile vars** (`answer_file_vars`): `restore_layout`
  plain (default) | lvm; lvm adds `restore_vg` (default vg0) and
  optional `restore_swap` (e.g. "8G") for a swap LV; root LV takes the
  remaining space. fstab covers root/ESP/swap with fresh UUIDs.
- **Initramfs regeneration** after restore (`update-initramfs` /
  `dracut`, best-effort) so LVM modules and storage drivers match the
  *target* hardware and layout — the hardware-independence step when
  target differs from the golden source.
- Server: `restoreLayoutEnv` threads the profile vars into the restore
  cloud-init as shell exports, whitelisted to safe characters (the
  values land inside a shell script; injection attempts are dropped,
  tested).
- Tests: layout-env unit matrix incl. injection cases, restore.sh
  content assertions (i386-pc/efi, lvm, initramfs), e2e extended —
  default profile exports plain, an LVM profile exports
  `DEPLOY_LAYOUT=lvm DEPLOY_VG=... DEPLOY_SWAP=...`.

## Phase 11 — Final docs
**Done.**

- `docs/ARCHITECTURE.md` (Phase 2)
- `docs/BOOTSTRAP.md` (Phase 4)
- `docs/WINDOWS.md`, `docs/LINUX.md`
- `docs/SECURITY.md`, `docs/OPERATIONS.md`
- `docs/RUNBOOK_A_USB.md`, `docs/RUNBOOK_B_PXE.md`, `docs/RUNBOOK_C_EDGE.md`
- `docs/STATUS.md` (this file)
- `README.md`
