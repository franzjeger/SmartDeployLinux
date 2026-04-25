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
**5 of 8 SECURITY.md §4 gaps closed.**

| Gap | State |
|---|---|
| #1 `/boot/<id>.ipxe` unauthenticated | ✅ Closed (token-bound `/boot/<token>.ipxe`; Phase 9a) |
| #2 token in query string for WinPE | ✅ Closed (`Authorization: Bearer`; Phase 9c) |
| #3 mTLS between services | ⏳ Open |
| #4 audit log mirror to file/syslog | ✅ Closed (`auditlog.Open` fan-out; Phase 9b) |
| #5 Headscale API key rotation | ⏳ Open |
| #6 MOK private key HSM handling | ⏳ Open |
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

## Phase 11 — Final docs
**Done.**

- `docs/ARCHITECTURE.md` (Phase 2)
- `docs/BOOTSTRAP.md` (Phase 4)
- `docs/WINDOWS.md`, `docs/LINUX.md`
- `docs/SECURITY.md`, `docs/OPERATIONS.md`
- `docs/RUNBOOK_A_USB.md`, `docs/RUNBOOK_B_PXE.md`, `docs/RUNBOOK_C_EDGE.md`
- `docs/STATUS.md` (this file)
- `README.md`
