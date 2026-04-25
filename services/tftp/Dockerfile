# dnsmasq running as proxyDHCP + TFTP for the LAN PXE secondary path.
# Coexists with an existing DHCP server on the same L2 — we never assign
# IPs, only hand out the boot filename per architecture.
#
# Container expects:
#   - /tftproot mounted with the iPXE binaries.
#   - host networking (--network host) so DHCP broadcasts reach it.

FROM alpine:3.20

RUN apk add --no-cache dnsmasq=2.90-r3 \
    && addgroup -S dnsmasq \
    && adduser -S -G dnsmasq dnsmasq

COPY dnsmasq.conf /etc/dnsmasq.conf
COPY entrypoint.sh /usr/local/bin/entrypoint
RUN chmod +x /usr/local/bin/entrypoint

EXPOSE 67/udp 69/udp 4011/udp

ENTRYPOINT ["/usr/local/bin/entrypoint"]
