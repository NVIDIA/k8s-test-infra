#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION
set -euo pipefail
k=(kubectl --context "${1:?expected a kube context}")
pool=kwok-dra-e2e
# Keep the GB300 GPU identities, NUMA topology and NVLink fabric. Scale the
# rack to four Nodes so all slots can participate in one ComputeDomain.
"${k[@]}" patch --local -f internal/sgpu/rackstate/testdata/gb300-nvl72.yaml --type=merge \
  -p '{"metadata":{"name":"kwok-dra-e2e","labels":null},"spec":{"rack":{"nodesPerRack":4},"node":{"topology":{"gpuFabric":{"domain":{"gpuCount":16}}}}}}' \
  -o yaml | "${k[@]}" apply -f -
for slot in 0 1 2 3; do
  "${k[@]}" apply -f - <<NODE
apiVersion: v1
kind: Node
metadata:
  name: ${pool}-${slot}
  annotations: {kwok.x-k8s.io/node: fake}
  labels:
    kubernetes.io/hostname: ${pool}-${slot}
    mokka.nvidia.com/node-type: KWOK
    mokka.nvidia.com/sgpu-node: "true"
    mokka.nvidia.com/pool: ${pool}
spec:
  taints: [{key: kwok.x-k8s.io/node, value: fake, effect: NoSchedule}]
status:
  capacity: {cpu: "8", memory: 32Gi, pods: "32"}
  allocatable: {cpu: "8", memory: 32Gi, pods: "32"}
NODE
done
"${k[@]}" apply -f - <<RACK
apiVersion: mokka.nvidia.com/v1alpha1
kind: SGPUInventory
metadata: {name: ${pool}}
spec:
  rackGroups:
    - id: virtual
      count: 1
      node: {type: KWOK}
      profileRef: {name: ${pool}}
      placement: {nodeSelector: {matchLabels: {mokka.nvidia.com/pool: ${pool}}}}
RACK
