# Field-test runbook

The validation protocol for everything the development sandbox could
not exercise: real firmware, real disks, a live Headscale, the Windows
ADK. Work through the tests in order — later tests assume earlier ones
passed. Record results in the table template at the bottom; every test
lists the evidence worth capturing while you're there.

**Legend**: each test has an ID (`FT-n`), prerequisites, steps,
expected results (✅), and where to look when it fails (🔍).

Hardware you'll want on the bench:

| Item | For |
|---|---|
| Server host (or VM) with Docker, DNS name, TLS cert chaining to your CA | the stack |
| Live Headscale (or Tailscale SaaS API key) | FT-2 onward |
| 1× USB stick ≥ 1 GB | FT-3 |
| 1× UEFI test machine with wipeable disk | FT-3–5 |
| 1× legacy-BIOS machine or VM (`-machine pc`, no OVMF) | FT-5b |
| 1× Windows host with ADK + WinPE add-on | FT-6 |
| 1× machine with a Windows license for capture | FT-7 |
| L2 segment you control + a small box for the edge agent | FT-8, FT-9 |
| Machines with wake-on-LAN enabled in firmware | FT-9c |

Nested-KVM VMs are fine substitutes for most machines (OVMF for UEFI,
SeaBIOS for legacy); FT-3's USB boot and FT-9c's WoL want real iron.

---

## FT-1 — Server bring-up

*Prereqs: DNS for `DEPLOY_FQDN` pointing at the host; TLS cert for it
chaining to the CA you'll pin on sticks.*

1. `cp .env.example .env` — set at minimum `DEPLOY_FQDN`,
   `HEADSCALE_URL`/`HEADSCALE_API_KEY` (or `TAILSCALE_*`), Postgres/S3
   passwords, `AUTH_BROKER_ISSUE_SHARED_SECRET` (`openssl rand -hex 32`),
   `EDGE_WAKE_TOKEN` if you'll run FT-9.
2. `make secrets` — generates the internal CA + per-service certs.
3. `make build-images && docker compose up -d`.
4. `docker compose exec api /usr/local/bin/api seed-admin you@example.com`
5. From another machine:
   `curl -f https://$DEPLOY_FQDN/readyz` and
   `curl -s https://$DEPLOY_FQDN/api/v1/auth/config`.

✅ `readyz` returns `ok`; auth config returns your issuer (or
`dev_mode: true` if OIDC is intentionally unset); the UI loads at the
FQDN and (with OIDC) bounces you through login; `/metrics` (internal
network) serves Prometheus text.
🔍 `docker compose logs api caddy` — the most common first failures are
cert-path mismatches (`make secrets` names vs. compose mounts) and the
FQDN cert not covering the name.
📸 Evidence: `docker compose ps` output, dashboard screenshot.

## FT-2 — Broker against live Headscale

*First live test of code issuance + tailnet key minting.*

1. In the UI: register a machine (any MAC), create a Linux image +
   profile, set it as the machine's default profile.
2. Issue a deployment code (UI Deploy wizard, or
   `deployctl deployments issue --machine <id> --profile <id>`).
3. Redeem it by hand from any machine with network access:
   `curl -s https://$DEPLOY_FQDN/api/v1/bootstrap/redeem -H 'Content-Type: application/json' -d '{"code":"<CODE>"}'`
4. Check Headscale: `headscale nodes list`.

✅ Redeem returns `auth_key`, `chainload_url`
(`/boot/<token>.ipxe` — a token, never a machine UUID), `expires_at`.
A node registered with `tag:deploy-bootstrap` appears in Headscale and
is **ephemeral**. Redeeming the same code again returns
`code_consumed_or_expired`. A deployment job in state `pending` appears
on the dashboard.
🔍 `docker compose logs auth-broker` — Headscale auth errors surface
here verbatim. Check `HEADSCALE_API_KEY` scope and expiry first.
📸 Evidence: redeem JSON (redact the auth key), `headscale nodes list`.

## FT-3 — Bootstrap stick build + boot

*The one flow that needs real USB boot. Requires the buildroot
toolchain fetch (hours, once) and root for losetup.*

1. Pin the fetch SHAs in `bootstrap/external/fetch.sh` (see file
   header), then `make build-bootstrap build-ipxe`.
2. In the UI → Bootstrap sticks → **Generate stick config**; save the
   CA PEM and run the emitted `make-stick.sh` command.
3. `dd` the image to the stick; register the stick
   (`deployctl bootstrap-sticks register …` — printed by make-stick).
4. Boot the UEFI test machine from the stick; enter a fresh code from
   FT-2 at the prompt.

✅ The stick joins the tailnet, syncs the clock, chainloads
`/boot/<token>.ipxe` over HTTPS with the pinned CA, and iPXE fetches
kernel+initrd. On the dashboard the job advances `pending → bootstrapped`.
Wrong code at the prompt → clean retry, and 5 wrong attempts lock the
code (`auth_codes.locked_at`).
🔍 Stick console (init logs are verbose by design), then
`docker compose logs auth-broker caddy`. TLS failures at this stage are
nearly always the pinned-CA-vs-served-cert mismatch.
📸 Evidence: photo of the stick console at chainload, job timeline.

## FT-4 — Linux zero-touch deploy, end to end

1. Point the profile's image media at a real Ubuntu live-server
   kernel/initrd (Images page), or stage them under `/static/`.
2. Issue a code, boot the target from the stick, enter it, walk away.

✅ Autoinstall runs with the rendered user-data (phone-home bearer
inside — check `/boot/<token>/user-data` while the job is live);
dashboard shows `imaging → completed` with the event trail via SSE (no
refresh); the machine reboots into the installed OS; the boot token is
dead afterward (`curl /boot/<token>.ipxe` → 410). Machine hardware
inventory appears on the machine page if the flow reported it.
🔍 Target console for curtin/subiquity errors; `journalctl` in the
installer environment; the job event trail is the server-side story.
📸 Evidence: SSE timeline screenshot, final `completed` job.

## FT-5 — Linux golden capture + restore

**a) Capture.** On a configured reference machine: clear
machine-id/host keys if you want (capture prunes them anyway), then use
**Capture golden image** on its machine page (target: a new linux
image; the boot profile must boot a live env with nocloud support —
Ubuntu live-server works). Boot with the stick + capture code.
✅ Events: archiving → uploading → `completed`; a new image version
appears with the archive's sha256; the image's media now carries
`deploy_method: golden`. A second local volume/USB is required for
scratch — the failure message says so if missing.

**b) Restore, UEFI + plain.** Deploy that image to the wipeable UEFI
machine (normal deploy flow).
✅ Disk is repartitioned (ESP+root), archive streamed on, **new**
machine-id and SSH host keys (compare against the source!), fstab has
the new UUIDs, machine boots via the fallback EFI path, hostname is the
target's, job `completed`.

**c) Restore, BIOS + LVM.** On the legacy-BIOS machine/VM, set the
profile vars `{"restore_layout":"lvm","restore_vg":"vg0","restore_swap":"4G"}`
and deploy again.
✅ GPT with BIOS-boot partition, `vg0/root` + `vg0/swap`, GRUB i386-pc
boots, `swapon --show` lists the LV, initramfs was regenerated (boot
survives the layout change from the golden source).
🔍 All variants: the restore script phone-homes each phase — the first
failed phase in the job timeline localizes it. Console shows the same
messages.
📸 Evidence: `lsblk` on the restored machine, before/after
machine-id + host-key fingerprints.

## FT-6 — WinPE build + Windows deploy

*Requires the Windows ADK host.*

1. On the Windows host, from the Deployment Tools prompt:
   `powershell -ExecutionPolicy Bypass -File winpe\scripts\build-boot-wim.ps1 -DeployCaPem C:\deploy-ca.pem -OutWim C:\out\boot.wim`
2. Upload boot.wim as an image version (or host at
   `/static/winpe/boot.wim`); set the Windows image's media
   (`wimboot_url`, `bootwim_url`, `wim_url` → your install.wim); create
   an `unattend` template on the profile (Profiles page scaffolds one).
3. Stage a matching driver pack (Driver packs page) with a
   `dmi-product` rule for the target.
4. Issue a code, boot the target from the stick.

✅ wimboot loads boot.wim with `_DEPLOY_JOB_ID`/`_DEPLOY_TOKEN` args;
startnet fetches deploy.cmd (auth via Bearer — watch the api access log
for the `Authorization` header, never `?token=`); plan returns the
driver-pack URL; DISM applies the WIM, injects the drivers, writes the
rendered unattend; machine reboots into OOBE/specialize; job
`completed`.
🔍 WinPE console (`wpeutil reboot` withheld on `:fatal`, so read the
error + `X:\deploy\*.log`); the job timeline mirrors each phase.
📸 Evidence: build-boot-wim sha256 output, WinPE console at plan fetch,
Device Manager clean after first boot (drivers landed).

## FT-7 — Windows golden capture

1. Sysprep the reference Windows machine
   (`sysprep /generalize /oobe /shutdown`).
2. **Capture golden image** on its machine page (target image:
   windows). Boot it with the stick + capture code; scratch volume
   required, as in FT-5a.

✅ DISM captures, uploads via the presigned URL, `capture-complete`
registers the version, job `completed`. Deploying that version to a
*different* hardware model with a matching driver pack yields a booting,
activated-OOBE Windows — the SmartDeploy headline claim, verified.

## FT-8 — LAN PXE + interactive menu

1. Copy `ipxe/build/*` into `services/tftp/tftproot/`, start the
   `lan-pxe` profile (`docker compose --profile lan-pxe up -d`).
2. PXE-boot a **registered** machine (MAC known, default profile set):
   ✅ menu appears with *Deploy assigned profile* highlighted; doing
   nothing for `PXE_MENU_TIMEOUT_MS` auto-deploys (zero-touch);
   the deploy chains a `/boot/<token>.ipxe` URL (watch caddy logs).
3. PXE-boot an **unregistered** machine:
   ✅ menu offers catalog/tools/local, defaults to local disk in 30 s,
   shows the register-this-MAC hint. No deploy items.
4. With `PXE_MENU_MODE=locked` (default), craft a foreign-profile
   deploy URL by hand: ✅ refused with an on-screen message, menu
   recovers. Flip to `open` (recreate api) — ✅ profile list appears.
5. Stop the api container, PXE-boot again:
   ✅ falls back to the legacy by-mac chain (or shell) — no brick.
6. Check the audit log: ✅ `job.created_via_pxe_menu` rows with source
   IPs for every menu deploy.

## FT-9 — Edge agent

1. On the edge box: run the edge-agent container
   (`EDGE_NAME`, `ADVERTISE_ROUTES`, `LAN_INTERFACE`, `EDGE_SITE=<site>`,
   `EDGE_WAKE_TOKEN`, `EDGE_CACHE_LISTEN=:8090`), enter an edge code.
2. **a) PXE via edge**: repeat FT-8 step 2 on the edge LAN.
3. **b) Image mirror**: create the site in the UI with
   `mirror_base_url: http://<edge-lan-ip>:8090`, home a machine to it,
   deploy twice.
   ✅ First deploy fills the cache (edge logs show one origin fetch);
   second machine's fetch is served locally (`X-Deploy-Cache: hit`,
   near-LAN-speed); corrupt a cached `/blobs` file by hand and re-fetch
   → the cache refuses to serve it and refills.
4. **c) Wake-on-LAN**: power off a WoL-enabled machine on the edge LAN,
   click **Wake** on its machine page.
   ✅ Within ~15 s the edge log shows `magic packet sent`, the machine
   powers on, and the machine page's wake history shows *sent by
   edge-<name>*.
🔍 Edge agent logs everything; the wake queue endpoint fails closed —
if the poller logs 404s, `EDGE_WAKE_TOKEN` differs between api and edge.

## FT-10 — Reporting, notifications, observability

1. After the deploys above: dashboard reports section.
   ✅ counts/success-rate/durations match reality; per-day chart shows
   the field-test days; CSV export opens in a spreadsheet with one row
   per job.
2. Set `NOTIFY_WEBHOOK_URL` to a Slack incoming webhook, restart the
   worker, complete one deploy. ✅ one Slack message per terminal job,
   no replay of history.
3. `docker compose -f docker-compose.yml -f docker-compose.observability.yml up -d`
   → Grafana :3001. ✅ dashboard panels populate within a minute.

## FT-11 — Security spot-checks

Run these with FT-4's artifacts at hand:

| Check | Expected |
|---|---|
| Re-fetch a completed job's `/boot/<token>.ipxe` | 410 |
| Phone-home with that token | 401 |
| Redeem a locked/expired code | `code_consumed_or_expired` |
| `POST /issue-code` directly to the broker without `X-Internal-Auth` | 404 |
| Wake-queue poll with a wrong bearer | 404 |
| A `viewer`-role user calling `POST /api/v1/machines` | 403 |
| Revoke the last admin's role | 409 |
| grep caddy/nginx access logs for `token=` | no hits (bearer-only) |

---

## Results record

Copy per test run:

| ID | Date | Hardware/notes | Result | Evidence link |
|---|---|---|---|---|
| FT-1 | | | ☐ pass ☐ fail | |
| FT-2 | | | ☐ pass ☐ fail | |
| FT-3 | | | ☐ pass ☐ fail | |
| FT-4 | | | ☐ pass ☐ fail | |
| FT-5a/b/c | | | ☐ pass ☐ fail | |
| FT-6 | | | ☐ pass ☐ fail | |
| FT-7 | | | ☐ pass ☐ fail | |
| FT-8 | | | ☐ pass ☐ fail | |
| FT-9a/b/c | | | ☐ pass ☐ fail | |
| FT-10 | | | ☐ pass ☐ fail | |
| FT-11 | | | ☐ pass ☐ fail | |

File findings as issues tagged `field-test`; anything that reproduces
in the sandbox gets a regression test before the fix lands.
