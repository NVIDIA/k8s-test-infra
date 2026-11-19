#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
# Run from the repository root after installing the two PoC controllers.
set -euo pipefail
kubectl create --dry-run=client -f examples/controlplane-crds/sgpu-rack-profile.yaml -o json |
  jq '.metadata.name = "kwok-metrics-a100" | .spec.rack.nodesPerRack = 10 |
      .spec.node.gpus.count = 8 | .spec.node.topology.gpuFabric.domain.gpuCount = 80 |
      .spec.node.topology.gpuSlots |= [.[0] as $slot | range(8) as $i |
        $slot + {index: $i, pciAddress: "0000:0\($i + 1):00.0"}]' |
  kubectl apply -f -
kubectl create --dry-run=client -f examples/kwok-gpu/inventory.yaml -o json |
  jq '.metadata.name = "kwok-metrics" | .spec.rackGroups[0].count = 10 |
      .spec.rackGroups[0].profileRef.name = "kwok-metrics-a100"' |
  kubectl apply -f -
kubectl create --dry-run=client -f examples/kwok-gpu/node.yaml -o json |
  jq '{apiVersion:"v1", kind:"List", items:[. as $template | range(1;101) as $i |
       ("kwok-metrics-" + ("00" + ($i|tostring))[-3:]) as $name | $template |
       .metadata.name = $name | .metadata.labels += {"kubernetes.io/hostname":$name,
         "nvidia.com/gpu.present":"true","nvidia.com/gpu.product":"NVIDIA-A100-SXM4-40GB",
         "nvidia.com/gpu.memory":"40960","nvidia.com/gpu.count":"8","type":"kwok"} |
       .status = {capacity:{cpu:"64",memory:"256Gi",pods:"110"},
                  allocatable:{cpu:"64",memory:"256Gi",pods:"110"}}]}' |
  kubectl apply -f -
