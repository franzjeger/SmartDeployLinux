# Changelog

## v1.0.0-rc — 2026-07-31

First release candidate. Phases 1–11 built the skeleton and design (see
`docs/STATUS.md` for the phase-by-phase record); phases 12–25 turned it
into a working product, and later phases proved the deploy core and the
tailnet transport in software (`tests/e2e-kvm`, `tests/e2e-tailnet`).

This is a release **candidate**, not a final GA: the physical hardware
sign-off in `docs/FIELD_TEST.md` — a real firmware boot of the USB stick,
a WinPE→DISM boot on the Windows ADK, and a run across two real networks —
is still outstanding. Clearing it on real machines promotes this to
`v1.0.0`.

### Deploy pipeline (works end-to-end for the first time — Phase 12)
- Fixed the token-hash mismatch, consuming phone-home tokens,
  non-bootable iPXE templates, empty boot tokens, stub `deploy.cmd`,
  hardcoded WIM path, dead driver-matcher/unattend code, broken
  seed-admin↔OIDC linking, and an unstartable compose stack.
- Job state machine with intermediate-state walking; job cancel;
  `/readyz`; dependency-free Prometheus `/metrics`.

### Imaging
- Golden-image **capture** for Windows (WinPE + DISM) and Linux
  (tar+zstd with identity pruning), uploading via presigned S3 PUT.
- Golden-image **restore** for Linux: UEFI and legacy BIOS, plain and
  LVM layouts (profile-selected), identity regeneration, initramfs
  rebuild for hardware independence.
- Driver-pack ingest + hardware matching (DMI/PCI) injected during
  Windows deploys; per-machine hardware inventory recorded on `/plan`.
- Image ingest: presigned uploads from the UI (chunked in-browser
  SHA-256) and `deployctl images upload`; versioned images.

### Boot paths
- USB bootstrap over Tailscale/Headscale (single-use codes, one-shot
  peppered boot tokens).
- LAN PXE with a server-generated **interactive iPXE menu** —
  zero-touch preserved via assigned-profile default + countdown;
  locked/open authorization modes; graceful fallback when the api is
  down.
- Edge agent: proxyDHCP/TFTP, tailnet subnet routing, **site-local
  verifying image cache** (one WAN transfer per site; sha256-verified
  blobs), and **wake-on-LAN** queue draining.

### Operations
- Bulk deployments (≤100 machines, optional WoL) with per-machine codes.
- Reporting dashboard: success rates, durations, per-day outcomes
  (CVD-validated chart), per-profile/site breakdowns, CSV export.
- Job-completion webhooks (Slack-compatible), stick-config generator,
  sites management, machine editing with hardware inventory.
- Prometheus + Grafana observability overlay; HA compose tier with 2×
  api replicas and a Postgres LISTEN/NOTIFY event bus (SSE fans out
  across replicas).

### Security
- Peppered one-shot tokens end to end; query-string token auth removed.
- mTLS between services; shared-secret gates on broker issue-code and
  the edge wake queue (fail closed).
- RBAC with OIDC: PKCE login in the UI, RFC 8628 device flow in the
  CLI, user/role admin with a last-admin lockout guard.
- Append-only audit trail incl. PXE-menu deployments with source IPs.
- govulncheck blocking in CI; gosec advisory.

### Verification
- ~95 unit/integration tests across all six services; 8-scenario
  black-box e2e harness (real api binary vs real Postgres) running in
  CI; `docs/FIELD_TEST.md` hardware validation protocol (FT-1…FT-11).
