# Runbook A — USB bootstrap deploy (primary path)

The headline flow. Use this for: WAN-deployed machines, branch offices,
remote employees, anywhere the deploy server is not on the target's
local L2 and you don't want to ship infrastructure to the site.

## What the operator needs

- The deploy server up and reachable on `https://$DEPLOY_FQDN`.
- A pre-built bootstrap stick image. (One operator builds this once;
  any number of operators can flash and reuse the same image.)
- The Headscale (or Tailscale) ACL deployed (`bootstrap/external/acls.json`).

## What the onsite user needs

- A USB stick with the bootstrap image flashed on it.
- A wired ethernet cable plugged into the target.
- A 6-character code, dictated to them by phone, email, or chat.

## One-time setup (operator)

1. Generate the project MOK keypair (one-time, per organization):
   ```sh
   bash bootstrap/keys/gen-mok.sh
   ```
   Keep `MOK.priv` offline. Distribute `MOK.cer` to operators for
   one-time enrollment per machine when Secure Boot is on.

2. Pin the upstream binary SHAs in `bootstrap/external/fetch.sh`:
   ```sh
   curl -fsSL https://deb.debian.org/debian/pool/main/s/shim-signed/...amd64.deb | sha256sum
   curl -fsSL https://pkgs.tailscale.com/stable/tailscale_1.86.0_amd64.tgz | sha256sum
   ```
   Paste the values into the `SHIM_DEB_SHA` and `TS_SHA` variables.
   This is a deliberate gate — we refuse to download until pinned.

3. Run fetch.sh:
   ```sh
   ( cd bootstrap && bash external/fetch.sh )
   ```

4. Build the image:
   ```sh
   make build-bootstrap
   ```
   Takes ~30 minutes the first time (buildroot fetches & builds the
   toolchain). Output: `bootstrap/output/deploy-bootstrap.img`.

5. Customize for your tailnet + deploy server:
   ```sh
   sudo bash bootstrap/make-stick.sh \
       --output     deploy-bootstrap-prod.img \
       --tailnet    deploy.your-org \
       --deploy-url https://deploy.your-org.example.com \
       --ca-cert    /path/to/deploy-ca.pem \
       --control-url https://headscale.your-org.example.com
   ```

6. Flash to a USB stick:
   ```sh
   sudo dd if=deploy-bootstrap-prod.img of=/dev/sdX bs=4M status=progress conv=fsync
   ```

7. Test boot it in nested KVM before mailing physical sticks:
   ```sh
   qemu-system-x86_64 -enable-kvm -m 2G \
       -drive file=deploy-bootstrap-prod.img,format=raw,if=virtio \
       -bios /usr/share/OVMF/OVMF_CODE.fd \
       -netdev user,id=n0 -device virtio-net,netdev=n0
   ```

## Per-deployment flow

### Operator side

1. **Register the machine** in the UI (asset tag + MAC if known; MAC
   can be discovered later from the redeem source).

2. **Choose a deployment profile** (e.g. `ubuntu-2404-baseline`,
   `win11-pro-domain-acmecorp`). The profile binds an image, an
   answer-file template, and any one-shot tokens for downstream
   secrets (domain join, BitLocker escrow, etc.).

3. **Issue a code:**
   ```sh
   deployctl deployments issue \
       --machine LAB-01 \
       --profile ubuntu-2404-baseline \
       --ttl 4h \
       --binding-cidr 203.0.113.0/24    # optional: restrict to a CIDR
   ```
   The CLI prints `4A7-K2P` (or whatever was generated). The code
   appears in the UI's deployment list with state `pending`.

4. **Hand off the stick + code** to the onsite user. The stick is
   non-secret; you can ship one stick to the entire branch. The code
   is the secret of this deployment.

### Onsite user side

1. Plug the stick into a USB port on the target machine.
2. Power on. (Or, if the machine is already on, choose USB from the
   firmware boot menu — usually F12 / F8 / Esc / F2.)
3. Wait for the kernel boot messages to stop. The screen will show:
   ```
   deployserver bootstrap
   ╭─ Enter your 6-character deployment code (e.g. 4A7-K2P): ╮
   │                                                          │
   │  > █                                                     │
   │                                                          │
   ╰──────────────────────────────────────────────────────────╯
   ```
4. Type the code. Confirm. Wait.
5. The screen will show:
   ```
   Bootstrap complete. Loading installer ...
   It is safe to unplug the USB stick once you see the installer banner.
   ```
6. The OS installer takes over. For Ubuntu autoinstall and Windows
   wimboot, no further user action is needed; the machine reboots into
   the new OS unattended.

If anything fails, the stick shows a dialog with the failure reason
and a retry option. See **Troubleshooting** below.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| "DHCP failed on eth0" | No link, or DHCP not on this VLAN | Check the cable. If the VLAN doesn't have DHCP, the slim profile cannot help — the user needs an admin to add a temporary lease, or you ship a `BOOTSTRAP_PROFILE=fat` stick with Wi-Fi. |
| "Code not recognized" | Operator typo, expired code | Re-issue. Codes expire on first successful redeem and after `ttl`. |
| "This code has expired or already been used" | Same | Re-issue. The audit log shows who used it and from where. |
| "Rate-limited by the deploy server" | More than 30 attempts from this source IP in an hour | Wait. If the IP is shared (NAT'd office) consider tightening per-code limits instead. |
| "This code is bound to a different network" | `--binding-cidr` blocked the redeem | The operator restricted this code to a specific source CIDR; redeem from there or re-issue without the constraint. |
| "Network or server error" | TLS cert chain mismatch, DNS resolution, server down | Re-flash the stick with the current `--ca-cert`. Check `https://$DEPLOY_FQDN/healthz` from any machine. |
| Machine boots from internal disk instead of USB | Boot order | Press the firmware boot-menu key. If Secure Boot is on and refuses unsigned bootloaders, see "Secure Boot" below. |

## Secure Boot

If the target has Secure Boot enabled and the user is enrolling the
project MOK for the first time:

1. The first boot from the stick lands in `MokManager.efi` (shim's
   enrollment UI), not directly in our bootstrap.
2. Choose **Enroll MOK** → **Continue** → **Yes** when asked to enroll
   the key. You will be prompted for the MOK password set at enrollment
   time (or the default if you didn't set one). Reboot.
3. Subsequent boots load shim → GRUB (signed by your MOK) → kernel →
   the rest of the flow.

If MokManager doesn't appear and the firmware just refuses to boot
the stick:
- Confirm Secure Boot is set to **Custom** mode if your firmware has
  one, not pure **Standard**.
- Confirm the firmware accepts the Microsoft UEFI CA (most do; some
  business firmware ships with only the Microsoft Windows CA).
- If neither works: disable Secure Boot for this deployment, image
  the machine, then re-enable. (Documented but ugly.)

## What's logged

Every code issuance, every redemption attempt (success and fail),
every deployment_jobs state transition is written to `audit_events`.
Operators with `audit.read` can query:

```sh
deployctl audit query --action 'auth_code.*' --since 24h
deployctl audit query --machine LAB-01
```

Audit log mirrors to a file/syslog sink configured in
`docs/SECURITY.md` so a Postgres compromise can't rewrite history.
