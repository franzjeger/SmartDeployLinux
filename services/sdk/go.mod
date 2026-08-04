// The deployserver Go SDK ships with ZERO third-party dependencies — the
// standard library only. Nothing is added to an importer's go.sum.
//
// The spec-parity and spec-sync checks (which parse the embedded OpenAPI
// YAML and so need a YAML library) live in a separate internal module,
// ./spectest, precisely so that test-only dependency never propagates to
// code that imports this package.
module github.com/your-org/deployserver/sdk

go 1.25.12
