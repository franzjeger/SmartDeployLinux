FROM golang:1.23.4-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w -buildid= -X main.version=${VERSION}" \
    -o /out/deployctl ./cmd/deployctl

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/deployctl /usr/local/bin/deployctl
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/deployctl"]
