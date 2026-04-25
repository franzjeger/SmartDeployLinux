# Runbook C — Bulk remote-site deploy via edge agent

For when you're imaging 30 machines at a new branch office and mailing
30 USB sticks is silly. Ship one small Linux box (Raspberry Pi, mini-PC,
NUC) loaded with the edge-agent container; it joins the tailnet as a
subnet router and runs local proxyDHCP+TFTP, so every other machine on
the LAN PXE-boots normally and chainloads via the tailnet.

## Topology

```
   Branch LAN (192.168.50.0/24, normal site DHCP)
   │
   ├── Targets: 30x desktops to image (PXE-enabled in firmware)
   │
   └── Edge box (1x mini-PC running our container)
         │   - LAN side: serves proxyDHCP + TFTP on 192.168.50.10
         │   - WAN side: tailscale member, advertises 192.168.50.0/24
         │
         └── Internet → Tailnet → Deploy server
                                  (tagged tag:deploy-edge in ACL)
```

Targets behave exactly as in Runbook B (LAN PXE) — they get a
proxyDHCP offer pointing at the edge box's TFTP, fetch iPXE, and iPXE
chainloads the deploy server's HTTPS endpoint over the tailnet via the
edge's subnet route.

## Prerequisites

- A small box at the site (Raspberry Pi 4 or 5 / mini-PC). Wired
  ethernet on the LAN. Outbound internet for the tailnet.
- Headscale or Tailscale ACL deployed including `tag:deploy-edge` and
  the `autoApprovers.routes` entries (see `docs/BOOTSTRAP.md §4`).
- The deploy server reachable at `https://$DEPLOY_FQDN`, cert chaining
  to the deploy CA.

## Setup

1. Install Docker on the edge box. Boot Raspbian / Ubuntu Server / etc.

2. Build the edge-agent image, multi-arch:
   ```sh
   docker buildx build \
       --platform linux/amd64,linux/arm64 \
       --tag your-registry/deployserver-edge-agent:1.0 \
       --push \
       services/edge-agent
   ```

3. On the edge box, create `/etc/edge-agent.env`:
   ```
   HEADSCALE_URL=https://headscale.your-org.example.com
   DEPLOY_URL=https://deploy.your-org.example.com
   DEPLOY_FQDN=deploy.your-org.example.com
   EDGE_NAME=branch-toronto
   LAN_INTERFACE=eth0
   LAN_SUBNET=192.168.50.0
   TFTP_SERVER_IP=192.168.50.10
   ADVERTISE_ROUTES=192.168.50.0/24
   ```

4. Run:
   ```sh
   docker run -d --restart unless-stopped \
       --name edge-agent \
       --network host \
       --cap-add NET_ADMIN --cap-add NET_BIND_SERVICE \
       --device /dev/net/tun \
       --env-file /etc/edge-agent.env \
       -v /tftproot:/tftproot:ro \
       -v edge-state:/var/lib/tailscale \
       your-registry/deployserver-edge-agent:1.0
   ```

5. Issue an **edge bootstrap code** in the UI (this is the same code
   flow as USB bootstrap, but the issued auth key is tagged
   `tag:deploy-edge`):
   ```sh
   deployctl deployments issue --kind edge --label "branch-toronto"
   # → 4A7-K2P
   ```

6. `docker exec -it edge-agent /usr/local/bin/edge-agent` and type the
   code at the prompt. The agent:
   - Calls `/api/v1/bootstrap/redeem`, gets an ephemeral auth key tagged
     `tag:deploy-edge`.
   - `tailscale up --advertise-routes=192.168.50.0/24 --accept-routes`.
   - Waits for tailnet membership.
   - Renders dnsmasq.conf and starts dnsmasq on `LAN_INTERFACE`.

7. Confirm in the deploy server UI that the new tailnet node appears,
   tagged `tag:deploy-edge`, and the route is approved (auto-approved
   if your ACL has `autoApprovers.routes` set up correctly).

## Imaging the targets

Now use Runbook B's flow for each target:

1. Power on a target.
2. Firmware DHCPs.
   - Site DHCP server: hands out IP lease as usual.
   - Edge agent's dnsmasq (proxyDHCP): hands out the iPXE filename per
     architecture.
3. Target TFTPs `ipxe.efi` (or `undionly.kpxe`) from the edge box.
4. iPXE chainloads `https://$DEPLOY_FQDN/boot/by-mac/<mac>.ipxe` over
   HTTPS — but the route to the deploy server's IP goes through the
   edge box's tailnet subnet route. The cert is verified against the
   deploy CA pinned at iPXE build time.
5. The rest is identical to Runbook A — per-machine iPXE script,
   image apply, autoinstall/unattend.

You can image all 30 targets in parallel; the edge box is a quiet
proxy and dnsmasq scales fine to small office sizes.

## Decommission

When the site is fully imaged:

1. `docker rm -f edge-agent` on the edge box, OR
2. Mark the edge tailnet node retired in Headscale (it'll re-key on
   next start; if you want to permanently disable, delete the
   `tag:deploy-edge` from autoApprovers.routes for that subnet).

The tailscaled state in the `edge-state` docker volume retains the
node identity. If you want a clean slate, also `docker volume rm
edge-state` and the next boot starts the operator-code dance again.

## Security notes

- Edge nodes are tagged `tag:deploy-edge`, NOT `tag:deploy-bootstrap`.
  The ACL grants edge nodes only `tag:deploy-server:443` access — they
  cannot reach other edge nodes, other deploy nodes, or anything else
  on the tailnet.
- Edge nodes advertise RFC1918 routes and rely on
  `autoApprovers.routes` to be auto-accepted. If a malicious operator
  joined a node tagged `tag:deploy-edge` claiming subnet
  `0.0.0.0/0`, the autoApprovers list would refuse it because it only
  accepts the three RFC1918 ranges.
- The edge box is a privileged position — anyone who roots it can
  observe and rewrite all PXE traffic on the LAN. Treat it as a
  standard piece of network infrastructure: signed boot, full disk
  encryption with TPM, no shared accounts, audit shipped offsite.
