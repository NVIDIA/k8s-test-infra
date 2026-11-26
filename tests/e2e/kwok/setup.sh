#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION
set -euo pipefail

cluster=${1:?expected a kind cluster name}
mode=${2:-binding}
case "$mode" in binding|dra) ;; *) echo "expected binding or dra" >&2; exit 2 ;; esac
context=kind-${cluster}
fixture=tests/e2e/kwok
release=https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0
k=(kubectl --context "$context")
h=(helm --kube-context "$context" --namespace mokka)

# Use the same pre-built node-daemon image as the other e2e jobs.
images=(mokka-control-plane:e2e mokka-kwok:e2e)
if [[ $mode == binding ]]; then
  docker image inspect nvml-mock:e2e >/dev/null
  images+=(nvml-mock:e2e)
fi
docker build -f deployments/control-plane/Dockerfile -t mokka-control-plane:e2e .
make build-mokka-kwok
docker build -f deployments/mokka-kwok/Dockerfile -t mokka-kwok:e2e dist/kwok
kind load docker-image --name "$cluster" "${images[@]}"

# The pinned upstream deployment enables Stage resources and selects only Nodes
# annotated kwok.x-k8s.io/node=fake. Its initialization stage makes the Node Ready.
curl -fsSL --retry 3 "$release/kwok.yaml" | "${k[@]}" apply --server-side -f -
"${k[@]}" wait --for=condition=Established crd/stages.kwok.x-k8s.io --timeout=90s
curl -fsSL --retry 3 "$release/stage-fast.yaml" | "${k[@]}" apply -f -
"${k[@]}" apply -f deployments/mokka-kwok/stages/pod-admission.yaml
"${k[@]}" rollout status -n kube-system deployment/kwok-controller --timeout=3m

"${h[@]}" upgrade --install mokka-crds deployments/mokka-crds/helm/mokka-crds --create-namespace --wait
"${k[@]}" wait --for=condition=Established crd/sgpuinventories.mokka.nvidia.com crd/sgpurackprofiles.mokka.nvidia.com --timeout=90s
values=(-f "$fixture/values.yaml")
if [[ $mode == dra ]]; then
  version=$(awk -F '[|]' '$2 == " DRA Driver (GPU) " {gsub(/[[:space:]]/, "", $3); print $3}' tests/e2e/VERSION-MATRIX.md)
  : "${version:?missing DRA driver pin in VERSION-MATRIX.md}"
  helm --kube-context "$context" upgrade --install nvidia-dra-driver dra-driver-nvidia-gpu \
    --repo https://helm.ngc.nvidia.com/nvidia --version "$version" -n nvidia --create-namespace \
    -f local/dra/dra-driver.values.yaml -f local/compute-domain/dra-driver.values.yaml --wait --timeout=5m
  "$fixture/dra-rack.sh" "$context"
  values+=(--set kwok.dataPlane.gpuPublishing=DRA --set kwok.dataPlane.computeDomains.enabled=true)
else
  "${k[@]}" apply -f "$fixture/resources.yaml"
  "${k[@]}" wait --for=condition=Ready node/kwok-worker --timeout=3m
fi
"${h[@]}" upgrade --install nvml-mock deployments/nvml-mock/helm/nvml-mock "${values[@]}" --wait --timeout=5m
