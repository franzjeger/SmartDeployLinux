#!/bin/sh
# Buildroot post-build script. Runs after the rootfs is staged, before
# the cpio archive is built.
#
# Buildroot calls this with the target rootfs as $1.

set -eu

TARGET=$1

# Ensure /etc/inittab uses tty1 + serial console.
cat > "$TARGET/etc/inittab" <<'INI'
::sysinit:/etc/init.d/rcS
::respawn:/sbin/getty -L tty1 0 vt100
::respawn:/sbin/getty -L ttyS0 115200 vt100
::ctrlaltdel:/sbin/reboot
::shutdown:/etc/init.d/rcK
::shutdown:/sbin/swapoff -a
::shutdown:/bin/umount -a -r
INI

# Force-set permissions on our scripts (overlay copy may have mangled them).
chmod 0755 "$TARGET/etc/init.d/S99deploy"
chmod 0755 "$TARGET/usr/local/bin/deploy-bootstrap"
chmod 0755 "$TARGET/usr/local/bin/exec-ipxe"

# Remove any random-seed files; we don't want a fixed seed shipped on
# every stick (very low risk in our threat model — we use kernel CRNG —
# but cheap to remove).
rm -f "$TARGET/var/lib/random-seed" "$TARGET/etc/random-seed" 2>/dev/null || true

# Ensure /var/log exists and is writeable.
install -d -m 0755 "$TARGET/var/log"
install -d -m 0755 "$TARGET/etc/deploy"
install -d -m 0755 "$TARGET/etc/ssl/certs"
install -d -m 0755 "$TARGET/opt/iPXE"

# Banner for the operator.
cat > "$TARGET/etc/issue" <<'EOF'

  deployserver bootstrap

  Type the deployment code shown in your operator UI.
  Press <Tab> to switch to the dialog if it has not appeared.

EOF

# Strip debug info from binaries we ship (saves ~15% on size).
# Buildroot's default toolchain wrapper passes --strip-all on install
# but local overlays bypass that — strip them manually.
if command -v strip >/dev/null; then
    find "$TARGET/usr/local/bin" -type f -exec strip --strip-unneeded {} + 2>/dev/null || true
fi
