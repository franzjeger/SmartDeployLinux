#!/bin/sh
# restore.sh — deploy a captured Linux golden archive (the output of
# capture.sh) onto a machine. Fetched token-gated from
# /v1/jobs/<id>/restore.sh and run in a live/rescue environment; the
# restore cloud-init the server renders for golden-image deploy jobs
# does this automatically.
#
# Flow:
#   1. pick the target disk (largest fixed, non-removable disk)
#   2. partition GPT: 512 MiB EFI system partition + ext4 root (v1 is
#      UEFI-only, single-disk, no LVM — see docs/LINUX.md)
#   3. stream-untar the golden archive onto the new root
#   4. regenerate machine identity: hostname, machine-id, SSH host
#      keys, fstab with the new filesystem UUIDs
#   5. install GRUB (removable path, so it boots even when efivars
#      writes are unavailable) and rebuild its config
#   6. phone home completed, reboot into the restored OS
#
# Requires: sh, curl, tar, sgdisk (or parted), mkfs.vfat, mkfs.ext4,
# blkid, chroot; zstd when the archive is .tar.zst.
# Env (injected by the rendered cloud-init):
#   DEPLOY_API, DEPLOY_JOB, DEPLOY_TOKEN   — as in capture.sh
#   DEPLOY_ARCHIVE_URL                     — golden archive (mirror-aware)
#   DEPLOY_HOSTNAME                        — hostname for the restored OS
#   DEPLOY_NO_REBOOT                       — set non-empty to skip reboot

set -eu

: "${DEPLOY_API:?DEPLOY_API required}"
: "${DEPLOY_JOB:?DEPLOY_JOB required}"
: "${DEPLOY_TOKEN:?DEPLOY_TOKEN required}"
: "${DEPLOY_ARCHIVE_URL:?DEPLOY_ARCHIVE_URL required}"
DEPLOY_HOSTNAME=${DEPLOY_HOSTNAME:-restored}

AUTH="Authorization: Bearer ${DEPLOY_TOKEN}"
CURL="curl --silent --show-error --fail"
[ -r /etc/ssl/certs/deploy-ca.pem ] && CURL="$CURL --cacert /etc/ssl/certs/deploy-ca.pem"

report() {
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

report imaging "linux golden restore starting"

# --- 1. pick the target disk (DESTRUCTIVE) ---------------------------
DISK=
DISK_SIZE=0
for line in $(lsblk -dbrno NAME,SIZE,TYPE,RM | awk '$3=="disk" && $4=="0" {print $1":"$2}'); do
    name=${line%%:*}
    size=${line##*:}
    # Skip the disk backing the live environment, if resolvable.
    livedev=$(findmnt -nro SOURCE / 2>/dev/null | sed 's/[0-9]*$//;s|/dev/||' || true)
    [ -n "$livedev" ] && [ "$name" = "$livedev" ] && continue
    if [ "$size" -gt "$DISK_SIZE" ]; then
        DISK=/dev/$name
        DISK_SIZE=$size
    fi
done
[ -n "$DISK" ] || fatal "no target disk found"
report imaging "partitioning $DISK (DESTRUCTIVE)"

# --- 2. partition ----------------------------------------------------
if command -v sgdisk >/dev/null 2>&1; then
    sgdisk --zap-all "$DISK" >/dev/null
    sgdisk -n 1:0:+512M -t 1:ef00 -c 1:ESP "$DISK" >/dev/null
    sgdisk -n 2:0:0     -t 2:8300 -c 2:root "$DISK" >/dev/null
else
    parted -s "$DISK" mklabel gpt \
        mkpart ESP fat32 1MiB 513MiB set 1 esp on \
        mkpart root ext4 513MiB 100%
fi
# Partition device naming: sda -> sda1, nvme0n1 -> nvme0n1p1.
case "$DISK" in
    *[0-9]) P1=${DISK}p1; P2=${DISK}p2 ;;
    *)      P1=${DISK}1;  P2=${DISK}2  ;;
esac
command -v partprobe >/dev/null 2>&1 && partprobe "$DISK" || true
sleep 2

mkfs.vfat -F32 -n ESP "$P1" >/dev/null || fatal "mkfs ESP failed"
mkfs.ext4 -q -F -L root "$P2" || fatal "mkfs root failed"

MNT=/mnt/deploy-restore
mkdir -p "$MNT"
mount "$P2" "$MNT" || fatal "mount root failed"
mkdir -p "$MNT/boot/efi"
mount "$P1" "$MNT/boot/efi" || fatal "mount ESP failed"

# --- 3. stream-untar the archive -------------------------------------
report imaging "streaming golden archive onto $P2"
case "$DEPLOY_ARCHIVE_URL" in
    *zst*) DECOMP="zstd -d" ;;
    *)     DECOMP="gzip -dc" ;;
esac
$CURL --max-time 0 "$DEPLOY_ARCHIVE_URL" \
    | $DECOMP \
    | tar --numeric-owner --xattrs --acls -xf - -C "$MNT" \
    || fatal "archive download/extract failed"

# --- 4. regenerate identity ------------------------------------------
report imaging "regenerating machine identity"
echo "$DEPLOY_HOSTNAME" > "$MNT/etc/hostname"
: > "$MNT/etc/machine-id"      # empty file -> regenerated on first boot
rm -f "$MNT"/etc/ssh/ssh_host_* 2>/dev/null || true
chroot "$MNT" ssh-keygen -A >/dev/null 2>&1 || true

ROOT_UUID=$(blkid -s UUID -o value "$P2")
ESP_UUID=$(blkid -s UUID -o value "$P1")
cat > "$MNT/etc/fstab" <<EOF
UUID=$ROOT_UUID  /          ext4  defaults  0 1
UUID=$ESP_UUID   /boot/efi  vfat  umask=0077  0 1
EOF

# --- 5. bootloader ---------------------------------------------------
report imaging "installing bootloader"
for fs in dev proc sys; do
    mount --bind /$fs "$MNT/$fs" || fatal "bind mount $fs failed"
done
[ -d /sys/firmware/efi/efivars ] && \
    mount --bind /sys/firmware/efi/efivars "$MNT/sys/firmware/efi/efivars" 2>/dev/null || true

BOOTLOADER_ID=deploy
# --removable installs to the fallback path (EFI/BOOT/BOOTX64.EFI) so
# the machine boots even when NVRAM writes fail in the rescue env.
chroot "$MNT" grub-install --target=x86_64-efi \
    --efi-directory=/boot/efi --bootloader-id="$BOOTLOADER_ID" --removable \
    || fatal "grub-install failed"
if chroot "$MNT" test -x /usr/sbin/update-grub; then
    chroot "$MNT" update-grub || fatal "update-grub failed"
else
    chroot "$MNT" grub2-mkconfig -o /boot/grub2/grub.cfg \
        || chroot "$MNT" grub-mkconfig -o /boot/grub/grub.cfg \
        || fatal "grub config generation failed"
fi

# --- 6. finish -------------------------------------------------------
for fs in sys/firmware/efi/efivars sys proc dev boot/efi ""; do
    umount "$MNT/$fs" 2>/dev/null || true
done
report completed "golden image restored to $DISK"
echo "restore complete"
[ -n "${DEPLOY_NO_REBOOT:-}" ] || reboot -f
