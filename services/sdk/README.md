# deployserver Go SDK

Typed Go client for the deployserver operator API (`/api/v1`).

Its surface is derived from — and kept in exact correspondence with — the
OpenAPI 3.1 spec the server publishes at `/api/openapi.yaml`. A parity
test (`TestOperationParity`) asserts every documented operation has
exactly one SDK method and vice versa, so the client cannot silently
drift from the API. That is the guarantee a code generator gives; here it
is enforced on a hand-written, idiomatic client.

- **Zero dependencies — full stop.** The module's `go.mod` has no
  `require`; importing it adds nothing to your `go.sum`. (The spec-parity
  check that needs a YAML parser lives in a separate internal module,
  `./spectest`, so its test dependency never reaches you.)
- **All 54 operations** across machines, profiles, images, driver-packs,
  catalog, jobs, deployments, reports, users, sites, bootstrap-sticks,
  and audit.
- **Typed errors.** `IsNotFound(err)` / `IsForbidden(err)` and a rich
  `*APIError` instead of string-matching.

## Install

```
go get github.com/your-org/deployserver/sdk
```

Module path: `github.com/your-org/deployserver/sdk` (Go 1.23+).

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	sdk "github.com/your-org/deployserver/sdk"
)

func main() {
	c, err := sdk.New(sdk.Options{
		BaseURL: os.Getenv("DEPLOY_API_URL"),   // https://deploy.example.com
		Token:   os.Getenv("DEPLOY_API_TOKEN"), // OIDC ID token or service bearer
	})
	if err != nil {
		log.Fatal(err)
	}

	machines, err := c.ListMachines(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, m := range machines {
		fmt.Println(m.ID)
	}
}
```

## Authentication

Pass `Options.Token` — an OIDC ID token obtained with `deployctl auth
login`, or a service bearer if your deployment uses one. It is sent as
`Authorization: Bearer <token>`. In dev mode (server started with no OIDC
configured) auth is open and the token may be empty.

To skip TLS verification against a dev server, supply your own
`*http.Client` via `Options.HTTP`:

```go
c, _ := sdk.New(sdk.Options{
	BaseURL: "https://localhost:8443",
	HTTP: &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}},
})
```

## Error handling

Every non-2xx response becomes an `*APIError` carrying the status, method,
path, and the server's error message. Classify without string-matching:

```go
m, err := c.GetMachine(ctx, id)
switch {
case err == nil:
	// ok
case sdk.IsNotFound(err):
	// 404
case sdk.IsForbidden(err):
	// 403 — the token lacks the required RBAC permission
default:
	var apiErr *sdk.APIError
	if errors.As(err, &apiErr) {
		log.Printf("api %d: %s", apiErr.Status, apiErr.Message)
	}
}
```

## Operation coverage

The method set is guaranteed complete by `TestOperationParity`. To read
the full contract this SDK build was compiled against:

```go
fmt.Printf("%s", sdk.SpecYAML()) // the embedded OpenAPI 3.1 document
```

## Contributing / keeping in sync

The source of truth for the API contract is
`services/api/internal/apispec/openapi.yaml`. This module embeds a
verbatim copy at `services/sdk/openapi.yaml`.

When the API spec changes:

1. `make sync-sdk-spec` — refresh the embedded copy.
2. Add/adjust the matching `Operation` in `operations.go`, the method in
   `resources.go`, and any model in `models.go`.
3. `cd services/sdk/spectest && go test ./...` — `TestEmbeddedSpecMatchesSource`
   checks the embedded copy is byte-identical to the api source, and
   `TestOperationParity` checks the SDK's `AllOperations` is in exact
   bijection with the spec. A drift in either direction fails the build.
   (`cd services/sdk && go test ./...` runs the dependency-free behavior
   tests; `make test-unit` runs both.)

deployctl's `machines` commands are built on this SDK (see
`services/deployctl/cmd/deployctl/machines.go`), so the client is
exercised by the CLI's tests too.

## License

Apache-2.0, same as the rest of the repository.
