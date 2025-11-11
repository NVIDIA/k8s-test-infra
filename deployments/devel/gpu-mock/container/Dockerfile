# Multi-stage Dockerfile for gpu-mockctl
#
# This Dockerfile builds the gpu-mockctl binary and mock NVML library
# in an optimized multi-stage build process.

# Build stage for mock NVML library
FROM golang:1.25 AS nvml-builder

# Install build dependencies for NVML
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    make \
    libc6-dev \
    && rm -rf /var/lib/apt/lists/*

# Build mock NVML library (Go-based implementation)
WORKDIR /src
COPY pkg/gpu/mocknvml/ ./pkg/gpu/mocknvml/
RUN cd pkg/gpu/mocknvml && \
    make clean-go && make build-go && \
    mkdir -p /tmp/mocknvml/lib64 && \
    cp -a build-go/lib64/* /tmp/mocknvml/lib64/

# Build stage for gpu-mockctl
FROM golang:1.25 AS builder

WORKDIR /src

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build gpu-mockctl binary
RUN CGO_ENABLED=1 \
    go build -a -ldflags '-linkmode external -extldflags "-static"' \
    -o /out/gpu-mockctl ./cmd/gpu-mockctl

# Runtime stage - minimal image
FROM debian:bookworm-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    jq \
    && rm -rf /var/lib/apt/lists/*

# Copy gpu-mockctl binary
COPY --from=builder /out/gpu-mockctl /usr/local/bin/gpu-mockctl

# Copy mock NVML library
COPY --from=nvml-builder /tmp/mocknvml/lib64 /usr/local/lib/mocknvml/

# Copy entrypoint script
COPY deployments/devel/gpu-mock/container/entrypoint.sh /usr/local/bin/entrypoint.sh

# Ensure binaries and scripts are executable
RUN chmod +x /usr/local/bin/gpu-mockctl && \
    chmod +x /usr/local/bin/entrypoint.sh

# Default command
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]