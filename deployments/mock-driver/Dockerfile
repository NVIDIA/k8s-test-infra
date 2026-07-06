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

# Mock NVIDIA driver container image. Substituted for the real driver image
# via GPU Operator values (driver.repository/image/version) so the operator
# deploys it as the driver DaemonSet. See deployments/mock-driver/scripts/
# nvidia-driver for the contract it satisfies. Built from the same artifacts
# as nvml-mock (mock NVML/CUDA libraries, the same nvidia-smi extraction,
# the shared driver-root shell library) but laid out as a driver rootfs
# (usr/lib64, usr/bin) that the entrypoint exposes at /run/nvidia/driver.

# NOTE: the builder stage mirrors the mocknvml/mockcuda part of the one in
# deployments/nvml-mock/Dockerfile -- keep them in sync (this image does not
# need the IB/IMEX/fabricmanager components).
ARG GOLANG_VERSION=1.26.4
FROM golang:${GOLANG_VERSION}-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY pkg/gpu/mocknvml/ pkg/gpu/mocknvml/
COPY pkg/gpu/mockcuda/ pkg/gpu/mockcuda/
RUN cd pkg/gpu/mocknvml && make clean && make
RUN cd pkg/gpu/mockcuda && make clean && make

FROM debian:bookworm-slim
ARG TARGETARCH
COPY --from=builder /src/pkg/gpu/mocknvml/libnvidia-ml.so.* /usr/local/lib/
COPY --from=builder /src/pkg/gpu/mockcuda/libcuda.so.* /usr/local/lib/
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends curl ca-certificates gnupg2; \
    rm -rf /var/lib/apt/lists/*; \
    # Extract just the nvidia-smi binary from nvidia-utils, mirroring the
    # extraction in deployments/nvml-mock/Dockerfile (same 580 pin, same
    # TARGETARCH -> repo-path mapping) -- keep the two in sync.
    # Destination /usr/bin: RPATH $ORIGIN/../lib64 resolves to /usr/lib64,
    # where the entrypoint installs the mock NVML under the runtime
    # DRIVER_VERSION name. Works both inside this container (startup probe)
    # and from the host driver root (/run/nvidia/driver/usr/bin/, validator
    # LD_PRELOAD exec).
    NVIDIA_REPO_ARCH=$(case ${TARGETARCH} in arm64) echo sbsa;; *) echo x86_64;; esac); \
    curl -fsSL "https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/${NVIDIA_REPO_ARCH}/3bf863cc.pub" | \
      gpg --dearmor -o /usr/share/keyrings/nvidia.gpg; \
    echo "deb [signed-by=/usr/share/keyrings/nvidia.gpg] https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/${NVIDIA_REPO_ARCH} /" \
      > /etc/apt/sources.list.d/nvidia.list; \
    apt-get update; \
    cd /tmp && apt-get download nvidia-utils-580=580.65.06-0ubuntu1; \
    dpkg-deb -x /tmp/nvidia-utils-580*.deb /tmp/nvidia-utils; \
    cp /tmp/nvidia-utils/usr/bin/nvidia-smi /usr/bin/nvidia-smi; \
    chmod +x /usr/bin/nvidia-smi; \
    apt-get install -y --no-install-recommends patchelf; \
    patchelf --set-rpath '$ORIGIN/../lib64' /usr/bin/nvidia-smi; \
    rm -rf /var/lib/apt/lists/*; \
    rm -rf /tmp/nvidia-utils /tmp/nvidia-utils-580*.deb \
           /usr/share/keyrings/nvidia.gpg /etc/apt/sources.list.d/nvidia.list; \
    apt-get purge -y --auto-remove curl ca-certificates gnupg2 patchelf
# GPU profiles and the driver-root layout library are shared with the
# nvml-mock chart/image (single source of truth).
COPY deployments/nvml-mock/helm/nvml-mock/profiles/ /profiles/
COPY deployments/nvml-mock/scripts/lib-driver-root.sh /scripts/lib-driver-root.sh
COPY deployments/mock-driver/scripts/nvidia-driver /usr/local/bin/nvidia-driver
RUN chmod +x /usr/local/bin/nvidia-driver
# The GPU Operator sets command ["nvidia-driver"] / args ["init"] explicitly;
# the entrypoint here is for standalone `docker run` parity.
ENTRYPOINT ["nvidia-driver"]
CMD ["init"]
