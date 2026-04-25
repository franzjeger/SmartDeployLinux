# USB Bootstrap — full specification

The bootstrap stick is the centerpiece of this project. This document is
the contract between `bootstrap/` and the rest of the system. Everything
else — Headscale ACLs, the auth-broker, the http-boot iPXE templates —
has to keep this contract working.

## 0. The design intent, in one paragraph

A USB stick that contains no secrets, that any operator can pre-build and
keep on a desk drawer, that turns any machine with a wired ethernet jack
and an internet uplink into a tailnet member able to fetch its install
media from a single central deploy server over HTTPS. The "secret" of any
particular deployment is a 6-character code typed by the onsite operator,
exchanged at boot time for a single-use, ephemeral, tagged, time-limited
Tailscale auth key. From step two onward, the deploy is just HTTP.

## 1. Image contents and size budget

Total target: **≤ 300 MB** for the wired-only profile.

| Component | Size (approx) | Source |
|---|---|---|
| Linux kernel (custom config) | 10 MB compressed | upstream LTS + buildroot config |
| initramfs (rootfs as ramdisk) | 25 MB | buildroot, busybox, our overlay |
| `tailscaled` static binary | 30 MB | `pkgs.tailscale` static build, pinned |
| `tailscale` CLI | 15 MB | same source |
| `iPXE` EFI + BIOS variants | 2 MB | `ipxe/` build |
| shim (Microsoft-signed) | 1 MB | upstream Debian `shim-signed` |
| GRUB2 (signed by our MOK) | 4 MB | `grub-mkstandalone` + our key |
| ca-certificates bundle | 0.4 MB | Mozilla root store + our deploy-CA |
| dialog / whiptail | 0.3 MB | buildroot |
| dhcpcd | 0.5 MB | buildroot |
| chrony (NTP) | 0.3 MB | buildroot |
| iproute2, ethtool | 1 MB | buildroot |
| Our scripts and config | 0.05 MB | `bootstrap/overlay/` |
| **Total** | **~90 MB** | leaves substantial headroom |

The image is laid out as a hybrid GPT/MBR disk:

```
GPT layout:
  Partition 1: ESP, FAT32, 40 MB
    /EFI/BOOT/BOOTX64.EFI       <- shim
    /EFI/BOOT/grubx64.efi       <- our signed GRUB
    /EFI/BOOT/grub.cfg          <- minimal, chains to /boot/grub.cfg
  Partition 2: Linux ext4, 250 MB
    /boot/vmlinuz
    /boot/initramfs.img         <- rootfs lives here
    /boot/grub.cfg
    /etc/deploy/config.json     <- tailnet name, deploy URL, CA fingerprint
                                   (NO auth keys, NO codes)
    /etc/ssl/certs/deploy-ca.pem
    /opt/iPXE/                  <- iPXE binaries chosen at boot time
    (everything else is in initramfs)
```

The hybrid MBR allows BIOS-only machines to boot via syslinux from a
secondary BIOS-boot partition (a 1 MB BIOS-boot type). Both UEFI and BIOS
paths land at the same kernel + initramfs.

## 2. Boot flow, step by step

This is the contract the implementation must honor.

```
1.  BIOS/UEFI loads bootloader from USB.
2.  Bootloader (shim → GRUB or syslinux) loads kernel + initramfs.
3.  Kernel boots. initramfs is the rootfs (no pivot_root needed).
4.  /etc/init.d/rcS runs busybox init scripts. Last script: S99deploy.
5.  S99deploy execs /usr/local/bin/deploy-bootstrap.
6.  deploy-bootstrap:
      a. Loads /etc/deploy/config.json into env.
      b. Brings up the first wired NIC (or, in fat profile, prompts
         for Wi-Fi). Times out after 30s.
      c. Runs udhcpc (default), then chrony (one-shot NTP sync) to fix
         clock to within ~5s of true. Tailscale auth requires sane time.
      d. Verifies it can reach DEPLOY_URL host on TCP 443. If not, shows
         a network-troubleshooter TUI and loops.
      e. Shows TUI: "Enter 6-character deployment code:"
      f. Operator types XXX-XXX (base32, no 0/O/1/I).
      g. POSTs the code to DEPLOY_URL/api/v1/bootstrap/redeem with HTTPS
         where the server cert MUST chain to the embedded deploy-ca.pem.
         If chain check fails, halt with a clear message. Do not proceed.
      h. On 200, parses the JSON response:
           {
             "auth_key":      "tskey-auth-...",
             "control_url":   "https://headscale.example.com",
             "chainload_url": "https://deploy.example.com/boot/<machine_id>.ipxe",
             "machine_id":    "uuid",
             "expires_at":    "2026-04-25T18:30:00Z"
           }
         On 4xx, retries up to 5 times with backoff and re-prompts.
      i. Starts tailscaled in userspace-networking mode, then
         `tailscale up --authkey=... --hostname=bootstrap-<random>
                       --login-server=<control_url>
                       --ephemeral`.
         This is interactive-flag-free: the authkey + ephemeral
         combination produces a self-expiring node tagged
         tag:deploy-bootstrap with no human approval step.
      j. Waits up to 30s for `tailscale status --json` to show the
         deploy server hostname reachable.
      k. Loads the per-arch iPXE binary from /opt/iPXE/.
      l. Execs iPXE with the chainload URL as the first command:
            chain --autofree {{chainload_url}}
         iPXE has the deploy-CA embedded as a trusted root, so the
         HTTPS fetch verifies cleanly.
      m. iPXE pulls boot.ipxe from http-boot, which pulls kernel + initrd
         (Linux) or wimboot + boot.wim (Windows). Stick is no longer
         needed; the operator can unplug it once the installer banner
         appears.
7.  Optional cleanup hook: if boot.ipxe sets `set deploy_unplug_ok 1`,
    deploy-bootstrap displays "It's safe to unplug the USB stick now."
```

## 3. The auth-broker service contract

### `POST /api/v1/bootstrap/issue-code`
- **Auth:** OIDC bearer with `deployment.create` permission.
- **Body:**
  ```json
  {
    "machine_id":    "uuid",
    "profile_id":    "uuid",
    "ttl_seconds":   86400,
    "issued_for":    "free-text label, e.g. 'sent to alice@branch'",
    "binding_ip":    "203.0.113.45/32"   // optional CIDR; if set, redeem
                                          // must come from this CIDR
  }
  ```
- **Response 200:**
  ```json
  {
    "code":       "4A7-K2P",
    "expires_at": "2026-04-26T18:30:00Z"
  }
  ```
- **Side effects:** writes `auth_codes` row with `argon2id(code)`, fires
  audit event `auth_code.issued`.

### `POST /api/v1/bootstrap/redeem`
- **Auth:** **none** — this is what the stick calls before it has any
  identity. Defense is rate-limit + single-use code + cert pin on the
  client side.
- **Headers:** `User-Agent: deploy-bootstrap/<version>` is logged but
  not enforced.
- **Body:**
  ```json
  {
    "code":    "4A7-K2P",
    "stick_image_sha256": "abcdef...",   // optional, advisory; logged
    "boot_uuid": "<random per-boot UUID>"  // for correlation
  }
  ```
- **Response 200 (success):** see step 6h above.
- **Response 401 (bad code, attempts <= max):**
  ```json
  { "error": "invalid_code", "attempts_remaining": 3 }
  ```
- **Response 410 (code expired or already redeemed):**
  ```json
  { "error": "code_consumed_or_expired" }
  ```
- **Response 429 (rate-limited):** `Retry-After` header set.
- **Side effects on success:**
  - Marks code redeemed (atomic UPDATE … WHERE redeemed_at IS NULL).
  - Calls Headscale or Tailscale API to mint an ephemeral, single-use,
    `tag:deploy-bootstrap`-tagged auth key with TTL = `AUTH_BROKER_TS_AUTHKEY_TTL`.
  - Creates a `deployment_jobs` row in state `bootstrapped`.
  - Fires audit event `auth_code.redeemed` with source IP, boot UUID,
    machine_id.
  - Optionally creates `one_shot_tokens` for downstream use (e.g.
    domain-join cred fetch from inside the installer).
- **Side effects on failure:** increment `attempts`, fire audit event
  `auth_code.redeem_failed`, evaluate rate limits.

### Rate limiting

Implemented in-memory with periodic Postgres flush, plus hard checks at
DB layer:

- **Per code:** 5 attempts total, then code is locked (no further attempts
  accepted, even with the right answer; operator must reissue).
- **Per source IP per hour:** 30 attempts, all codes considered.
- **Per source IP per minute:** 10 attempts (sliding window).

## 4. Headscale / Tailscale ACL

The ACL below is what the operator pastes into Headscale (or Tailscale
admin policy editor) to create the deploy-related tags and lock down what
each tag can talk to. Without these, the `tag:deploy-bootstrap` nodes
created by the auth-broker would have free run of the tailnet.

```json
{
  "tagOwners": {
    "tag:deploy-server":    ["group:deploy-admin"],
    "tag:deploy-bootstrap": ["group:deploy-admin"],
    "tag:deploy-target":    ["group:deploy-admin"],
    "tag:deploy-edge":      ["group:deploy-admin"]
  },

  "groups": {
    "group:deploy-admin": ["admin@example.com"]
  },

  "acls": [
    {
      "action": "accept",
      "src":    ["tag:deploy-bootstrap"],
      "dst":    ["tag:deploy-server:443"]
    },
    {
      "action": "accept",
      "src":    ["tag:deploy-target"],
      "dst":    ["tag:deploy-server:443", "tag:deploy-server:80"]
    },
    {
      "action": "accept",
      "src":    ["tag:deploy-edge"],
      "dst":    ["tag:deploy-server:443"]
    },
    {
      "action": "accept",
      "src":    ["group:deploy-admin"],
      "dst":    ["*:*"]
    }
  ],

  "ssh": [],

  "autoApprovers": {
    "routes": {
      "10.0.0.0/8":     ["tag:deploy-edge"],
      "192.168.0.0/16": ["tag:deploy-edge"],
      "172.16.0.0/12":  ["tag:deploy-edge"]
    }
  }
}
```

Why each rule exists:

- `tag:deploy-bootstrap → tag:deploy-server:443`: the stick can do exactly
  one thing — fetch iPXE chain over HTTPS. It cannot reach anything else
  on the tailnet, ever, even if compromised.
- `tag:deploy-target → tag:deploy-server:443,80`: post-iPXE the in-OS
  installer (autoinstall, WinPE) needs to phone home. Port 80 is included
  for iPXE chainload to a non-TLS internal blob endpoint when feasible
  (we recommend 443 only, but the path is allowed).
- `tag:deploy-edge → tag:deploy-server:443`: edge agents fetch their own
  config and forward chainload requests for downstream LAN clients.
- `autoApprovers.routes` for `tag:deploy-edge`: an edge agent can advertise
  RFC1918 subnet routes without manual approval — that's the whole point
  of the bulk-remote-site mode.
- `group:deploy-admin → *:*`: humans who run the platform can talk to
  everything from their workstation tailnet node. Adjust to your own
  reality.

What's intentionally **not** in the ACL:

- No rule from `tag:deploy-bootstrap` to anything other than the deploy
  server. A compromised stick cannot pivot.
- No rule between `tag:deploy-target` and other targets. No
  bootstrap-to-bootstrap. No target-to-edge.
- No SSH access via the tailnet to deploy nodes. SSH is admin-only and
  goes over your normal admin path.

## 5. `make-stick.sh` workflow

```sh
./bootstrap/make-stick.sh \
    --output         deploy-bootstrap.img \
    --tailnet        your-org.headscale.example.com \
    --deploy-url     https://deploy.your-org.example.com \
    --ca-cert        /path/to/deploy-ca.pem \
    --headscale-url  https://headscale.your-org.example.com  # optional, overrides tailnet
```

What it does:

1. Verifies the buildroot tree has been built (`bootstrap/output/images/`).
2. Embeds `--ca-cert`, `--deploy-url`, and tailnet info into
   `/etc/deploy/config.json` inside the image's rootfs.
3. Re-signs the ESP contents (GRUB + kernel) with the project MOK key
   pulled from `bootstrap/keys/MOK.priv` (the user generates this once;
   the public half is distributed to operators for one-time enrollment
   per machine).
4. Produces a flashable `.img` and an `.img.sha256`.
5. Logs a `bootstrap_sticks` row in the database via the API (so we know
   which CA cert is on which stick — important for later rotation).

The same physical stick is reusable across an arbitrary number of
deployments, by an arbitrary number of operators. The only per-deployment
thing is the typed code.

## 6. Threat model

| Adversary capability | What they get | Mitigation |
|---|---|---|
| Steals an unflashed stick image (`.img`) | Tailnet name, deploy URL, CA fingerprint. None of those are credentials. They cannot join the tailnet. | Embedded data is non-secret by design. |
| Steals a flashed USB stick | Same as above plus convenience. Still no credentials. | Same. |
| Intercepts a code in transit (e.g. SMS/email) | Can plug a stick into a machine they control and complete one deployment as if they were the legitimate operator. | Codes are single-use, short-TTL. The deploy is logged. Optional `binding_ip` constraint. Audit alarms on anomalous source IPs. Codes should be delivered out-of-band when possible (phone the onsite user). |
| Compromises the auth-broker | Can mint Tailscale auth keys. | mTLS to Headscale, key in Vault/Secrets Manager, not on disk. Audit-logged. **This is a critical surface — see `SECURITY.md`.** |
| Compromises a deployed-machine post-install | Full host compromise of that one machine. | Per-deploy one-shot tokens for domain-join etc. mean compromise of host N doesn't grant access to host N+1's secrets. |
| MitM between stick and deploy server | Cannot present a cert that chains to the embedded CA. Stick refuses to redeem. | CA pinned in iPXE *and* in deploy-bootstrap's HTTPS client. |
| Replays a successful redeem | Code already marked consumed. Replay returns 410. | Atomic UPDATE on redemption. |

## 7. CA rotation

Rotating the CA embedded in sticks is a real operational event. Process:

1. Issue new internal CA, deploy new server cert that chains to **both**
   old and new CA during the transition.
2. Build new sticks with new CA (`make-stick.sh --ca-cert new.pem`).
3. Old sticks still work as long as the deploy server's cert chain
   includes the old CA.
4. Revoke old CA after all sticks are confirmed re-flashed (the
   `bootstrap_sticks` table tracks who has what CA fingerprint, so this
   is an inventory audit).

## 8. Known limitations (v1)

- Wired-only. Wi-Fi requires `iwd` + firmware blobs; out of scope until
  `BOOTSTRAP_PROFILE=fat`.
- Secure Boot requires the user to enroll the project MOK once on each
  machine the first time the stick boots. Unavoidable without a
  Microsoft-signed shim of our own.
- The TUI assumes a sighted operator with a keyboard. No serial-console
  variant in v1 (sketched as future work — would replace dialog with a
  read-from-stdin prompt).
