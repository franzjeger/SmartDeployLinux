// spectest is an internal, non-shippable module that enforces the SDK's
// correspondence with the OpenAPI contract. It is separate from the SDK
// module so its YAML-parsing test dependency (gopkg.in/yaml.v3) never
// propagates to code that imports the SDK — the SDK itself stays
// dependency-free.
module github.com/your-org/deployserver/sdk/spectest

go 1.25.12

require (
	github.com/your-org/deployserver/sdk v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

replace github.com/your-org/deployserver/sdk => ../
