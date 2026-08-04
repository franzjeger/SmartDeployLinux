# API client collections

Ready-to-use [Postman](https://www.postman.com/) and
[Bruno](https://www.usebruno.com/) collections for the deployserver
operator API — every one of the API's operations, grouped by tag, with
bearer auth, path/query parameters, and example request bodies filled in
from the OpenAPI schemas.

Both are **generated from the OpenAPI spec** (`services/api/internal/apispec/openapi.yaml`)
and are asserted byte-identical to that generator's output by a test, so
they can never drift from the API. Do not edit them by hand — change the
spec (and the handlers) and run `make collections`.

## Postman

`postman/deployserver.postman_collection.json` — a Collection Format v2.1
file, plus `postman/deployserver.postman_environment.json`.

1. **Import** both files (Import → Files).
2. Select the **deployserver (local)** environment and set:
   - `baseUrl` — e.g. `https://deploy.example.com` (over your tailnet).
   - `token` — an API token (`deployctl api-tokens create`, or the API
     tokens panel in the web UI) or an OIDC ID token.
3. Send any request. Bearer auth is applied collection-wide from `token`.

## Bruno

`bruno/deployserver/` is a Bruno collection (one `.bru` file per
operation, foldered by tag). Bruno stores requests as plain text in git,
so this is the format to prefer for version-controlled, reviewable API
work.

1. **Open collection** → pick `clients/bruno/deployserver`.
2. Choose the **local** environment; set `baseUrl` and `token` in it.
3. Run any request, or run the whole collection headless:

   ```sh
   bru run . -r --env local --env-var baseUrl=https://deploy.example.com --env-var token=$DEPLOY_API_TOKEN
   ```

## Regenerating

```sh
make collections     # rewrites clients/postman and clients/bruno from the spec
```

The generator lives at `services/api/cmd/gen-collections`; the
`internal/collections` package holds the emitter and the tests that keep
these files honest:

- **operation parity** — each collection covers exactly the operations the
  spec documents (a bijection, same guarantee the SDKs carry);
- **up-to-date** — the committed files are byte-identical to a fresh
  generation, with no orphans;
- **structure / determinism** — valid v2.1, collection-level bearer auth,
  stable output.

Both collections are additionally verified to execute end-to-end (57/57
requests) with `newman` and `bru` against a stub server.
