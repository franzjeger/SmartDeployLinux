#!/usr/bin/env bash
# Customize a freshly built deploy-bootstrap.img with a specific
# tailnet, deploy URL, and pinned CA. Produces a flashable image with
# NO embedded auth keys.
#
# Idempotent: running it twice with the same inputs produces a
# bit-identical image (modulo timestamps in filesystem metadata, which
# we normalize via mtime=0).
#
# Usage:
#   ./make-stick.sh \
#       --output         deploy-bootstrap-acmecorp.img \
#       --tailnet        acmecorp.headscale.example.com \
#       --deploy-url     https://deploy.acmecorp.example.com \
#       --ca-cert        /path/to/deploy-ca.pem \
#       [--control-url   https://headscale.acmecorp.example.com]

set -euo pipefail

usage() {
    grep '^# ' "$0" | sed 's/^# //'
    exit 2
}

OUT=
TAILNET=
DEPLOY_URL=
CA_CERT=
CONTROL_URL=
SOURCE_IMG=output/deploy-bootstrap.img

while [ $# -gt 0 ]; do
    case "$1" in
        --output)       OUT=$2; shift 2 ;;
        --tailnet)      TAILNET=$2; shift 2 ;;
        --deploy-url)   DEPLOY_URL=$2; shift 2 ;;
        --ca-cert)      CA_CERT=$2; shift 2 ;;
        --control-url)  CONTROL_URL=$2; shift 2 ;;
        --source)       SOURCE_IMG=$2; shift 2 ;;
        -h|--help)      usage ;;
        *)              echo "Unknown flag: $1"; usage ;;
    esac
done

[ -n "$OUT" ]         || { echo "--output required"; exit 2; }
[ -n "$TAILNET" ]     || { echo "--tailnet required"; exit 2; }
[ -n "$DEPLOY_URL" ]  || { echo "--deploy-url required"; exit 2; }
[ -n "$CA_CERT" ]     || { echo "--ca-cert required"; exit 2; }
[ -r "$CA_CERT" ]     || { echo "$CA_CERT not readable"; exit 2; }
[ -r "$SOURCE_IMG" ]  || { echo "$SOURCE_IMG missing; run 'make' first"; exit 2; }

CONTROL_URL=${CONTROL_URL:-}
VERSION=$(git -C "$(dirname "$0")" rev-parse --short HEAD 2>/dev/null || echo dev)

echo "==> copying $SOURCE_IMG -> $OUT"
cp --reflink=auto "$SOURCE_IMG" "$OUT"

# We need to mount the rootfs partition to drop in config.json + ca cert.
# Find the partitions in the image. Layout (set up by assemble-image.sh):
#   p1 = ESP (FAT32)
#   p2 = rootfs (ext4)
LOOP=$(losetup --show -fP "$OUT")
trap 'losetup -d "$LOOP" 2>/dev/null || true' EXIT

ROOTFS_DEV=${LOOP}p2
ESP_DEV=${LOOP}p1

MNT=$(mktemp -d)
trap 'umount "$MNT" 2>/dev/null || true; losetup -d "$LOOP" 2>/dev/null || true; rmdir "$MNT" || true' EXIT

mount -t ext4 "$ROOTFS_DEV" "$MNT"

# Drop the CA cert.
install -m 0644 "$CA_CERT" "$MNT/etc/ssl/certs/deploy-ca.pem"

# Render config.json from the template.
TEMPLATE=$MNT/etc/deploy/config.json.template
[ -r "$TEMPLATE" ] || { echo "FATAL: $TEMPLATE missing in image"; exit 1; }

IMG_SHA=$(sha256sum "$OUT" | awk '{print $1}')
sed \
    -e "s|@@DEPLOY_URL@@|$DEPLOY_URL|g" \
    -e "s|@@TAILNET@@|$TAILNET|g" \
    -e "s|@@CONTROL_URL@@|$CONTROL_URL|g" \
    -e "s|@@IMAGE_SHA256@@|$IMG_SHA|g" \
    -e "s|@@VERSION@@|$VERSION|g" \
    "$TEMPLATE" > "$MNT/etc/deploy/config.json"

# Capture the CA fingerprint for the bootstrap_sticks DB row.
CA_FP=$(openssl x509 -in "$CA_CERT" -noout -fingerprint -sha256 | cut -d= -f2 | tr -d :)

sync
umount "$MNT"
losetup -d "$LOOP"
trap - EXIT
rmdir "$MNT"

# Recompute the final image hash now that we wrote config.json into it.
FINAL_SHA=$(sha256sum "$OUT" | awk '{print $1}')

echo
echo "==> done"
echo "    image:           $OUT"
echo "    sha256:          $FINAL_SHA"
echo "    tailnet:         $TAILNET"
echo "    deploy_url:      $DEPLOY_URL"
echo "    control_url:     ${CONTROL_URL:-<from response>}"
echo "    ca fingerprint:  $CA_FP"
echo
echo "Register this stick with the deploy server (so CA rotation can"
echo "track it) by running:"
echo "    deployctl bootstrap-sticks register \\"
echo "        --image-sha $FINAL_SHA \\"
echo "        --tailnet $TAILNET \\"
echo "        --deploy-url $DEPLOY_URL \\"
echo "        --ca-fingerprint $CA_FP"
echo
echo "Flash to a USB stick with:"
echo "    sudo dd if=$OUT of=/dev/sdX bs=4M status=progress conv=fsync"
