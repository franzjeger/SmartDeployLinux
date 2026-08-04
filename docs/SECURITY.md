# Security model

Companion to `docs/BOOTSTRAP.md §6` (which covers the auth-broker
threat model in depth) and the per-component "Trust boundaries" table
in `docs/ARCHITECTURE.md §5`. This doc focuses on the system as a
whole and the audit/key-rotation/incident-response side.

## STRIDE summary by component

| Component | Top concerns |
|---|---|
| **Bootstrap stick** | Spoofing (stolen stick + intercepted code), Tampering (image modified before flash), Information disclosure (none — no secrets at rest), Repudiation (every redeem audited). |
| **Auth-broker** | Spoofing the broker (must mTLS to Headscale, run on tagged node), Tampering audit logs (mirrored to syslog/file), DoS (rate limited, but not infinite — see §3). |
| **API** | Authn bypass (OIDC + chi middleware enforced), Authz bugs (RBAC table is small, audit-tested in Phase 9), SQL injection (pgx parameterized queries; no string concatenation in query bodies). |
| **Worker** | Local privilege escalation if compromised (it talks to S3 and runs jobs); we run it nonroot in distroless. |
| **http-boot** | Source IP spoofing on `/boot/*` (mitigated by per-redeem one-shot bootstrap tokens — see §4). Resource exhaustion via large blob fetches (nginx limits + S3 byte-range). |
| **Edge agent** | Compromise turns it into an L2 attacker on the remote LAN. Treat as critical infrastructure (FDE, TPM, no shared creds). |
| **Deployed targets (during apply)** | One-shot tokens for downstream secrets so compromise of host N doesn't yield host N+1 secrets. |

## 1. Boot artifact signing

| Artifact | Signed by | Verification |
|---|---|---|
| shim (`shimx64.efi`) | Microsoft | Firmware Secure Boot |
| GRUB2 (`grubx64.efi`) | Project MOK | shim verifies against MOK enrolled by user |
| Linux kernel | Project MOK | GRUB verifies via MOKList |
| iPXE binaries | Project MOK | shim/GRUB chain |
| WIM / qcow2 / driver-pack blobs | cosign keyless | API verifies on upload; worker re-verifies before serving |
| Deploy server TLS cert | Internal CA pinned at iPXE build | iPXE refuses to chain on mismatch |

## 2. Secret management

- **Long-lived secrets** (deploy CA private key, MOK private key,
  Headscale API key, OIDC client secret, Postgres password) live in
  the user's secret store of choice. The `.env.example` references
  them via paths/values; the user fills them in. We do **not** ship
  any default credentials.
- **Ephemeral secrets** (Tailscale auth keys minted by the broker,
  one-shot tokens, deployment codes) are generated per-event with a
  TTL. Codes are hashed (argon2id) before storage.
- **In-installer secrets** (domain-join credentials) are never written
  into images or rolled into long-lived files on disk. They flow as
  one-shot tokens delivered at apply time and are wiped from
  `unattend.xml` immediately after specialize via a `SetupComplete.cmd`
  scrub step (Linux: cloud-init `bootcmd` that removes the
  user-data fetch).

## API tokens

Long-lived API tokens let headless clients (SDKs, `deployctl`, CI,
automation) authenticate without the interactive OIDC device flow, whose
ID tokens are short-lived.

- **Scope.** A token authenticates **as** its owning user. By default it
  carries that user's full permissions; optionally it can be **scoped to a
  subset of the owner's roles** (`roles` on create — least privilege). A
  scoped token's effective permissions are computed at verification time
  as the owner's permissions held *through those named roles*, so it
  cannot exceed the owner and it shrinks automatically if the owner later
  loses a scoped role. Scoping to a role you do not hold is rejected
  (400). Either way a token can never grant more than the owner has, so
  self-service issuance (`apitoken.write`, seeded to the operator role) is
  safe.
- **Storage.** Only a peppered SHA-256 hash is stored
  (`tokens.HashAPIToken`, domain-separated from boot tokens via a distinct
  digest tag; pepper = the deploy FQDN, as for boot tokens). The plaintext
  secret is returned exactly once, at creation, and is unrecoverable
  afterward. Listings show only a short `dpsk_…` display prefix.
- **Lifecycle.** Tokens are individually revocable
  (`DELETE /api/v1/api-tokens/{id}`) and may carry an expiry; verification
  rejects revoked or expired tokens and stamps `last_used_at` for audit.
  Creation and revocation are audited (`api_token.created` / `.revoked`).
- **Transport.** Presented as `Authorization: Bearer dpsk_…`. The auth
  middleware routes the `dpsk_` prefix to DB verification instead of OIDC.
  API tokens require an OIDC-configured deployment — with OIDC unset the
  operator API is open (dev mode) and no bearer is consulted.
- **Rotation.** Prefer a short `--expires-in-days`; to rotate, create a
  new token, cut clients over, then revoke the old one. Revocation takes
  effect immediately (the next request 401s).

## 3. Rate limits and DoS

| Endpoint | Per IP | Per object |
|---|---|---|
| `/api/v1/bootstrap/redeem` | 30/hr (DB-backed), 10/min (in-process) | 5 attempts/code, then code locked |
| `/api/v1/bootstrap/issue-code` | inherited from API authn (operator) | per operator, 100/hr (TODO Phase 9) |
| `/boot/<id>.ipxe` | 60/min (nginx) | one-shot bootstrap token (TODO Phase 9 — currently public, see §4) |
| `/boot/<id>/user-data` | same | same |

Behind a public Caddy, the upstream firewall should additionally
limit by Source IP at L4 to absorb floods before they reach the app.

## 4. Known security gaps

Real punch list, not aspirational. ✅ closed in Phase 9; ⏳ open.
**8 of 8 closed as of Phase 9h.**

1. ✅ **`/boot/<id>.ipxe` was unauthenticated.** Closed in Phase 9a.
   Redeem now mints a `boot_token` (32-byte URL-safe random,
   SHA-256-hashed-with-pepper at rest) bound to the auth_code and
   stored in `one_shot_tokens(purpose='boot')`. Chainload URL is
   `https://.../boot/<token>.ipxe`. Render endpoints lookup
   token→machine; idempotent (no consume on each fetch — the iPXE
   chain and user-data fetch use the same token). Tokens are marked
   consumed at the deployment_job's terminal-state transition (see
   `Store.MarkBootTokensConsumedForJob`, called from `TransitionJob`).
2. ✅ **Token in query string for WinPE.** Closed in Phase 9c.
   `winpe/scripts/{startnet,deploy}.cmd` now use
   `Authorization: Bearer %TOKEN%` and dropped `?token=` from URLs.
   Job ID stays in the URL path because routes already include it.
3. ✅ **mTLS between API and http-boot.** Closed in Phase 9f.
   `scripts/gen-internal-ca.sh` issues an internal CA + per-service
   server/client certs into `secrets/`. The api opens a separate
   `:8443` mTLS listener with `tls.RequireAndVerifyClientCert` against
   the internal CA, hosting only `/internal/render/*` (called by
   http-boot). http-boot's render Go service presents its client cert
   when calling the api. Both services have an `mtls` package
   (`api/internal/mtls`, `http-boot/internal/mtls`) with the loader and
   server/client tls.Config helpers; api ships 3 tests including a
   real-handshake round-trip with negative cases (no-cert and
   foreign-CA both rejected at handshake). The public `/api/v1/*`
   surface stays on `:8080` behind Caddy + OIDC because operator and
   installer-phone-home callers can't present internal-CA certs. mTLS
   for api↔auth-broker and api↔worker is the obvious next-extension —
   neither currently calls the api over HTTP, so adding it is a small
   delta when those calls land.
4. ✅ **Audit log mirror.** Closed in Phase 9b. Both auth-broker and
   api now have an `auditlog.Open(path)` helper that returns a
   slog.Logger writing JSON to stdout AND (if `AUDIT_FILE` is set) to
   an append-only file. The file is the durable mirror that survives
   Postgres compromise. Operator mounts a volume on `AUDIT_FILE`'s
   parent dir and backs it up separately.
5. ✅ **Headscale API key rotation.** Closed in Phase 9g.
   `scripts/rotate-headscale-key.sh` automates the mint + list + expire
   operations against Headscale's `/api/v1/apikey` endpoint. Recommended
   cadence: every 30–90 days. The full procedure (mint → update broker
   env → restart → verify → expire old) is documented in
   `docs/OPERATIONS.md §"Headscale key rotation"`. Vault integration
   is left as a follow-on: the rotation script writes the new key to
   stdout; the operator pipes it into Vault (or any secret store) and
   updates the broker's env from there.
6. ✅ **MOK private key on HSM.** Closed in Phase 9h with
   documentation. The default `bootstrap/keys/gen-mok.sh` flow generates
   a filesystem-resident key suitable for lab and small-deploy use. For
   production, use a YubiKey 5 (or any PKCS#11-capable HSM) as the
   signing key and never let the private key touch a filesystem.

   **YubiKey + sbsign procedure (production-recommended):**

   ```sh
   # One-time per YubiKey. Generates an RSA-2048 keypair on the device's
   # signing slot 9c and emits the public-key cert (the MOK.cer to enroll
   # via shim/MokManager).
   yubico-piv-tool -a generate -s 9c -A RSA2048
   yubico-piv-tool -a verify-pin -a selfsign-certificate -s 9c \
       -S '/CN=deployserver MOK' -i pub.pem -o MOK.cer
   yubico-piv-tool -a import-certificate -s 9c -i MOK.cer

   # On every grub.efi / kernel build, sign via the YubiKey instead of
   # an on-disk key:
   sbsign --engine pkcs11 \
       --key 'pkcs11:object=PIV%20AUTH%20key;type=private' \
       --cert /path/to/MOK.cer \
       --output grubx64.efi.signed grubx64.efi
   ```

   The PKCS#11 module is provided by `opensc` or `yubico-piv-tool`'s
   library. Your build host needs `libengine-pkcs11-openssl`. The
   YubiKey PIN is required at signing time — operationally this is
   either typed by an operator or provided via PIN cache during a
   batched build session.

   For higher-tier deployments (a signing server with a network HSM
   like AWS CloudHSM or YubiHSM2), the same pattern applies; only the
   PKCS#11 URI changes. Never automate PIN entry into CI logs.

   `bootstrap/external/fetch.sh`'s sbsign call is the integration point;
   replace its `--key bootstrap/keys/MOK.priv` with the PKCS#11 URI
   when running in HSM mode.
7. ✅ **Per-operator issue-code rate limit.** Closed in Phase 9d.
   `auth-broker.handleIssueCode` calls `Store.CountIssuedByActorRecently`
   and returns 429 when the operator has exceeded
   `AUTH_BROKER_RATE_LIMIT_ISSUE_PER_OPERATOR_PER_HOUR` (default 100).
   Caps blast radius of a compromised admin account.
8. ✅ **Replay protection on plan/wim fetches.** Closed in Phase 9e.
   Per-job WinPE endpoints (`/v1/jobs/{id}/{deploy.cmd,plan,image.wim,
   drivers.zip,unattend.xml}`) implemented in
   `services/api/cmd/api/winpe.go`. All require `Authorization: Bearer
   <one-shot-token>` and use `Store.VerifyOneShotTokenForJob`, which
   rejects the call if (a) the token is unknown/expired/consumed,
   (b) the token is not bound to the URL's job id, or (c) the job is no
   longer in `bootstrapped`/`imaging` state. Terminal-state transitions
   (`completed`/`failed`/`cancelled`) call
   `Store.MarkBootTokensConsumedForJob` to revoke the boot token, so
   even a leaked token can't be replayed against a finished deployment.

## 5. Audit log

Schema is in `services/api/internal/migrations/0001_init.sql`
(`audit_events`). Required events:

- `auth_code.issued`
- `auth_code.redeemed`
- `auth_code.redeem_failed` (with reason in `data`)
- `deployment_job.created` / `started` / `completed` / `failed`
- `image.uploaded` / `signed`
- `driver_pack.uploaded`
- `bootstrap_stick.built` / `retired`
- `user.login` / `logout`
- `user.role_changed`
- `headscale_authkey.minted` (broker-side, with TTL and tags)

Operators with `audit.read` can query via the API and CLI.
Append-only at the application layer; mirror configured per §4 #4.

## 6. CA rotation procedure

See `docs/BOOTSTRAP.md §7`. Summary:

1. Generate new internal CA, deploy server cert chains to both old + new.
2. Build new sticks with new CA via `make-stick.sh --ca-cert new.pem`.
3. `bootstrap_sticks` table tracks every flashed stick's CA fingerprint.
   Inventory: `deployctl bootstrap-sticks list --ca-fingerprint <old>`
   shows which org units still need re-flashing.
4. After all sticks confirmed re-flashed, retire old CA from server
   chain.

## 7. Incident response checklist

In order, when you suspect a code or stick has been misused:

1. **Lock the code:** `UPDATE auth_codes SET locked_at = now() WHERE id = ?`.
2. **Revoke the Tailscale key** if redemption already minted one:
   call Headscale `DELETE /api/v1/preauthkey/<key>` and force-expire
   the resulting node `POST /api/v1/node/<id>/expire`.
3. **Revoke the deployment job's one-shot tokens:** `UPDATE one_shot_tokens
   SET consumed_at = now() WHERE auth_code_id = ?` (we mark consumed,
   not delete, to preserve the audit chain).
4. **Inspect the audit log** for the source IP, redeem timestamp, and
   any subsequent activity from the bootstrap tailnet node.
5. **Forensics:** if the machine completed imaging, treat as
   compromised. The image itself is signed so you can compare against
   the canonical SHA, but anything written post-apply (domain-joined
   creds, post-install playbooks) needs to be considered tainted.

## PXE menu trust model (Phase 23)

`PXE_MENU_MODE=locked` (default) grants LAN clients nothing beyond the
pre-existing network-trusted by-mac boot: only a machine's assigned
profile can be deployed to it. `open` mode lets ANY client on the LAN
deploy ANY registered profile to ANY registered machine — use it only
on physically controlled lab networks. Every menu-initiated deployment
writes an audit event (`job.created_via_pxe_menu`, actor `pxe-menu`,
source IP) and flows through the standard one-shot-token lifecycle.
Menu-minted auth_codes rows are synthetic (label `pxe-menu`, already
expired+redeemed) and can never be redeemed via the broker.
