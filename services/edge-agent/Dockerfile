# Edge agent: a small box deployed to a remote LAN that joins the tailnet
# as a subnet router and runs local proxyDHCP + TFTP, so a roomful of
# machines can PXE-boot without each needing a USB stick.
#
# Image is multi-arch (amd64 + arm64) because most users run this on a
# Raspberry Pi or mini-PC.
#
# Components in the image:
#   - tailscaled (static, pinned)
#   - dnsmasq    (proxyDHCP + TFTP)
#   - edge-agent (this Go binary; orchestrates the two and registers
#                 with the deploy server)

FROM golang:1.23.4-alpine AS gobuild
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build \
    -trimpath -ldflags="-s -w" \
    -o /out/edge-agent ./cmd/edge-agent

FROM alpine:3.20
ARG TARGETARCH
ARG TAILSCALE_VERSION=1.86.0

RUN apk add --no-cache dnsmasq=2.90-r3 ca-certificates curl iptables iproute2 \
    && addgroup -S tsd \
    && adduser -S -G tsd tsd

# Pull the matching tailscale tarball.
RUN ARCH=${TARGETARCH:-amd64} \
    && curl -fsSL "https://pkgs.tailscale.com/stable/tailscale_${TAILSCALE_VERSION}_${ARCH}.tgz" \
    | tar -xzC /tmp \
    && cp /tmp/tailscale_${TAILSCALE_VERSION}_${ARCH}/tailscale /usr/local/bin/ \
    && cp /tmp/tailscale_${TAILSCALE_VERSION}_${ARCH}/tailscaled /usr/local/sbin/ \
    && rm -rf /tmp/tailscale_${TAILSCALE_VERSION}_${ARCH}

COPY --from=gobuild /out/edge-agent /usr/local/bin/edge-agent
COPY entrypoint.sh /usr/local/bin/entrypoint
RUN chmod +x /usr/local/bin/entrypoint

EXPOSE 41641/udp 67/udp 69/udp 4011/udp

ENTRYPOINT ["/usr/local/bin/entrypoint"]
