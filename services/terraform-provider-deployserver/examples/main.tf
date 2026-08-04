terraform {
  required_providers {
    deployserver = {
      source = "your-org/deployserver"
    }
  }
}

# Configure via arguments, or the DEPLOY_API_URL / DEPLOY_API_TOKEN
# environment variables. Reach the server over your tailnet.
provider "deployserver" {
  endpoint = "https://deploy.example.com"
  # token  = "dpsk_..."   # prefer the DEPLOY_API_TOKEN env var
}

resource "deployserver_machine" "lab01" {
  asset_tag          = "lab-01"
  mac_primary        = "aa:bb:cc:dd:ee:ff"
  vendor             = "Dell Inc."
  model              = "OptiPlex 7090"
  default_profile_id = "" # optionally pin a deployment profile UUID
}

resource "deployserver_site" "oslo" {
  name            = "oslo-dc"
  description     = "Oslo datacenter"
  mirror_base_url = "https://mirror.oslo.example.com"
}

# A least-privilege token for a CI runner, scoped to just the operator
# role. The secret is written (sensitively) to state exactly once.
resource "deployserver_api_token" "ci" {
  name            = "ci-runner"
  expires_in_days = 90
  roles           = ["operator"]
}

output "ci_token" {
  value     = deployserver_api_token.ci.token
  sensitive = true
}

data "deployserver_machines" "all" {}

output "machine_count" {
  value = length(data.deployserver_machines.all.machines)
}
