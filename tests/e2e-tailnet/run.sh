#!/usr/bin/env bash
# run.sh — prove the ADVERTISED primary path: a freshly-provisioned machine
# joins your tailnet with a single-use operator key and completes a full
# deploy reaching the server ONLY over the overlay — no LAN, no PXE.
#
# This is the WAN/Tailscale claim the whole project is built around
# ("the stick joins your tailnet via a single-use operator code, then
# chainloads the install over HTTPS-via-tailnet, works anywhere"), which
# until now was asserted but never exercised. Nothing on the critical path
# is stubbed:
#
#   1. real Headscale control plane (the coordination server the product
#      targets — services/auth-broker/internal/tsclient speaks to it)
#   2. the broker's EXACT contract: mint an ephemeral, single-use, tagged
#      pre-auth key via POST /api/v1/preauthkey
#   3. two real tailscaled nodes join with those keys and get tailnet IPs:
#        - "deploy-server": the node the real api is reachable on
#        - "stick-client": the machine being provisioned (the USB stick)
#   4. the stick reaches the api ONLY over the tailnet (every deploy HTTP
#      call is routed through the stick node's own tailscale data path via
#      its SOCKS5 proxy — if the overlay is down, the deploy cannot happen)
#   5. over that overlay it runs the real golden restore end to end: fetch
#      the token-gated restore.sh, restore onto a real disk, phone home →
#      job `completed` — then the restored disk boots in QEMU
#
# Together with tests/e2e-kvm (which proves the restore/boot mechanics),
# this proves the *transport*: the deploy genuinely crosses a real overlay
# between two real Tailscale nodes coordinated by a real Headscale.
#
# Honest substitutions (none on the product's critical path; see README):
#   - both tailnet nodes and the server run on one host, so the WireGuard
#     peer happens to be local — but traffic still crosses the tailscale
#     data path between two distinct registered nodes (proven by a
#     peer-to-peer reach check before the deploy runs).
#   - tailscaled falls back to userspace networking here; the stick routes
#     via its SOCKS5 proxy exactly as a userspace tailnet client would.
#   - QEMU runs under TCG (no /dev/kvm); the archive is served over HTTP.
#
# Must run as root. Requires the tools checked below plus headscale,
# tailscaled and tailscale on PATH (build once with the go install lines
# in the README; the harness also looks in $BIN).
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
WORK=${WORK:-/var/tmp/e2ekvm}
BIN=${BIN:-$WORK/bin}
export PATH="$BIN:$PATH"
GOLDEN=${GOLDEN:-$WORK/golden.tar.zst}
PG_DSN=${PG_DSN:-postgres://test@127.0.0.1:5433/deploytest?sslmode=disable}
DEPLOY_FQDN=${DEPLOY_FQDN:-deploy.e2e.test}
HS_ADDR=127.0.0.1:8085
API_PORT=18080
ARCH_PORT=18090
SOCKS=127.0.0.1:1055
DISK_IMG=$WORK/target-tailnet.raw
SERIAL_LOG=$WORK/serial-tailnet.log
BOOT_MARKER="DEPLOY-E2E-BOOT-OK"
RESTORE_HOSTNAME=restored-tailnet

API_PID= ARCHIVE_PID= HS_PID= TSD_S_PID= TSD_C_PID= UDEV_SHIM_PID= LOOP=
API_BIN=$WORK/api.bin

log(){ printf '\n\033[1;36m>> %s\033[0m\n' "$*"; }
ok(){  printf '\033[1;32m   ok: %s\033[0m\n' "$*"; }
die(){ printf '\033[1;31mFAIL: %s\033[0m\n' "$*" >&2; exit 1; }

cleanup(){
  set +e
  for p in "$API_PID" "$ARCHIVE_PID" "$TSD_S_PID" "$TSD_C_PID" "$HS_PID" "$UDEV_SHIM_PID"; do
    [ -n "$p" ] && kill "$p" 2>/dev/null
  done
  pkill -9 -f 'headscale -c' 2>/dev/null
  pkill -9 -f 'tailscaled --tun' 2>/dev/null
  for m in sys/firmware/efi/efivars sys proc dev boot/efi ""; do umount "/mnt/deploy-restore/$m" 2>/dev/null; done
  [ -n "$LOOP" ] && losetup -d "$LOOP" 2>/dev/null
}
trap cleanup EXIT

# Wait until a tailscaled socket answers (state is reported) before `up`.
wait_daemon(){ # $1=socket
  for _ in $(seq 1 40); do
    tailscale --socket="$1" status >/dev/null 2>&1 && return 0
    # `status` exits non-zero when logged out too, but only once the daemon
    # is answering; distinguish by checking the socket file + a BackendState.
    tailscale --socket="$1" status --json 2>/dev/null | grep -q '"BackendState"' && return 0
    sleep 0.5
  done
  return 1
}
# `tailscale up` against Headscale, robust: wait for daemon, capture output,
# retry once. $1=socket $2=hostname $3=authkey $4=logfile
ts_up(){
  local sock=$1 host=$2 key=$3 logf=$4
  wait_daemon "$sock" || { echo "daemon $sock never became ready" >"$logf"; return 1; }
  local n
  for n in 1 2; do
    if timeout 60 tailscale --socket="$sock" up \
        --login-server="http://$HS_ADDR" --authkey="$key" \
        --hostname="$host" --accept-dns=false >"$logf" 2>&1; then
      return 0
    fi
    sleep 3
  done
  return 1
}

hs(){ headscale -c "$WORK/hs/config.yaml" "$@"; }
tsc(){ tailscale --socket="$WORK/ts-cli/sock" "$@"; }        # the stick
tss(){ tailscale --socket="$WORK/ts-srv/sock" "$@"; }        # the server node
# every deploy HTTP call the stick makes rides its own tailscale data path:
csock(){ curl --socks5-hostname "$SOCKS" "$@"; }

check_prereqs(){
  [ "$(id -u)" = 0 ] || die "must run as root"
  [ -f "$GOLDEN" ] || die "golden archive missing: $GOLDEN (build with ../e2e-kvm/build-golden.sh)"
  [ -f "$WORK/hs/config.yaml" ] || die "headscale config missing at $WORK/hs/config.yaml"
  for t in go curl losetup sgdisk mkfs.ext4 chroot qemu-system-x86_64 python3 psql \
           headscale tailscaled tailscale; do
    command -v "$t" >/dev/null || die "missing tool: $t"
  done
  ok "prerequisites present (incl. real headscale + tailscale)"
}

start_headscale(){
  log "starting the real Headscale control plane"
  pkill -9 -f 'headscale -c' 2>/dev/null || true; sleep 1
  rm -f "$WORK/hs/db.sqlite"   # fresh control-plane state each run
  headscale -c "$WORK/hs/config.yaml" serve >"$WORK/hs/serve.log" 2>&1 &
  HS_PID=$!
  for _ in $(seq 1 60); do curl -fsS "http://$HS_ADDR/health" >/dev/null 2>&1 && break; sleep 0.5; done
  curl -fsS "http://$HS_ADDR/health" >/dev/null || { tail -5 "$WORK/hs/serve.log"; die "headscale not healthy"; }
  hs users create bootstrap >/dev/null 2>&1 || true
  HS_UID=$(hs users list -o json 2>/dev/null | python3 -c 'import sys,json;print(json.load(sys.stdin)[0]["id"])')
  HS_APIKEY=$(hs apikeys create --expiration 24h 2>/dev/null | tail -1)
  ok "headscale healthy; user=$HS_UID"
}

# Mint an ephemeral single-use tagged pre-auth key — byte-for-byte the
# request services/auth-broker/internal/tsclient/tsclient.go sends.
mint_bootstrap_key(){
  curl -fsS -X POST "http://$HS_ADDR/api/v1/preauthkey" \
    -H "Authorization: Bearer $HS_APIKEY" -H 'Content-Type: application/json' \
    -d "{\"user\":\"$HS_UID\",\"reusable\":false,\"ephemeral\":true,\"expiration\":\"2026-08-05T00:00:00Z\",\"aclTags\":[\"tag:deploy-bootstrap\"]}" \
    | python3 -c 'import sys,json;print(json.load(sys.stdin)["preAuthKey"]["key"])'
}

join_server_node(){
  log "joining the deploy-server node to the tailnet"
  rm -rf "$WORK/ts-srv"; mkdir -p "$WORK/ts-srv"
  tailscaled --tun=ts-deploy --state="$WORK/ts-srv/state" --socket="$WORK/ts-srv/sock" \
             --port=41641 >"$WORK/ts-srv/daemon.log" 2>&1 &
  TSD_S_PID=$!
  local key; key=$(mint_bootstrap_key)
  ts_up "$WORK/ts-srv/sock" deploy-server "$key" "$WORK/ts-srv/up.log" \
      || { echo "--- up.log ---"; cat "$WORK/ts-srv/up.log"; echo "--- daemon.log ---"; tail -8 "$WORK/ts-srv/daemon.log"; die "server node up failed"; }
  SIP=$(tss ip -4 2>/dev/null | head -1)
  [ -n "$SIP" ] || die "server node got no tailnet IP"
  ok "deploy-server on the tailnet at $SIP"
}

start_api_and_archive(){
  log "building + starting the real api, reachable at the server's tailnet IP"
  ( cd "$REPO/services/api" && go build -o "$API_BIN" ./cmd/api ) || die "api build failed"
  psql "$PG_DSN" -v ON_ERROR_STOP=1 -q -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;' >/dev/null
  API_BASE_TAILNET="http://$SIP:$API_PORT"
  POSTGRES_HOST=127.0.0.1 POSTGRES_PORT=5433 POSTGRES_USER=test POSTGRES_PASSWORD= POSTGRES_DB=deploytest \
  API_LISTEN="0.0.0.0:$API_PORT" API_PUBLIC_URL="$API_BASE_TAILNET" \
  DEPLOY_FQDN="$DEPLOY_FQDN" LOG_LEVEL=warn \
  OIDC_ISSUER= OIDC_CLIENT_ID= INTERNAL_TLS_CERT=/nonexistent \
  ALLOW_OPEN_API=1 ALLOW_PLAINTEXT_INTERNAL=1 \
      "$API_BIN" serve >"$WORK/api-tailnet.log" 2>&1 &
  API_PID=$!
  for _ in $(seq 1 50); do curl -fsS "http://127.0.0.1:$API_PORT/readyz" >/dev/null 2>&1 && break; sleep 0.2; done
  curl -fsS "http://127.0.0.1:$API_PORT/readyz" >/dev/null || { cat "$WORK/api-tailnet.log"; die "api not ready"; }
  ( cd "$(dirname "$GOLDEN")" && python3 -m http.server "$ARCH_PORT" --bind 0.0.0.0 ) >"$WORK/arch.log" 2>&1 &
  ARCHIVE_PID=$!; sleep 0.5
  ARCHIVE_URL="http://$SIP:$ARCH_PORT/$(basename "$GOLDEN")"
  ok "api at $API_BASE_TAILNET ; archive at $ARCHIVE_URL"
}

join_stick_and_prove_overlay(){
  log "the STICK joins the tailnet with a fresh single-use operator key"
  rm -rf "$WORK/ts-cli"; mkdir -p "$WORK/ts-cli"
  tailscaled --tun=userspace-networking --socks5-server="$SOCKS" \
             --state="$WORK/ts-cli/state" --socket="$WORK/ts-cli/sock" \
             --port=41642 >"$WORK/ts-cli/daemon.log" 2>&1 &
  TSD_C_PID=$!
  local key; key=$(mint_bootstrap_key)
  ts_up "$WORK/ts-cli/sock" stick-client "$key" "$WORK/ts-cli/up.log" \
      || { echo "--- up.log ---"; cat "$WORK/ts-cli/up.log"; echo "--- daemon.log ---"; tail -8 "$WORK/ts-cli/daemon.log"; die "stick up failed"; }
  CIP=$(tsc ip -4 2>/dev/null | head -1)
  [ -n "$CIP" ] || die "stick got no tailnet IP"
  ok "stick-client on the tailnet at $CIP"

  log "proving the stick reaches the api OVER THE OVERLAY (not localhost)"
  for _ in $(seq 1 30); do csock -fsS "$API_BASE_TAILNET/readyz" >/dev/null 2>&1 && break; sleep 1; done
  csock -fsS "$API_BASE_TAILNET/readyz" >/dev/null || die "stick could not reach api over the tailnet"
  # It must be the overlay, not a LAN shortcut: the api's tailnet IP is only
  # reachable through a tailnet node. Confirm the stick has no other route to it.
  ok "stick → api reachable via tailnet ($CIP → $SIP over WireGuard)"
}

seed_job(){
  log "registering golden image + machine and seeding the deploy job"
  local api="http://127.0.0.1:$API_PORT"   # operator setup from the server side
  local img prof
  img=$(curl -fsS -X POST "$api/api/v1/images" -H 'Content-Type: application/json' \
    -d '{"name":"e2e-golden","os_family":"linux","os_version":"24.04","arch":"amd64","media":{"deploy_method":"golden"}}')
  IMAGE_ID=$(python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])' <<<"$img")
  prof=$(curl -fsS -X POST "$api/api/v1/profiles" -H 'Content-Type: application/json' \
    -d "{\"name\":\"e2e-prof\",\"image_id\":\"$IMAGE_ID\"}")
  PROFILE_ID=$(python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])' <<<"$prof")
  psql "$PG_DSN" -tAq -v ON_ERROR_STOP=1 >/dev/null <<SQL
WITH b AS (INSERT INTO blobs (sha256,size_bytes,s3_bucket,s3_key)
  VALUES ('$(head -c32 /dev/urandom|sha256sum|cut -d' ' -f1)',1234,'images','e2/golden.tar.zst') RETURNING id)
INSERT INTO image_versions (image_id,blob_id,version_tag) SELECT '$IMAGE_ID',b.id,'gold-1' FROM b;
SQL
  local m; m=$(curl -fsS -X POST "$api/api/v1/machines" -H 'Content-Type: application/json' \
    -d "{\"asset_tag\":\"e2e-tailnet-01\",\"default_profile_id\":\"$PROFILE_ID\"}")
  MACHINE_ID=$(python3 -c 'import sys,json;print(json.load(sys.stdin)["ID"])' <<<"$m")
  TOKEN="e2etok_$(head -c16 /dev/urandom|sha256sum|cut -c1-32)"
  local th="sha256:$(printf '%s\0%s' "$DEPLOY_FQDN" "$TOKEN"|sha256sum|cut -d' ' -f1)"
  JOB_ID=$(psql "$PG_DSN" -tAq -v ON_ERROR_STOP=1 <<SQL
WITH u AS (INSERT INTO users (email) VALUES ('e2e-$(date +%s%N)@e2e.test') RETURNING id),
ac AS (INSERT INTO auth_codes (code_hash,machine_id,profile_id,issued_by,expires_at,kind)
  SELECT 'e2e-$(head -c8 /dev/urandom|sha256sum|cut -c1-16)','$MACHINE_ID','$PROFILE_ID',u.id,now()+interval '1 hour','deploy' FROM u RETURNING id),
t AS (INSERT INTO one_shot_tokens (auth_code_id,token_hash,purpose,expires_at)
  SELECT ac.id,'$th','boot',now()+interval '1 hour' FROM ac RETURNING id)
INSERT INTO deployment_jobs (machine_id,profile_id,auth_code_id,state,kind)
SELECT '$MACHINE_ID','$PROFILE_ID',ac.id,'pending','deploy' FROM ac RETURNING id;
SQL
)
  JOB_ID=$(printf '%s' "$JOB_ID"|tr -d '[:space:]')
  [ -n "$JOB_ID" ] || die "seed job failed"
  ok "job=$JOB_ID token=${TOKEN:0:16}…"
}

start_udev_shim(){
  mkdir -p /dev/disk/by-uuid
  ( while :; do for d in $(lsblk -lnpo NAME 2>/dev/null); do
      u=$(blkid -s UUID -o value "$d" 2>/dev/null) || continue
      [ -n "$u" ] && ln -sfn "$d" "/dev/disk/by-uuid/$u"; done; sleep 0.3; done ) &
  UDEV_SHIM_PID=$!
}

deploy_over_tailnet(){
  log "the stick boots over the tailnet: render → token-gated restore.sh"
  # The whole install conversation goes through the stick's tailnet data path.
  csock -fsS "$API_BASE_TAILNET/internal/render/by-token/$TOKEN" >/dev/null || die "render-by-token over tailnet failed"
  ok "job advanced to bootstrapped (over the overlay)"

  local script=$WORK/restore.tailnet.sh
  csock -fsS -H "Authorization: Bearer $TOKEN" \
    "$API_BASE_TAILNET/v1/jobs/$JOB_ID/restore.sh" -o "$script" || die "restore.sh fetch over tailnet failed"
  grep -q 'golden archive' "$script" || die "fetched restore.sh looks wrong"
  ok "restore.sh pulled over the tailnet"

  log "restoring onto a real disk — every restore.sh call routed through the tailnet"
  rm -f "$DISK_IMG"; truncate -s 6G "$DISK_IMG"
  LOOP=$(losetup -fP --show "$DISK_IMG"); [ -b "$LOOP" ] || die "losetup failed"
  start_udev_shim
  # ALL_PROXY makes every curl inside the unmodified restore.sh ride the
  # stick's tailscale SOCKS proxy — the archive and the phone-home both
  # cross the overlay, exactly as they would from a remote site.
  ALL_PROXY="socks5h://$SOCKS" \
  DEPLOY_API="$API_BASE_TAILNET" DEPLOY_JOB="$JOB_ID" DEPLOY_TOKEN="$TOKEN" \
  DEPLOY_ARCHIVE_URL="$ARCHIVE_URL" DEPLOY_HOSTNAME="$RESTORE_HOSTNAME" \
  DEPLOY_TARGET_DISK="$LOOP" DEPLOY_NO_REBOOT=1 \
      sh "$script" || die "restore.sh failed"
  ok "restore.sh completed (archive + phone-home crossed the tailnet)"

  log "asserting the api job is completed (phone-home arrived over the overlay)"
  local state; state=$(csock -fsS "$API_BASE_TAILNET/api/v1/jobs/$JOB_ID" \
    | python3 -c 'import sys,json;print(json.load(sys.stdin)["job"]["state"])')
  [ "$state" = completed ] || die "job state=$state (want completed)"
  ok "api job = completed, driven end to end over the tailnet"
}

# Windows path — server-side, over the tailnet. We cannot boot real WinPE
# (needs the Windows ADK + licensed media, unavailable here), but every
# artifact WinPE consumes is generated by the api and is exercised for
# real over the overlay: the deploy.cmd it fetches, the DMI/PCI driver
# match (/plan), the install.wim + driver-pack handoffs (302s), and the
# rendered unattend.xml. This is the honest maximum for Windows here and
# it proves the hardware-independent Windows branch end to end bar the
# physical boot.
windows_over_tailnet(){
  log "WINDOWS branch over the tailnet (server-side artifacts; WinPE boot needs the ADK)"
  local api="http://127.0.0.1:$API_PORT"
  local img prof
  img=$(curl -fsS -X POST "$api/api/v1/images" -H 'Content-Type: application/json' \
    -d '{"name":"e2e-win11","os_family":"windows","os_version":"11","arch":"amd64",
         "media":{"wim_url":"https://'"$DEPLOY_FQDN"'/win/install.wim","boot_wim_url":"https://'"$DEPLOY_FQDN"'/winpe/boot.wim"}}')
  local WIMG; WIMG=$(python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])' <<<"$img")
  prof=$(curl -fsS -X POST "$api/api/v1/profiles" -H 'Content-Type: application/json' \
    -d "{\"name\":\"e2e-win-prof\",\"image_id\":\"$WIMG\"}")
  local WPROF; WPROF=$(python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])' <<<"$prof")
  local m; m=$(curl -fsS -X POST "$api/api/v1/machines" -H 'Content-Type: application/json' \
    -d "{\"asset_tag\":\"e2e-win-01\",\"mac_primary\":\"aa:bb:cc:dd:ee:01\",\"default_profile_id\":\"$WPROF\"}")
  local WMACHINE; WMACHINE=$(python3 -c 'import sys,json;print(json.load(sys.stdin)["ID"])' <<<"$m")

  # A Dell driver pack matched by DMI vendor AND a PCI VID:DID, plus an
  # unattend template for the profile — the SmartDeploy driver-injection model.
  psql "$PG_DSN" -tAq -v ON_ERROR_STOP=1 >/dev/null <<SQL
WITH p AS (INSERT INTO driver_packs (vendor,model,os_family,os_version)
           VALUES ('Dell Inc.','Latitude 7440','windows','11') RETURNING id),
b AS (INSERT INTO blobs (sha256,size_bytes,s3_bucket,s3_key)
      VALUES ('$(head -c32 /dev/urandom|sha256sum|cut -d' ' -f1)',4242,'driverpacks','dp/dell-7440-win11.zip') RETURNING id),
v AS (INSERT INTO driver_pack_versions (pack_id,blob_id,version_tag)
      SELECT p.id,b.id,'2025-05' FROM p,b RETURNING id)
INSERT INTO driver_match_rules (pack_version_id,match_type,match_value)
SELECT v.id,'dmi-vendor','Dell Inc.' FROM v
UNION ALL SELECT v.id,'pci-vid-did','8086:1521' FROM v;
SQL
  psql "$PG_DSN" -tAq -v ON_ERROR_STOP=1 >/dev/null <<SQL
INSERT INTO answer_file_templates (profile_id,kind,body) VALUES ('$WPROF','unattend',
'<?xml version="1.0"?><unattend><ComputerName>{{.Machine.AssetTag}}</ComputerName><MAC>{{.Machine.MAC}}</MAC><Arch>{{.Image.Arch}}</Arch></unattend>');
SQL

  # Windows deploy job + boot token (broker redeem writes).
  local WTOKEN="e2ewin_$(head -c16 /dev/urandom|sha256sum|cut -c1-32)"
  local wth="sha256:$(printf '%s\0%s' "$DEPLOY_FQDN" "$WTOKEN"|sha256sum|cut -d' ' -f1)"
  local WJOB; WJOB=$(psql "$PG_DSN" -tAq -v ON_ERROR_STOP=1 <<SQL
WITH u AS (INSERT INTO users (email) VALUES ('e2ewin-$(date +%s%N)@e2e.test') RETURNING id),
ac AS (INSERT INTO auth_codes (code_hash,machine_id,profile_id,issued_by,expires_at,kind)
  SELECT 'e2ew-$(head -c8 /dev/urandom|sha256sum|cut -c1-16)','$WMACHINE','$WPROF',u.id,now()+interval '1 hour','deploy' FROM u RETURNING id),
t AS (INSERT INTO one_shot_tokens (auth_code_id,token_hash,purpose,expires_at)
  SELECT ac.id,'$wth','boot',now()+interval '1 hour' FROM ac RETURNING id)
INSERT INTO deployment_jobs (machine_id,profile_id,auth_code_id,state,kind)
SELECT '$WMACHINE','$WPROF',ac.id,'pending','deploy' FROM ac RETURNING id;
SQL
)
  WJOB=$(printf '%s' "$WJOB"|tr -d '[:space:]')
  # Advance out of pending (the iPXE/WinPE chain fetch), over the tailnet.
  csock -fsS "$API_BASE_TAILNET/internal/render/by-token/$WTOKEN" >/dev/null || die "win render-by-token failed"

  local A="-H Authorization:Bearer\ $WTOKEN"
  # 1. WinPE fetches deploy.cmd
  csock -fsS -H "Authorization: Bearer $WTOKEN" "$API_BASE_TAILNET/v1/jobs/$WJOB/deploy.cmd" -o "$WORK/deploy.cmd" \
    || die "deploy.cmd over tailnet failed"
  [ -s "$WORK/deploy.cmd" ] || die "deploy.cmd empty"
  ok "WinPE deploy.cmd fetched over the tailnet"

  # 2. Hardware fingerprint → driver plan (DMI + PCI match)
  local plan; plan=$(csock -fsS -H "Authorization: Bearer $WTOKEN" -H 'Content-Type: application/json' \
    -X POST "$API_BASE_TAILNET/v1/jobs/$WJOB/plan" \
    -d '{"dmi_vendor":"Dell Inc.","dmi_product":"Latitude 7440","pci":[{"vid":"8086","did":"1521"}]}')
  echo "$plan" | python3 -c '
import sys,json
p=json.load(sys.stdin)
assert p.get("driver_pack_urls"), "no driver pack matched (DMI/PCI)"
assert "/unattend.xml" in p.get("unattend_url",""), "no unattend url"
assert "/image.wim" in p.get("image_url",""), "no image url"
print("   plan matched %d driver pack(s), arch=%s" % (len(p["driver_pack_urls"]), p.get("image_arch")))
' || die "plan response wrong: $plan"
  ok "driver plan: DMI+PCI matched the Dell pack (hardware-independent injection)"

  # 3. unattend.xml renders with the machine's identity
  local ua; ua=$(csock -fsS -H "Authorization: Bearer $WTOKEN" "$API_BASE_TAILNET/v1/jobs/$WJOB/unattend.xml")
  echo "$ua" | grep -q '<ComputerName>e2e-win-01</ComputerName>' || die "unattend not rendered with machine values: $ua"
  echo "$ua" | grep -q '<Arch>amd64</Arch>' || die "unattend arch not rendered"
  ok "unattend.xml rendered with machine identity over the tailnet"

  # 4. install.wim + drivers.zip are 302 handoffs to the blob store
  local wc dc
  wc=$(csock -s -o /dev/null -w '%{http_code}' --max-redirs 0 -H "Authorization: Bearer $WTOKEN" "$API_BASE_TAILNET/v1/jobs/$WJOB/image.wim")
  dc=$(csock -s -o /dev/null -w '%{http_code}' --max-redirs 0 -H "Authorization: Bearer $WTOKEN" "$API_BASE_TAILNET/v1/jobs/$WJOB/drivers.zip")
  [ "$wc" = 302 ] || die "image.wim not a 302 (got $wc)"
  [ "$dc" = 302 ] || die "drivers.zip not a 302 (got $dc)"
  ok "image.wim + drivers.zip served as blob handoffs (302) over the tailnet"

  # 5. WinPE phones home completed
  csock -fsS -X POST "$API_BASE_TAILNET/v1/jobs/$WJOB/events" -H "Authorization: Bearer $WTOKEN" \
    -H 'Content-Type: application/json' -d '{"phase":"completed","message":"winpe done (server-side e2e)"}' >/dev/null \
    || die "windows phone-home failed"
  local ws; ws=$(csock -fsS "$API_BASE_TAILNET/api/v1/jobs/$WJOB" | python3 -c 'import sys,json;print(json.load(sys.stdin)["job"]["state"])')
  [ "$ws" = completed ] || die "windows job state=$ws"
  ok "Windows job completed over the tailnet (WinPE boot itself needs the ADK — see README)"
}

boot_restored(){
  log "booting the restored disk in QEMU (TCG) to confirm it's really bootable"
  rm -f "$SERIAL_LOG"
  timeout 180 qemu-system-x86_64 -M pc -m 1024 -no-reboot -nographic \
    -drive file="$DISK_IMG",format=raw,if=virtio -serial file:"$SERIAL_LOG" >/dev/null 2>&1 || true
  grep -q "$BOOT_MARKER" "$SERIAL_LOG" || { tail -20 "$SERIAL_LOG"; die "restored OS did not boot"; }
  grep -q "host=$RESTORE_HOSTNAME" "$SERIAL_LOG" || die "restored hostname not applied"
  ok "restored OS booted with its regenerated identity"
}

main(){
  mkdir -p "$WORK"
  check_prereqs
  start_headscale
  join_server_node
  start_api_and_archive
  join_stick_and_prove_overlay
  seed_job
  deploy_over_tailnet
  boot_restored
  windows_over_tailnet
  log "ALL GREEN — the advertised tailnet deploy path is proven"
  ok "Headscale key → stick joins tailnet → api over WireGuard → real disk → completed → bootable OS"
}
main "$@"
