#!/usr/bin/env bash
# setup.sh — one-time prerequisites for the tailnet deploy proof:
#   1. build the real Headscale + Tailscale binaries from source (the
#      control plane the product targets and the client the stick runs)
#   2. write a minimal Headscale config + DERP map under $WORK
#   3. build the golden image (delegates to ../e2e-kvm/build-golden.sh)
#
# Binaries and state live under $WORK (default /var/tmp/e2ekvm) so run.sh
# finds them. Re-running is cheap: existing binaries/images are reused.
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=${WORK:-/var/tmp/e2ekvm}
BIN=$WORK/bin
HS_VERSION=${HS_VERSION:-v0.26.1}
TS_VERSION=${TS_VERSION:-v1.86.0}
mkdir -p "$BIN" "$WORK/hs"

echo ">> building headscale $HS_VERSION + tailscale $TS_VERSION (module proxy)"
GOBIN=$BIN go install "github.com/juanfont/headscale/cmd/headscale@$HS_VERSION"
GOBIN=$BIN go install "tailscale.com/cmd/tailscaled@$TS_VERSION"
GOBIN=$BIN go install "tailscale.com/cmd/tailscale@$TS_VERSION"

echo ">> writing Headscale config + DERP map"
cat >"$WORK/hs/derp.yaml" <<'EOF'
# Single local DERP region. Same-host tailnet nodes connect directly; this
# only satisfies Headscale's "at least one region" startup requirement.
regions:
  900:
    regionid: 900
    regioncode: local
    regionname: Local Embedded
    nodes:
      - name: local900
        regionid: 900
        hostname: 127.0.0.1
        ipv4: 127.0.0.1
        stunport: 3478
        stunonly: false
        derpport: 8085
EOF

cat >"$WORK/hs/config.yaml" <<EOF
server_url: http://127.0.0.1:8085
listen_addr: 127.0.0.1:8085
metrics_listen_addr: 127.0.0.1:9095
grpc_listen_addr: 127.0.0.1:50443
grpc_allow_insecure: false
noise:
  private_key_path: $WORK/hs/noise_private.key
prefixes:
  v4: 100.64.0.0/10
  v6: fd7a:115c:a1e0::/48
database:
  type: sqlite
  sqlite:
    path: $WORK/hs/db.sqlite
derp:
  server:
    enabled: false
    automatically_add_embedded_derp_region: false
  urls: []
  paths:
    - $WORK/hs/derp.yaml
  auto_update_enabled: false
dns:
  magic_dns: false
  override_local_dns: false
  base_domain: deploy.internal
  nameservers:
    global: []
private_key_path: $WORK/hs/private.key
log:
  level: warn
EOF
"$BIN/headscale" -c "$WORK/hs/config.yaml" configtest && echo "   headscale config valid"

echo ">> building the golden image (if missing)"
if [ ! -f "$WORK/golden.tar.zst" ]; then
  "$HERE/../e2e-kvm/build-golden.sh" "$WORK/golden.tar.zst"
else
  echo "   reusing $WORK/golden.tar.zst"
fi

echo ">> setup complete. Ensure a Postgres is reachable at \$PG_DSN, then: sudo ./run.sh"
