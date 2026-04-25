#!/usr/bin/env bash
# Generate the internal CA and per-service certs used for mTLS between
# deploy-server containers (api, http-boot, worker, auth-broker).
#
# Called once at first deploy. Output goes into secrets/ which is
# gitignored.
#
# This CA is SEPARATE from the deploy-server's public TLS CA (the one
# pinned on bootstrap sticks). Mixing them is a footgun: the public CA
# is presented on the WAN-facing endpoint, while this internal CA never
# leaves the trusted docker network.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$REPO_ROOT/secrets"
cd "$REPO_ROOT/secrets"

DAYS_CA=3650
DAYS_SVC=825    # max for some browsers; doesn't matter inside the network
                # but keeps openssl from complaining

# CA -------------------------------------------------------------------

if [ ! -f internal-ca.pem ]; then
    echo "==> generating internal CA"
    openssl genrsa -out internal-ca-key.pem 4096
    openssl req -new -x509 \
        -key internal-ca-key.pem \
        -out internal-ca.pem \
        -days $DAYS_CA \
        -subj '/CN=deployserver Internal CA/O=deployserver/' \
        -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
        -addext 'keyUsage=critical,keyCertSign,cRLSign'
    chmod 0600 internal-ca-key.pem
fi

# Per-service certs ---------------------------------------------------

gen_svc_cert() {
    local svc=$1
    if [ -f "${svc}.pem" ]; then
        return 0
    fi
    echo "==> issuing cert for ${svc}"
    openssl genrsa -out "${svc}-key.pem" 2048
    cat > "${svc}.cnf" <<EOF
[req]
distinguished_name = dn
req_extensions     = v3
prompt             = no
[dn]
CN = ${svc}.deployserver.internal
O  = deployserver
[v3]
basicConstraints = critical,CA:FALSE
keyUsage         = critical,digitalSignature,keyEncipherment
extendedKeyUsage = serverAuth,clientAuth
subjectAltName   = DNS:${svc},DNS:${svc}.deployserver.internal,DNS:localhost,IP:127.0.0.1
EOF
    openssl req -new -key "${svc}-key.pem" -out "${svc}.csr" -config "${svc}.cnf"
    openssl x509 -req \
        -in "${svc}.csr" \
        -CA internal-ca.pem -CAkey internal-ca-key.pem -CAcreateserial \
        -out "${svc}.pem" \
        -days $DAYS_SVC \
        -extfile "${svc}.cnf" -extensions v3
    chmod 0600 "${svc}-key.pem"
    rm -f "${svc}.csr" "${svc}.cnf"
}

for svc in api http-boot worker auth-broker; do
    gen_svc_cert "$svc"
done

cat <<EOF

==> internal CA + service certs ready
    secrets/internal-ca.pem        - distribute to every service
    secrets/internal-ca-key.pem    - KEEP ON ISSUER ONLY; chmod 600
    secrets/<svc>.pem              - server + client cert per service
    secrets/<svc>-key.pem          - private key per service

Mount the relevant files into each container:

  api:         secrets/api.pem secrets/api-key.pem secrets/internal-ca.pem
  auth-broker: secrets/auth-broker.pem ...
  http-boot:   secrets/http-boot.pem ...
  worker:      secrets/worker.pem ...

The internal listener on each service uses tls.RequireAndVerifyClientCert
with internal-ca.pem as the only trusted root. SECURITY.md §4 #3.
EOF
