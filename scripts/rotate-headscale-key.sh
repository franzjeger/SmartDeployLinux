#!/usr/bin/env bash
# Rotate the Headscale API key used by the auth-broker.
#
# Headscale supports multiple concurrent API keys. The rotation pattern:
#   1. Mint a new key with a future expiration.
#   2. Update the auth-broker's HEADSCALE_API_KEY env (operator action).
#   3. Restart the broker so it picks up the new key.
#   4. Once you've confirmed the broker is healthy on the new key,
#      explicitly expire the old key.
#
# This script automates steps 1 and 4. Steps 2 and 3 are operator
# actions because they require touching your secret store + restarting
# the deploy-server stack.
#
# SECURITY.md §4 #5.

set -euo pipefail

usage() {
    grep '^# ' "$0" | sed 's/^# //'
    exit 2
}

cmd=${1:-help}
case "$cmd" in
    mint)    ;;
    expire)  ;;
    list)    ;;
    *)       usage ;;
esac

: "${HEADSCALE_URL:?must be set, e.g. https://headscale.example.com}"
: "${HEADSCALE_API_KEY:?current API key, used to authenticate this rotation call}"

api() {
    local method=$1 path=$2 body=${3:-}
    local args=( --silent --show-error --fail
        --header "Authorization: Bearer $HEADSCALE_API_KEY"
        --header "Content-Type: application/json"
        --max-time 10 -X "$method" )
    if [ -n "$body" ]; then
        args+=( --data-raw "$body" )
    fi
    curl "${args[@]}" "$HEADSCALE_URL$path"
}

case "$cmd" in
    mint)
        # Default: 90-day TTL. Override by passing --days N.
        DAYS=${2:-90}
        EXP=$(date -u -d "+$DAYS days" +%Y-%m-%dT%H:%M:%SZ)
        echo "==> minting new Headscale API key (expires $EXP)"
        resp=$(api POST /api/v1/apikey "{\"expiration\":\"$EXP\"}")
        # Headscale response: {"apiKey": {"id":"...", "prefix":"...", "expiration":"..."}, "key":"..."}
        # The full key is only returned ONCE on creation.
        printf '%s\n' "$resp"
        echo
        echo "==> next steps:"
        echo "    1. Save the 'key' field above into your secret store."
        echo "    2. Update HEADSCALE_API_KEY in the auth-broker's environment."
        echo "    3. Restart the broker:  docker compose restart auth-broker"
        echo "    4. Verify the broker is healthy:"
        echo "         curl -fsS https://\$DEPLOY_FQDN/healthz"
        echo "    5. Once confirmed, expire the OLD key with:"
        echo "         HEADSCALE_API_KEY=<old-key> $0 expire <old-key-prefix>"
        ;;
    list)
        echo "==> active Headscale API keys"
        api GET /api/v1/apikey | sed 's/,/,\n/g'
        ;;
    expire)
        PREFIX=${2:?usage: $0 expire <key-prefix>}
        echo "==> expiring key with prefix $PREFIX"
        api POST /api/v1/apikey/expire "{\"prefix\":\"$PREFIX\"}"
        echo
        echo "==> done. Key with prefix $PREFIX will no longer authenticate."
        ;;
esac
