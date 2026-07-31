#!/bin/sh
# Substitute env vars into the dnsmasq config and exec dnsmasq in the
# foreground.

set -eu

: "${LISTEN_INTERFACE:?must be set, e.g. eth0}"
: "${SUBNET:?must be set, e.g. 192.168.1.0}"
: "${TFTP_SERVER_IP:?must be set; the IP of the host running this container on the LAN}"
: "${DEPLOY_FQDN:?must be set; e.g. deploy.example.com}"

CONF=/etc/dnsmasq.conf
sed -i \
    -e "s|__INTERFACE__|${LISTEN_INTERFACE}|g" \
    -e "s|__SUBNET__|${SUBNET}|g" \
    -e "s|__TFTP_SERVER_IP__|${TFTP_SERVER_IP}|g" \
    -e "s|__DEPLOY_FQDN__|${DEPLOY_FQDN}|g" \
    "$CONF"

echo "==> dnsmasq config:"
grep -v '^#' "$CONF" | grep -v '^$' | sed 's/^/    /'

# Generate the second-stage script served to stock (non-embedded) iPXE
# binaries via dhcp-boot tag:ipxe,default.ipxe. Quoted heredoc so iPXE
# macros like ${net0/mac} survive; only DEPLOY_FQDN is substituted.
if [ -w /tftproot ] || [ ! -e /tftproot/default.ipxe ]; then
    cat > /tftproot/default.ipxe <<EOF || echo "WARN: cannot write /tftproot/default.ipxe (read-only mount?)"
#!ipxe
chain https://${DEPLOY_FQDN}/boot/menu/by-mac/\${net0/mac}.ipxe || chain https://${DEPLOY_FQDN}/boot/by-mac/\${net0/mac}.ipxe || shell
EOF
fi

# tftproot is read-only; verify the binaries we promise to serve exist.
for f in undionly.kpxe ipxe.efi; do
    if [ ! -r "/tftproot/$f" ]; then
        echo "WARN: /tftproot/$f missing; clients requesting that arch will fail"
    fi
done

exec dnsmasq --no-daemon --conf-file="$CONF"
