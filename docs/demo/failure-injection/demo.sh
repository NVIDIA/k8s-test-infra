#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
#
# SPDX-License-Identifier: Apache-2.0
#
# End-to-end demo of nvml-mock failure injection. Spins up a dedicated
# Kind cluster, deploys the chart with ecc_uncorrectable + Xid 79
# enabled, and walks through the verification steps for each failure
# mode. Designed to be self-contained: nothing about this script
# touches the standard nvml-mock-demo cluster created by
# ../standalone/demo.sh.

set -euo pipefail

###############################################################################
# Configuration
###############################################################################
CLUSTER_NAME="nvml-mock-failure-demo"
IMAGE_NAME="nvml-mock:failure-demo"
RELEASE_NAME="nvml-mock"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
KIND_CONFIG="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/kind.yaml"

###############################################################################
# Helpers
###############################################################################
info() { echo "==> $*"; }
sub()  { echo "    $*"; }
fail() { echo "ERROR: $*" >&2; exit 1; }

# wait_for_pod_ready waits up to 90s for the first nvml-mock DaemonSet
# pod to be Ready, then echoes its name. Re-execs after every helm
# upgrade because rollout-restart drops the old pod.
wait_for_pod_ready() {
  kubectl rollout status daemonset/${RELEASE_NAME} --timeout=90s >/dev/null
  kubectl get pods -l app.kubernetes.io/name=${RELEASE_NAME} \
    -o jsonpath='{.items[0].metadata.name}'
}

###############################################################################
# Step 1 -- Create a dedicated Kind cluster
###############################################################################
info "Creating Kind cluster: ${CLUSTER_NAME}"
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  sub "Cluster already exists, reusing it"
else
  kind create cluster --name "${CLUSTER_NAME}" --config="${KIND_CONFIG}"
fi

###############################################################################
# Step 2 -- Build the nvml-mock image
###############################################################################
info "Building image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" \
  -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"

###############################################################################
# Step 3 -- Load image into Kind
###############################################################################
info "Loading image into Kind cluster"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

###############################################################################
# Step 4 -- Install nvml-mock with ecc_uncorrectable + Xid 79
###############################################################################
# Why ecc_uncorrectable as the entry-point mode?
#   * The device stays addressable (handle lookup keeps succeeding) so
#     nvidia-smi continues to run inside the pod and the demo can
#     observe counters growing without the GPU dropping off the API
#     surface.
#   * Combined with `xid.code=79` it also exercises the NVML event-set
#     delivery path (NVML_EVENT_TYPE_XID_CRITICAL_ERROR).
# Why after_calls=3?
#   * The mock's call counter is per-process. Setting after_calls=3
#     means a single `nvidia-smi -q -d ECC` invocation (which issues
#     dozens of guarded NVML calls) will trip the device on the third
#     call and report non-zero uncorrectable counters from then on.
info "Installing chart with mode=ecc_uncorrectable, after_calls=3, xid.code=79"
helm upgrade --install "${RELEASE_NAME}" "${REPO_ROOT}/${CHART_PATH}" \
  --set image.repository=nvml-mock \
  --set image.tag=failure-demo \
  --set gpu.profile=h100 \
  --set gpu.count=2 \
  --set gpu.failureInjection.enabled=true \
  --set gpu.failureInjection.mode=ecc_uncorrectable \
  --set gpu.failureInjection.after_calls=3 \
  --set gpu.failureInjection.xid.code=79 \
  --wait --timeout 120s

POD=$(wait_for_pod_ready)
sub "DaemonSet pod ready: ${POD}"

###############################################################################
# Step 5 -- Verify rendered ConfigMap carries the failure block
###############################################################################
info "Verifying rendered ConfigMap"
if ! kubectl get configmap "${RELEASE_NAME}" -o jsonpath='{.data.config\.yaml}' \
    | grep -q "mode: ecc_uncorrectable"; then
  fail "ConfigMap is missing the failure block; helm template did not inject failure: under device_defaults"
fi
sub "config.yaml contains the failure block"

###############################################################################
# Step 6 -- Verify ECC uncorrectable counters grow once tripped
###############################################################################
# nvidia-smi -q -d ECC issues many guarded NVML calls per device, so
# the third call meets after_calls=3 and the engine starts reporting a
# strictly-increasing uncorrectable counter for the rest of the
# process lifetime.
info "Querying ECC counters (expect non-zero uncorrectable post-trip)"
ECC_OUT=$(kubectl exec "${POD}" -- nvidia-smi -q -d ECC | head -80)
echo "${ECC_OUT}" | sed 's/^/    /'

# Extract the aggregate uncorrectable total. A healthy or pre-trip
# device reports 0; a tripped device reports a strictly-positive
# integer (the running call count at trip time).
UNCORR=$(echo "${ECC_OUT}" | awk '
  /Aggregate Uncorrectable Errors/    { in_block = 1; next }
  in_block && /Total/                 { print $NF; exit }
')
UNCORR=${UNCORR:-0}
case "${UNCORR}" in
  *[!0-9]* | "" )
    sub "Could not parse uncorrectable total (got: '${UNCORR}'); falling back to grep"
    if echo "${ECC_OUT}" | grep -q "Uncorrectable.*[1-9]"; then
      sub "OK: ECC output contains a non-zero uncorrectable count"
    else
      fail "ECC counters are still zero — failure injection did not trip"
    fi
    ;;
  0 )
    fail "ECC uncorrectable total is 0 — failure injection did not trip"
    ;;
  * )
    sub "OK: ECC uncorrectable total = ${UNCORR} (>0 confirms trip)"
    ;;
esac

###############################################################################
# Step 7 -- Inspect the YAML the engine actually loaded
###############################################################################
info "Failure block inside the running pod (sanity)"
kubectl exec "${POD}" -- sh -c 'awk "/^device_defaults:/,/^[a-z]/" /etc/nvml-mock/config.yaml' \
  | sed 's/^/    /'

###############################################################################
# Step 8 -- Print follow-up commands for the OTHER failure modes
###############################################################################
# These are not run automatically because they make the GPU disappear
# from the NVML API surface — `nvidia-smi -L` returns an error, which
# we don't want as the closing impression of an automated demo. Run
# them manually if you want to see lost / fallen_off_bus behaviour.
cat <<EOF

==> Next steps -- try the other failure modes manually

  # mode: lost  ─  GPU drops off the API surface; every device call
  # and every handle lookup returns ERROR_GPU_IS_LOST (Xid not
  # delivered because the NVML event set requires the device to stay
  # addressable, mirroring real NVML behaviour).
  helm upgrade ${RELEASE_NAME} ${CHART_PATH} \\
    --reuse-values \\
    --set gpu.failureInjection.mode=lost \\
    --set gpu.failureInjection.after_calls=1 \\
    --set gpu.failureInjection.xid.code=0
  kubectl rollout restart daemonset/${RELEASE_NAME}
  kubectl rollout status   daemonset/${RELEASE_NAME} --timeout=60s
  kubectl exec ${POD} -- nvidia-smi -L || true   # expect: 'GPU is lost'

  # mode: fallen_off_bus  ─  same NVML surface as 'lost' but typically
  # paired with a Xid 79 to mark the cause.
  helm upgrade ${RELEASE_NAME} ${CHART_PATH} \\
    --reuse-values \\
    --set gpu.failureInjection.mode=fallen_off_bus \\
    --set gpu.failureInjection.after_calls=1 \\
    --set gpu.failureInjection.xid.code=79

  # Observe the Xid event with a small Go consumer (the device-plugin
  # health monitor and dcgm-exporter use the same NVML API):
  kubectl exec ${POD} -- env MOCK_NVML_DEBUG=1 \\
    nvidia-smi -q -d ECC,POWER,TEMPERATURE 2>&1 | head -40

==> Tear down
  kind delete cluster --name ${CLUSTER_NAME}

EOF

info "Failure-injection demo complete."
