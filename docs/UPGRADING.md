# Upgrading a running deploy server

For the single-host small tier — the `docker-compose.yml` +
`docker-compose.deploy.yml` stack. For the backup/restore reference and
the other day-2 procedures, see [`OPERATIONS.md`](OPERATIONS.md).

## The short version

```sh
cd /path/to/deployserver
git pull
docker compose up -d --build
```

`--build` is not optional. The small tier builds its images locally and
tags them `localhost/deployserver-*`; there is no registry to pull from,
so a plain `docker compose up -d` after a `git pull` happily restarts the
**old** images and you will think the upgrade did nothing.

For the same reason, do not reach for `docker compose pull` here. With
locally-built images it fails on every service:

```
failed to resolve reference "localhost/deployserver-api:dev":
dial tcp [::1]:443: connect: connection refused
```

`docker compose pull` is only right if you have set `REGISTRY` to a real
registry and push images there.

## Before you start

Three files on the host are **yours**, not the repo's. Nothing in an
upgrade should touch them, and if you sync files around by hand rather
than with `git pull`, exclude all three:

| File | What it holds |
|---|---|
| `.env` | credentials, host bindings, `COMPOSE_FILE`, `COMPOSE_PROFILES` |
| `secrets/` | the internal CA and per-service mTLS certs |
| `docker-compose.local.yml` | per-host tuning (memory limits, extra mounts) |

`.env` and `secrets/` have always been gitignored; `docker-compose.local.yml`
is the supported place for host tuning so that no tracked file needs local
edits. If you find yourself editing `docker-compose.deploy.yml` on a
server, move that value into `.env` instead — otherwise the next `git pull`
reverts it, and if it was the api's port binding the stack will not come
back up.

## Full procedure

### 1. Back up

The database is the only thing you cannot rebuild. Take the dump *before*
the new api starts, because it migrates on startup (see step 3).

```sh
docker compose exec -T postgres pg_dump -U deploy -Fc deploy \
  > deploy-$(date -u +%Y%m%dT%H%M%SZ).pgdump
```

A copy of the tree is cheap insurance too, mostly so you can diff your
config afterwards:

```sh
tar czf ../deployserver-tree-$(date -u +%Y%m%d).tar.gz --ignore-failed-read .
```

Write it outside the directory being archived, or tar ends up trying to
include the growing archive in itself.

`--ignore-failed-read` matters: parts of `secrets/` are root-owned with
mode 0600, so an unprivileged tar will hit permission errors and would
otherwise abort. Those keys are not in the archive — back them up the way
you back up other root keys, as `OPERATIONS.md` §1 says.

### 2. Fetch and rebuild

```sh
git pull
docker compose up -d --build
```

Expect a few minutes for the Go services to compile.

### 3. Migrations

There is nothing to run. The api applies migrations on startup and the
operation is idempotent:

```go
// Apply migrations on startup. Idempotent.
if err := st.MigrateUp(ctx); err != nil {
```

`docker compose run --rm api migrate up` still exists if you want to
apply them as a separate, explicit step before the service starts.

Migrations are **forward-only** — there are no down migrations. Rolling
back to an older server release across a schema change means restoring
the dump from step 1, which is the whole reason step 1 comes first.

One consequence worth knowing: the worker and the api start in parallel,
so on an upgrade that adds tables the worker can briefly log something
like

```
ERROR reap wake_requests: relation "wake_requests" does not exist
```

That is a startup race against the api's migration, not corruption. It
clears on the worker's next tick.

### 4. Verify

```sh
docker compose ps
```

`api`, `http-boot` and `postgres` must reach `(healthy)`, not merely
`Up`. The healthchecks are the point — a container whose listener has
died still reports `Up`, which is exactly how a completely dead boot
path once went unnoticed for months.

`auth-broker`, `worker` and `dnsmasq` have no healthcheck yet and show a
bare `Up`; check their logs directly.

Then confirm the two paths that matter:

```sh
# operator UI + API
curl -sf http://<host>:<API_HOST_PORT>/healthz

# boot path — nginx AND the renderer behind it
docker compose exec http-boot curl -fsk https://localhost:8443/healthz
docker compose exec http-boot curl -fs  http://localhost:8444/healthz
```

Check the applied schema matches the release you just deployed:

```sh
docker compose exec -T postgres \
  psql -U deploy -d deploy -c 'select filename from schema_migrations order by 1'
```

### 5. Roll back

```sh
git checkout <previous-tag-or-sha>
docker compose up -d --build
```

If the upgrade applied migrations, also restore the dump:

```sh
docker compose exec -T postgres \
  pg_restore -U deploy -d deploy --clean --if-exists < deploy-<timestamp>.pgdump
```

Restore into the *old* schema version, i.e. after checking the old code
out, not before.

## Upgrading a host that predates this layout

Older installs kept host-specific values in `docker-compose.deploy.yml`
and were not git clones at all. To bring one onto the current layout:

1. Back up (step 1 above).
2. Note your host values: the api's bind address and port, and — if you
   run the `lan-pxe` profile — the dnsmasq interface, subnet and TFTP
   server IP.
3. Clone the repo fresh alongside the old directory.
4. Copy `.env` across and add the host values as `API_BIND_ADDR`,
   `API_HOST_PORT`, `PXE_INTERFACE`, `PXE_SUBNET`, `PXE_TFTP_SERVER_IP`.
   Add `COMPOSE_FILE` and `COMPOSE_PROFILES` if they are missing — without
   them `docker compose up -d` silently omits the auth-broker and the
   LAN-PXE listener.
5. Move any other local tuning into `docker-compose.local.yml` and append
   it to `COMPOSE_FILE`.
6. Carry `secrets/` over. If parts of it are root-owned, an unprivileged
   `mv` fails — renaming a directory needs write permission on the
   directory itself, not just its parent. Copy it with a container
   instead, which runs as root and preserves ownership:

   ```sh
   docker run --rm -v /parent/dir:/h alpine \
     cp -a /h/old-install/secrets /h/new-install/secrets
   ```

7. `docker compose down` in the old directory, swap the directories, then
   `docker compose up -d --build` in the new one.

The compose project name is pinned to `deployserver` in
`docker-compose.yml`, so the volumes keep their names and the database
survives the directory swap.
