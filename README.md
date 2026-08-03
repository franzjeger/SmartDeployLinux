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
- Golden-image capture AND restore for both OS families: WinPE+DISM
  for Windows; tar+zstd with identity regeneration for Linux (UEFI or
  BIOS, plain or LVM layouts).
- Interactive PXE menu with zero-touch preserved (assigned profile
  auto-deploys on a countdown); locked/open authorization modes.
- Site-local image distribution: the edge box caches and
  sha256-verifies images, so a 40-machine site costs one WAN transfer.
- Wake-on-LAN scheduling, bulk deployments, job webhooks, reporting
  dashboard with CSV export, Prometheus/Grafana observability.
- Web UI + REST API + CLI (OIDC PKCE + device-flow login). RBAC via
  OIDC. Long-lived, revocable API tokens (`deployctl api-tokens create`)
  for headless clients and automation. mTLS service-to-service.
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
cp .env.example .env       # edit at minimum: DEPLOY_FQDN, OIDC_*, POSTGRES_PASSWORD
make secrets               # one-time: internal CA + per-service mTLS certs
make build-images          # compiles the service containers (a few minutes)
docker compose up -d       # brings up Postgres, MinIO, API, http-boot, etc.
make seed-admin            # creates the first OIDC-mapped admin
```

`make secrets` is not optional — every service mounts `secrets/` and the
api's internal listener requires a client cert signed by that CA, so the
stack will not come up without it.

Use `make build-images` here, not `make build`. `make build` additionally
downloads and compiles buildroot and iPXE from source to produce the USB
bootstrap image — tens of minutes and a pile of host build dependencies.
You only need that when you are ready to make a stick (`make
build-bootstrap`), not to run the server.

Two settings in `.env` decide what actually starts, and both are easy to
miss:

- `COMPOSE_FILE` — which overlays merge. Without it you must pass `-f`
  flags on every command.
- `COMPOSE_PROFILES` — which optional services run. Without it, `docker
  compose up -d` silently leaves out the auth-broker (needed to issue
  deployment codes) and the LAN-PXE listener.

Leaving `OIDC_ISSUER` / `OIDC_CLIENT_ID` empty starts the api with **no
authentication at all** — it logs `OIDC verifier unavailable; public API
will be open` and serves `/api/v1/*` to anyone who can reach the port.
That is fine for a lab on a trusted network; configure OIDC before the
server holds anything you care about.

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
- [`docs/UPGRADING.md`](docs/UPGRADING.md) — upgrading a running server,
  with rollback and the pre-`.env` migration path.
- [`docs/RUNBOOK_A_USB.md`](docs/RUNBOOK_A_USB.md) — primary deploy path.
- [`docs/RUNBOOK_B_PXE.md`](docs/RUNBOOK_B_PXE.md) — LAN PXE.
- [`docs/RUNBOOK_C_EDGE.md`](docs/RUNBOOK_C_EDGE.md) — edge agent.
- [`docs/FIELD_TEST.md`](docs/FIELD_TEST.md) — hardware validation protocol.

The REST API is documented at `/api/docs` (human) and `/api/openapi.yaml`
(OpenAPI 3.1, for client generation) on a running server. Typed clients
built from that contract ship in-repo, each with zero runtime
dependencies, all 57 operations, and a parity test that keeps it in exact
correspondence with the spec:

- **Go** — [`services/sdk`](services/sdk/README.md). `deployctl`'s
  `machines` commands are built on it.
- **TypeScript** — [`services/sdk-ts`](services/sdk-ts/README.md). ESM +
  `.d.ts`, runs in Node 18+ and the browser on the global `fetch`.
- **Python** — [`services/sdk-py`](services/sdk-py/README.md). `TypedDict`
  models, `py.typed`, clean under `mypy --strict` and `pyright`.

## Status

**v1.0.0.** All designed phases (1–25) are implemented and tested —
~95 unit/integration tests plus an 8-scenario end-to-end harness run
against a real Postgres in CI. See `CHANGELOG.md` for the release
summary, `docs/STATUS.md` for the phase-by-phase record, and
`docs/FIELD_TEST.md` for the hardware validation protocol (the flows
that need real firmware, a live Headscale, or the Windows ADK).

## License

Apache-2.0.
