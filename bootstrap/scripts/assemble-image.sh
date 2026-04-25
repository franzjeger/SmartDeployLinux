#!/usr/bin/env bash
# Assemble a hybrid GPT/MBR USB image from buildroot output + iPXE
# binaries. Output is a flashable .img.
#
# Layout produced:
#   GPT MBR + protective MBR
#   p1: ESP, FAT32, 64 MiB
#       /EFI/BOOT/BOOTX64.EFI    -- shim (Microsoft-signed)
#       /EFI/BOOT/grubx64.efi    -- our signed GRUB2
#       /EFI/BOOT/grub.cfg
#       /EFI/BOOT/IPXE_NEXT.EFI  -- placeholder; replaced at boot time
#   p2: ext4, rest of image
#       /boot/vmlinuz, /boot/initramfs.img
#       /etc/deploy/config.json.template + ssl certs
#       /opt/iPXE/{ipxe.efi, undionly.kpxe, ipxe.lkrn}
#
# This script needs root (loop devices, mkfs, mount). For unprivileged
# builds, re-implement on top of guestfish or systemd-nspawn (out of
# scope for v1).

set -euo pipefail

usage() {
    grep '^# ' "$0" | sed 's/^# //'
    exit 2
}

OUTPUT=
SIZE=320M
KERNEL=
INITRAMFS=
IPXE_EFI=
IPXE_BIOS=
IPXE_LKRN=
KEYS_DIR=

while [ $# -gt 0 ]; do
    case "$1" in
        --output)     OUTPUT=$2; shift 2 ;;
        --size)       SIZE=$2; shift 2 ;;
        --kernel)     KERNEL=$2; shift 2 ;;
        --initramfs)  INITRAMFS=$2; shift 2 ;;
        --ipxe-efi)   IPXE_EFI=$2; shift 2 ;;
        --ipxe-bios)  IPXE_BIOS=$2; shift 2 ;;
        --ipxe-lkrn)  IPXE_LKRN=$2; shift 2 ;;
        --keys-dir)   KEYS_DIR=$2; shift 2 ;;
        *) usage ;;
    esac
done

[ -n "$OUTPUT" ]    || usage
[ -n "$KERNEL" ]    || usage
[ -n "$INITRAMFS" ] || usage
[ -n "$IPXE_EFI" ]  || usage
[ -n "$IPXE_BIOS" ] || usage

[ "$EUID" -eq 0 ] || { echo "this script needs root for losetup+mkfs"; exit 1; }

WORK=$(mktemp -d)
trap 'cleanup' EXIT
cleanup() {
    set +e
    [ -n "${MOUNT_ESP:-}" ] && umount "$MOUNT_ESP"
    [ -n "${MOUNT_FS:-}" ]  && umount "$MOUNT_FS"
    [ -n "${LOOP:-}" ]      && losetup -d "$LOOP"
    rm -rf "$WORK"
}

echo "==> creating $OUTPUT ($SIZE)"
truncate -s "$SIZE" "$OUTPUT"

echo "==> partitioning"
sgdisk --clear \
       --new=1:2048:+64M  --typecode=1:ef00 --change-name=1:ESP \
       --new=2:0:0        --typecode=2:8300 --change-name=2:bootstrap \
       --hybrid=1 \
       "$OUTPUT"

LOOP=$(losetup --show -fP "$OUTPUT")
ESP_DEV=${LOOP}p1
FS_DEV=${LOOP}p2

echo "==> formatting"
mkfs.vfat -F32 -n ESP "$ESP_DEV" >/dev/null
mkfs.ext4 -L bootstrap -F "$FS_DEV" >/dev/null

MOUNT_ESP=$WORK/esp
MOUNT_FS=$WORK/fs
mkdir -p "$MOUNT_ESP" "$MOUNT_FS"
mount "$ESP_DEV" "$MOUNT_ESP"
mount "$FS_DEV" "$MOUNT_FS"

echo "==> populating ESP"
mkdir -p "$MOUNT_ESP/EFI/BOOT"
# Microsoft-signed shim is expected to live next to this script as
# external/shim/shimx64.efi (downloaded once via bootstrap/external/fetch.sh).
SHIM=../external/shim/shimx64.efi
GRUB=../external/grub/grubx64.efi
if [ -r "$SHIM" ] && [ -r "$GRUB" ]; then
    install -m 0644 "$SHIM" "$MOUNT_ESP/EFI/BOOT/BOOTX64.EFI"
    install -m 0644 "$GRUB" "$MOUNT_ESP/EFI/BOOT/grubx64.efi"
else
    echo "WARN: shim/grub binaries missing. Secure Boot path will not work."
    echo "      Run bootstrap/external/fetch.sh first."
fi

cat > "$MOUNT_ESP/EFI/BOOT/grub.cfg" <<'GRUB'
set timeout=2
set default=0

menuentry "deploy-bootstrap" {
    insmod ext2
    search --set=root --label bootstrap
    linux /boot/vmlinuz console=tty1 console=ttyS0,115200 quiet
    initrd /boot/initramfs.img
}
GRUB

# Placeholder for runtime exec-ipxe.
install -m 0644 "$IPXE_EFI" "$MOUNT_ESP/EFI/BOOT/IPXE_NEXT.EFI"

echo "==> populating rootfs partition"
mkdir -p "$MOUNT_FS/boot" "$MOUNT_FS/opt/iPXE" "$MOUNT_FS/etc/deploy" "$MOUNT_FS/etc/ssl/certs"
install -m 0644 "$KERNEL"    "$MOUNT_FS/boot/vmlinuz"
install -m 0644 "$INITRAMFS" "$MOUNT_FS/boot/initramfs.img"
install -m 0644 "$IPXE_EFI"  "$MOUNT_FS/opt/iPXE/ipxe.efi"
install -m 0644 "$IPXE_BIOS" "$MOUNT_FS/opt/iPXE/undionly.kpxe"
[ -n "${IPXE_LKRN:-}" ] && [ -r "$IPXE_LKRN" ] && \
    install -m 0644 "$IPXE_LKRN" "$MOUNT_FS/opt/iPXE/ipxe.lkrn"

# config.json template lives in initramfs (overlay), not on this
# partition; make-stick.sh writes the rendered config.json into the
# rootfs partition's /etc/deploy/.
touch "$MOUNT_FS/etc/deploy/.placeholder"

sync

# Normalize timestamps for reproducibility.
find "$MOUNT_ESP" "$MOUNT_FS" -exec touch -d @0 {} +

umount "$MOUNT_ESP"
umount "$MOUNT_FS"
losetup -d "$LOOP"
trap - EXIT
rm -rf "$WORK"

echo "==> done: $OUTPUT"
sha256sum "$OUTPUT"
