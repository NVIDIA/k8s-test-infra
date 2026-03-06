#!/bin/bash
# Spike: run real nvidia-smi against mock NVML in a container
# Usage: ./tests/e2e/spike-nvidia-smi.sh
set -euo pipefail

GOLANG_VERSION=${GOLANG_VERSION:-1.25}
DRIVER_VERSION="550.163.01"

echo "=== Building gpu-mock image ==="
docker build -t gpu-mock:spike -f deployments/gpu-mock/Dockerfile \
  --build-arg GOLANG_VERSION="$GOLANG_VERSION" .

echo "=== Building spike container ==="
cat <<'DOCKERFILE' | docker build -t nvidia-smi-spike -f - .
FROM gpu-mock:spike AS mock

FROM ubuntu:22.04
# Install nvidia-smi from nvidia-utils package
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      gnupg2 curl ca-certificates && \
    curl -fsSL https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/x86_64/3bf863cc.pub | \
      gpg --dearmor -o /usr/share/keyrings/nvidia.gpg && \
    echo "deb [signed-by=/usr/share/keyrings/nvidia.gpg] https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/x86_64 /" \
      > /etc/apt/sources.list.d/nvidia.list && \
    apt-get update && \
    apt-get download nvidia-utils-550 && \
    dpkg --force-depends -i nvidia-utils-550*.deb && \
    rm -f nvidia-utils-550*.deb && \
    rm -rf /var/lib/apt/lists/*

# Copy mock libraries
COPY --from=mock /usr/local/lib/libnvidia-ml.so.* /usr/local/lib/
RUN ln -sf /usr/local/lib/libnvidia-ml.so.*.*.* /usr/local/lib/libnvidia-ml.so.1 && \
    ln -sf /usr/local/lib/libnvidia-ml.so.1 /usr/local/lib/libnvidia-ml.so && \
    echo "/usr/local/lib" > /etc/ld.so.conf.d/mock-nvml.conf && \
    ldconfig

# Copy config
COPY deployments/gpu-mock/helm/gpu-mock/profiles/a100.yaml /config/config.yaml

ENV MOCK_NVML_CONFIG=/config/config.yaml
ENV MOCK_NVML_DEBUG=1
ENV MOCK_NVML_NUM_DEVICES=2
DOCKERFILE

echo "=== Running nvidia-smi against mock NVML ==="
echo ""
echo "--- nvidia-smi (default) ---"
docker run --rm nvidia-smi-spike nvidia-smi 2>&1 || true
echo ""
echo "--- nvidia-smi -q (query) ---"
docker run --rm nvidia-smi-spike nvidia-smi -q 2>&1 | head -100 || true
echo ""
echo "--- nvidia-smi -L (list GPUs) ---"
docker run --rm nvidia-smi-spike nvidia-smi -L 2>&1 || true
echo ""
echo "--- Full debug stderr ---"
docker run --rm nvidia-smi-spike bash -c 'nvidia-smi 2>/tmp/debug 1>/tmp/out; echo "=== stdout ==="; cat /tmp/out; echo "=== stderr (stub calls) ==="; cat /tmp/debug' || true

echo ""
echo "=== Cleanup ==="
docker rmi nvidia-smi-spike 2>/dev/null || true
