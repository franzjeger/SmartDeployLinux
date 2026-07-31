package sdk

import _ "embed"

// specYAML is a verbatim copy of the OpenAPI 3.1 contract the server
// publishes at /api/openapi.yaml (source of truth:
// services/api/internal/apispec/openapi.yaml). It is embedded here so the
// SDK's parity test can run hermetically — with no network and no
// dependency on the api module — and so `SpecYAML` can hand callers the
// exact contract this SDK build was generated against.
//
// The copy is kept honest two ways: the in-module parity test
// (TestOperationParity) asserts every operation in this spec has exactly
// one SDK method and vice versa, and TestEmbeddedSpecMatchesSource (when
// run from a full checkout) asserts these bytes are identical to the api
// module's source spec.
//
//go:embed openapi.yaml
var specYAML []byte

// SpecYAML returns the OpenAPI 3.1 spec this SDK was built against.
func SpecYAML() []byte { return specYAML }
