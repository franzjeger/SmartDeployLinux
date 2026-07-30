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

## Phase 11 — Final docs
**Done.**

- `docs/ARCHITECTURE.md` (Phase 2)
- `docs/BOOTSTRAP.md` (Phase 4)
- `docs/WINDOWS.md`, `docs/LINUX.md`
- `docs/SECURITY.md`, `docs/OPERATIONS.md`
- `docs/RUNBOOK_A_USB.md`, `docs/RUNBOOK_B_PXE.md`, `docs/RUNBOOK_C_EDGE.md`
- `docs/STATUS.md` (this file)
- `README.md`
