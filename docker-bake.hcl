// Buildx bake definition (deferred since Phase 3, delivered Phase 24).
// `make build-images` uses `docker compose build`; this file is the
// CI/multi-platform alternative:
//
//   docker buildx bake                        # all services, load local
//   docker buildx bake --push --set "*.platform=linux/amd64,linux/arm64"

variable "REGISTRY" { default = "ghcr.io/your-org/deployserver" }
variable "TAG" { default = "dev" }

group "default" {
  targets = ["api", "auth-broker", "http-boot", "worker", "edge-agent", "tftp"]
}

target "_common" {
  platforms = ["linux/amd64"]
}

target "api" {
  inherits = ["_common"]
  context = "./services/api"
  tags = ["${REGISTRY}-api:${TAG}"]
}
target "auth-broker" {
  inherits = ["_common"]
  context = "./services/auth-broker"
  tags = ["${REGISTRY}-auth-broker:${TAG}"]
}
target "http-boot" {
  inherits = ["_common"]
  context = "./services/http-boot"
  tags = ["${REGISTRY}-http-boot:${TAG}"]
}
target "worker" {
  inherits = ["_common"]
  context = "./services/worker"
  tags = ["${REGISTRY}-worker:${TAG}"]
}
target "edge-agent" {
  inherits = ["_common"]
  context = "./services/edge-agent"
  platforms = ["linux/amd64", "linux/arm64"]
  tags = ["${REGISTRY}-edge-agent:${TAG}"]
}
target "tftp" {
  inherits = ["_common"]
  context = "./services/tftp"
  tags = ["${REGISTRY}-tftp:${TAG}"]
}
