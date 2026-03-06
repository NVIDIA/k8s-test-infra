#!/bin/bash
# Spike: run real nvidia-smi against mock NVML in a container
# Usage: cd <repo-root> && ./tests/e2e/spike-nvidia-smi.sh
#
# The gpu-mock Dockerfile already includes:
# 1. Mock NVML library (compiled from source)
# 2. Real nvidia-smi binary (from nvidia-utils package)
# This script just builds it and runs nvidia-smi with debug enabled.
#
# NOTE: nvidia-smi is x86_64 only. On ARM hosts (Apple Silicon),
# Docker uses QEMU emulation via --platform linux/amd64.
# The Go cross-compilation + package install takes ~10-15 minutes.
set -euo pipefail

GOLANG_VERSION=${GOLANG_VERSION:-1.25}
PLATFORM="linux/amd64"
IMAGE="gpu-mock:spike"

echo "=== Building gpu-mock image (platform: $PLATFORM) ==="
echo "    This may take 10-15 minutes on ARM hosts (QEMU emulation)."
echo ""

docker build --platform "$PLATFORM" -t "$IMAGE" \
  --build-arg GOLANG_VERSION="$GOLANG_VERSION" \
  -f deployments/gpu-mock/Dockerfile .

# Common env + run args
RUN_ARGS=(
  --rm --platform "$PLATFORM"
  -e MOCK_NVML_CONFIG=/config/config.yaml
  -e MOCK_NVML_DEBUG=1
  -e MOCK_NVML_NUM_DEVICES=2
)

# We need a config file inside the container. The Dockerfile doesn't COPY one
# by default (setup.sh handles it at runtime), so mount it.
CONFIG="deployments/gpu-mock/helm/gpu-mock/profiles/a100.yaml"
RUN_ARGS+=(-v "$(pwd)/$CONFIG:/config/config.yaml:ro")

echo ""
echo "=== Running nvidia-smi against mock NVML ==="
echo ""
echo "--- nvidia-smi (default) ---"
docker run "${RUN_ARGS[@]}" "$IMAGE" nvidia-smi 2>&1 || true
echo ""
echo "--- nvidia-smi -q (query) ---"
docker run "${RUN_ARGS[@]}" "$IMAGE" nvidia-smi -q 2>&1 | head -100 || true
echo ""
echo "--- nvidia-smi -L (list GPUs) ---"
docker run "${RUN_ARGS[@]}" "$IMAGE" nvidia-smi -L 2>&1 || true
echo ""
echo "--- Full debug stderr ---"
docker run "${RUN_ARGS[@]}" "$IMAGE" \
  bash -c 'nvidia-smi 2>/tmp/debug 1>/tmp/out; echo "=== stdout ==="; cat /tmp/out; echo "=== stderr (stub calls) ==="; cat /tmp/debug' || true

echo ""
echo "=== Cleanup ==="
docker rmi "$IMAGE" 2>/dev/null || true
