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

ARG GOLANG_VERSION=1.26.6
FROM golang:${GOLANG_VERSION}-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY pkg/ pkg/
COPY cmd/ cmd/
COPY internal/ internal/
COPY shims/ shims/
COPY Makefile ./
RUN cd pkg/gpu/mocknvml && make clean && make
RUN cd pkg/gpu/mockcuda && make clean && make
RUN cd shims/libibmock && make clean && make
RUN cd shims/libpcisysfs && make clean && make

RUN make build

FROM debian:bookworm-slim
ARG KUBECTL_VERSION=v1.36.1
ARG TARGETARCH
COPY --from=builder /src/pkg/gpu/mocknvml/libnvidia-ml.so.* /usr/local/lib/
COPY --from=builder /src/pkg/gpu/mockcuda/libcuda.so.* /usr/local/lib/
COPY --from=builder /src/shims/libibmock/libibmocksys.so.* /usr/local/lib/
COPY --from=builder /src/shims/libibmock/libibmockumad.so.* /usr/local/lib/
COPY --from=builder /src/shims/libibmock/libibmockverbs.so.* /usr/local/lib/
COPY --from=builder /src/shims/libpcisysfs/libpcisysfs.so.* /usr/local/lib/
COPY --from=builder /src/dist/nvml-mock-nri /usr/local/bin/nvml-mock-nri
COPY --from=builder /src/dist/nvml-mock-ctl /usr/local/bin/nvml-mock-ctl
COPY --from=builder /src/dist/check-fabric     /usr/local/bin/check-fabric
COPY --from=builder /src/dist/node-agent /usr/local/bin/node-agent
# Build-time only, so it lives outside scripts/ — that directory is copied
# wholesale into the runtime image below, and this script cannot run there once
# patchelf is purged.
COPY deployments/nvml-mock/build/bundle-ib-tools.sh /usr/local/bin/bundle-ib-tools.sh
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends curl ca-certificates gnupg2 \
        pciutils \
        infiniband-diags rdma-core ibverbs-utils ibverbs-providers; \
    rm -rf /var/lib/apt/lists/*; \
    # libibverbs prints "couldn't open config directory '/etc/libibverbs.d'"
    # when the dir is missing. ibverbs-providers ships .driver files there,
    # but keep an explicit mkdir as a belt-and-braces guarantee so the
    # warning never appears even if the providers package is purged. \
    mkdir -p /etc/libibverbs.d; \
    curl -fsSLo /usr/local/bin/kubectl \
      "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl"; \
    curl -fsSLo /tmp/kubectl.sha256 \
      "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl.sha256"; \
    echo "$(cat /tmp/kubectl.sha256)  /usr/local/bin/kubectl" | sha256sum --check; \
    chmod +x /usr/local/bin/kubectl; \
    rm /tmp/kubectl.sha256; \
    # Extract just the nvidia-smi binary from nvidia-utils (Ubuntu 22.04 repo:
    # glibc 2.35, runs on bookworm). The 580 branch renders the full GB200/GB300
    # NVLink5 surface (`nvlink -e/-info/-gLowPwrInfo/-gLWidth`); older binaries
    # only show the legacy layout, though the mock implements the fields either
    # way. Pin 580.65.06-0ubuntu1: it's the only nvidia-utils-580 here that ships
    # the binary (later 580.x are transitional dummies) and matches the GB200
    # profile's driver_version. See NVIDIA/k8s-test-infra#371.
    # Map TARGETARCH to NVIDIA repo path: amd64 → x86_64, arm64 → sbsa.
    NVIDIA_REPO_ARCH=$(case ${TARGETARCH} in arm64) echo sbsa;; *) echo x86_64;; esac); \
    curl -fsSL "https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/${NVIDIA_REPO_ARCH}/3bf863cc.pub" | \
      gpg --dearmor -o /usr/share/keyrings/nvidia.gpg; \
    echo "deb [signed-by=/usr/share/keyrings/nvidia.gpg] https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/${NVIDIA_REPO_ARCH} /" \
      > /etc/apt/sources.list.d/nvidia.list; \
    apt-get update; \
    cd /tmp && apt-get download nvidia-utils-580=580.65.06-0ubuntu1; \
    dpkg-deb -x /tmp/nvidia-utils-580*.deb /tmp/nvidia-utils; \
    cp /tmp/nvidia-utils/usr/bin/nvidia-smi /usr/local/bin/nvidia-smi; \
    chmod +x /usr/local/bin/nvidia-smi; \
    # Set RPATH so nvidia-smi finds libs relative to its own location ($ORIGIN/../lib64).
    # This makes it work in every mount context: /run/nvidia/driver/usr/bin/,
    # /var/lib/nvml-mock/driver/usr/bin/, and CDI-injected /usr/bin/.
    apt-get install -y --no-install-recommends patchelf; \
    patchelf --set-rpath '$ORIGIN/../lib64' /usr/local/bin/nvidia-smi; \
    # Same treatment for the InfiniBand CLI tools, for the same reason: the node
    # agent's infiniband simulator relocates them into the node overlay, where
    # their libibmad/libibumad/libibverbs/libnl dependencies are not on any
    # default search path. The script stages that closure next to them and bakes
    # in the RPATH. Injected containers therefore run them straight from a
    # minimal image that ships no IB libraries at all.
    # See NVIDIA/k8s-test-infra#438.
    sh /usr/local/bin/bundle-ib-tools.sh; \
    rm -rf /var/lib/apt/lists/*; \
    rm -rf /tmp/nvidia-utils /tmp/nvidia-utils-580*.deb \
           /usr/share/keyrings/nvidia.gpg /etc/apt/sources.list.d/nvidia.list; \
    apt-get purge -y --auto-remove curl ca-certificates gnupg2 patchelf
COPY --chmod=755 deployments/nvml-mock/scripts/ /scripts/
