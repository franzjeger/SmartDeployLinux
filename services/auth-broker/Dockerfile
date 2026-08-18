# Multi-stage build. Final image is distroless static, non-root.

FROM golang:1.25.12-alpine AS build
ARG VERSION=dev
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -buildid= -X main.version=${VERSION}" \
    -o /out/auth-broker ./cmd/auth-broker

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/auth-broker /usr/local/bin/auth-broker
USER nonroot:nonroot
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/auth-broker"]
