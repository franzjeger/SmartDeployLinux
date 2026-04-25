#!/bin/sh
# entrypoint: start tailscaled, then edge-agent. edge-agent waits for
# tailnet membership before starting dnsmasq.

set -eu

: "${HEADSCALE_URL:?or set TAILSCALE_SAAS=1}"
: "${ADVERTISE_ROUTES:?e.g. 192.168.50.0/24}"
: "${LAN_INTERFACE:?e.g. eth0}"
: "${TFTP_SERVER_IP:?the IP of this box on the LAN}"
: "${DEPLOY_FQDN:?e.g. deploy.example.com}"

mkdir -p /var/lib/tailscale
tailscaled \
    --statedir=/var/lib/tailscale \
    --tun=userspace-networking \
    --socks5-server=localhost:1055 \
    --port=41641 \
    >/var/log/tailscaled.log 2>&1 &

# Hand off to edge-agent (which performs `tailscale up`, then dnsmasq).
exec /usr/local/bin/edge-agent
