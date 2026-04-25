#!/usr/bin/env bash
# Generate the project Machine Owner Key (MOK).
#
# Run ONCE per organization. The resulting MOK.priv signs the GRUB and
# kernel artifacts that ship on the bootstrap stick. The MOK.crt is
# enrolled by the user the first time the stick boots on each machine
# (shim's MokManager UI prompts for it).
#
# After running this:
#   - Keep MOK.priv offline. Anyone with this can sign anything that
#     boots under your shim chain.
#   - Distribute MOK.cer (DER form) to operators so they can answer the
#     "Enroll MOK" prompt.

set -euo pipefail

cd "$(dirname "$0")"

if [ -f MOK.priv ] || [ -f MOK.crt ]; then
    echo "MOK.priv or MOK.crt already exists in $(pwd)."
    echo "Refusing to overwrite. Move the existing files aside if you really want to regenerate."
    exit 1
fi

ORG=${ORG:-deployserver}
CN=${CN:-deployserver Bootstrap MOK}
DAYS=${DAYS:-3650}

echo "==> generating MOK keypair for org=$ORG cn='$CN' days=$DAYS"

openssl req -new -x509 -newkey rsa:2048 \
    -keyout MOK.priv \
    -outform PEM -out MOK.crt \
    -days "$DAYS" \
    -nodes \
    -subj "/CN=${CN}/O=${ORG}/" \
    -addext "basicConstraints=critical,CA:FALSE" \
    -addext "keyUsage=digitalSignature" \
    -addext "extendedKeyUsage=codeSigning"

# DER form is what shim's MokManager wants on disk for enrollment.
openssl x509 -in MOK.crt -outform DER -out MOK.cer

chmod 0600 MOK.priv

cat <<EOF

==> done. Files written:
    MOK.priv   (KEEP OFFLINE; chmod 600)
    MOK.crt    (PEM form, used by sbsign)
    MOK.cer    (DER form, distribute to operators for enrollment)

Fingerprint:
$(openssl x509 -in MOK.crt -noout -fingerprint -sha256 | sed 's/^/    /')

Next steps:
  1. Run bootstrap/external/fetch.sh to download shim and grub.
     fetch.sh will sign grub with MOK.priv automatically.
  2. Build the bootstrap image: make -C bootstrap.
  3. On first boot of any machine using the stick, the firmware will
     show shim's MokManager prompt. Enroll MOK.cer when asked.
EOF
