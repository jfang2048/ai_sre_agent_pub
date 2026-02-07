# Multi-stage Docker build for SRE Agent
# Stage 1: Build the Go binaries
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make

# Set working directory
WORKDIR /build

# Copy go mod files
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# Copy source code
COPY backend/ ./backend/

# Build the binaries
# Build Collector
RUN cd backend && CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X main.version=$(git describe --tags --always 2>/dev/null || echo 'dev')" \
    -o ../sre-collector ./cmd/collector

# Build Controller
RUN cd backend && CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X main.version=$(git describe --tags --always 2>/dev/null || echo 'dev')" \
    -o ../sre-controller ./cmd/controller

# Stage 2: Runtime image (Collector)
FROM alpine:3.19 AS collector

RUN apk add --no-cache ca-certificates tzdata wget
RUN addgroup -S sre-collector && adduser -S -G sre-collector -h /var/lib/sre-collector -s /sbin/nologin -D sre-collector
RUN mkdir -p /etc/sre-collector /var/lib/sre-collector /var/log/sre-collector

COPY --from=builder /build/sre-collector /usr/local/bin/sre-collector
COPY configs/collector.yaml /etc/sre-collector/config.yaml

RUN chown -R sre-collector:sre-collector /var/lib/sre-collector /var/log/sre-collector /etc/sre-collector

ENV SRE_COLLECTOR_CONFIG=/etc/sre-collector/config.yaml

EXPOSE 9090

USER sre-collector
ENTRYPOINT ["/usr/local/bin/sre-collector"]

# Stage 3: Runtime image (Controller)
FROM alpine:3.19 AS controller

RUN apk add --no-cache ca-certificates tzdata wget
RUN addgroup -S sre-controller && adduser -S -G sre-controller -h /var/lib/sre-controller -s /sbin/nologin -D sre-controller
RUN mkdir -p /etc/sre-controller /var/lib/sre-controller/web /var/lib/sre-controller/data /var/lib/sre-controller/configs

COPY --from=builder /build/sre-controller /usr/local/bin/sre-controller
COPY configs/controller.yaml /etc/sre-controller/config.yaml
COPY configs/agent_playbooks.yaml /var/lib/sre-controller/configs/agent_playbooks.yaml
# Copy static assets
COPY web /var/lib/sre-controller/web

RUN chown -R sre-controller:sre-controller /var/lib/sre-controller /etc/sre-controller

WORKDIR /var/lib/sre-controller

EXPOSE 8080
ENV SRE_CONTROLLER_CONFIG=/etc/sre-controller/config.yaml
ENV SRE_CONTROLLER_WEB_PATH=/var/lib/sre-controller/web
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O- http://localhost:8080/healthz || exit 1

USER sre-controller
ENTRYPOINT ["/usr/local/bin/sre-controller"]
