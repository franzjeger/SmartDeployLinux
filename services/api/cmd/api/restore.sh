#!/bin/sh
# restore.sh — deploy a captured Linux golden archive (the output of
# capture.sh) onto a machine. Fetched token-gated from
# /v1/jobs/<id>/restore.sh and run in a live/rescue environment; the
# restore cloud-init the server renders for golden-image deploy jobs
# does this automatically.
#
# Boot mode is auto-detected: UEFI (ESP + GRUB x86_64-efi --removable)
# when /sys/firmware/efi exists, legacy BIOS (GPT with a BIOS boot
# partition + GRUB i386-pc) otherwise.
#
# Disk layout is chosen by DEPLOY_LAYOUT (from the profile's
# answer-file vars, key `restore_layout`):
#   plain (default)  boot partition(s) + one ext4 root partition
#   lvm              boot partition(s) + LVM PV; VG (DEPLOY_VG, default
#                    vg0) with a root LV and, when DEPLOY_SWAP is set
#                    (e.g. "8G"), a swap LV
#
# Flow:
#   1. pick the target disk (largest fixed, non-removable disk)
#   2. partition per boot mode + layout
#   3. stream-untar the golden archive onto the new root
#   4. regenerate machine identity: hostname, machine-id, SSH host
#      keys, fstab for the new filesystems (incl. swap)
#   5. install GRUB for the detected boot mode, rebuild its config,
#      regenerate the initramfs (so LVM/driver changes take effect —
#      hardware independence)
#   6. phone home completed, reboot into the restored OS
#
# Requires: sh, curl, tar, sgdisk (or parted), mkfs.vfat, mkfs.ext4,
# blkid, chroot; zstd for .tar.zst archives; lvm2 when DEPLOY_LAYOUT=lvm.
# Env (injected by the rendered cloud-init):
#   DEPLOY_API, DEPLOY_JOB, DEPLOY_TOKEN   — as in capture.sh
#   DEPLOY_ARCHIVE_URL                     — golden archive (mirror-aware)
#   DEPLOY_HOSTNAME                        — hostname for the restored OS
#   DEPLOY_LAYOUT                          — plain | lvm (default plain)
#   DEPLOY_VG                              — LVM VG name (default vg0)
#   DEPLOY_SWAP                            — LVM swap LV size, e.g. 8G
#   DEPLOY_NO_REBOOT                       — set non-empty to skip reboot
#   DEPLOY_TARGET_DISK                     — force the target disk (e.g.
#                                            /dev/sdb); overrides the
#                                            largest-fixed-disk auto-pick.
#                                            Use when the machine has more
#                                            than one fixed disk and the
#                                            biggest is not the OS disk, or
#                                            to drive restore against a
#                                            specific block device.

set -eu

: "${DEPLOY_API:?DEPLOY_API required}"
: "${DEPLOY_JOB:?DEPLOY_JOB required}"
: "${DEPLOY_TOKEN:?DEPLOY_TOKEN required}"
: "${DEPLOY_ARCHIVE_URL:?DEPLOY_ARCHIVE_URL required}"
DEPLOY_HOSTNAME=${DEPLOY_HOSTNAME:-restored}
DEPLOY_LAYOUT=${DEPLOY_LAYOUT:-plain}
DEPLOY_VG=${DEPLOY_VG:-vg0}
DEPLOY_SWAP=${DEPLOY_SWAP:-}

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

# --- boot mode -------------------------------------------------------
if [ -d /sys/firmware/efi ]; then
    BOOT_MODE=uefi
else
    BOOT_MODE=bios
fi
case "$DEPLOY_LAYOUT" in
    plain|lvm) ;;
    *) fatal "unknown DEPLOY_LAYOUT '$DEPLOY_LAYOUT' (plain|lvm)" ;;
esac
if [ "$DEPLOY_LAYOUT" = lvm ] && ! command -v lvcreate >/dev/null 2>&1; then
    fatal "DEPLOY_LAYOUT=lvm but lvm2 tools missing in this environment"
fi

report imaging "linux golden restore starting ($BOOT_MODE, layout=$DEPLOY_LAYOUT)"

# --- 1. pick the target disk (DESTRUCTIVE) ---------------------------
if [ -n "${DEPLOY_TARGET_DISK:-}" ]; then
    # Operator/automation override: use exactly this block device. Must be
    # a real block device and must not be the live root's disk.
    [ -b "$DEPLOY_TARGET_DISK" ] || fatal "DEPLOY_TARGET_DISK '$DEPLOY_TARGET_DISK' is not a block device"
    livedev=$(findmnt -nro SOURCE / 2>/dev/null | sed 's/[0-9]*$//;s|/dev/||' || true)
    [ -n "$livedev" ] && [ "$DEPLOY_TARGET_DISK" = "/dev/$livedev" ] && \
        fatal "DEPLOY_TARGET_DISK '$DEPLOY_TARGET_DISK' is the live root disk; refusing"
    DISK=$DEPLOY_TARGET_DISK
else
    DISK=
    DISK_SIZE=0
    for line in $(lsblk -dbrno NAME,SIZE,TYPE,RM | awk '$3=="disk" && $4=="0" {print $1":"$2}'); do
        name=${line%%:*}
        size=${line##*:}
        livedev=$(findmnt -nro SOURCE / 2>/dev/null | sed 's/[0-9]*$//;s|/dev/||' || true)
        [ -n "$livedev" ] && [ "$name" = "$livedev" ] && continue
        if [ "$size" -gt "$DISK_SIZE" ]; then
            DISK=/dev/$name
            DISK_SIZE=$size
        fi
    done
fi
[ -n "$DISK" ] || fatal "no target disk found"
report imaging "partitioning $DISK (DESTRUCTIVE)"

# --- 2. partition per boot mode + layout -----------------------------
command -v sgdisk >/dev/null 2>&1 || fatal "sgdisk required"
sgdisk --zap-all "$DISK" >/dev/null
if [ "$BOOT_MODE" = uefi ]; then
    sgdisk -n 1:0:+512M -t 1:ef00 -c 1:ESP "$DISK" >/dev/null
else
    # BIOS boot partition for GRUB core.img on GPT disks.
    sgdisk -n 1:0:+2M -t 1:ef02 -c 1:bios_boot "$DISK" >/dev/null
fi
if [ "$DEPLOY_LAYOUT" = lvm ]; then
    sgdisk -n 2:0:0 -t 2:8e00 -c 2:pv "$DISK" >/dev/null
else
    sgdisk -n 2:0:0 -t 2:8300 -c 2:root "$DISK" >/dev/null
fi
case "$DISK" in
    *[0-9]) P1=${DISK}p1; P2=${DISK}p2 ;;
    *)      P1=${DISK}1;  P2=${DISK}2  ;;
esac
command -v partprobe >/dev/null 2>&1 && partprobe "$DISK" || true
sleep 2

SWAP_DEV=
if [ "$DEPLOY_LAYOUT" = lvm ]; then
    pvcreate -ff -y "$P2" >/dev/null || fatal "pvcreate failed"
    vgcreate "$DEPLOY_VG" "$P2" >/dev/null || fatal "vgcreate failed"
    if [ -n "$DEPLOY_SWAP" ]; then
        lvcreate -y -L "$DEPLOY_SWAP" -n swap "$DEPLOY_VG" >/dev/null || fatal "lvcreate swap failed"
        SWAP_DEV=/dev/$DEPLOY_VG/swap
        mkswap "$SWAP_DEV" >/dev/null || fatal "mkswap failed"
    fi
    lvcreate -y -l 100%FREE -n root "$DEPLOY_VG" >/dev/null || fatal "lvcreate root failed"
    ROOT_DEV=/dev/$DEPLOY_VG/root
else
    ROOT_DEV=$P2
fi

mkfs.ext4 -q -F -L root "$ROOT_DEV" || fatal "mkfs root failed"
MNT=/mnt/deploy-restore
mkdir -p "$MNT"
mount "$ROOT_DEV" "$MNT" || fatal "mount root failed"
if [ "$BOOT_MODE" = uefi ]; then
    mkfs.vfat -F32 -n ESP "$P1" >/dev/null || fatal "mkfs ESP failed"
    mkdir -p "$MNT/boot/efi"
    mount "$P1" "$MNT/boot/efi" || fatal "mount ESP failed"
fi

# --- 3. stream-untar the archive -------------------------------------
report imaging "streaming golden archive onto $ROOT_DEV"
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
: > "$MNT/etc/machine-id"
rm -f "$MNT"/etc/ssh/ssh_host_* 2>/dev/null || true
chroot "$MNT" ssh-keygen -A >/dev/null 2>&1 || true

ROOT_UUID=$(blkid -s UUID -o value "$ROOT_DEV")
{
    echo "UUID=$ROOT_UUID  /  ext4  defaults  0 1"
    if [ "$BOOT_MODE" = uefi ]; then
        echo "UUID=$(blkid -s UUID -o value "$P1")  /boot/efi  vfat  umask=0077  0 1"
    fi
    if [ -n "$SWAP_DEV" ]; then
        echo "UUID=$(blkid -s UUID -o value "$SWAP_DEV")  none  swap  sw  0 0"
    fi
} > "$MNT/etc/fstab"

# --- 5. bootloader + initramfs ---------------------------------------
report imaging "installing bootloader ($BOOT_MODE)"
for fs in dev proc sys; do
    mount --bind /$fs "$MNT/$fs" || fatal "bind mount $fs failed"
done
[ "$BOOT_MODE" = uefi ] && [ -d /sys/firmware/efi/efivars ] && \
    mount --bind /sys/firmware/efi/efivars "$MNT/sys/firmware/efi/efivars" 2>/dev/null || true

if [ "$BOOT_MODE" = uefi ]; then
    # --removable installs the fallback path (EFI/BOOT/BOOTX64.EFI) so
    # the machine boots even when NVRAM writes fail in the rescue env.
    chroot "$MNT" grub-install --target=x86_64-efi \
        --efi-directory=/boot/efi --bootloader-id=deploy --removable \
        || fatal "grub-install (uefi) failed"
else
    chroot "$MNT" grub-install --target=i386-pc "$DISK" \
        || fatal "grub-install (bios) failed"
fi
if chroot "$MNT" test -x /usr/sbin/update-grub; then
    chroot "$MNT" update-grub || fatal "update-grub failed"
else
    chroot "$MNT" grub2-mkconfig -o /boot/grub2/grub.cfg \
        || chroot "$MNT" grub-mkconfig -o /boot/grub/grub.cfg \
        || fatal "grub config generation failed"
fi

# Regenerate the initramfs so the restored OS boots on THIS hardware
# and layout (LVM modules, storage drivers) even when the golden source
# machine differed — the hardware-independence step. Best-effort: a
# golden image captured on identical hardware boots without it.
report imaging "regenerating initramfs"
if chroot "$MNT" test -x /usr/sbin/update-initramfs; then
    chroot "$MNT" update-initramfs -u -k all || report imaging "update-initramfs failed (continuing)"
elif chroot "$MNT" command -v dracut >/dev/null 2>&1; then
    chroot "$MNT" dracut --force --regenerate-all || report imaging "dracut failed (continuing)"
fi

# --- 6. finish -------------------------------------------------------
for fs in sys/firmware/efi/efivars sys proc dev boot/efi ""; do
    umount "$MNT/$fs" 2>/dev/null || true
done
report completed "golden image restored to $DISK ($BOOT_MODE, $DEPLOY_LAYOUT)"
echo "restore complete"
[ -n "${DEPLOY_NO_REBOOT:-}" ] || reboot -f
