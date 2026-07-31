# deployserver TypeScript SDK

Typed TypeScript/JavaScript client for the deployserver operator API
(`/api/v1`).

Its surface is derived from — and kept in exact correspondence with — the
OpenAPI 3.1 spec the server publishes at `/api/openapi.yaml`. A parity
test asserts every documented operation has exactly one SDK method and
vice versa, so the client cannot silently drift from the API. That is the
guarantee a code generator gives; here it is enforced on a hand-written,
idiomatic client — and the operation table is wired into the methods, so
dropping an operation is a **compile error**, not just a test failure.

- **Zero runtime dependencies.** HTTP goes through the global `fetch`
  (Node 18+ and every modern browser). The YAML parser the parity test
  needs is a **devDependency**, so `npm install @your-org/deployserver-sdk`
  pulls nothing but this package.
- **All 51 operations** across machines, profiles, images, driver-packs,
  catalog, jobs, deployments, reports, users, sites, bootstrap-sticks,
  and audit.
- **Typed errors.** `isNotFound(err)` / `isForbidden(err)` and a rich
  `ApiError` instead of string-matching.
- **ESM + `.d.ts`.** Ships type declarations and runs in Node and the
  browser.

## Install

```
npm install @your-org/deployserver-sdk
```

Requires Node 18+ (for global `fetch`) or a modern browser. In older Node
you can inject a `fetch` implementation via `Options.fetch`.

## Quick start

```ts
import { DeployClient, isNotFound } from "@your-org/deployserver-sdk";

const c = new DeployClient({
  baseUrl: "https://deploy.example.com",
  token: process.env.DEPLOY_API_TOKEN, // OIDC ID token or service bearer
});

const machines = await c.listMachines();
for (const m of machines) {
  console.log(m.ID, m.AssetTag ?? "(untagged)");
}

// Issue a one-shot deployment code:
const { code, expires_at } = await c.issueDeployment({
  machine_id: "1f4c…",
  profile_id: "9ab2…",
  ttl_seconds: 3600,
});
console.log(`boot code ${code} valid until ${expires_at}`);
```

## Authentication

Pass `Options.token` — an OIDC ID token obtained with `deployctl auth
login`, or a service bearer. It is sent as `Authorization: Bearer
<token>`. In dev mode (server started with no OIDC configured) auth is
open and the token may be omitted.

## Error handling

Every non-2xx response rejects with an `ApiError` carrying the status,
method, path, and the server's error message. Classify without
string-matching:

```ts
import { DeployClient, ApiError, isNotFound, isForbidden } from "@your-org/deployserver-sdk";

try {
  const m = await c.getMachine(id);
} catch (err) {
  if (isNotFound(err)) {
    // 404
  } else if (isForbidden(err)) {
    // 403 — the token lacks the required RBAC permission
  } else if (err instanceof ApiError) {
    console.error(`api ${err.status}: ${err.message}`);
  } else {
    throw err; // transport/other
  }
}
```

## The contract

The exact OpenAPI 3.1 document this build was generated against ships in
the package and is reachable via the `./openapi.yaml` export condition
(or `require.resolve`):

```ts
import spec from "@your-org/deployserver-sdk/openapi.yaml"; // with a YAML loader
```

## Contributing / keeping in sync

The source of truth for the API contract is
`services/api/internal/apispec/openapi.yaml`. This package embeds a
verbatim copy at `services/sdk-ts/openapi.yaml`.

When the API spec changes:

1. `npm run sync-spec` — refresh the embedded copy.
2. Add/adjust the matching entry in `src/operations.ts`, the method in
   `src/client.ts`, and any type in `src/models.ts`.
3. `npm test` — compiles (a missing operation is a compile error), then
   runs the parity test (`ALL_OPERATIONS` ↔ spec bijection), the sync
   test (embedded copy byte-identical to the api source), and the
   `fetch`-backed behavior tests.

## License

Apache-2.0, same as the rest of the repository.
