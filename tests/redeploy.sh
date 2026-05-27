#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Rebuild nvml-mock, load into kind, and roll out the DaemonSet.
#
# Image tag defaults to the current date (YYYYMMDD-HHMM) so each run is distinct.
#
# Usage (from repo root or any directory):
#   ./tests/redeploy.sh
#   ./tests/redeploy.sh --tag 20260526-dev   # fixed tag
#   ./tests/redeploy.sh --skip-build         # only load/helm/restart (image must exist locally)
#
# Docker builds always use --no-cache so C shim / mock-ib changes are not masked by layer cache.
#
# Environment overrides:
#   KIND_CLUSTER   kind cluster name (default: nvml-mock-demo)
#   IMAGE_REPO     image repository (default: ghcr.io/nvidia/nvml-mock)
#   HELM_RELEASE   helm release name (default: nvml-mock)
#   HELM_NAMESPACE namespace (default: default)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

KIND_CLUSTER="${KIND_CLUSTER:-nvml-mock-demo}"
IMAGE_REPO="${IMAGE_REPO:-ghcr.io/nvidia/nvml-mock}"
HELM_RELEASE="${HELM_RELEASE:-nvml-mock}"
HELM_NAMESPACE="${HELM_NAMESPACE:-default}"

SKIP_BUILD=0
IMAGE_TAG="${IMAGE_TAG:-$(date +%Y%m%d-%H%M)}"

usage() {
  sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage 0
      ;;
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    --tag)
      [[ $# -ge 2 ]] || { echo "error: --tag requires a value" >&2; exit 1; }
      IMAGE_TAG="$2"
      shift 2
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage 1
      ;;
  esac
done

IMAGE="${IMAGE_REPO}:${IMAGE_TAG}"

echo "=== nvml-mock redeploy ==="
echo "  repo:         ${REPO_ROOT}"
echo "  image:        ${IMAGE}"
echo "  kind cluster: ${KIND_CLUSTER}"
echo "  helm:         ${HELM_RELEASE} (${HELM_NAMESPACE})"
echo

cd "${REPO_ROOT}"

if [[ "${SKIP_BUILD}" -eq 0 ]]; then
  echo ">>> docker build --no-cache -f deployments/nvml-mock/Dockerfile -t ${IMAGE} ."
  docker build --no-cache -f deployments/nvml-mock/Dockerfile -t "${IMAGE}" .
else
  echo ">>> skipping docker build (--skip-build)"
  if ! docker image inspect "${IMAGE}" >/dev/null 2>&1; then
    echo "error: local image ${IMAGE} not found; run without --skip-build" >&2
    exit 1
  fi
fi

echo ">>> kind load docker-image ${IMAGE} --name ${KIND_CLUSTER}"
kind load docker-image "${IMAGE}" --name "${KIND_CLUSTER}"

echo ">>> helm upgrade ${HELM_RELEASE}"
helm upgrade "${HELM_RELEASE}" deployments/nvml-mock/helm/nvml-mock \
  -n "${HELM_NAMESPACE}" \
  --reuse-values \
  --set "image.tag=${IMAGE_TAG}" \
  --set "updateStrategy.rollingUpdate.maxUnavailable=100%"

echo ">>> kubectl rollout restart daemonset/${HELM_RELEASE} -n ${HELM_NAMESPACE}"
kubectl rollout restart "daemonset/${HELM_RELEASE}" -n "${HELM_NAMESPACE}"

echo ">>> kubectl rollout status"
kubectl rollout status "daemonset/${HELM_RELEASE}" -n "${HELM_NAMESPACE}" --timeout=180s

echo
echo "=== done: ${IMAGE} deployed ==="
