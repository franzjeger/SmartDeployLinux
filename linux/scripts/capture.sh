#!/bin/sh
# capture.sh — Linux golden-image capture, the counterpart of WinPE's
# capture.cmd. Fetched (token-gated) from /v1/jobs/<id>/capture.sh and
# run inside a rescue/live environment booted on the golden machine —
# the capture cloud-init the server renders for capture jobs does this
# automatically on distros whose live images support nocloud.
#
# Flow:
#   1. locate the root filesystem of the *installed* OS (not the live env)
#   2. locate scratch space (another disk/partition with enough room)
#   3. tar the root filesystem (xattrs/acls/numeric-owner, pruned of
#      volatile paths) compressed with zstd or gzip
#   4. sha256 + size -> POST /v1/jobs/<id>/capture-upload -> presigned PUT
#   5. upload, then POST /v1/jobs/<id>/capture-complete
#
# Requires: sh, curl, tar, sha256sum, lsblk, mount; zstd optional.
# Env (injected by the rendered cloud-init, or export manually):
#   DEPLOY_API   e.g. https://deploy.example.com
#   DEPLOY_JOB   deployment job uuid
#   DEPLOY_TOKEN one-shot bearer token

set -eu

: "${DEPLOY_API:?DEPLOY_API required}"
: "${DEPLOY_JOB:?DEPLOY_JOB required}"
: "${DEPLOY_TOKEN:?DEPLOY_TOKEN required}"

AUTH="Authorization: Bearer ${DEPLOY_TOKEN}"
CURL="curl --silent --show-error --fail"
# Pinned CA if the bootstrap environment carries one; system store otherwise.
[ -r /etc/ssl/certs/deploy-ca.pem ] && CURL="$CURL --cacert /etc/ssl/certs/deploy-ca.pem"

report() {
    # report <phase> <message> — best-effort phone-home.
    $CURL --max-time 10 -X POST \
        -H "$AUTH" -H "Content-Type: application/json" \
        --data-binary "{\"phase\":\"$1\",\"message\":\"$2\"}" \
        "$DEPLOY_API/v1/jobs/$DEPLOY_JOB/events" >/dev/null 2>&1 || true
}

fatal() {
    echo "FATAL: $1" >&2
    report failed "$1"
    exit 1
}

report imaging "linux capture starting"

# --- 1. find the installed root filesystem ---------------------------
SRC_MNT=/mnt/deploy-capture-src
mkdir -p "$SRC_MNT"
ROOT_DEV=
for dev in $(lsblk -rno NAME,TYPE | awk '$2=="part"{print "/dev/"$1}'); do
    mountpoint -q "$SRC_MNT" && umount "$SRC_MNT" || true
    if mount -o ro "$dev" "$SRC_MNT" 2>/dev/null; then
        if [ -f "$SRC_MNT/etc/fstab" ] && [ -d "$SRC_MNT/etc" ] && [ -d "$SRC_MNT/usr" ]; then
            ROOT_DEV=$dev
            break
        fi
        umount "$SRC_MNT"
    fi
done
[ -n "$ROOT_DEV" ] || fatal "no installed root filesystem found"
report imaging "capturing root filesystem on $ROOT_DEV"

# --- 2. find scratch space -------------------------------------------
# Need roughly the used size of the source (compression usually wins).
NEED_KB=$(df -Pk "$SRC_MNT" | awk 'NR==2{print $3}')
SCRATCH=
for cand in /var/tmp /tmp /run; do
    AVAIL_KB=$(df -Pk "$cand" 2>/dev/null | awk 'NR==2{print $4}')
    [ -n "$AVAIL_KB" ] && [ "$AVAIL_KB" -gt "$NEED_KB" ] && { SCRATCH=$cand; break; }
done
if [ -z "$SCRATCH" ]; then
    # Try mounting another writable partition as scratch.
    SCRATCH_MNT=/mnt/deploy-capture-scratch
    mkdir -p "$SCRATCH_MNT"
    for dev in $(lsblk -rno NAME,TYPE | awk '$2=="part"{print "/dev/"$1}'); do
        [ "$dev" = "$ROOT_DEV" ] && continue
        if mount "$dev" "$SCRATCH_MNT" 2>/dev/null; then
            AVAIL_KB=$(df -Pk "$SCRATCH_MNT" | awk 'NR==2{print $4}')
            [ "$AVAIL_KB" -gt "$NEED_KB" ] && { SCRATCH=$SCRATCH_MNT; break; }
            umount "$SCRATCH_MNT"
        fi
    done
fi
[ -n "$SCRATCH" ] || fatal "no scratch space large enough (${NEED_KB}KB needed)"

# --- 3. tar the root filesystem --------------------------------------
if command -v zstd >/dev/null 2>&1; then
    EXT=tar.zst; COMPRESS="zstd -T0"
else
    EXT=tar.gz; COMPRESS="gzip -1"
fi
IMG="$SCRATCH/golden.$EXT"
report imaging "archiving to $IMG"
# Volatile/host-specific paths are pruned; the deploy-side answer file
# regenerates machine identity (hostname, machine-id, ssh host keys).
tar --numeric-owner --xattrs --acls --one-file-system \
    -C "$SRC_MNT" \
    --exclude='./etc/machine-id' \
    --exclude='./etc/ssh/ssh_host_*' \
    --exclude='./var/log/*' \
    --exclude='./var/cache/*' \
    --exclude='./tmp/*' \
    --exclude='./var/tmp/*' \
    --exclude='./swap*' \
    -cf - . | $COMPRESS > "$IMG" || fatal "tar failed"
umount "$SRC_MNT" || true

# --- 4. register -----------------------------------------------------
SHA=$(sha256sum "$IMG" | awk '{print $1}')
SIZE=$(wc -c < "$IMG" | tr -d ' ')
report imaging "registering blob (sha $SHA, $SIZE bytes)"
RESP=$($CURL --max-time 60 -X POST \
    -H "$AUTH" -H "Content-Type: application/json" \
    --data-binary "{\"sha256\":\"$SHA\",\"size_bytes\":$SIZE}" \
    "$DEPLOY_API/v1/jobs/$DEPLOY_JOB/capture-upload") \
    || fatal "capture-upload registration failed"
UPLOAD_URL=$(printf '%s' "$RESP" | sed -n 's/.*"upload_url":"\([^"]*\)".*/\1/p')
[ -n "$UPLOAD_URL" ] || fatal "no upload_url in response"

# --- 5. upload + finalize --------------------------------------------
report imaging "uploading archive to object store"
curl --silent --show-error --fail -T "$IMG" "$UPLOAD_URL" \
    || fatal "archive upload failed"

$CURL --max-time 60 -X POST \
    -H "$AUTH" -H "Content-Type: application/json" \
    --data-binary "{\"sha256\":\"$SHA\"}" \
    "$DEPLOY_API/v1/jobs/$DEPLOY_JOB/capture-complete" \
    || fatal "capture-complete failed"

rm -f "$IMG"
report completed "linux golden image captured and registered"
echo "capture complete"
