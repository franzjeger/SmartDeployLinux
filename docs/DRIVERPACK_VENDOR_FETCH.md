# Design: fetching driver packs from vendor catalogs

Status: **design** — approved direction, not yet implemented. Tracking
issue: see `gh issue list --label driverpacks`.

## The insight

"Can we scrape drivers from Dell/HP/Lenovo?" — no scraping needed. All
three publish machine-readable enterprise driver-pack catalogs, built
for exactly this use (SCCM/MDT tooling consumes them):

| Vendor | Catalog | Format |
|---|---|---|
| Dell | `https://downloads.dell.com/catalog/DriverPackCatalog.cab` | CAB containing XML; per-model WinPE + OS packs, download URL, hash |
| Lenovo | `https://download.lenovo.com/cdrt/td/catalogv2.xml` | Plain XML; Think-line packs per model/OS |
| HP | `https://hpia.hpcloud.hp.com/downloads/driverpackcatalog/HPClientDriverPackCatalog.cab` | CAB containing XML |

These are supported, stable interfaces with sha/md5 hashes included —
which slots straight into the blob store's existing sha256 verification.

## Operator experience (the goal)

On a machine's detail page, once its DMI vendor/model has been reported
(or entered): **one button — "Fetch driver pack"**. It shows the
matching pack from the vendor catalog (name, OS, size, hash), the
operator confirms, and the server does the rest: download, verify,
ingest to the blob store, create the `driver_packs` /
`driver_pack_versions` row, and attach a `dmi-product` match rule. No
manual downloads, no manual uploads, no manually typed match rules.

## Architecture

1. **Catalog client** (`services/api/internal/vendorcatalog/`):
   - Fetches + caches each vendor catalog (daily TTL; they change rarely).
   - Normalizes to one struct: vendor, models[], os, url, sha, size.
   - Lenovo first (plain XML). Dell second — its catalog is a CAB, see
     "CAB problem" below. HP last (same CAB issue, quirkier schema).

2. **API**:
   - `GET /api/v1/vendor-driverpacks?vendor=&model=&os=` → matching
     catalog entries. Powers the confirm dialog.
   - `POST /api/v1/vendor-driverpacks/fetch` `{machine_id | vendor+model, os}`
     → creates a fetch job row, returns job id.

3. **Fetch job** (worker): a `driverpack_fetch_jobs` table; the worker
   claims rows on its existing 30s tick (`FOR UPDATE SKIP LOCKED`, same
   pattern as notify). Streams the vendor download straight into the S3
   `drivers` bucket (packs are 0.5–2 GiB; never buffered to disk),
   verifies the catalog hash during the stream, then creates the pack
   version + match rule in one transaction. Failures land in the job row
   with the reason; the UI polls the job id.

4. **UI**: button on machine detail (enabled when vendor/model known) +
   a "From vendor catalog…" path on the Driver packs page for fetching
   by explicit model without a registered machine.

## The CAB problem (why Lenovo ships first)

Dell's and HP's catalogs are CAB archives. The worker image is
distroless — no shell, no cabextract. Options, in preference order:

1. Pure-Go CAB reader (the format is old and stable). Evaluate existing
   libraries; vendor one if licensing fits, else implement the minimal
   MSZIP-decompression path — driver-pack catalogs use plain MSZIP.
2. Sidecar fetch container based on alpine with `cabextract` (breaks the
   one-binary worker; last resort).

Lenovo's catalog is plain XML, so the whole pipeline can be built and
verified end-to-end on Lenovo alone before touching CAB parsing.

## Storage prerequisites (done)

The blob store must be enabled: `COMPOSE_PROFILES=...,with-minio`,
`S3_PUBLIC_ENDPOINT` set to a host-reachable origin, and a host port for
MinIO (see `docker-compose.local.yml` on the deploy host). Enabled on
the homelab deployment 2026-08-02; the pre-existing manual upload flow
works from that point.

## Non-goals

- Scraping vendor support pages. The catalogs are the contract.
- Per-device INF-level matching from vendor update feeds (Dell
  CatalogPC, Lenovo updates catalog) — driver *packs* are the unit here.
- Windows Update integration.
