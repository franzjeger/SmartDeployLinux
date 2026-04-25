#!/usr/bin/env bash
# Download external binaries that we don't build ourselves:
#   - shim (Microsoft-signed UEFI first-stage loader)
#   - GRUB2 (we sign with our own MOK; downloaded as source for the
#     reproducible build, but for a quickstart we accept the upstream
#     Debian-signed grubx64.efi and re-sign it with our MOK)
#   - tailscaled + tailscale (static linux/amd64)
#
# Pinned by sha256. Do NOT loosen these without auditing.
#
# Run from $REPO/bootstrap/. Output goes into bootstrap/external/{shim,grub,tailscale}/.

set -euo pipefail

cd "$(dirname "$0")"
mkdir -p shim grub tailscale

# --- shim --------------------------------------------------------------
# Pulled from Debian's signed shim package. shimx64.efi inside the .deb
# is what UEFI loads; this is the binary signed by Microsoft. The
# corresponding .deb URL is pinned by version + amd64 architecture.
SHIM_VER=15.8-1+deb12u1
SHIM_DEB_URL="https://deb.debian.org/debian/pool/main/s/shim-signed/shim-signed_${SHIM_VER}_amd64.deb"
SHIM_DEB_SHA=""   # FILL after first run; refusing to download until pinned

if [ ! -s shim/shimx64.efi ]; then
    if [ -z "$SHIM_DEB_SHA" ]; then
        echo "FATAL: SHIM_DEB_SHA not pinned in $0."
        echo "       To bootstrap initially, run:"
        echo "         curl -fsSL '$SHIM_DEB_URL' | sha256sum"
        echo "       Paste the result into SHIM_DEB_SHA and re-run."
        exit 1
    fi
    echo "==> fetching shim"
    curl -fsSL "$SHIM_DEB_URL" -o shim/shim.deb
    echo "$SHIM_DEB_SHA  shim/shim.deb" | sha256sum -c -
    ( cd shim && ar x shim.deb data.tar.* && tar xf data.tar.* ./usr/lib/shim/shimx64.efi.signed --strip-components=4 && mv shimx64.efi.signed shimx64.efi )
    rm -f shim/shim.deb shim/data.tar.* shim/control.tar.* shim/debian-binary 2>/dev/null || true
fi

# --- grub --------------------------------------------------------------
# Build a standalone grub-mkstandalone image (GRUB 2.12) that supports
# loading a kernel + initramfs from the ESP. Sign it with our MOK key.
GRUB_VER=2.12
GRUB_TGZ_URL="https://ftp.gnu.org/gnu/grub/grub-${GRUB_VER}.tar.xz"
GRUB_TGZ_SHA=f3c97391f7c4eaa677a78e090c7e97e6dc47b16f0f7423ef8651d8796a7c1115

if [ ! -s grub/grubx64.efi ]; then
    echo "==> fetching+building grub ${GRUB_VER}"
    curl -fsSL "$GRUB_TGZ_URL" -o grub/grub.tar.xz
    echo "$GRUB_TGZ_SHA  grub/grub.tar.xz" | sha256sum -c -
    tar -xJf grub/grub.tar.xz -C grub
    pushd "grub/grub-${GRUB_VER}"
    ./configure --with-platform=efi --target=x86_64 --disable-werror >/dev/null
    make -j"$(nproc)" >/dev/null
    ./grub-mkstandalone -O x86_64-efi -o ../grubx64.efi \
        --modules="part_gpt part_msdos ext2 fat normal linux echo all_video search search_label gfxterm gfxmenu efi_gop efi_uga test loadenv configfile" \
        boot/grub/grub.cfg=/dev/null
    popd
    rm -rf grub/grub.tar.xz "grub/grub-${GRUB_VER}"
fi

# Sign GRUB with our MOK if keys are present.
if [ -r ../keys/MOK.priv ] && [ -r ../keys/MOK.crt ]; then
    if ! sbverify --cert ../keys/MOK.crt grub/grubx64.efi >/dev/null 2>&1; then
        echo "==> signing grub with MOK"
        sbsign --key ../keys/MOK.priv --cert ../keys/MOK.crt --output grub/grubx64.efi grub/grubx64.efi
    fi
else
    echo "WARN: ../keys/MOK.priv missing. Run bootstrap/keys/gen-mok.sh first."
fi

# --- tailscale ---------------------------------------------------------
TS_VER=1.86.0
TS_URL="https://pkgs.tailscale.com/stable/tailscale_${TS_VER}_amd64.tgz"
TS_SHA=""    # FILL after first run; refusing to download until pinned

if [ ! -s tailscale/tailscaled ] || [ ! -s tailscale/tailscale ]; then
    if [ -z "$TS_SHA" ]; then
        echo "FATAL: TS_SHA not pinned in $0."
        echo "       To bootstrap initially, run:"
        echo "         curl -fsSL '$TS_URL' | sha256sum"
        echo "       Paste the result into TS_SHA and re-run."
        exit 1
    fi
    echo "==> fetching tailscale ${TS_VER}"
    curl -fsSL "$TS_URL" -o tailscale/ts.tgz
    echo "$TS_SHA  tailscale/ts.tgz" | sha256sum -c -
    tar -xzf tailscale/ts.tgz -C tailscale --strip-components=1
    rm -f tailscale/ts.tgz
    chmod +x tailscale/tailscaled tailscale/tailscale
fi

echo "==> external binaries ready"
ls -la shim/ grub/ tailscale/
