# Copyright 2026 NVIDIA CORPORATION
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

ARG GOLANG_VERSION=1.25
FROM golang:${GOLANG_VERSION}-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY pkg/gpu/mocknvml/ pkg/gpu/mocknvml/
COPY pkg/gpu/mockcuda/ pkg/gpu/mockcuda/
RUN cd pkg/gpu/mocknvml && make clean && make
RUN cd pkg/gpu/mockcuda && make clean && make

FROM debian:bookworm-slim
ARG KUBECTL_VERSION=v1.32.0
ARG TARGETARCH=amd64
COPY --from=builder /src/pkg/gpu/mocknvml/libnvidia-ml.so.* /usr/local/lib/
COPY --from=builder /src/pkg/gpu/mockcuda/libcuda.so.* /usr/local/lib/
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends curl ca-certificates gnupg2; \
    rm -rf /var/lib/apt/lists/*; \
    curl -fsSLo /usr/local/bin/kubectl \
      "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl"; \
    curl -fsSLo /tmp/kubectl.sha256 \
      "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl.sha256"; \
    echo "$(cat /tmp/kubectl.sha256)  /usr/local/bin/kubectl" | sha256sum --check; \
    chmod +x /usr/local/bin/kubectl; \
    rm /tmp/kubectl.sha256; \
    # Extract nvidia-smi binary from nvidia-utils package (no full install needed).
    # Use Ubuntu 22.04 repo (binary-compatible with Debian bookworm, has nvidia-utils-550).
    curl -fsSL https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/x86_64/3bf863cc.pub | \
      gpg --dearmor -o /usr/share/keyrings/nvidia.gpg; \
    echo "deb [signed-by=/usr/share/keyrings/nvidia.gpg] https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/x86_64 /" \
      > /etc/apt/sources.list.d/nvidia.list; \
    apt-get update; \
    cd /tmp && apt-get download nvidia-utils-550; \
    dpkg-deb -x /tmp/nvidia-utils-550*.deb /tmp/nvidia-utils; \
    cp /tmp/nvidia-utils/usr/bin/nvidia-smi /usr/local/bin/nvidia-smi; \
    chmod +x /usr/local/bin/nvidia-smi; \
    # Set RPATH so nvidia-smi finds libs relative to its own location ($ORIGIN/../lib64).
    # This makes it work in every mount context: /run/nvidia/driver/usr/bin/,
    # /var/lib/nvidia-mock/driver/usr/bin/, and CDI-injected /usr/bin/.
    apt-get install -y --no-install-recommends patchelf; \
    patchelf --set-rpath '$ORIGIN/../lib64' /usr/local/bin/nvidia-smi; \
    rm -rf /var/lib/apt/lists/*; \
    rm -rf /tmp/nvidia-utils /tmp/nvidia-utils-550*.deb \
           /usr/share/keyrings/nvidia.gpg /etc/apt/sources.list.d/nvidia.list; \
    apt-get purge -y --auto-remove curl ca-certificates gnupg2 patchelf
COPY deployments/gpu-mock/scripts/ /scripts/
RUN chmod +x /scripts/*.sh
