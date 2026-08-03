# Top-level Makefile. Each subsystem has its own Makefile; this one
# orchestrates them and is the single entry point for CI.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

# Pinned tool versions. CI fails if these drift.
GO_VERSION             ?= 1.23.4
NODE_VERSION           ?= 22.11.0
BUILDROOT_VERSION      ?= 2025.02
IPXE_REF               ?= v1.21.1
TAILSCALE_VERSION      ?= 1.86.0
DNSMASQ_VERSION        ?= 2.90
SHIM_VERSION           ?= 15.8
GRUB_VERSION           ?= 2.12

# Tagging scheme for produced container images.
REGISTRY               ?= ghcr.io/your-org/deployserver
TAG                    ?= dev

# --- top-level targets -----------------------------------------------------

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z0-9_.-]+:.*?## / {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

secrets: ## Generate the internal CA + per-service certs (one-time).
	bash scripts/gen-internal-ca.sh

build: build-images build-bootstrap build-ipxe ## Build everything.

build-images: ## Build all service container images.
	docker compose build

build-bootstrap: ## Build the USB bootstrap image (calls bootstrap/Makefile).
	$(MAKE) -C bootstrap all

build-ipxe: ## Build the iPXE binaries (calls ipxe/Makefile).
	$(MAKE) -C ipxe all

up: ## Bring up the small-tier compose stack.
	docker compose up -d

down: ## Stop the compose stack.
	docker compose down

logs: ## Tail compose logs.
	docker compose logs -f --tail=200

seed-admin: ## Seed first admin user (after up). Reads OIDC_* from .env.
	docker compose exec api /usr/local/bin/api seed-admin

migrate: ## Run DB migrations.
	docker compose exec api /usr/local/bin/api migrate up

# --- tests -----------------------------------------------------------------

test: test-unit test-ts test-py ## Run unit tests (Go + TypeScript + Python SDKs).

test-unit: ## Run Go unit tests across all services.
	cd services/api && go test ./...
	cd services/auth-broker && go test ./...
	cd services/worker && go test ./...
	cd services/edge-agent && go test ./...
	cd services/http-boot && go test ./...
	cd services/deployctl && go test ./...
	cd services/sdk && go test ./...
	cd services/sdk/spectest && go test ./...

test-ts: ## Build + test the TypeScript SDK (needs Node 18+).
	cd services/sdk-ts && npm ci && npm test

test-py: ## Type-check + test the Python SDK (needs Python 3.11+).
	cd services/sdk-py && python3 -m pip install -q -e ".[test]" && \
	  python3 -m unittest discover -s tests

sync-sdk-spec: ## Refresh every SDK's embedded OpenAPI spec from the api source of truth.
	cp services/api/internal/apispec/openapi.yaml services/sdk/openapi.yaml
	cp services/api/internal/apispec/openapi.yaml services/sdk-ts/openapi.yaml
	cp services/api/internal/apispec/openapi.yaml services/sdk-py/openapi.yaml

test-e2e: ## Run the deploy-spine e2e smoke test (needs DEPLOY_TEST_PG_DSN).
	cd tests/e2e && go test ./... -count=1 -v

# --- security --------------------------------------------------------------

sec-scan: ## govulncheck (blocking) + gosec high-severity (advisory), per module.
	for svc in auth-broker api http-boot edge-agent worker deployctl sdk; do \
	  ( cd services/$$svc && govulncheck ./... ) || exit 1; \
	done
	for svc in auth-broker api http-boot edge-agent worker deployctl sdk; do \
	  ( cd services/$$svc && gosec -severity high ./... ) || true; \
	done
	trivy fs --severity HIGH,CRITICAL --exit-code 1 .

# --- cleaning --------------------------------------------------------------

clean:
	$(MAKE) -C bootstrap clean
	$(MAKE) -C ipxe clean

.PHONY: help secrets build build-images build-bootstrap build-ipxe up down logs \
        seed-admin migrate test test-unit test-e2e sec-scan clean
