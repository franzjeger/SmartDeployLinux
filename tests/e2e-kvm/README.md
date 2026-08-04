# e2e-kvm — end-to-end deploy proof

This harness proves the **core promise** of the whole system with nothing
stubbed on the critical path: a golden Linux image, captured once, is
restored by the project's own `restore.sh` onto a blank disk and the
result actually boots as a new, identity-regenerated machine — driven by
the real api over the real token flow.

It is the answer to "does the deploy spine actually work, from server to a
booted OS?" — verified, not asserted.

## What it exercises (all real)

```
 real api  ──issues──▶  boot token + golden deploy job
    ▲                          │
    │ phone-home               │ token-gated
    │ (restore.sh)             ▼
 job=completed ◀──  REAL restore.sh  ──▶  REAL block device (loop disk)
                          │                   • GPT partitioning (sgdisk)
                          │                   • ext4 + ESP/BIOS-boot
                          ▼                   • stream-untar golden .tar.zst
                    QEMU boot  ◀───────────   • GRUB install (chroot)
                    (serial log)              • initramfs regen (chroot)
                          │                   • identity regen (hostname,
                          ▼                     machine-id, SSH host keys,
              DEPLOY-E2E-BOOT-OK                fstab by-UUID)
              host=restored-e2e
              root=/dev/vda2   ◀── booted from root=UUID=…, resolved on a
                                   *different* disk device than restore saw
```

Concretely, `run.sh`:

1. builds and starts the **real `api`** (dev mode) against a real Postgres,
   applying its own embedded migrations;
2. registers a golden image + profile + machine over the **operator API**
   and seeds the boot token + deploy job exactly as the auth-broker's
   redeem would (the one part Headscale/OIDC would normally do);
3. fetches the **token-gated `restore.sh`** straight from the api — and
   confirms it is refused without the token;
4. runs that exact script against a **real loop-backed block device**:
   real GPT, real ext4, real stream-untar of a real bootable golden
   archive, real `grub-install` + `update-initramfs` in a chroot, and
   `restore.sh`'s own phone-home;
5. asserts the api moved the job to **`completed`** — driven entirely by
   the script, not the harness;
6. **boots the restored disk in QEMU** and asserts, from the serial log,
   that the OS boots and prints its freshly regenerated identity.

The golden image (`build-golden.sh`) is a real, minimal Ubuntu with a real
kernel, GRUB and initramfs-tools. A one-shot systemd unit prints
`DEPLOY-E2E-BOOT-OK host=… machine-id=… root=…` to the serial console on
first boot and powers the machine off, so the run terminates on its own
and the assertion has something unambiguous to match.

## The hardware-independence check, made concrete

`restore.sh` writes `fstab` and the GRUB root by **UUID**, so the image
boots regardless of what the disk is called on the target. The harness
proves this the hard way: `restore.sh` sees the disk as `/dev/loop0`, but
QEMU presents it as a virtio disk. The boot line
`root=UUID=… … root=/dev/vda2` shows the same filesystem UUID resolving to
a *different device node* than restore ever saw — which is exactly what
"hardware-independent" has to mean.

## Running it

```sh
# 1. a Postgres the harness may DROP SCHEMA on:
export PG_DSN='postgres://user@host:5432/db?sslmode=disable'

# 2. build the golden image once (needs root; debootstraps Ubuntu):
sudo ./build-golden.sh /var/tmp/e2ekvm/golden.tar.zst

# 3. run the proof (needs root: loop devices, mount, chroot):
sudo GOLDEN=/var/tmp/e2ekvm/golden.tar.zst PG_DSN="$PG_DSN" ./run.sh
```

Requires (all present in the deploy tooling image): `go`, `qemu-system-x86_64`,
`sgdisk`, `mkfs.ext4`, `debootstrap`, `zstd`, `losetup`, `chroot`,
`python3`, `psql`, `curl`.

## Honest scope — what is real and what is substituted

Everything on the critical path is real: the server, the token gating, the
partitioning, the filesystems, the archive extraction, the bootloader
install, the identity regeneration, the phone-home, and the boot itself.

Two sandbox substitutions, neither of which touches product behaviour:

- **QEMU runs under TCG software emulation**, because the CI/sandbox host
  exposes no `/dev/kvm`. The guest, its BIOS, GRUB, kernel, initramfs and
  systemd all run for real — just slower. On a KVM-capable host add
  `-enable-kvm` for a ~10× speedup; nothing else changes.
- **A tiny `blkid`-based shim maintains `/dev/disk/by-uuid`** during the
  run. A real rescue/live environment runs `udevd`, which populates those
  symlinks as filesystems appear; Ubuntu's `grub-mkconfig` keys its
  `root=UUID=` decision off them. This sandbox has no `udevd`, so the shim
  stands in for that one job. It creates nothing the product wouldn't see
  on real hardware.

- **The golden archive is served over local HTTP** instead of from S3/the
  blob store. `restore.sh` streams it with the same `curl | zstd -d | tar`
  path either way; only the URL's origin differs.

## `DEPLOY_TARGET_DISK`

To drive the real `restore.sh` at a specific loop device (instead of its
largest-fixed-disk auto-pick, which on the host would select the host's
own root disk), the script honours a `DEPLOY_TARGET_DISK` override. That
is a genuine operator feature too — for machines whose biggest disk is not
the OS disk — not a test-only hook. It refuses a non-block-device or the
live root's disk.
