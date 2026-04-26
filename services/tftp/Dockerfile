# dnsmasq running as proxyDHCP + TFTP for the LAN PXE secondary path.
# Coexists with an existing DHCP server on the same L2 — we never assign
# IPs, only hand out the boot filename per architecture.
#
# Container expects:
#   - /tftproot mounted with the iPXE binaries.
#   - host networking (--network host) so DHCP broadcasts reach it.

FROM alpine:3.20

# Don't pin dnsmasq's apk version — Alpine rotates patch versions out of
# the live repos and a pinned build randomly fails to build months later.
# We rely on Alpine's own stability in the 3.x branch.
# The dnsmasq Alpine package already creates the dnsmasq user/group at
# install time; don't duplicate that.
RUN apk add --no-cache dnsmasq

COPY dnsmasq.conf /etc/dnsmasq.conf
COPY entrypoint.sh /usr/local/bin/entrypoint
RUN chmod +x /usr/local/bin/entrypoint

EXPOSE 67/udp 69/udp 4011/udp

ENTRYPOINT ["/usr/local/bin/entrypoint"]
