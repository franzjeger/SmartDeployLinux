# e2e-tailnet — the advertised path, proven over a real overlay

`tests/e2e-kvm` proved the restore/boot *mechanics*. This harness proves
the **transport** — the claim the entire project is built on:

> the stick joins your tailnet via a single-use operator code, then
> chainloads the install over HTTPS-via-tailnet — works anywhere, no
> remote-site infra, no PXE-on-LAN.

Until now that was asserted but never exercised. Here it runs against the
**real components**, with nothing stubbed on the critical path.

## What it proves

```
 real Headscale  ──POST /api/v1/preauthkey──▶  ephemeral single-use tagged key
 (control plane)         (the auth-broker's exact tsclient contract)
      │                                                   │
      │ registers                                         │ tailscale up --authkey
      ▼                                                   ▼
 deploy-server node ◀───── WireGuard data path ─────▶ stick-client node
 100.64.0.1  (api here)                                100.64.0.2 (the machine)
      │                                                   │
      │   the stick reaches the api ONLY over the overlay │
      │   (every deploy HTTP call rides its SOCKS proxy)  │
      ▼                                                   ▼
   ┌─────────────────────── over the tailnet ───────────────────────┐
   │ LINUX:  render-by-token → token-gated restore.sh → real disk    │
   │         (GPT/ext4/GRUB/initramfs) → phone-home → job completed   │
   │         → the restored disk BOOTS in QEMU                        │
   │ WINDOWS: deploy.cmd → /plan DMI+PCI driver match → unattend.xml  │
   │         → image.wim + drivers.zip (302 handoffs) → completed     │
   └─────────────────────────────────────────────────────────────────┘
```

Concretely, `run.sh`:

1. starts the **real Headscale** control plane (the coordination server
   `services/auth-broker/internal/tsclient` speaks to);
2. mints an **ephemeral, single-use, tagged pre-auth key** via
   `POST /api/v1/preauthkey` — byte-for-byte the broker's redeem contract;
3. joins a **deploy-server** node to the tailnet and starts the real `api`
   reachable at that node's tailnet IP;
4. joins a second **stick-client** node with a *fresh* single-use key and
   **proves it reaches the api over WireGuard** (not localhost) before any
   deploy work begins;
5. **Linux:** drives the whole `tests/e2e-kvm` deploy — but every HTTP call
   (`render-by-token`, `restore.sh`, the golden archive, the phone-home)
   is routed through the stick's tailscale data path, so the deploy only
   succeeds *because the overlay works*. Then boots the restored disk.
6. **Windows:** drives the full server-side WinPE conversation over the
   same overlay — `deploy.cmd`, the `/plan` **DMI + PCI driver match**
   (a Dell pack matched by vendor and PCI VID:DID), the rendered
   `unattend.xml`, and the `image.wim` / `drivers.zip` 302 blob handoffs —
   then phones home to `completed`.

## Running it

```sh
# 1. build headscale/tailscale, write their config, build the golden image:
sudo ./setup.sh

# 2. a Postgres the harness may DROP SCHEMA on:
export PG_DSN='postgres://user@host:5432/db?sslmode=disable'

# 3. the proof (root: tailnet, loop devices, mount, chroot, QEMU):
sudo PG_DSN="$PG_DSN" ./run.sh
```

Needs, in addition to `e2e-kvm`'s tools: `headscale`, `tailscaled`,
`tailscale` (built by `setup.sh` into `$WORK/bin`) and `/dev/net/tun`.

## Honest scope — real vs. substituted

**Real, on the critical path:** the Headscale control plane; the exact
ephemeral single-use pre-auth-key contract; two distinct Tailscale nodes
registering and exchanging traffic over WireGuard; the api reached only via
the overlay; the full Linux restore-and-boot; the entire server-side
Windows artifact chain including hardware-independent driver matching.

**Substituted (documented, none changes product behaviour):**

- **One host.** Both tailnet nodes and the server run on one machine, so
  the WireGuard peer is local. Traffic still crosses the tailscale data
  path between two separately-registered nodes — the harness asserts a
  peer-to-peer reach over WireGuard before deploying — but it is not two
  physical machines on two networks. It proves the *coordination and
  transport*, not NAT traversal across the real internet (that is
  Tailscale/DERP's job, not this project's code).
- **Userspace tailscaled + SOCKS.** The stick routes via its tailscale
  SOCKS proxy, exactly as a userspace tailnet client does.
- **No real WinPE boot.** Booting WinPE and running DISM needs the Windows
  ADK and licensed media, which aren't available here. Every artifact WinPE
  *consumes* is generated and verified over the tailnet; the physical
  WinPE→DISM boot remains a `docs/FIELD_TEST.md` item.
- **QEMU under TCG; archive over HTTP** — as in `e2e-kvm`.

## Why this matters

The project had built five client surfaces and a full API before anything
had crossed the overlay it's designed around. This closes that gap: the
single most load-bearing, previously-unproven claim — join-tailnet-then-
deploy-over-HTTPS — now runs green against the real Headscale and real
Tailscale, for both OS families.
