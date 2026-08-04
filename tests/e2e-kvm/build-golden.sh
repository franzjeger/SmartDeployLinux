#!/usr/bin/env bash
# build-golden.sh — build a minimal but genuinely bootable Linux golden
# image and package it exactly as capture.sh would: a zstd-compressed tar
# of a full root filesystem. This is the *source* image an operator would
# capture once; the harness then restores it onto a blank disk with the
# project's real restore.sh and boots the result.
#
# We debootstrap Ubuntu (the archive is reachable; a real kernel, GRUB and
# initramfs-tools are included so the restored disk boots for real). A tiny
# oneshot service prints an unmistakable marker to the serial console on
# first boot so the harness can assert "it actually booted" from the QEMU
# serial log, then powers the machine off so the run terminates on its own.
set -euo pipefail

SUITE=${SUITE:-noble}
MIRROR=${MIRROR:-http://archive.ubuntu.com/ubuntu}
OUT=${1:?usage: build-golden.sh <out.tar.zst>}
ROOT=$(mktemp -d /var/tmp/golden.XXXXXX)
MARKER="DEPLOY-E2E-BOOT-OK"

cleanup() { rm -rf "$ROOT"; }
trap cleanup EXIT

echo ">> debootstrap $SUITE -> $ROOT"
debootstrap --arch=amd64 --variant=minbase \
    --include=linux-image-virtual,grub-pc,initramfs-tools,systemd-sysv,udev,zstd,iproute2 \
    "$SUITE" "$ROOT" "$MIRROR"

echo ">> configuring golden image"
# Serial console everywhere: GRUB menu, kernel, and a getty.
cat >"$ROOT/etc/default/grub" <<'EOF'
GRUB_DEFAULT=0
GRUB_TIMEOUT=1
GRUB_DISTRIBUTOR=deploy
GRUB_CMDLINE_LINUX_DEFAULT=""
GRUB_CMDLINE_LINUX="console=tty0 console=ttyS0,115200 net.ifnames=0"
GRUB_TERMINAL="console serial"
GRUB_SERIAL_COMMAND="serial --unit=0 --speed=115200"
EOF

# First-boot proof service: print a marker that embeds the freshly
# regenerated identity (hostname + machine-id) so the log proves this is
# the *restored* instance, then power off.
cat >"$ROOT/etc/systemd/system/deploy-e2e-proof.service" <<EOF
[Unit]
Description=deploy e2e boot proof
After=multi-user.target
[Service]
Type=oneshot
StandardOutput=journal+console
ExecStart=/bin/sh -c 'echo "$MARKER host=\$(cat /etc/hostname) machine-id=\$(cat /etc/machine-id) root=\$(findmnt -nro SOURCE /)"'
ExecStartPost=/bin/sh -c 'sleep 2; systemctl poweroff --no-block'
[Install]
WantedBy=multi-user.target
EOF
chroot "$ROOT" systemctl enable deploy-e2e-proof.service >/dev/null 2>&1
# Set a root password so the image is a realistic OS (not used by the proof).
echo 'root:deploy' | chroot "$ROOT" chpasswd

echo ">> packaging golden archive -> $OUT"
tar --numeric-owner --xattrs --acls -C "$ROOT" -cf - . | zstd -q -T0 -19 -o "$OUT" -f
echo ">> golden image ready: $(du -h "$OUT" | cut -f1)  ($OUT)"
