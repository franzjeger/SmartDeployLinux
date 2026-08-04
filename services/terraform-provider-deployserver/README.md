# terraform-provider-deployserver

A [Terraform](https://www.terraform.io/) provider for the deployserver
operator API — manage machines, sites, and API tokens declaratively.

It's a thin, typed layer over the project's **Go SDK**
([`services/sdk`](../sdk/README.md)), which is itself kept in lockstep
with the OpenAPI contract — so the provider inherits that same single
source of truth, and every request rides the exact client the CLI and
other tooling use.

## Resources & data sources

| Type | Kind | Operations |
|---|---|---|
| `deployserver_machine` | resource | create / read / update / delete / import |
| `deployserver_site` | resource | create (upsert) / read / delete / import |
| `deployserver_api_token` | resource | create / read / delete (immutable; changes recreate) |
| `deployserver_machines` | data source | list all machines |

## Provider configuration

```hcl
provider "deployserver" {
  endpoint = "https://deploy.example.com" # or the DEPLOY_API_URL env var
  token    = "dpsk_..."                   # or the DEPLOY_API_TOKEN env var
}
```

`token` is a deployserver API token (`deployctl api-tokens create`, or the
web UI) or an OIDC ID token. It is marked sensitive. Reach `endpoint` over
your tailnet.

## Example

See [`examples/main.tf`](examples/main.tf):

```hcl
resource "deployserver_machine" "lab01" {
  asset_tag   = "lab-01"
  mac_primary = "aa:bb:cc:dd:ee:ff"
  vendor      = "Dell Inc."
  model       = "OptiPlex 7090"
}

# A least-privilege CI token, scoped to the operator role.
resource "deployserver_api_token" "ci" {
  name            = "ci-runner"
  expires_in_days = 90
  roles           = ["operator"]
}

output "ci_token" {
  value     = deployserver_api_token.ci.token
  sensitive = true
}
```

The `deployserver_api_token` secret is available only at creation, so
Terraform stores it (sensitively) in state — treat your state as a secret,
as always. Every input on the token forces replacement (and the old token
is revoked on destroy), because the secret cannot be re-derived.

## Building & testing locally

```sh
cd services/terraform-provider-deployserver
go build ./...     # build the provider
go test ./...      # schema + model-mapping unit tests
```

To try it against a running server without a registry, point Terraform at
your local build with a dev override:

```hcl
# ~/.terraformrc  (or $TF_CLI_CONFIG_FILE)
provider_installation {
  dev_overrides {
    "your-org/deployserver" = "/abs/path/to/built/binary/dir"
  }
  direct {}
}
```

Then `terraform plan` / `apply` in a config using the provider — no
`terraform init` required.

This provider is validated end-to-end in development: `terraform apply` /
update / `destroy` are exercised against a live api server, asserting a
clean (drift-free) re-plan and that resources are actually created and
removed.
