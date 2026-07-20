# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Custom Kind node image for local GPU Operator + nvml-mock development.
# Pre-bakes nvidia-container-toolkit so the 'nvidia' containerd runtime handler
# resolves to /usr/bin/nvidia-container-runtime without any post-cluster setup.
#
# This replicates what the CI workflow does via docker exec after cluster creation
# (see .github/workflows/nvml-mock-e2e.yaml, "Install nvidia-container-toolkit"
# and "Configure toolkit for CDI mode" steps).
#
# Build and create a cluster:
#   make cluster-create

ARG KIND_NODE_VERSION=v1.35.0
FROM kindest/node:${KIND_NODE_VERSION}

RUN apt-get update -qq && \
    apt-get install -y -qq curl gpg && \
    curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
        | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg && \
    curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
        | sed "s#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g" \
        | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list && \
    apt-get update -qq && \
    apt-get install -y -qq nvidia-container-toolkit && \
    rm -rf /var/lib/apt/lists/*

RUN nvidia-ctk runtime configure --runtime=containerd --cdi.enabled --set-as-default

RUN mkdir -p /etc/nvidia-container-runtime
COPY nvidia-container-runtime.toml /etc/nvidia-container-runtime/config.toml
