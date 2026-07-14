#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail
: "${CLUSTER_NAME:?CLUSTER_NAME is required}"
: "${NVML_MOCK_NAMESPACE:=nvml-mock-system}"
NODE_KERNEL=$(docker exec "$CLUSTER_NAME-control-plane" uname -r)
wait_for_success() {
  local pod=$1 attempts=${2:-36} phase
  for i in $(seq 1 "$attempts"); do
    phase=$(kubectl get pod "$pod" -o jsonpath='{.status.phase}' 2>/dev/null || echo Pending)
    [[ "$phase" == Succeeded ]] && return 0
    if [[ "$phase" == Failed ]]; then
      kubectl describe pod "$pod" | tail -40 || true
      kubectl logs "$pod" || true
      return 1
    fi
    echo "attempt $i/$attempts: $pod phase=$phase"
    sleep 5
  done
  kubectl describe pod "$pod" | tail -40 || true
  return 1
}

cat <<'PODEOF' | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: kata-nri-ambient
spec:
  runtimeClassName: kata-qemu
  restartPolicy: Never
  containers:
    - name: probe
      image: ubuntu:22.04
      command: ["sh", "-c"]
      args:
        - |
          set -eu
          echo "GUEST_KERNEL=$(uname -r)"
          test -d /opt/nvml-mock
          test -L /opt/nvml-mock/driver/usr/lib64/libnvidia-ml.so.1
          test -r /opt/nvml-mock/driver/config/config.yaml
          if touch /opt/nvml-mock/.write-test 2>/dev/null; then exit 1; fi
          command -v nvidia-smi
          ! ls /dev/nvidia[0-9]* >/dev/null 2>&1
          nvidia-smi -L
          nvidia-smi
PODEOF
wait_for_success kata-nri-ambient
AMBIENT_LOGS=$(kubectl logs kata-nri-ambient)
echo "$AMBIENT_LOGS"
GUEST_KERNEL=$(sed -n 's/^GUEST_KERNEL=//p' <<<"$AMBIENT_LOGS")
[[ -n "$GUEST_KERNEL" ]] || { echo "FAIL: no guest kernel captured"; exit 1; }
[[ "$GUEST_KERNEL" != "$NODE_KERNEL" ]] \
  || { echo "FAIL: pod kernel ($GUEST_KERNEL) == node kernel ($NODE_KERNEL)"; exit 1; }
grep -qF "NVIDIA A100-SXM4-40GB" <<<"$AMBIENT_LOGS" \
  || { echo "FAIL: profile GPU name not reported by nvidia-smi in guest"; exit 1; }
GPU_COUNT=$(grep -c '^GPU ' <<<"$AMBIENT_LOGS" || true)
[[ "$GPU_COUNT" == 2 ]] \
  || { echo "FAIL: expected 2 ambient GPUs in -L output, got $GPU_COUNT"; exit 1; }
kubectl delete pod kata-nri-ambient --ignore-not-found

cat <<'PODEOF' | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: kata-nri-devices
  annotations:
    nvml-mock.nvidia.com/devices: "true"
spec:
  runtimeClassName: kata-qemu
  restartPolicy: Never
  containers:
    - name: probe
      image: ubuntu:22.04
      command: ["sh", "-c"]
      args:
        - |
          set -eu
          for node in /dev/nvidia0 /dev/nvidia1 /dev/nvidiactl /dev/nvidia-uvm /dev/nvidia-uvm-tools; do
            test -c "$node"
          done
          nvidia-smi -L
PODEOF
wait_for_success kata-nri-devices
kubectl logs kata-nri-devices
kubectl delete pod kata-nri-devices --ignore-not-found

cat <<'PODEOF' | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: kata-nri-optout
  annotations:
    nvml-mock.nvidia.com/inject: "false"
spec:
  runtimeClassName: kata-qemu
  restartPolicy: Never
  containers:
    - name: probe
      image: ubuntu:22.04
      command: ["sh", "-c"]
      args:
        - |
          set -eu
          ! test -e /opt/nvml-mock
          ! command -v nvidia-smi
          echo CLEAN
PODEOF
wait_for_success kata-nri-optout 24
kubectl logs kata-nri-optout | grep -q '^CLEAN$' \
  || { echo "FAIL: opt-out pod did not report CLEAN"; exit 1; }
kubectl delete pod kata-nri-optout --ignore-not-found
echo "PASS: NRI ambient, device opt-in, and opt-out contracts work under Kata"
