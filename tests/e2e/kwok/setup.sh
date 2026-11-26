#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION
set -euo pipefail

cluster=${1:?expected a kind cluster name}
context=kind-${cluster}
fixture=tests/e2e/kwok
release=https://github.com/kubernetes-sigs/kwok/releases/download/v0.8.0
k=(kubectl --context "$context")
h=(helm --kube-context "$context" --namespace mokka)

# Use the same pre-built node-daemon image as the other e2e jobs.
docker image inspect nvml-mock:e2e >/dev/null
docker build -f deployments/control-plane/Dockerfile -t mokka-control-plane:e2e .
make build-mokka-kwok
docker build -f deployments/mokka-kwok/Dockerfile -t mokka-kwok:e2e dist/kwok
kind load docker-image --name "$cluster" nvml-mock:e2e mokka-control-plane:e2e mokka-kwok:e2e

# The pinned upstream deployment enables Stage resources and selects only Nodes
# annotated kwok.x-k8s.io/node=fake. Its initialization stage makes the Node Ready.
curl -fsSL --retry 3 "$release/kwok.yaml" | "${k[@]}" apply --server-side -f -
"${k[@]}" wait --for=condition=Established crd/stages.kwok.x-k8s.io --timeout=90s
curl -fsSL --retry 3 "$release/stage-fast.yaml" | "${k[@]}" apply -f -
"${k[@]}" apply -f deployments/mokka-kwok/stages/pod-admission.yaml
"${k[@]}" rollout status -n kube-system deployment/kwok-controller --timeout=3m

"${h[@]}" upgrade --install mokka-crds deployments/mokka-crds/helm/mokka-crds --create-namespace --wait
"${k[@]}" wait --for=condition=Established crd/sgpuinventories.mokka.nvidia.com crd/sgpurackprofiles.mokka.nvidia.com --timeout=90s
"${k[@]}" apply -f "$fixture/resources.yaml"
"${k[@]}" wait --for=condition=Ready node/kwok-worker --timeout=3m
"${h[@]}" upgrade --install nvml-mock deployments/nvml-mock/helm/nvml-mock -f "$fixture/values.yaml" --wait --timeout=5m
