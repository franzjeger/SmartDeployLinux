# http-boot is the static media server + per-machine iPXE script renderer.
# Two responsibilities, deliberately collocated for byte-range performance:
#
#  1. Serve large binary blobs (kernels, initrds, WIMs, qcow2s) over HTTPS
#     with byte-range support. nginx is the right tool here.
#
#  2. Render per-machine iPXE scripts on demand. A small Go binary handles
#     this; nginx proxies to it.

FROM golang:1.23.4-alpine AS gobuild
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY scripts/ ./scripts/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out/render ./scripts/render

FROM nginx:1.27-alpine
RUN apk add --no-cache curl ca-certificates tini
COPY conf/nginx.conf /etc/nginx/nginx.conf
COPY --from=gobuild /out/render /usr/local/bin/render
COPY conf/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
EXPOSE 8443
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/entrypoint.sh"]
