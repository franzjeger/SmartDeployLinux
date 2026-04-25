# Architecture

This document is the locked-in design contract. Every component below has a
chosen tool, a pinned version, and a rejected alternative. Before changing a
component choice, update the decision log at the bottom.

## 1. Component diagram

```mermaid
graph LR
    subgraph "Operator's workstation"
        OP[Operator browser / CLI]
    end

    subgraph "Remote site (anywhere with internet)"
        USB[USB Bootstrap stick<br/>buildroot + tailscaled + iPXE]
        TGT[Target machine being imaged]
        EDGE[Edge agent<br/>RPi or mini-PC<br/>optional, for bulk sites]
    end

    subgraph "Datacenter LAN"
        LAN_TGT[Target machines on same L2 as deploy server]
    end

    subgraph "Deploy server (single host or HA)"
        UI[UI<br/>SvelteKit]
        API[API<br/>Go + chi]
        AB[Auth-broker<br/>Go]
        WRK[Worker<br/>Go]
        HB[http-boot<br/>nginx + iPXE scripts]
        TFTP[tftp/proxyDHCP<br/>dnsmasq, optional]
        MB[matchbox<br/>ignition / autoinstall configs]
        PG[(PostgreSQL 16)]
        S3[(MinIO / S3)]
        OTEL[OpenTelemetry collector]
    end

    subgraph "Identity / overlay"
        HS[Headscale<br/>or Tailscale SaaS]
        OIDC[OIDC provider<br/>Keycloak, Authentik, etc.]
    end

    OP -->|HTTPS| UI
    UI -->|HTTPS| API
    OP -.->|CLI| API
    API --> PG
    API --> S3
    API --> AB
    API --> WRK
    API <-->|OIDC| OIDC

    OP -.->|"hand off stick<br/>+ 6-char code"| USB
    USB -->|DHCP / DNS / NTP| INTERNET((Internet))
    USB -->|"HTTPS POST<br/>code redeem<br/>(cert-pinned)"| AB
    AB -->|"create ephemeral<br/>auth key"| HS
    AB -->|"return key + URL"| USB
    USB -->|"tailscale up<br/>--ephemeral"| HS
    USB -->|"iPXE chain<br/>HTTPS over tailnet"| HB
    HB --> S3
    HB --> MB

    LAN_TGT -->|DHCP/PXE| TFTP
    TFTP --> HB

    EDGE -->|tailnet subnet route| HS
    EDGE -->|local proxyDHCP+TFTP| TGT
    TGT -->|"PXE chain<br/>via edge → tailnet"| HB

    WRK --> PG
    WRK --> S3
    API --> OTEL
    AB --> OTEL
    WRK --> OTEL
```

## 2. Sequence diagrams

### 2a. USB bootstrap → Linux deploy (the primary path)

```mermaid
sequenceDiagram
    autonumber
    actor Operator
    actor Onsite as Onsite user
    participant UI
    participant API
    participant AB as auth-broker
    participant HS as Headscale
    participant Stick as USB stick<br/>(target machine)
    participant HB as http-boot
    participant Target as Target OS installer

    Operator->>UI: Create deployment for machine M,<br/>profile = ubuntu-2404-baseline
    UI->>API: POST /machines/M/deployments
    API->>AB: issue-code(machine=M, profile=P, ttl=24h)
    AB->>AB: generate XXX-XXX (base32, no 0/O/1/I)
    AB-->>UI: code = "4A7-K2P"
    UI-->>Operator: show code, hand stick + code to onsite user

    Onsite->>Stick: plug in, power on target
    Stick->>Stick: kernel + initramfs boot
    Stick->>Stick: udhcpc on first wired NIC, NTP sync
    Stick->>Onsite: TUI "Enter deployment code:"
    Onsite->>Stick: types 4A7-K2P
    Stick->>AB: POST /bootstrap/redeem {code: "4A7-K2P"}<br/>(HTTPS, pinned root CA)
    AB->>AB: validate code (single-use, TTL, rate-limit)
    AB->>HS: create ephemeral auth key<br/>tag:deploy-bootstrap, ttl=1h
    HS-->>AB: tskey-...
    AB->>API: log audit event "code redeemed"
    AB-->>Stick: {authkey, chainload_url, machine_id}
    Stick->>HS: tailscale up --authkey=... --ephemeral
    HS-->>Stick: tailnet IP assigned
    Stick->>HB: chainload <https://deploy.tailnet/boot/<machine_id>.ipxe>
    HB->>HB: render per-machine iPXE script<br/>(kernel, initrd, autoinstall URL)
    HB-->>Stick: kernel + initrd over HTTPS
    Stick->>Target: kexec into installer
    Target->>HB: GET autoinstall user-data + meta-data
    HB-->>Target: rendered cloud-init / autoinstall
    Target->>HB: GET package mirror (or upstream)
    Target->>API: POST /machines/M/status (cloud-init phone-home)
    API->>API: mark deployment complete, expire auth code
```

### 2b. USB bootstrap → Windows deploy

```mermaid
sequenceDiagram
    autonumber
    participant Stick as USB stick
    participant AB as auth-broker
    participant HS as Headscale
    participant HB as http-boot
    participant WinPE
    participant Target as Target Windows install

    Note over Stick: identical to Linux flow up to<br/>iPXE chainload (steps 1-12 of 2a)
    Stick->>HB: chainload boot.ipxe
    HB-->>Stick: ipxe script: wimboot + boot.wim (WinPE) over HTTPS
    Stick->>WinPE: wimboot loads WinPE x64
    WinPE->>WinPE: startnet.cmd → bootstrap.cmd
    WinPE->>HB: GET deploy.cmd (per-machine, has machine_id baked in)
    WinPE->>WinPE: identify hardware (DMI, PCI VID/DID via wmic / pnputil)
    WinPE->>API: GET /machines/M/driver-pack-manifest
    API-->>WinPE: list of matched driver packs (URLs in S3)
    WinPE->>HB: GET install.wim (hardware-independent golden image)
    WinPE->>WinPE: DISM /Apply-Image to C:\
    WinPE->>HB: GET each matched driver pack (cab/zip)
    WinPE->>WinPE: DISM /Image:C:\ /Add-Driver /Recurse
    WinPE->>API: GET /machines/M/unattend.xml<br/>(rendered with one-shot domain-join token)
    WinPE->>WinPE: copy unattend.xml to C:\Windows\Panther\
    WinPE->>WinPE: bcdboot, wpeutil reboot
    Target->>Target: specialize pass, OOBE pass
    Target->>API: phone-home, deployment complete
    API->>API: revoke one-shot domain-join token
```

### 2c. LAN PXE deploy (datacenter)

```mermaid
sequenceDiagram
    autonumber
    participant Switch as DHCP server (existing)
    participant Target
    participant Dnsmasq as dnsmasq (proxyDHCP+TFTP)
    participant HB as http-boot

    Target->>Switch: DHCPDISCOVER
    Switch-->>Target: DHCPOFFER (IP, no boot info)
    Target->>Dnsmasq: DHCPDISCOVER (broadcast, also reaches dnsmasq)
    Dnsmasq-->>Target: proxyDHCP offer: filename per arch<br/>(undionly.kpxe / ipxe.efi / etc.)
    Target->>Dnsmasq: TFTP get filename
    Dnsmasq-->>Target: iPXE binary
    Target->>HB: HTTPS chainload boot.ipxe (deploy server's CA pinned in iPXE)
    HB-->>Target: per-machine boot script
    Note over Target,HB: continues identically to Linux/Windows flows above
```

### 2d. Edge agent: bulk remote site

```mermaid
sequenceDiagram
    autonumber
    participant Admin
    participant Edge as Edge agent box
    participant HS as Headscale
    participant Switch as Site DHCP
    participant Target
    participant HB as http-boot (central)

    Admin->>Edge: first boot, runs setup wizard<br/>(operator code, like USB stick)
    Edge->>HS: tailscale up --advertise-routes=10.50.0.0/24<br/>tag:deploy-edge
    HS->>HS: admin pre-approved this route in ACL
    Edge->>Edge: start local dnsmasq:<br/>proxyDHCP + TFTP only, no IP allocation
    Target->>Switch: DHCPDISCOVER
    Switch-->>Target: DHCPOFFER (IP)
    Target->>Edge: DHCPDISCOVER (also broadcast)
    Edge-->>Target: proxyDHCP: ipxe binary via TFTP
    Target->>Edge: TFTP get ipxe.efi
    Edge-->>Target: iPXE binary (cert-pinned to deploy CA)
    Target->>HB: HTTPS chainload over tailnet (via edge subnet route)
    Note over Target,HB: rest of flow identical
```

## 3. Data model (Postgres)

```mermaid
erDiagram
    USERS ||--o{ AUDIT_EVENTS : "creates"
    USERS ||--o{ AUTH_CODES : "issues"
    USERS }o--o{ ROLES : "has"
    ROLES ||--o{ ROLE_PERMISSIONS : "grants"

    MACHINES ||--o{ DEPLOYMENT_JOBS : "is target of"
    MACHINES }o--|| DEPLOYMENT_PROFILES : "default profile"
    MACHINES ||--o{ AUTH_CODES : "bound to"

    DEPLOYMENT_PROFILES }o--|| IMAGES : "uses"
    DEPLOYMENT_PROFILES }o--o{ DRIVER_PACKS : "associates"
    DEPLOYMENT_PROFILES ||--o{ ANSWER_FILE_TEMPLATES : "renders"

    IMAGES ||--o{ IMAGE_VERSIONS : "has"
    IMAGE_VERSIONS }o--|| BLOBS : "stored as"

    DRIVER_PACKS ||--o{ DRIVER_PACK_VERSIONS : "has"
    DRIVER_PACK_VERSIONS }o--|| BLOBS : "stored as"
    DRIVER_PACK_VERSIONS ||--o{ DRIVER_MATCH_RULES : "matches by"

    DEPLOYMENT_JOBS ||--o{ DEPLOYMENT_EVENTS : "has events"
    DEPLOYMENT_JOBS }o--|| AUTH_CODES : "redeemed via"

    AUTH_CODES ||--o{ AUDIT_EVENTS : "logs"
    AUTH_CODES ||--o{ ONE_SHOT_TOKENS : "spawns"

    BOOTSTRAP_STICKS ||--o{ AUDIT_EVENTS : "logs build"

    USERS {
      uuid id PK
      string email UK
      string oidc_subject
      timestamptz created_at
      timestamptz disabled_at
    }

    ROLES {
      uuid id PK
      string name UK
    }

    ROLE_PERMISSIONS {
      uuid role_id FK
      string permission
    }

    MACHINES {
      uuid id PK
      string asset_tag UK
      string mac_primary
      string uuid_smbios
      string vendor
      string model
      uuid default_profile_id FK
      jsonb attributes
      timestamptz created_at
    }

    DEPLOYMENT_PROFILES {
      uuid id PK
      string name UK
      uuid image_id FK
      jsonb answer_file_vars
      timestamptz created_at
    }

    IMAGES {
      uuid id PK
      string name UK
      string os_family
      string os_version
      string arch
      timestamptz created_at
    }

    IMAGE_VERSIONS {
      uuid id PK
      uuid image_id FK
      string version_tag
      uuid blob_id FK
      string signature
      timestamptz created_at
    }

    DRIVER_PACKS {
      uuid id PK
      string vendor
      string model
      string os_family
      string os_version
    }

    DRIVER_PACK_VERSIONS {
      uuid id PK
      uuid pack_id FK
      string version_tag
      uuid blob_id FK
      timestamptz created_at
    }

    DRIVER_MATCH_RULES {
      uuid id PK
      uuid pack_version_id FK
      string match_type
      string match_value
    }

    BLOBS {
      uuid id PK
      string sha256 UK
      bigint size_bytes
      string s3_bucket
      string s3_key
      string signature
      timestamptz created_at
    }

    AUTH_CODES {
      uuid id PK
      string code_hash UK
      uuid machine_id FK
      uuid profile_id FK
      uuid issued_by FK
      inet issued_from_ip
      timestamptz issued_at
      timestamptz expires_at
      timestamptz redeemed_at
      inet redeemed_from_ip
      int attempts
    }

    ONE_SHOT_TOKENS {
      uuid id PK
      uuid auth_code_id FK
      string token_hash UK
      string purpose
      timestamptz expires_at
      timestamptz consumed_at
    }

    DEPLOYMENT_JOBS {
      uuid id PK
      uuid machine_id FK
      uuid profile_id FK
      uuid auth_code_id FK
      string state
      timestamptz created_at
      timestamptz started_at
      timestamptz finished_at
      jsonb result
    }

    DEPLOYMENT_EVENTS {
      uuid id PK
      uuid job_id FK
      string phase
      string message
      jsonb data
      timestamptz at
    }

    BOOTSTRAP_STICKS {
      uuid id PK
      string image_sha256
      string tailnet
      string deploy_url
      string ca_fingerprint
      uuid built_by FK
      timestamptz built_at
    }

    ANSWER_FILE_TEMPLATES {
      uuid id PK
      uuid profile_id FK
      string kind
      text body
      timestamptz updated_at
    }

    AUDIT_EVENTS {
      bigserial id PK
      timestamptz at
      uuid actor_id
      string action
      uuid subject_id
      jsonb data
      inet source_ip
    }
```

Notes on the data model:

- `auth_codes.code_hash` stores `argon2id(code)` — the cleartext code never
  hits disk. Rate-limit and attempts counter run on the hash.
- `blobs` is the content-addressed store. Same image referenced by multiple
  versions (e.g. cosigned by different keys) shares one blob.
- `audit_events` is append-only at the application layer, *and* mirrored to a
  syslog/file sink (see SECURITY.md) so a Postgres compromise can't erase it.
- `one_shot_tokens` is the mechanism by which an unattended installer pulls
  a domain-join credential exactly once. The token is bound to the
  redeeming `auth_code`, expires at deployment-job completion, and is hashed
  at rest.

## 4. Decision log

Format: **Choice** — why it. Alternative — why not.

### Language / framework

- **API in Go (1.23+)** — single static binary, trivial cross-compile for
  the edge-agent (linux/arm64 and linux/amd64), strong stdlib for HTTPS,
  ergonomic JSON. Alternative: Python FastAPI — rejected because the
  edge-agent and worker need to run on a 1 GB RPi without pulling a Python
  runtime, and we want a single language across services.

- **UI in SvelteKit (2.x)** — small bundle, easy SSR for the operator UI,
  good form ergonomics for the deployment-builder wizard. Alternative:
  Next.js — rejected because we don't need the React ecosystem here and
  Svelte's bundle size matters when the UI is sometimes accessed from
  remote sites over the same tailnet.

### Networking and overlay

- **Headscale (0.26+) as the default overlay control plane** — self-hosted,
  open-source, ACL-compatible with Tailscale's policy syntax. Alternative:
  Tailscale SaaS — *also* supported (the auth-broker abstracts both behind
  a small interface), but Headscale is the recommended default for this
  project so the entire stack is open-source by default. We test against
  both.

- **Tailscale 1.86+ as the node agent** — official, well-maintained, the
  ephemeral-key + tag mechanic we rely on is first-class. No alternative
  considered for the agent itself; ZeroTier and Nebula don't have
  equivalent ephemeral-tagged-node primitives at the time of writing.

### Boot

- **iPXE (commit pinned in `ipxe/Makefile`)** with `DOWNLOAD_PROTO_HTTPS`,
  `NET_PROTO_IPV6`, `IMAGE_TRUST_CMD`, `CERT_CMD`, `NSLOOKUP_CMD` enabled,
  built with the deploy-server CA embedded as a trusted root. Alternative:
  GRUB2 standalone HTTP-boot — rejected because GRUB's HTTP support is
  fragile, its TLS story is worse, and the wimboot path for Windows is
  iPXE-native.

- **shim (15.8) + signed GRUB2 (2.12)** for Secure Boot on the bootstrap
  stick. Shim is shipped unmodified (it's signed by Microsoft); GRUB2 and
  the kernel are signed by a project MOK key that the user enrolls once on
  first boot. Alternative: Microsoft's signed Windows Boot Manager →
  rejected, can only chain to Windows.

- **dnsmasq (2.90)** for the LAN PXE secondary path — runs as proxyDHCP so
  it doesn't fight an existing DHCP server on the LAN, and serves TFTP. We
  also use it inside the edge-agent container for the bulk remote-site
  case. Alternative: ISC Kea — rejected for this role because Kea is heavy
  for proxyDHCP-only use and dnsmasq's TFTP is rock-solid.

### Bootstrap rootfs

- **buildroot (2025.02 LTS)** for the USB stick image — reproducible
  builds, total image-size control, no package-manager surface area at
  runtime, hashed output suitable for stick-image attestation. Alternative:
  Alpine — rejected because we don't need apk at runtime, the static
  busybox + only-the-binaries-we-need posture is smaller and gives us
  fewer moving parts to worry about for Secure Boot signing.

### Storage

- **PostgreSQL 16** for relational state. No alternative seriously
  considered.

- **MinIO (RELEASE.2025-09-07T16-13-09Z)** for S3-compatible object
  storage in the bundled compose stack. Alternative: SeaweedFS — rejected
  because S3 compatibility is the lingua franca and MinIO's operational
  story is simpler at the small/medium tier. For HA, the user is expected
  to point at any S3-compatible service (real S3, Ceph RGW, Garage, etc.).

### Worker / queue

- **In-process job queue backed by Postgres `LISTEN`/`NOTIFY` plus a
  `jobs` table with row-level locking** — operationally cheap, no Redis,
  no Beanstalkd, no separate broker. Alternative: Redis Streams or NATS
  JetStream — rejected because the job throughput here is "tens per
  minute peak", not thousands per second.

### Observability

- **OpenTelemetry collector** as the single ingest, exporting to whatever
  the user runs (Tempo, Jaeger, Prometheus, Loki). We ship an opinionated
  bundled stack as `docker-compose.observability.yml` (Tempo + Prometheus
  + Grafana + Loki) but it's optional.

### Image signing

- **cosign (2.5+)** with keyless signing as the default for non-MS
  artifacts (Linux kernels, initramfs, image WIMs/qcow2s, driver packs).
  Alternative: GPG — rejected because cosign's verification workflow is
  built into our content-addressed blob store and we can attest to
  manifests cleanly. For Windows binaries we don't sign anything; we
  consume Microsoft-signed artifacts.

### Auth

- **OIDC for human users, mTLS for service-to-service, short-lived bearer
  tokens for installer phone-home.** No alternative considered — this is
  the obvious answer.

### Reverse proxy

- **Caddy 2.10+** in front of all HTTPS endpoints. Auto-cert from Let's
  Encrypt for the public side; configurable to use the project's internal
  CA on the tailnet side. Alternative: nginx — rejected for the front
  proxy because Caddy's automatic cert management is one less moving
  part. nginx is still used internally as the static media server in
  `services/http-boot/` because its byte-range and large-file performance
  is best-in-class.

## 5. Trust boundaries (one-pager)

| Boundary | Authentication | Confidentiality | Integrity |
|---|---|---|---|
| Operator browser ↔ UI | OIDC | TLS (Caddy) | TLS |
| UI ↔ API | session cookie + CSRF | TLS | TLS |
| Operator CLI ↔ API | OIDC device-code flow → bearer | TLS | TLS |
| Stick ↔ auth-broker (pre-tailnet) | one-time code (rate-limited) | TLS, **CA pinned** | TLS |
| Stick / target ↔ deploy server (post-tailnet) | tailnet membership + tag | tailnet WireGuard + TLS | TLS |
| Edge-agent ↔ deploy server | tailnet + mTLS | tailnet WireGuard | mTLS |
| API/AB/Worker ↔ Postgres | mTLS or unix socket | mTLS | mTLS |
| API/AB/Worker ↔ MinIO | static creds, KMS-wrapped | TLS | TLS |
| Installer phone-home ↔ API | one-shot bearer (bound to auth_code) | tailnet + TLS | TLS |

## 6. Known limitations (carried into all phases)

1. **Wired-only USB bootstrap in v1.** Wi-Fi requires `iwd` plus firmware
   blobs that push the image past the 300 MB target. A `BOOTSTRAP_PROFILE=fat`
   build target is sketched but not delivered in v1.
2. **ARM64 Windows is documented but not validated** in v1. The amd64
   path is the reference implementation.
3. **Driver-pack curation is the user's responsibility.** We ingest Dell
   Command | Update / HP Image Assistant / Lenovo SUS bundles and match
   them; we don't curate or redistribute them.
4. **Microsoft binaries are never redistributed.** The user provides
   Windows ISOs and ADK; we orchestrate, we do not host.
5. **Secure Boot requires a one-time MOK enrollment per machine** the
   first time the stick boots, unless the user disables Secure Boot. This
   is unavoidable without a Microsoft-signed shim of our own.
6. **Wake-on-LAN is LAN-only**, by definition. The edge-agent can issue
   WOL to its local subnet on operator request.

