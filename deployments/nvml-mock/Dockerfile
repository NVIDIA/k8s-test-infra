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

ARG GOLANG_VERSION=1.26
FROM golang:${GOLANG_VERSION}-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY pkg/gpu/mocknvml/ pkg/gpu/mocknvml/
COPY pkg/gpu/mockcuda/ pkg/gpu/mockcuda/
COPY pkg/network/mockib/ pkg/network/mockib/
COPY pkg/system/mockpcisysfs/ pkg/system/mockpcisysfs/
COPY pkg/imexcoord/ pkg/imexcoord/
COPY cmd/mock-ib/ cmd/mock-ib/
COPY cmd/render-pci-sysfs/ cmd/render-pci-sysfs/
COPY cmd/fake-imex/ cmd/fake-imex/
COPY cmd/check-fabric/ cmd/check-fabric/
RUN cd pkg/gpu/mocknvml && make clean && make
RUN cd pkg/gpu/mockcuda && make clean && make
RUN cd pkg/network/mockib && make clean && make
RUN mkdir -p /out \
    && go build -mod=vendor -o /out/mock-ib ./cmd/mock-ib \
    && go build -mod=vendor -o /out/render-pci-sysfs ./cmd/render-pci-sysfs \
    && go build -mod=vendor -o /out/nvidia-imex     ./cmd/fake-imex/daemon \
    && go build -mod=vendor -o /out/nvidia-imex-ctl ./cmd/fake-imex/ctl \
    && go build -mod=vendor -o /out/check-fabric    ./cmd/check-fabric

FROM debian:bookworm-slim
ARG KUBECTL_VERSION=v1.32.0
ARG TARGETARCH
COPY --from=builder /src/pkg/gpu/mocknvml/libnvidia-ml.so.* /usr/local/lib/
COPY --from=builder /src/pkg/gpu/mockcuda/libcuda.so.* /usr/local/lib/
COPY --from=builder /src/pkg/network/mockib/libibmocksys.so.* /usr/local/lib/
COPY --from=builder /src/pkg/network/mockib/libibmockumad.so.* /usr/local/lib/
COPY --from=builder /out/mock-ib /usr/local/bin/mock-ib
COPY --from=builder /out/render-pci-sysfs /usr/local/bin/render-pci-sysfs
# Fake IMEX binaries used for ComputeDomain simulation on KIND. Installed
# at the same paths the upstream compute-domain-daemon expects in $PATH,
# so the thin daemon image layer (Dockerfile.compute-domain-daemon) can
# COPY them straight from this image. See NVIDIA/k8s-test-infra#304.
COPY --from=builder /out/nvidia-imex     /usr/bin/nvidia-imex
COPY --from=builder /out/nvidia-imex-ctl /usr/bin/nvidia-imex-ctl
# check-fabric: tiny Go consumer that calls nvmlDeviceGetGpuFabricInfo
# via the LD_PRELOAD'd mock library. Used by docs/demo/compute-domain
# to confirm the topology overlay assigned the expected clique to each
# node. See NVIDIA/k8s-test-infra#304.
COPY --from=builder /out/check-fabric    /usr/local/bin/check-fabric
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends curl ca-certificates gnupg2 \
        infiniband-diags rdma-core; \
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
    # Map TARGETARCH to NVIDIA repo path: amd64 → x86_64, arm64 → sbsa.
    NVIDIA_REPO_ARCH=$(case ${TARGETARCH} in arm64) echo sbsa;; *) echo x86_64;; esac); \
    curl -fsSL "https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/${NVIDIA_REPO_ARCH}/3bf863cc.pub" | \
      gpg --dearmor -o /usr/share/keyrings/nvidia.gpg; \
    echo "deb [signed-by=/usr/share/keyrings/nvidia.gpg] https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/${NVIDIA_REPO_ARCH} /" \
      > /etc/apt/sources.list.d/nvidia.list; \
    apt-get update; \
    cd /tmp && apt-get download nvidia-utils-550; \
    dpkg-deb -x /tmp/nvidia-utils-550*.deb /tmp/nvidia-utils; \
    cp /tmp/nvidia-utils/usr/bin/nvidia-smi /usr/local/bin/nvidia-smi; \
    chmod +x /usr/local/bin/nvidia-smi; \
    # Set RPATH so nvidia-smi finds libs relative to its own location ($ORIGIN/../lib64).
    # This makes it work in every mount context: /run/nvidia/driver/usr/bin/,
    # /var/lib/nvml-mock/driver/usr/bin/, and CDI-injected /usr/bin/.
    apt-get install -y --no-install-recommends patchelf; \
    patchelf --set-rpath '$ORIGIN/../lib64' /usr/local/bin/nvidia-smi; \
    rm -rf /var/lib/apt/lists/*; \
    rm -rf /tmp/nvidia-utils /tmp/nvidia-utils-550*.deb \
           /usr/share/keyrings/nvidia.gpg /etc/apt/sources.list.d/nvidia.list; \
    apt-get purge -y --auto-remove curl ca-certificates gnupg2 patchelf
COPY deployments/nvml-mock/scripts/ /scripts/
RUN chmod +x /scripts/*.sh
