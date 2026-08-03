#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Emit N KWOK fake GPU nodes for the hybrid Mokka + KWOK cell.
#
# These nodes are hollow: no kubelet, no container runtime, no GPU. KWOK's
# controller answers for them at the API level and `stage-fast` walks pods
# through their phases without executing anything. That is the whole point of
# the cell: a KWOK node advertises the same GPU labels and the same
# nvidia.com/gpu capacity that a Mokka node does, and NOTHING above the API
# server can tell them apart from labels alone.
#
# The label set mirrors what AICR's own kwok/scripts/apply-nodes.sh injects, so
# the fleet looks to AICR exactly like its own KWOK lane's fleet does. Values
# are taken from Mokka's gb200 profile so the fake nodes and the real Mokka
# nodes claim the same hardware.
#
# Usage:
#   kwok-gpu-nodes.sh <count> [taint]     taint: "true" (default) | "false"
#
#   kwok-gpu-nodes.sh 250          | kubectl apply -f -
#   kwok-gpu-nodes.sh 250 false    | kubectl apply -f -
#
# With taint=true the nodes carry kwok.x-k8s.io/node=fake:NoSchedule, which is
# how AICR's KWOK lane ships them: workloads must tolerate the taint explicitly.
# With taint=false they are freely schedulable, which is the configuration that
# lets a fleet-surveying AICR check target a hollow node.
set -euo pipefail

COUNT="${1:?usage: kwok-gpu-nodes.sh <count> [taint true|false]}"
TAINT="${2:-true}"

# gb200 profile shape, from deployments/nvml-mock/helm/nvml-mock/profiles/gb200.yaml
GPU_PRODUCT="NVIDIA-GB200"
GPU_COUNT="8"
GPU_MEMORY="196608"        # MiB per GPU (192 GiB)
DRIVER_MAJOR="580"
DRIVER_MINOR="65"
DRIVER_VERSION="580.65.06"

for i in $(seq 1 "${COUNT}"); do
  NAME=$(printf "kwok-gpu-%04d" "${i}")
  cat <<YAML
---
apiVersion: v1
kind: Node
metadata:
  name: ${NAME}
  annotations:
    kwok.x-k8s.io/node: "fake"
    node.alpha.kubernetes.io/ttl: "0"
    nvidia.com/gpu.driver.version: "${DRIVER_VERSION}"
  labels:
    beta.kubernetes.io/arch: arm64
    beta.kubernetes.io/os: linux
    kubernetes.io/arch: arm64
    kubernetes.io/hostname: ${NAME}
    kubernetes.io/os: linux
    kubernetes.io/role: agent
    node-role.kubernetes.io/agent: ""
    type: kwok
    aicr.run/node-type: accelerated
    nvidia.com/gpu.present: "true"
    nvidia.com/gpu.product: ${GPU_PRODUCT}
    nvidia.com/gpu.count: "${GPU_COUNT}"
    nvidia.com/gpu.memory: "${GPU_MEMORY}"
    nvidia.com/cuda.driver.major: "${DRIVER_MAJOR}"
    nvidia.com/cuda.driver.minor: "${DRIVER_MINOR}"
spec:
YAML
  if [ "${TAINT}" = "true" ]; then
    cat <<'YAML'
  taints:
    - key: kwok.x-k8s.io/node
      value: fake
      effect: NoSchedule
YAML
  fi
  cat <<YAML
status:
  allocatable:
    cpu: "192"
    memory: 2048Gi
    pods: "250"
    nvidia.com/gpu: "${GPU_COUNT}"
  capacity:
    cpu: "192"
    memory: 2048Gi
    pods: "250"
    nvidia.com/gpu: "${GPU_COUNT}"
  nodeInfo:
    architecture: arm64
    containerRuntimeVersion: containerd://2.3.1
    kernelVersion: 6.1.0-fake
    kubeProxyVersion: fake
    kubeletVersion: fake
    operatingSystem: linux
    osImage: fake
  phase: Running
YAML
done
