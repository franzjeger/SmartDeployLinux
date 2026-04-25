# Operations

Day-2 docs. Covers backup, upgrade, scaling, log rotation, capacity
planning, failure modes, and the procedures for the operational
events that recur (CA rotation, key rotation, etc.).

## 1. Backup

What needs backing up:

| Thing | Where | How |
|---|---|---|
| Postgres | `postgres-data` volume | `pg_dump --format=custom --no-owner` daily; ship to S3 or equivalent. |
| MinIO blobs | `minio-data` volume | `mc mirror local/blobs s3://offsite/...` daily; or rely on the S3 backend's own snapshotting if external. |
| Internal CA + MOK private keys | offline | These do **not** live in volumes — they live in your secrets store. Back them up the way you back up other root keys. Loss of the MOK key means re-flashing every stick with a new MOK; loss of the CA key means the same plus rotating every server cert. |
| Caddy data (cert state, ACME) | `caddy-data` volume | Standard Caddy backup. |

The Postgres dump is the only critical backup; everything else is
either reproducible or operationally backed up elsewhere.

```sh
docker compose exec postgres pg_dump -U deploy -Fc deploy \
  > backup-$(date -u +%Y%m%dT%H%M%SZ).pgdump
```

Restore:

```sh
docker compose exec -T postgres pg_restore -U deploy -d deploy --clean --if-exists \
  < backup-2026-04-25T00-00-00Z.pgdump
```

**Test the restore.** A backup you've never restored isn't a backup.
Quarterly: spin up a fresh `docker-compose.yml`, restore the latest
dump, run `make test-e2e` against it.

## 2. Upgrades

The pattern:

1. `git pull` → check release notes for migration steps.
2. `docker compose pull` → fetch new images.
3. Apply migrations: `docker compose run --rm api migrate up`. Phase 4
   ships only `0001_init.sql`; future migrations are forward-only.
4. `docker compose up -d` → rolling restart of services.

The bootstrap stick is **decoupled from server upgrades**. A v1.0
stick continues to work against a v1.5 deploy server as long as the
`/api/v1/bootstrap/redeem` contract is preserved (which it must be —
the contract is the project's compatibility surface).

If the bootstrap stick itself needs upgrading (e.g. tailscale CVE,
kernel CVE, new firmware), rebuild and re-flash. The
`bootstrap_sticks` table tracks deployment.

## 3. Scaling

The small-tier compose stack handles ~tens of concurrent deployments
on a 4-vCPU/8 GB box. Bottlenecks in order of likelihood:

1. **Outbound NIC bandwidth** while serving WIMs / qcow2s. A 50 GB
   Windows WIM × 30 concurrent deployments saturates 1 Gb/s easily.
   Mitigations:
   - Move blobs to a CDN (CloudFront, Cloudflare R2 with signed URLs);
     point http-boot at it via `IMAGE_URL_BASE`.
   - For LAN PXE, run a local nginx in `--profile lan-cache` mode that
     reverse-proxies the central blob store with a large disk cache.
2. **Postgres** — only saturates above ~hundreds of deployments per
   minute. Vertical scale or move to managed.
3. **Auth-broker** — bound by Tailscale/Headscale API latency. ~5-10
   redeems per second sustained; far above any realistic deployment
   pace.

For the 3-node HA tier:
- 3x app nodes behind a load balancer.
- External Postgres (managed Postgres or 3-node Patroni).
- External S3 (real S3, Ceph RGW, Garage).
- Caddy on each app node with shared cert storage in S3 (Caddy
  supports this).
- See `docker-compose.ha.yml` (TODO; tracked in STATUS).

## 4. Log rotation

All services log to stdout in JSON. The compose stack's default
docker logging driver is `json-file` — set a max-size or switch to
`journald` to avoid disk-fill:

```yaml
# docker-compose.yml override
services:
  api:
    logging:
      driver: json-file
      options:
        max-size: 100m
        max-file: "5"
```

Ship to your log aggregator (Loki, Splunk, Cloudwatch) via Promtail /
Fluent Bit / equivalent. Required: keep audit log entries for at
least 1 year.

## 5. Failure modes table

| Failure | Detection | Mitigation | Time-to-recover |
|---|---|---|---|
| Postgres unreachable | `/healthz` 503; api/auth-broker error logs | Restart Postgres; if data corrupted, restore latest backup | ≤15 min |
| MinIO unreachable | http-boot 5xx on blob fetches | Restart MinIO; check disk; failover to external S3 | ≤15 min |
| Headscale unreachable | auth-broker `/redeem` returns 502 (`overlay_unavailable`) | Restart Headscale or fail over to a second instance; **codes already issued are still valid**, so deployments resume when Headscale recovers | minutes |
| Caddy ACME failure | TLS warnings in browsers | Switch to internal cert temporarily; investigate ACME provider | ≤1h |
| Deploy CA expired | Sticks refuse to connect | Run §6 below (CA rotation) | hours, but predictable from cert expiry |
| MOK key compromised | Detected externally (audit) | Generate new MOK; re-sign GRUB/kernel/iPXE; re-flash sticks; revoke old MOK from machines via `mokutil --delete`. **This is painful — protect the MOK key.** | days |
| Auth-broker container compromise | EDR alerts; anomalous `headscale_authkey.minted` rate | Rotate Headscale API key; rebuild broker container; review audit | hours |
| MinIO bucket public-mistake | Periodic config audit | Rotate any exposed creds; reissue any one-shot tokens that may have been observed | hours |

## 5b. Headscale API key rotation

The auth-broker carries a Headscale API key that authorizes minting
ephemeral pre-auth keys for bootstrap nodes. Compromise of this key
gives an attacker the ability to add tagged nodes to your tailnet.
Rotate on a 30-90 day cadence.

```sh
# 1. Mint a new key (defaults to 90-day TTL).
HEADSCALE_URL=https://headscale.example.com \
HEADSCALE_API_KEY=<current> \
  bash scripts/rotate-headscale-key.sh mint

# Save the printed `key` field to your secret store.
# Update HEADSCALE_API_KEY in the broker's environment.

# 2. Restart broker; verify health.
docker compose restart auth-broker
curl -fsS https://$DEPLOY_FQDN/healthz

# 3. Expire the OLD key (use prefix from the list output).
HEADSCALE_API_KEY=<new> \
  bash scripts/rotate-headscale-key.sh expire <old-key-prefix>
```

`scripts/rotate-headscale-key.sh list` shows all active keys with
their prefixes and expirations. The Headscale API only returns the
full key string on creation — capture it then.

## 6. CA rotation runbook

See `docs/BOOTSTRAP.md §7` for the why; here are the commands:

```sh
# 1. New CA
openssl req -new -x509 -newkey rsa:4096 -days 3650 -nodes \
    -keyout secrets/ca-2026.key \
    -out    secrets/ca-2026.pem \
    -subj   "/CN=deployserver Internal CA 2026/O=YourOrg/"

# 2. New server cert chained to new CA
# (skip if your TLS setup uses Let's Encrypt for the public side; this
# matters for the cert sticks pin against)
... openssl ca config ...

# 3. Configure server to present BOTH old and new chain during transition
cat secrets/ca-2026.pem secrets/ca-2025.pem > secrets/ca-bundle.pem
# update Caddyfile / nginx to serve cert_chain = server.pem + ca-bundle.pem

# 4. Build new sticks
sudo bash bootstrap/make-stick.sh --ca-cert secrets/ca-2026.pem ...

# 5. Inventory: which sticks have the OLD CA?
deployctl bootstrap-sticks list --ca-fingerprint $(openssl x509 -in secrets/ca-2025.pem -noout -fingerprint -sha256 | cut -d= -f2 | tr -d :)

# 6. After all sticks reissued, drop old CA from server cert chain.
```

## 7. Capacity planning

Per concurrent deployment, a rough budget:

| Resource | Cost during imaging |
|---|---|
| CPU on deploy server | ~0.05 core (mostly nginx serving blobs) |
| RAM on deploy server | ~50 MB per concurrent target |
| Outbound NIC | image_size_gb × 8 / image_apply_time_s Gbit/s; e.g. 4 GB Ubuntu / 5 min ≈ 0.1 Gbit/s sustained |
| Postgres connections | ~3 per deployment for the duration of imaging (state transitions, events) |
| MinIO bandwidth | same as outbound NIC if blobs colocated |

A 4 vCPU / 8 GB / 1 Gbit single host comfortably handles 20-30
concurrent deployments. Above that, scale outward.
