# The registry proxy for dependency installation jobs.
# Build from the repository root:
#   docker build -f deploy/registry-proxy/Dockerfile -t cloudexec-registry-proxy:1 .
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/registry-proxy ./cmd/registry-proxy
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /registry-proxy ./cmd/registry-proxy

# Nothing but the binary: no shell, no package manager, no certificates (the
# proxy only relays encrypted bytes and never terminates TLS).
FROM scratch
COPY --from=build /registry-proxy /registry-proxy
USER 65534:65534
EXPOSE 3128
ENTRYPOINT ["/registry-proxy"]
