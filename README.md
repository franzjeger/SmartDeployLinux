# Deployserver

Open-source, USB-bootstrap-first deployment server. A self-hosted equivalent
of SmartDeploy with hardware-independent imaging for Windows and Linux,
re-architected so it works over WAN/Tailscale instead of being trapped on a
LAN by PXE's L2-broadcast dependency.

## What it does

- Images Windows 10 / 11 (incl. 24H2+) / Server 2019–2025 and Ubuntu /
  Debian / RHEL-family / Fedora from one golden image per OS.
- Hardware-independent: matches driver packs to target machines on the fly
  using DMI vendor/model and PCI VID/DID.
- Three boot modes:
  - **USB bootstrap (primary):** stick joins your tailnet via a single-use
    operator code, then iPXE chainloads the install over HTTPS-via-tailnet.
    Works anywhere with internet.
  - **LAN PXE (datacenter):** iPXE via dnsmasq proxyDHCP+TFTP.
  - **Edge agent (bulk remote sites):** small Linux box at a remote LAN
    joins the tailnet as a subnet router and runs local proxyDHCP+TFTP, so
    a roomful of machines can PXE without 50 sticks.
- Web UI + REST API + CLI. RBAC via OIDC. mTLS service-to-service.
- Single-host `docker compose up` for the small tier; 3-node HA tier with
  external Postgres + S3.

## Why USB-bootstrap-first

Tailscale (and any L3 overlay) does not bridge L2 broadcast. PXE's DHCP
discovery is L2-broadcast. Trying to tunnel PXE across an overlay is
fragile. The USB stick replaces that first second of broadcast with a
unicast HTTPS request to a known endpoint; from step two onward the rest
of the install is just HTTP. No remote-site infra required.

The stick contains *no* long-lived secrets. It can be made once and reused
for any deployment by anyone, anywhere.

## Quick start (small tier)

```sh
cp .env.example .env       # edit at minimum: DEPLOY_FQDN, HEADSCALE_URL, OIDC_*
make build                  # compiles all containers
docker compose up -d        # brings up Postgres, MinIO, API, UI, http-boot, etc.
make seed-admin             # creates the first OIDC-mapped admin
```

Then:

1. In the UI, register a machine (asset tag + MAC).
2. Click **Generate bootstrap stick image** — produces `deploy-bootstrap.img`.
3. `dd` it to a USB stick.
4. Click **Issue deployment code** for the machine. Get a 6-character code
   like `4A7-K2P`.
5. Plug the stick into the target, boot from USB, type the code at the
   prompt. Walk away. Phone-home arrives when the OS is installed.

## Documentation

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — component diagram,
  sequence diagrams, data model, decision log.
- [`docs/BOOTSTRAP.md`](docs/BOOTSTRAP.md) — the USB stick spec.
- [`docs/WINDOWS.md`](docs/WINDOWS.md) — WIM capture, driver packs, unattend.
- [`docs/LINUX.md`](docs/LINUX.md) — kickstart / autoinstall / preseed /
  cloud-init.
- [`docs/SECURITY.md`](docs/SECURITY.md) — threat model and mitigations.
- [`docs/OPERATIONS.md`](docs/OPERATIONS.md) — backups, upgrades, scale.
- [`docs/RUNBOOK_A_USB.md`](docs/RUNBOOK_A_USB.md) — primary deploy path.
- [`docs/RUNBOOK_B_PXE.md`](docs/RUNBOOK_B_PXE.md) — LAN PXE.
- [`docs/RUNBOOK_C_EDGE.md`](docs/RUNBOOK_C_EDGE.md) — edge agent.

## Status

This is **work in progress**. See `docs/STATUS.md` for what's actually
implemented vs. designed.

## License

Apache-2.0.
