FROM golang:1.23.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w -buildid=" \
    -o /out/worker ./cmd/worker

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/worker /usr/local/bin/worker
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/worker"]
