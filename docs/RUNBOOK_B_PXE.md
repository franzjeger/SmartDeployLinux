# Runbook B — LAN PXE deploy (datacenter / lab)

Use this when the deploy server is on the same L2 as the targets, or
when you have an L3 routed network with `ip-helper` style DHCP relay
configured to forward broadcasts to the deploy server.

## When to use this vs. USB bootstrap

| Scenario | Recommended path |
|---|---|
| Imaging a rack of new hardware in your DC | LAN PXE (this runbook) |
| Re-imaging an office full of machines | LAN PXE if the office has the deploy server on the same L2; otherwise edge agent (Runbook C) |
| One machine at a remote site | USB bootstrap (Runbook A) |
| You can't get DHCP changes approved | USB bootstrap (Runbook A) |

## Prerequisites

- `services/tftp/` container running on a host with **layer-2 access** to
  the target VLAN.
- The host's IP on that VLAN, which goes into `TFTP_SERVER_IP` (clients
  TFTP from this address; dnsmasq announces it in DHCP options).
- The existing DHCP server keeps issuing leases. dnsmasq runs as
  **proxyDHCP** alongside it — there is no IP conflict. (Verified on:
  ISC DHCP, Windows Server DHCP, Mikrotik RouterOS, pfSense.)
- The custom iPXE binaries (`ipxe.efi`, `undionly.kpxe`) staged in
  `/tftproot/` — see `Makefile` target `build-ipxe`.
- Caddy in front of the deploy server is reachable on `https://$DEPLOY_FQDN`
  from the target VLAN.

## Bring-up

1. Build iPXE if you haven't:
   ```sh
   make build-ipxe
   cp ipxe/build/ipxe.efi services/tftp/tftproot/
   cp ipxe/build/undionly.kpxe services/tftp/tftproot/
   ```

2. Set env in `.env`:
   ```
   LISTEN_INTERFACE=eth1            # host's NIC on the target VLAN
   SUBNET=192.168.50.0              # the target VLAN's network address
   TFTP_SERVER_IP=192.168.50.10     # this host's IP on that VLAN
   DEPLOY_FQDN=deploy.example.com
   ```

3. Start with the `lan-pxe` profile:
   ```sh
   docker compose --profile lan-pxe up -d dnsmasq
   ```

4. Boot a target. It DHCPs, gets a normal lease from your DHCP server,
   and ALSO gets a proxyDHCP offer from dnsmasq with a per-arch boot
   filename. It TFTPs the iPXE binary, runs it, and the embedded
   `embed.script` in iPXE chainloads
   `https://$DEPLOY_FQDN/boot/by-mac/<mac>.ipxe` over HTTPS.

5. From there the flow is identical to the USB bootstrap path —
   per-machine iPXE script, OS-specific kernel/initrd or wimboot,
   autoinstall/unattend.

## Coexistence with existing DHCP

dnsmasq does NOT issue DHCPOFFER for IP leases when configured with
`dhcp-range=...,proxy`. It only sends the PXE-specific options
(`option-arch`, `boot-file-name`, `next-server`). Clients merge:
- the lease offer from your normal DHCP
- the proxyDHCP boot offer from dnsmasq

If you observe clients ignoring the proxyDHCP offer, check that:
- The two servers are on the same L2 (proxyDHCP doesn't traverse relays
  unless your relay is configured for it — most aren't).
- Your normal DHCP isn't already setting `next-server` / `filename`. If
  it is, the client will use those instead. Either remove those from
  your existing DHCP config or use `dhcp-ignore` filtering on the
  proxyDHCP side.
- Clients are PXE-capable in firmware (UEFI: enabled; some boards ship
  with PXE off by default).

## Secure Boot on LAN PXE

The iPXE binaries we ship are NOT Microsoft-signed. Targets booting via
PXE with Secure Boot enabled will refuse to load `ipxe.efi`.

Options:

1. **Disable Secure Boot in the target's firmware** (acceptable for
   lab/DC).
2. **Chain through shim:** load shim (Microsoft-signed) as the boot
   filename, have shim load grub.efi (signed by your MOK), and have
   GRUB chainload iPXE. Requires the user to enroll the MOK once per
   machine.
3. **Use UEFI HTTP Boot directly** (architecture 0x0010 / 0x000f). Set
   `dhcp-boot=tag:efihttp,http://...` per the dnsmasq.conf comment.
   Modern firmware handles HTTPS too, but the cert must chain to a
   firmware-trusted CA — usually only Microsoft CA, so this is mostly
   useful with cert-pinned in-firmware enrollment, which is rare.

For most users: option 1 in the lab, option 2 in production with
managed MOK enrollment.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Target boots to grub-rescue or iPXE shell with no chain | iPXE compiled without HTTPS, or deploy server cert not chaining to embedded CA. Rebuild iPXE; check `ipxe/certs/deploy-ca.pem`. |
| Target sees no proxyDHCP offer | dnsmasq not on the same L2; or interface mis-set in `LISTEN_INTERFACE`. Check `tcpdump -i $LISTEN_INTERFACE 'udp port 67 or udp port 4011'`. |
| TFTP times out | Firewall on host blocking UDP 69. dnsmasq uses random high ports for the data transfer; ensure the firewall is stateful or open the ephemeral range. |
| Boot starts but stops at "iPXE> " | The chain command failed. Drop into shell, type `chain https://...`, look at the actual error. |
