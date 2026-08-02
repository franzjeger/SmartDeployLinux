#!/usr/bin/env bash
# Practice deploy: run a full LAN-PXE deployment against a throwaway VM.
#
# Exercises the same server-side path a real machine takes — iPXE chain
# over TLS, PXE menu, one-shot token, kernel/initrd fetch, answer-file
# render, phone-home — with no hardware and nothing to re-cable. Use it
# to rehearse before touching real machines, and after upgrades to prove
# the boot chain still works end to end.
#
#   ./run.sh --deploy-host 192.168.200.31 --mac 52:54:00:aa:bb:01
#
# Everything runs in containers; the host needs docker and /dev/kvm.
# Nothing is installed on the host and the VM disk is disposable.

set -euo pipefail

DEPLOY_HOST=""
MAC="52:54:00:aa:bb:01"
WORKDIR="${WORKDIR:-$HOME/vm-practice}"
IPXE_DIR="${IPXE_DIR:-$HOME/ipxe-build}"
MEM_MB="${MEM_MB:-8192}"
CPUS="${CPUS:-4}"
DISK_GB="${DISK_GB:-25}"
TIMEOUT_MIN="${TIMEOUT_MIN:-40}"

usage() { sed -n '2,20p' "$0"; exit 1; }

while [ $# -gt 0 ]; do
    case "$1" in
        --deploy-host) DEPLOY_HOST="$2"; shift 2;;
        --mac)         MAC="$2"; shift 2;;
        --workdir)     WORKDIR="$2"; shift 2;;
        --ipxe-dir)    IPXE_DIR="$2"; shift 2;;
        -h|--help)     usage;;
        *) echo "unknown arg: $1" >&2; usage;;
    esac
done
[ -n "$DEPLOY_HOST" ] || { echo "--deploy-host is required" >&2; usage; }

need() { command -v "$1" >/dev/null || { echo "missing: $1" >&2; exit 1; }; }
need docker
[ -e /dev/kvm ] || { echo "no /dev/kvm — KVM is required" >&2; exit 1; }

IPXE_BIN="$IPXE_DIR/ipxe-serial.lkrn"
if [ ! -f "$IPXE_BIN" ]; then
    cat >&2 <<EOF
missing $IPXE_BIN

Build it first (see ipxe/Makefile). The serial-console variant is what
makes this harness observable — a graphical iPXE build boots fine but
prints nothing you can grep:

  make -C ipxe DEPLOY_URL=https://$DEPLOY_HOST all
EOF
    exit 1
fi

mkdir -p "$WORKDIR"
DISK="$WORKDIR/practice.qcow2"
LOG="$WORKDIR/serial.log"
rm -f "$LOG"

echo "==> fresh disk (${DISK_GB}G)"
rm -f "$DISK"
docker run --rm -v "$WORKDIR:/vm" alpine sh -c \
    "apk add --no-cache qemu-img >/dev/null && qemu-img create -f qcow2 /vm/$(basename "$DISK") ${DISK_GB}G" >/dev/null

echo "==> booting VM (mac $MAC, ${MEM_MB}MB, ${CPUS} cpu)"
docker rm -f practice-vm >/dev/null 2>&1 || true
docker run -d --name practice-vm --device /dev/kvm \
    -v "$IPXE_DIR:/b:ro" -v "$WORKDIR:/vm" alpine sh -c \
    "apk add --no-cache qemu-system-x86_64 >/dev/null 2>&1 && exec qemu-system-x86_64 \
        -enable-kvm -cpu host -m $MEM_MB -smp $CPUS \
        -kernel /b/$(basename "$IPXE_BIN") \
        -drive file=/vm/$(basename "$DISK"),format=qcow2,if=virtio \
        -netdev user,id=n0 -device virtio-net-pci,netdev=n0,mac=$MAC \
        -display none -serial file:/vm/$(basename "$LOG") -monitor none" >/dev/null

cleanup() { docker rm -f practice-vm >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> watching (timeout ${TIMEOUT_MIN}m) — serial log: $LOG"
deadline=$(( $(date +%s) + TIMEOUT_MIN * 60 ))
last=""
while [ "$(date +%s)" -lt "$deadline" ]; do
    line=$(tr -d '\r' < "$LOG" 2>/dev/null \
        | sed 's/\x1b\[[0-9;?]*[a-zA-Z]//g' \
        | grep -aiE 'Deploying |Could not boot|Invalid argument|casper|cloud-init|autoinstall|curtin|installation (complete|failed)' \
        | tail -1 || true)
    [ -n "$line" ] && [ "$line" != "$last" ] && { echo "    $line"; last="$line"; }
    sleep 15
done

echo "==> timeout reached; VM left running for inspection:"
echo "    docker logs practice-vm ; less $LOG"
trap - EXIT
