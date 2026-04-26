# Build Journal — SmartDeployLinux

A chronological record of the build, decisions, bugs caught, and
corrections from the founding session (2026-04-25/26). Companion to
`docs/STATUS.md` (the live what's-built tracker) and `docs/ARCHITECTURE.md`
(the design contract).

This document is the operator's history of the project: why it looks
the way it does, what was tried, what was rejected, and which mistakes
to avoid repeating. Not a replacement for the per-phase docs — those
explain how each piece works. This explains how we *got there*.

---

## 0. The brief

The user pasted a master prompt asking for "an open-source SmartDeploy
equivalent, USB-bootstrap-first edition." Multi-OS (Windows + Linux),
hardware-independent imaging, three boot modes (USB bootstrap / LAN PXE
/ edge-agent), Postgres + S3 backend, OIDC + RBAC, mTLS service-to-
service, signed boot artifacts.

The core insight driving the design: **PXE depends on L2 broadcast;
Tailscale is L3 unicast. Don't tunnel PXE across the overlay — replace
the first second of broadcast with a USB stick that brings up Linux +
tailscaled + iPXE, joins the tailnet via a single-use operator code,
then chainloads HTTP-over-tailnet from there.**

The non-negotiables: no closed-source dependencies, no long-lived
secrets on USB sticks, no hand-waving over hard parts (Secure Boot
chains, driver injection, Windows activation, ACL design).

## 1. Phase 1 — scope + risks

Restated requirements, identified top 5 risk areas, and pushed back
on places where the prompt was wrong:

1. **The single-use code → ephemeral Tailscale key handoff is the trust
   root.** A leaked code = one machine deployed under attacker control,
   which can pivot via post-install playbooks. Mitigation: rate-limit,
   single-use, short TTL, IP-bind on issuance, audit alarms, scope
   post-install secrets to one-shot tokens.
2. **WinPE over HTTPS-via-tailnet is more nuanced than the prompt
   makes it sound.** iPXE wimboot is the real path; Tailscale-on-WinPE
   is unsupported but doable. Documented honestly.
3. **Secure Boot signing chain.** Shim (Microsoft-signed) + GRUB/kernel
   (signed by project MOK) + one-time MOK enrollment per machine.
   Unavoidable UX cost flagged.
4. **300 MB USB image budget is tight, not impossible.** Wired-only,
   no Wi-Fi firmware blobs. "Fat" profile sketched but deferred.
5. **Hardware-independent Windows imaging is a discipline, not a
   feature.** SmartDeploy's commercial value is largely the curated
   Platform Pack library; we deliver tooling that ingests Dell/HP/
   Lenovo bundles cleanly but don't pretend to ship pre-curated
   driver libraries.

**Pushback on the prompt:**
- WinPE pulls install.wim via plain HTTP from inside the tailnet, not
  HTTPS-with-custom-CA (DISM doesn't speak that cleanly).
- Wi-Fi prompt deferred to future "fat" build; documented design.
- e2e nested-KVM test runs on self-hosted runners or local; gated in
  CI.
- Audit log mirrors append-only to file/syslog so a Postgres
  compromise can't rewrite history.
- Operator codes use a 31-char alphabet (drop 0/1/I/L/O) split as
  XXX-XXX — easier to dictate, ~8.9e8 codes.

## 2. Phase 2 — Architecture

Locked in `docs/ARCHITECTURE.md` as the design contract. Component
diagram, sequence diagrams (USB bootstrap → Linux, USB → Windows, LAN
PXE, edge agent), full ER model, decision log.

Key component picks:
- **Go 1.23+** for all services. Single binary, easy cross-compile,
  edge-agent runs on Pi.
- **Headscale** as default overlay (open-source); Tailscale SaaS also
  supported via the same broker abstraction.
- **iPXE pinned at `v1.21.1`** with HTTPS + cert-pin + IPv6 + NTP
  enabled, deploy-CA embedded as trusted root.
- **buildroot 2025.02** for the USB rootfs (vs Alpine — buildroot
  wins on reproducibility and Secure-Boot-friendliness).
- **dnsmasq 2.90** for the LAN PXE proxyDHCP+TFTP path (vs ISC Kea —
  dnsmasq is rock-solid for proxyDHCP-only).
- **PostgreSQL 16** + **MinIO** (vs SeaweedFS — S3 compat is the
  lingua franca).
- **Caddy 2.10** as TLS terminator, **nginx** as the static-blob
  server inside http-boot.
- **cosign** for signing non-MS artifacts.
- **chi/v5** + **pgx/v5** for the Go API stack.

## 3. Phase 3 — Skeleton

Full monorepo tree. Root `Makefile`, `docker-compose.yml` (small tier
single-host), `.env.example` parameterized for everything, README,
STATUS.md tracker.

## 4. Phase 4 — USB bootstrap → Linux deploy spine

This was the main structural deliverable. Substantial code:

- `bootstrap/Makefile` orchestrating buildroot
- `bootstrap/buildroot/configs/bootstrap_slim_defconfig` — ~90MB
  wired-only profile
- `bootstrap/buildroot/board/kernel-config-slim` — curated minimal
  kernel (e1000e/igb/ixgbe/r8169/realtek for NIC; AHCI/NVMe/USB
  storage; no SCSI/sound/RAID/iSCSI)
- `bootstrap/scripts/assemble-image.sh` — hybrid GPT/MBR USB image
  with ESP (FAT32, 64 MB) + Linux ext4
- `bootstrap/overlay/usr/local/bin/deploy-bootstrap` — the script that
  runs after kernel boot: DHCP, NTP, TUI prompt, redeem call, tailscale
  up --ephemeral, kexec-into-iPXE
- `bootstrap/overlay/usr/local/bin/exec-ipxe` — handles BIOS (kexec)
  vs UEFI (efibootmgr next-only) handoff
- `bootstrap/make-stick.sh` — customizes image with tailnet/deploy-URL/
  CA cert; produces flashable .img
- `bootstrap/external/fetch.sh` — fetches shim (Microsoft-signed) +
  GRUB2 (signed by project MOK) + tailscale binary, all SHA-pinned
- `bootstrap/keys/gen-mok.sh` — one-time MOK keypair generator
- `ipxe/Makefile` + `ipxe/embed.script` + `ipxe/patches/general.h.patch`
  — custom iPXE build with HTTPS + cert-pin

Then the auth-broker (the security-critical centerpiece):
- Single-use codes (argon2id-hashed at rest, pepper = deploy FQDN)
- Headscale + Tailscale-SaaS clients behind a narrow interface
- Per-IP rate limit (DB-backed, survives restart) + per-code attempt
  counter with auto-lock at 5 attempts
- Atomic UPDATE consume so concurrent redemptions can't double-spend
- Audit-logs every event (issued / redeemed / failed / locked)

API + http-boot wired together to render per-machine iPXE scripts and
serve cloud-init/autoinstall.

`services/api/internal/migrations/0001_init.sql` — full schema from
the ER model: 21 tables including users, role_permissions, machines,
images+image_versions, deployment_profiles, answer_file_templates,
auth_codes, one_shot_tokens, deployment_jobs, deployment_events,
audit_events, redeem_attempts, jobs (worker queue), and the seeded
admin/operator/viewer roles.

**What was verifiable in this sandbox:** Go build/vet on all services,
9 codes-package unit tests passing, SQL migration applies clean to
real Postgres 16, all 8 shell scripts parse clean.

**What was deferred to actual execution:** booting the stick image
end-to-end (requires buildroot + iPXE + tailscale fetches + root for
losetup/mkfs + /dev/kvm).

## 5. Phase 5 — Windows deploy

`services/api/internal/driverpack/match.go` — driver-pack matching
engine with priority ordering: PCI VID:DID > DMI product > DMI baseboard >
DMI vendor + OS version > DMI vendor. 9 unit tests covering all the
precedence cases including ties, OS-version prefix matching, and case-
insensitive DMI normalization.

`services/api/internal/answerfile/unattend.go` — Go text/template
renderer for unattend.xml with the UTF-16LE+base64 encoding helpers
Windows wants for password fields. 5 unit tests.

`winpe/scripts/{startnet,deploy}.cmd` — fetched-fresh-on-every-boot
deploy.cmd so logic updates don't require rebuilding boot.wim. PCI
fingerprint extraction in the cmd file is incomplete; v1 ships
DMI-only matching from WinPE.

`docs/WINDOWS.md` — full pipeline doc including the WinPE build
procedure (requires Windows ADK on a Windows host) and driver-pack
manifest format.

## 6. Phase 6 — LAN PXE

`services/tftp/` — alpine-based dnsmasq container running proxyDHCP +
TFTP only (`port=0` to disable DNS). Per-architecture boot file via
`option:client-arch` (BIOS=0, UEFI x64=7, ARM64=11, HTTPClient=16).
`docs/RUNBOOK_B_PXE.md` documents coexistence with existing DHCP and
the Secure Boot caveats.

## 7. Phase 7 — Edge agent

`services/edge-agent/` — Go binary that runs on a small Linux box at
a remote site:
1. Reads operator code from local stdin (or future TUI)
2. Calls /bootstrap/redeem to get a `tag:deploy-edge` ephemeral key
3. `tailscale up --advertise-routes=<RFC1918>` so deploy server can
   reach LAN-side targets
4. Renders + starts dnsmasq locally for proxyDHCP+TFTP

Multi-arch Dockerfile (amd64 + arm64). `docs/RUNBOOK_C_EDGE.md`
documents the bulk-deploy workflow.

## 8. Phase 8 — API + UI buildout

This phase happened across many sub-phases over many turns.

### 8a — DB layer + migrations runner
`services/api/internal/store/` package wraps pgx with hand-written
SQL. Embedded migration runner via `embed.FS` and a `schema_migrations`
tracking table. Idempotent.

### 8b — Phone-home + deployment events
`POST /v1/jobs/{id}/events` accepting `Authorization: Bearer
<one-shot-token>`. Validates token, writes event row, transitions
deployment_jobs.state if the phase implies a state change.

### 8c — Worker housekeeping
`services/worker/` — small Go process on a 30-second tick:
- Reaps `redeem_attempts` rows older than 24h
- Locks expired `auth_codes` past their TTL
- Logs first-tick errors during the brief race against api startup
  migrations; subsequent ticks succeed silently

### 8d — OIDC + RBAC
`services/api/internal/auth/` — wraps coreos/go-oidc/v3 (pinned to
v3.11.0 because v3.18+ requires Go 1.25 which our build image
doesn't have). Middleware extracts bearer, verifies, upserts user by
oidc_subject, loads role permissions into request context.

When OIDC is unconfigured (dev mode), middleware is bypassed AND
`HasPerm` returns true; the `/me` endpoint reports `dev_mode:true` so
the UI can show a banner.

**Bug caught:** I shadowed the request `r` variable with the resolver
local var. Would have silently used the wrong context. Fixed during
build.

### 8e — Operator API endpoints for deployctl
`POST /api/v1/deployments/issue` — proxies to auth-broker with
`issued_by` injected from the verified OIDC principal so the operator
can't claim someone else's identity. `POST /api/v1/bootstrap-sticks`
register/list (the inventory used during CA rotation). Audit query
filters (since/action prefix/machine).

### 8f — `api seed-admin <email>`
Idempotent first-run helper. Upserts user + admin role. `oidc_subject`
populates on first OIDC login.

### 8g — Minimal embedded UI (first attempt)
Single-page HTML+JS embedded via `go:embed`. Tabs: Dashboard /
Machines / Issue Code / Audit / Sticks. **Frank pushed back: too
basic, not first-class.** Replaced in 8h.

### 8h — First-class UI rewrite
Sidebar navigation, hash router, modal + toast helpers, status badges
with pulsing dots for live deployments, deployment wizard (4-step:
Target → Profile → Options → Review), machine detail page with
deployment history, job detail page with event timeline, auto-refresh
on live data (5s on dashboard/jobs, 4s on job detail). Full design
system in CSS variables (~390 lines of CSS).

Backend additions: `Store.ListJobs/GetJob/GetJobEvents`, `ListImages`,
`DeleteMachine` (refuses if non-terminal jobs exist). 4 new API
routes.

### 8i — Profile management + answer-file editor
Full CRUD on profiles. Profile detail page with:
- Variables JSON editor (validated on save)
- Multi-tab template editor — tabs are filtered by image OS family
  (Linux: autoinstall/kickstart/preseed/cloud-init/ignition; Windows:
  unattend)
- Default scaffold templates pre-populated for each kind, including
  phone-home `curl` calls using `{{ deploy_fqdn }} / {{ job_id }} /
  {{ one_shot_token }}` template variables
- `●` dot indicator on tabs that have a saved template

7 new API routes (`POST/PATCH/DELETE /api/v1/profiles/{id}`,
`GET /api/v1/profiles/{id}` returning profile + vars + templates,
`PUT/DELETE /api/v1/profiles/{id}/templates`). All audit-logged.

### 8j — Image registration by URL
Migration `0003_image_media.sql` adds `images.media JSONB`. Operators
register images by URL pattern (kernel/initrd/wimboot/bootwim/install_wim)
without 4 GB browser uploads. Renderer pulls these URLs into the iPXE
chain script verbatim.

Image detail page with media-URL form (fields adapt to OS family),
"Used by profiles" panel, edit metadata + delete. Status pill on the
list page indicates how many media URLs are populated.

## 9. Phase 9 — Security hardening: 8/8 SECURITY.md §4 gaps closed

Each gap was identified at the end of Phase 8 and closed in dedicated
sub-phases:

1. **9a `/boot/<id>.ipxe` token-binding.** Was: machine UUID in URL,
   no auth. Now: 32-byte URL-safe random `boot_token` minted at redeem
   time, sha256-hashed-with-pepper at rest, stored in `one_shot_tokens
   (purpose='boot')`. URL is `/boot/<token>.ipxe`. Idempotent — same
   token works for both iPXE chain and user-data fetch. Revoked at
   deployment-job terminal-state transition via
   `Store.MarkBootTokensConsumedForJob`.

2. **9c WinPE token in Authorization header.** `winpe/scripts/{startnet,
   deploy}.cmd` use `Authorization: Bearer %TOKEN%` exclusively;
   `?token=` removed from all URLs.

3. **9f mTLS api ↔ http-boot.** `scripts/gen-internal-ca.sh` issues
   internal CA + 4 per-service certs. `services/{api,http-boot}/internal/
   mtls/` packages with `Bundle.Load`/`ServerConfig`/`ClientConfig`.
   API gets a separate `:8443` mTLS listener with
   `tls.RequireAndVerifyClientCert` for `/internal/render/*`. Public
   `/api/v1/*` stays on `:8080`. 3 mtls tests including end-to-end
   handshake with negative cases (no-cert + foreign-CA both rejected).

4. **9b Audit log file mirror.** `services/{auth-broker,api}/internal/
   auditlog/` packages return slog.Logger fan-outing to stdout AND
   (if `AUDIT_FILE` set) an append-only file. `Store.SetAuditLogger`
   wires fan-out to `audit_events` inserts. 4 unit tests.

5. **9g Headscale API key rotation.** `scripts/rotate-headscale-key.sh`
   with mint/list/expire subcommands. `OPERATIONS.md §5b` documents
   the mint→deploy→verify→expire cycle.

6. **9h MOK on HSM.** `SECURITY.md §4 #6` documents YubiKey + PKCS#11 +
   sbsign procedure for production deployments where MOK.priv must
   never touch a filesystem.

7. **9d Per-operator issue rate limit.** `Store.CountIssuedByActorRecently`
   + `AUTH_BROKER_RATE_LIMIT_ISSUE_PER_OPERATOR_PER_HOUR` (default
   100). Caps blast radius of compromised admin account.

8. **9e Per-job WinPE endpoints with state-gated tokens.**
   `/v1/jobs/{id}/{deploy.cmd,plan,image.wim,drivers.zip,unattend.xml}`
   require Bearer token AND job state in `bootstrapped`/`imaging`.
   Terminal-state transitions revoke tokens. New
   `Store.VerifyOneShotTokenForJob`. 1 integration test.

## 10. Phase 10 — CI

`.github/workflows/ci.yml` — three jobs:
- `go`: matrix across all 6 services, build/vet/test with Postgres
  16 sidecar exposing `:5432` so api integration tests run via
  `DEPLOY_TEST_PG_DSN`
- `shell`: bash -n / sh -n + advisory shellcheck
- `sql`: applies migration to real Postgres, asserts ≥21 tables

**CI was failing for a non-code reason:** the GitHub Actions billing/
spending limit on Frank's account hadn't been raised, so jobs were
rejected before starting ("recent account payments have failed or
your spending limit needs to be increased"). Frank's side to fix.

E2e nested-KVM harness in `tests/e2e/` is still scaffolded but not
authored.

## 11. Phase 11 — Final docs

- `docs/ARCHITECTURE.md` (Phase 2 — locked design contract)
- `docs/BOOTSTRAP.md` (Phase 4 — USB stick spec)
- `docs/WINDOWS.md`, `docs/LINUX.md`
- `docs/SECURITY.md` (with the 8-gap punch list, all closed)
- `docs/OPERATIONS.md` (backups, upgrades, scaling, key rotation)
- `docs/RUNBOOK_A_USB.md` / `_B_PXE.md` / `_C_EDGE.md`
- `docs/STATUS.md` (live what's-built tracker)
- `docs/BUILD_JOURNAL.md` (this file)

## 12. Deployment to docker.tailb0b06a.ts.net

After phases 1-9 closed, Frank asked for a deploy to his existing
docker host: `192.168.200.23` LAN / `100.85.18.119` Tailscale /
MagicDNS `docker.tailb0b06a.ts.net`. The host already runs ~20
selfhosted services (pihole, NPM, n8n, grafana, etc.) and uses
`tailscale serve` heavily for HTTPS-over-tailnet.

Path:
1. SSH in as root (memory said keys missing; turned out they
   work).
2. rsync repo to `/opt/docker/smartdeploy/`.
3. Generate internal CA + service certs with `scripts/gen-internal-ca.sh`.
4. Created `docker-compose.deploy.yml` overlay that profiles out
   caddy / ui / minio / minio-init / auth-broker / dnsmasq, keeping
   only postgres + api + http-boot + worker.
5. Brought up partial stack.
6. Once Frank gave a Tailscale API key + saved a tagOwners-merged
   ACL, the auth-broker came up and verified end-to-end:
   - issue-code → 6-character code
   - redeem → real `tskey-auth-...` ephemeral key + token-bound
     chainload URL
7. `tailscale serve --bg --https=18080 http://192.168.200.23:18080`
   exposes the api at `https://docker.tailb0b06a.ts.net:18080/`
   tailnet-only with a real Let's Encrypt cert auto-issued for the
   `*.ts.net` zone. No public-internet exposure.
8. Added Homepage entry under "Automatisering".

## 13. Bugs caught and fixed during the build

This is the punch list of mistakes I ran into and corrected — kept
honest because future-me will hit similar things:

**Compile-time / type errors:**
- Shadowed `r` variable in OIDC middleware (request var clashing with
  resolver var). Caught by `go vet`.
- Unused `os` import in `render.go` after refactoring out file
  reads. Caught by `go vet`.
- Missing `chi.URLParam` import in `operator.go` after Edit failed
  silently. Caught by build.
- Multiple `Edit` calls failed silently with "File has not been read
  yet" when too much time had passed since the last `Read`. Forced
  re-Read between Edit batches.

**Runtime errors:**
- `go-oidc/v3 v3.18.0` requires Go 1.25; our Docker build image is
  pinned at 1.23.4. Pinned to v3.11.0 instead.
- pgx text encoder rejects int parameters concatenated with `||`
  in SQL (e.g. `($1 || ' hours')::interval`). Cast to string client-
  side before passing.
- Migration cleanup ordering in integration tests: `machines.default_
  profile_id` references `deployment_profiles`, so machines must be
  deleted before profiles in the test setup.

**Deploy-time:**
- compose validation rejected the profile setup because `api` had
  `depends_on: minio` which forced minio out of `with-minio`
  profile-gate. Removed minio from api+worker depends_on (api uses
  minio lazily; not a hard startup dependency).
- `services/http-boot/Dockerfile` used `supervisord` which has a
  pyexpat ABI mismatch on Alpine 3.20+. Replaced with `tini` + a
  small shell entrypoint.
- The same Dockerfile only copied `scripts/`; missing `internal/
  mtls/` after that package was added. Added `COPY internal/`.
- `secrets/*.pem` were 0600 root-owned but distroless containers
  run as `nonroot` UID 65532. Chowned files post-generation.
- Ports 8090/8091/8092 were all taken on Frank's docker host
  (librespeed/explorer/census-server). Moved api to 18080.
- 0.0.0.0 host port binding conflicted with `tailscale serve` which
  also listens on the tailnet interface. Fixed by binding api to
  `192.168.200.23:18080:8080` (LAN IP only).
- Migration 0002 collided with smoke-test fixtures: pre-existing
  image with name `ubuntu-2404` had a different UUID than the
  migration's hardcoded one. Rewrote 0002 to use name-based lookups
  (idempotent against existing data).

**Operational:**
- The directory at `/opt/docker/homepage/config/` on the docker host
  is unattached legacy; the actual Homepage config lives in the
  named Docker volume `homepage_config`. To edit:
  `docker cp homepage:/app/config/services.yaml -`. Spent a turn
  fixing the wrong file before catching this.
- API's `/` returned chi's default 404 because no handler was
  registered. Added a simple HTML index so a browser hit on the
  bare URL is useful.
- Browser's HSTS pin on `docker.tailb0b06a.ts.net` (from existing
  NPM-fronted services) upgraded `http://...:18080` → `https://`
  causing SSL handshake failure. Fixed by serving over `tailscale
  serve` (which provides real LE cert).

## 14. Frank's corrections, applied in chronological order

These are the explicit pushbacks Frank made — captured because they
encode his preferences for future work:

1. **"Skip building it; just do it best-practice"** — when I was
   asking for confirmation before each phase. Stopped doing that.
2. **"No internet exposure"** — when I suggested `deploy.sybr.no` with
   a public LE cert. Wrong instinct on my part; the whole product
   design IS no internet exposure. Switched to `tailscale serve`.
3. **"Mac mini does not exist"** — the memory note about a Mac mini
   was stale. Removed `mac_mini_siem.md` and stopped using
   `*.macmini.lan`. Now using `docker.tailb0b06a.ts.net` (his
   canonical pattern).
4. **"That UI is pretty worthless"** — when the first UI I built was
   just a route map / tab stub. Replaced with the first-class
   sidebar UI (Phase 8h).
5. **"I want a first-class interface and functionality. The same or
   better than SmartDeploy."** — drove the buildout of profile mgmt
   (8i), image registration by URL (8j), proper deployment wizard,
   live job detail with timeline.
6. **"Just do best practice immediately"** — when I was over-explaining
   each step. Tightened response style.

## 15. Where things stand at end of session 2026-04-26

**Live stack on `docker.tailb0b06a.ts.net`:**
- `postgres` (healthy)
- `api` (mTLS internal listener + public listener at host :18080,
  served via `tailscale serve` as `https://docker.tailb0b06a.ts.net:18080/`)
- `auth-broker` (configured against Tailscale SaaS API)
- `http-boot` (nginx + render Go process)
- `worker` (housekeeping)

**SECURITY.md §4: 8/8 closed.**

**Test count:** 50 passing across 6 Go services (38 unit + 12
integration vs Postgres 16). Plus the embedded migration runner
verified idempotent across 3 migrations.

**Repo:** `https://github.com/franzjeger/SmartDeployLinux` (private).

**Memory updated:**
- Removed stale `mac_mini_siem.md`
- Added `smartdeploy_on_docker_host.md` with the deployment specifics
  (paths, port, mTLS UID gotcha, tailscale-serve binding, Homepage
  named-volume gotcha)

## 16. What's still open

- **GitHub Actions billing** — user-side fix; runs are rejected
  before starting until spending limit is raised.
- **SvelteKit UI as a separate codebase** — not happening; the
  embedded vanilla-JS UI shipped in 8g/8h/8i/8j is the actual UI.
- **e2e nested-KVM test harness** — `tests/e2e/` scaffolded but not
  authored. Phase 10 partial.
- **Real image upload via UI (multipart)** — deferred in favor of
  URL-based registration (8j). Multipart upload would require MinIO
  running and stream-with-sha256 plumbing.
- **Live SSE event stream** for the job detail page. Currently 4s
  polling; SSE would be smoother. Trivial to add when needed.
- **Bootstrap stick build wizard in UI** — currently CLI-only via
  `bootstrap/make-stick.sh`. The actual buildroot build is ~30
  minutes so the UI would have to be async-job-based.
- **ARM64 Windows** — designed but not validated.
- **BitLocker recovery key escrow** — sketched in WINDOWS.md §6 but
  not implemented.
- **An end-to-end real Linux deploy** against a live VM — closest we
  came was issue → redeem → real Tailscale auth key minted, but no
  install actually executed because there's no real Ubuntu netboot
  kernel/initrd registered yet (placeholder URLs point at the live-
  server ISO). Operator picks correct upstream URLs and registers a
  fresh image to actually boot something.

## 17. How to pick up where we left off

If a future session opens this project:

1. `git log --oneline` shows the chronology — phases are tagged in
   commit messages.
2. `docs/STATUS.md` is the live what's-built tracker.
3. `docs/SECURITY.md §4` shows which security gaps are closed and how.
4. The deploy host SSH access is `root@docker.tailb0b06a.ts.net`
   (memory: `smartdeploy_on_docker_host.md`).
5. `docker compose -f docker-compose.yml -f docker-compose.deploy.yml
   --profile with-broker ps` on the host shows the running services.
6. The pinned Tailscale API key is in `/opt/docker/smartdeploy/.env`
   on the host. Rotate per `OPERATIONS.md §5b`.
7. The internal mTLS CA + service certs are in `/opt/docker/smartdeploy/
   secrets/` chowned to UID 65532. To regenerate, run `make secrets`
   then re-chown.

The conversation transcript itself is preserved by Claude Code at
`~/.claude/projects/-home-frank-Projects-SmartdeployLinux/<session>.jsonl`
on the workstation.
