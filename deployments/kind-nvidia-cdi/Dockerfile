# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Kind node image with nvidia-container-toolkit + CDI pre-configured for mock GPU
# E2E. Everything the cluster needs is baked in here, so the Kind cluster configs
# need no containerdConfigPatches — just point kind-action at this node_image.
# Base image must match helm/kind-action default (kind v0.31.0 → kindest/node:v1.35.0).
#
# Build:
#   docker build -f deployments/kind-nvidia-cdi/Dockerfile \
#     -t kind-nvidia-cdi:local deployments/kind-nvidia-cdi
#
# Use with Kind:
#   kind create cluster --config tests/e2e/kind-gpu-operator-config.yaml \
#     --image kind-nvidia-cdi:local

ARG KIND_NODE_IMAGE=kindest/node:v1.35.0@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f
FROM ${KIND_NODE_IMAGE}

# nvidia-container-runtime config (CDI mode) and the full containerd config
# (CDI + nvidia runtime baked in). See the respective files for details.
COPY nvidia-container-runtime.toml /etc/nvidia-container-runtime/config.toml
COPY containerd-config.toml /etc/containerd/config.toml

RUN apt-get update -qq \
    && apt-get install -y -qq curl gpg \
    && curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
      | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg \
    && curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
      | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' \
      | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list \
    && apt-get update -qq \
    && apt-get install -y -qq nvidia-container-toolkit \
    && rm -rf /var/lib/apt/lists/*
