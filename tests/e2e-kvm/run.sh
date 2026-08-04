#!/usr/bin/env bash
# run.sh — prove the deploy core end to end against real components.
#
# This drives the project's REAL golden-image restore path with nothing
# stubbed on the critical line:
#
#   1. build & start the real api against a real Postgres (dev mode)
#   2. register a machine/profile/golden image over the operator API and
#      seed the boot token + deploy job exactly as the auth-broker would
#   3. fetch the token-gated restore.sh straight from the api
#   4. run that exact restore.sh against a REAL block device (a loop disk):
#      real GPT partitioning, real ext4, real stream-untar of a real
#      bootable golden archive, real GRUB install + initramfs regen in a
#      chroot, and restore.sh's own phone-home to the api
#   5. assert the api moved the job to `completed` (driven by restore.sh)
#   6. boot the restored disk in QEMU and assert, from the serial log,
#      that the restored OS actually boots and reports its regenerated
#      identity
#
# The only concession to this sandbox: QEMU runs under TCG software
# emulation because the environment exposes no /dev/kvm. Everything the
# server, the script, the partitioning, the bootloader, and the boot
# itself does is real. Add -enable-kvm on a KVM-capable host for speed.
#
# Must run as root (loop devices, mount, chroot). Requires: the tools in
# check_prereqs below, a golden archive from build-golden.sh, and a
# Postgres reachable at $PG_DSN.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
WORK=${WORK:-/var/tmp/e2ekvm}
GOLDEN=${GOLDEN:-$WORK/golden.tar.zst}
PG_DSN=${PG_DSN:-postgres://test@127.0.0.1:5433/deploytest?sslmode=disable}
DEPLOY_FQDN=${DEPLOY_FQDN:-deploy.e2e.test}
DISK_IMG=$WORK/target.raw
DISK_SIZE_GB=${DISK_SIZE_GB:-6}
SERIAL_LOG=$WORK/serial.log
BOOT_MARKER="DEPLOY-E2E-BOOT-OK"
RESTORE_HOSTNAME=restored-e2e

API_PID= ARCHIVE_PID= LOOP= API_BASE= UDEV_SHIM_PID=
API_BIN=$WORK/api.bin

log()  { printf '\n\033[1;36m>> %s\033[0m\n' "$*"; }
ok()   { printf '\033[1;32m   ok: %s\033[0m\n' "$*"; }
die()  { printf '\033[1;31mFAIL: %s\033[0m\n' "$*" >&2; exit 1; }

cleanup() {
    set +e
    [ -n "$API_PID" ] && kill "$API_PID" 2>/dev/null
    [ -n "$ARCHIVE_PID" ] && kill "$ARCHIVE_PID" 2>/dev/null
    [ -n "$UDEV_SHIM_PID" ] && kill "$UDEV_SHIM_PID" 2>/dev/null
    # Best-effort unwind of any mounts restore.sh may have left on failure.
    for m in sys/firmware/efi/efivars sys proc dev boot/efi ""; do
        umount "/mnt/deploy-restore/$m" 2>/dev/null
    done
    [ -n "$LOOP" ] && losetup -d "$LOOP" 2>/dev/null
}
trap cleanup EXIT

check_prereqs() {
    [ "$(id -u)" = 0 ] || die "must run as root (loop/mount/chroot)"
    [ -f "$GOLDEN" ] || die "golden archive missing: $GOLDEN (run build-golden.sh)"
    for t in go curl losetup sgdisk mkfs.ext4 chroot qemu-system-x86_64 python3 sha256sum psql; do
        command -v "$t" >/dev/null || die "missing tool: $t"
    done
    ok "prerequisites present"
}

start_api() {
    log "building + starting the real api (dev mode) against Postgres"
    ( cd "$REPO/services/api" && go build -o "$API_BIN" ./cmd/api ) || die "api build failed"

    # Fresh schema so the run is deterministic; the api applies its own
    # embedded migrations on startup.
    psql "$PG_DSN" -v ON_ERROR_STOP=1 -q \
        -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;' >/dev/null
    ok "schema reset (api will migrate on startup)"

    local port=18080
    API_BASE="http://127.0.0.1:$port"
    DEPLOY_TEST_PG_DSN="$PG_DSN" \
    POSTGRES_HOST=127.0.0.1 POSTGRES_PORT=5433 POSTGRES_USER=test \
    POSTGRES_PASSWORD= POSTGRES_DB=deploytest \
    API_LISTEN="127.0.0.1:$port" API_PUBLIC_URL="$API_BASE" \
    DEPLOY_FQDN="$DEPLOY_FQDN" LOG_LEVEL=warn \
    OIDC_ISSUER= OIDC_CLIENT_ID= INTERNAL_TLS_CERT=/nonexistent \
        "$API_BIN" serve >"$WORK/api.log" 2>&1 &
    API_PID=$!

    for _ in $(seq 1 50); do
        if curl -fsS "$API_BASE/readyz" >/dev/null 2>&1; then break; fi
        sleep 0.2
    done
    curl -fsS "$API_BASE/readyz" >/dev/null || { cat "$WORK/api.log"; die "api not ready"; }
    ok "api ready at $API_BASE"
}

# jget <json> <key>  — tiny field extractor (no jq dependency).
jget() { python3 -c 'import sys,json;print(json.load(sys.stdin).get(sys.argv[1],""))' "$2" <<<"$1"; }

seed_job() {
    log "registering golden image + machine and seeding the deploy job"
    local img prof
    img=$(curl -fsS -X POST "$API_BASE/api/v1/images" -H 'Content-Type: application/json' \
        -d '{"name":"e2e-golden","os_family":"linux","os_version":"24.04","arch":"amd64",
             "media":{"deploy_method":"golden"}}')
    IMAGE_ID=$(jget "$img" id); [ -n "$IMAGE_ID" ] || die "create image: $img"

    prof=$(curl -fsS -X POST "$API_BASE/api/v1/profiles" -H 'Content-Type: application/json' \
        -d "{\"name\":\"e2e-prof\",\"image_id\":\"$IMAGE_ID\"}")
    PROFILE_ID=$(jget "$prof" id); [ -n "$PROFILE_ID" ] || die "create profile: $prof"

    # A golden image needs an uploaded version (blob) for the api to treat
    # it as a restorable archive (isLinuxGolden checks the blob key). The
    # s3_key is only used to build the archive URL, which we override at
    # restore time — its presence is what matters here.
    psql "$PG_DSN" -tAq -v ON_ERROR_STOP=1 <<SQL >/dev/null
WITH b AS (
  INSERT INTO blobs (sha256, size_bytes, s3_bucket, s3_key)
  VALUES ('$(head -c32 /dev/urandom | sha256sum | cut -d' ' -f1)', 1234, 'images',
          'e2/golden.tar.zst') RETURNING id
)
INSERT INTO image_versions (image_id, blob_id, version_tag)
SELECT '$IMAGE_ID', b.id, 'gold-1' FROM b;
SQL
    ok "golden version registered"

    local m
    m=$(curl -fsS -X POST "$API_BASE/api/v1/machines" -H 'Content-Type: application/json' \
        -d "{\"asset_tag\":\"e2e-01\",\"default_profile_id\":\"$PROFILE_ID\"}")
    MACHINE_ID=$(jget "$m" ID); [ -n "$MACHINE_ID" ] || die "create machine: $m"
    ok "image=$IMAGE_ID profile=$PROFILE_ID machine=$MACHINE_ID"

    # The auth-broker's redeem writes, done directly (Headscale isn't here):
    # user, auth code, boot token, pending deploy job. Boot-token hash is the
    # api scheme: sha256(pepper || 0x00 || token), pepper = DEPLOY_FQDN.
    TOKEN="e2etok_$(head -c16 /dev/urandom | sha256sum | cut -c1-32)"
    local tokhash
    tokhash="sha256:$(printf '%s\0%s' "$DEPLOY_FQDN" "$TOKEN" | sha256sum | cut -d' ' -f1)"

    JOB_ID=$(psql "$PG_DSN" -tAq -v ON_ERROR_STOP=1 <<SQL
WITH u AS (
  INSERT INTO users (email) VALUES ('e2e-$(date +%s%N)@e2e.test') RETURNING id
), ac AS (
  INSERT INTO auth_codes (code_hash, machine_id, profile_id, issued_by, expires_at, kind)
  SELECT 'e2e-code-$(head -c8 /dev/urandom|sha256sum|cut -c1-16)', '$MACHINE_ID', '$PROFILE_ID', u.id,
         now() + interval '1 hour', 'deploy' FROM u RETURNING id
), t AS (
  INSERT INTO one_shot_tokens (auth_code_id, token_hash, purpose, expires_at)
  SELECT ac.id, '$tokhash', 'boot', now() + interval '1 hour' FROM ac RETURNING id
)
INSERT INTO deployment_jobs (machine_id, profile_id, auth_code_id, state, kind)
SELECT '$MACHINE_ID', '$PROFILE_ID', ac.id, 'pending', 'deploy' FROM ac RETURNING id;
SQL
)
    JOB_ID=$(printf '%s' "$JOB_ID" | tr -d '[:space:]')
    [ -n "$JOB_ID" ] || die "seed job failed"
    ok "job=$JOB_ID token=${TOKEN:0:16}…"

    # Boot render advances the job out of pending (as the real iPXE fetch does).
    curl -fsS "$API_BASE/internal/render/by-token/$TOKEN" >/dev/null || die "render-by-token failed"
    ok "job advanced to bootstrapped via token render"
}

serve_archive() {
    log "serving the golden archive over HTTP (stands in for image storage)"
    ( cd "$(dirname "$GOLDEN")" && python3 -m http.server 18090 --bind 127.0.0.1 ) \
        >"$WORK/archive.log" 2>&1 &
    ARCHIVE_PID=$!
    sleep 0.5
    ARCHIVE_URL="http://127.0.0.1:18090/$(basename "$GOLDEN")"
    curl -fsSI "$ARCHIVE_URL" >/dev/null || die "archive not served"
    ok "archive at $ARCHIVE_URL"
}

make_target_disk() {
    log "creating a blank ${DISK_SIZE_GB}G target disk on a loop device"
    rm -f "$DISK_IMG"
    truncate -s "${DISK_SIZE_GB}G" "$DISK_IMG"
    LOOP=$(losetup -fP --show "$DISK_IMG")
    [ -b "$LOOP" ] || die "losetup failed"
    ok "target disk = $LOOP ($DISK_IMG)"
}

# A real rescue/live environment runs udevd, which populates
# /dev/disk/by-uuid as filesystems appear. Ubuntu's grub-mkconfig only
# emits `root=UUID=` when that symlink exists (otherwise it falls back to
# the raw device path — which is stable on real hardware but not between a
# loop device here and the VM's virtio disk). This sandbox has no udevd,
# so we run a tiny blkid-based shim that maintains /dev/disk/by-uuid the
# same way udev would. It changes nothing about the product; it just makes
# the harness's rescue environment behave like a real one.
start_udev_shim() {
    mkdir -p /dev/disk/by-uuid
    ( while :; do
        for dev in $(lsblk -lnpo NAME 2>/dev/null); do
            u=$(blkid -s UUID -o value "$dev" 2>/dev/null) || continue
            [ -n "$u" ] && ln -sfn "$dev" "/dev/disk/by-uuid/$u"
        done
        sleep 0.3
      done ) &
    UDEV_SHIM_PID=$!
    ok "udev by-uuid shim running (pid $UDEV_SHIM_PID)"
}

run_restore() {
    log "fetching the token-gated restore.sh from the api and running it"
    local script=$WORK/restore.fetched.sh
    curl -fsS -H "Authorization: Bearer $TOKEN" \
        "$API_BASE/v1/jobs/$JOB_ID/restore.sh" -o "$script" || die "restore.sh fetch (auth) failed"
    grep -q "golden archive" "$script" || die "fetched restore.sh looks wrong"
    # Unauthenticated fetch must be refused.
    if curl -fsS "$API_BASE/v1/jobs/$JOB_ID/restore.sh" -o /dev/null 2>/dev/null; then
        die "restore.sh served without a token"
    fi
    ok "restore.sh fetched (and confirmed token-gated)"

    log "running the REAL restore.sh onto $LOOP (partition/extract/grub/phone-home)"
    DEPLOY_API="$API_BASE" \
    DEPLOY_JOB="$JOB_ID" \
    DEPLOY_TOKEN="$TOKEN" \
    DEPLOY_ARCHIVE_URL="$ARCHIVE_URL" \
    DEPLOY_HOSTNAME="$RESTORE_HOSTNAME" \
    DEPLOY_TARGET_DISK="$LOOP" \
    DEPLOY_NO_REBOOT=1 \
        sh "$script" || die "restore.sh failed (see output above)"
    ok "restore.sh completed"
}

assert_job_completed() {
    log "asserting the api job is completed (driven by restore.sh's phone-home)"
    local job state
    job=$(curl -fsS "$API_BASE/api/v1/jobs/$JOB_ID")
    state=$(python3 -c 'import sys,json;print(json.load(sys.stdin)["job"]["state"])' <<<"$job")
    [ "$state" = completed ] || die "job state is '$state', expected completed"
    ok "api job state = completed"
}

boot_in_qemu() {
    log "booting the restored disk in QEMU (TCG) and capturing the serial console"
    rm -f "$SERIAL_LOG"
    # No network, no KVM: pure disk boot. 90s is ample even under TCG for a
    # minimal image that powers itself off after printing the marker.
    timeout 180 qemu-system-x86_64 \
        -M pc -m 1024 -no-reboot -nographic \
        -drive file="$DISK_IMG",format=raw,if=virtio \
        -serial file:"$SERIAL_LOG" \
        >/dev/null 2>&1 || true

    echo "----- serial (tail) -----"
    tail -n 25 "$SERIAL_LOG" 2>/dev/null || true
    echo "-------------------------"
    grep -q "$BOOT_MARKER" "$SERIAL_LOG" || die "restored OS did not boot (marker '$BOOT_MARKER' absent)"
    grep -q "host=$RESTORE_HOSTNAME" "$SERIAL_LOG" || die "restored hostname not applied"
    ok "restored OS booted and reported its regenerated identity"
}

main() {
    mkdir -p "$WORK"
    check_prereqs
    start_api
    seed_job
    serve_archive
    make_target_disk
    start_udev_shim
    run_restore
    assert_job_completed
    boot_in_qemu
    log "ALL GREEN — the deploy core is proven end to end"
    ok "server → token → restore.sh → real disk → completed job → bootable OS"
}

main "$@"
