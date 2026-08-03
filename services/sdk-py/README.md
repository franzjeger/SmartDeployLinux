# deployserver Python SDK

Typed Python client for the deployserver operator API (`/api/v1`).

Its surface is derived from — and kept in exact correspondence with — the
OpenAPI 3.1 spec the server publishes at `/api/openapi.yaml`. A parity
test asserts every documented operation has exactly one SDK method and
vice versa, so the client cannot silently drift from the API. That is the
guarantee a code generator gives; here it is enforced on a hand-written,
idiomatic client — and because the client references the named operation
constants directly, dropping one is caught **statically** by mypy/pyright
(`Name "OP_ME" is not defined`), not just at test time.

- **Zero runtime dependencies.** HTTP goes through the standard library
  (`urllib.request`). `pip install deployserver-sdk` pulls nothing else.
- **All 57 operations** across machines, profiles, images, driver-packs,
  catalog, jobs, deployments, reports, users, sites, bootstrap-sticks,
  and audit.
- **Fully typed** — `TypedDict` models, a `py.typed` marker, and clean
  `mypy --strict` / `pyright` (strict) runs.
- **Typed errors** — `is_not_found(err)` / `is_forbidden(err)` and a rich
  `ApiError`.

## Install

```
pip install deployserver-sdk
```

Requires Python 3.11+.

## Quick start

```python
from deployserver_sdk import DeployClient, is_not_found

c = DeployClient("https://deploy.example.com", token="…")  # OIDC ID token or service bearer

for m in c.list_machines():
    print(m["ID"], m["AssetTag"] or "(untagged)")

# Issue a one-shot deployment code:
res = c.issue_deployment({
    "machine_id": "1f4c…",
    "profile_id": "9ab2…",
    "ttl_seconds": 3600,
})
print(f"boot code {res['code']} valid until {res['expires_at']}")
```

Models are `TypedDict`s, so responses are ordinary dicts at runtime (no
attribute wrappers) while your type checker sees precise field names.

## Authentication

Pass `token` — an OIDC ID token obtained with `deployctl auth login`, or
a service bearer. It is sent as `Authorization: Bearer <token>`. In dev
mode (server started with no OIDC configured) auth is open and the token
may be omitted.

## Error handling

Every non-2xx response raises `ApiError` carrying the status, method,
path, and the server's error message. Classify without string-matching:

```python
from deployserver_sdk import DeployClient, ApiError, is_not_found, is_forbidden

try:
    m = c.get_machine(machine_id)
except ApiError as err:
    if is_not_found(err):
        ...          # 404
    elif is_forbidden(err):
        ...          # 403 — token lacks the required RBAC permission
    else:
        print(err.status, err.api_message)
```

## Contributing / keeping in sync

The source of truth for the API contract is
`services/api/internal/apispec/openapi.yaml`. This package embeds a
verbatim copy at `services/sdk-py/openapi.yaml`.

When the API spec changes:

1. `make sync-sdk-spec` — refresh the embedded copy (updates all SDKs).
2. Add/adjust the matching constant in `operations.py`, the method in
   `_client.py`, and any type in `models.py`.
3. Run the checks:

   ```
   pip install -e ".[test]"       # test-only dep: PyYAML
   python -m unittest discover -s tests
   mypy && pyright
   ```

   `test_operation_parity_bijection` checks `ALL_OPERATIONS` against the
   spec; `test_embedded_spec_matches_source` checks the copy is
   byte-identical to the api source; the behavior tests exercise the real
   `urllib` transport against a local server. A dropped operation also
   fails `mypy`/`pyright`.

## License

Apache-2.0, same as the rest of the repository.
