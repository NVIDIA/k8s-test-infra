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

# Release builds resolve dependencies through NVIDIA's Artifactory rather than
# the public proxy. The credential is a short-lived bearer token, so it arrives
# as a BuildKit secret: a --build-arg would be recorded in image history, and
# the GOAUTH helper the runner writes does not cross into a build stage on its
# own. GOPROXY carries no credential, so it is an ordinary build arg.
#
# Both default to the public path, so `docker build` with no arguments still
# works. The publish workflow overrides GOPROXY and mounts the secret.
ARG GOLANG_VERSION=1.27.3
FROM golang:${GOLANG_VERSION}-bookworm AS builder
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY} \
    GOSUMDB=sum.golang.org \
    GONOPROXY=none \
    GOTOOLCHAIN=local
WORKDIR /src
COPY go.mod go.sum ./
# The mount lasts only for this RUN; the later build steps read the module cache
# it populates and need no credential.
RUN --mount=type=secret,id=goauth,mode=0500 \
    if [ -f /run/secrets/goauth ]; then export GOAUTH=/run/secrets/goauth; fi; \
    go mod download
COPY api/ api/
COPY pkg/ pkg/
COPY cmd/ cmd/
COPY internal/ internal/
COPY shims/ shims/
COPY Makefile ./
RUN cd pkg/gpu/mocknvml && make clean && make
RUN cd shims/libcuda && make clean && make
RUN cd shims/libibmock && make clean && make
RUN cd shims/libmockfs && make clean && make

RUN make build

# The NVIDIA Container Toolkit binaries the node daemon installs on each node
# (MEP-0006): the runtime, its CDI hook and nvidia-ctk, taken from the toolkit's
# own image. Its rpm tree is built on CentOS 7, so the binaries need glibc 2.17
# or later on the host. Pinned by the multi-architecture index digest.
FROM nvcr.io/nvidia/k8s/container-toolkit:v1.19.1@sha256:c927adbc9b7755c5cb90022fdcc5c1295f5fe5fe1f38200a2dc65e85632b029c AS container-toolkit

# Every nvidia-smi the image ships, one per driver branch, each in a directory
# named after the driver it came from (/out/<version>/nvidia-smi). A real node's
# nvidia-smi always comes with its driver, and the NVML field ids and NVLink
# count it reads changed between branches, so the node agent stages the one
# matching the profile's driver (internal/agent/gpudriver/nvidia_smi.go).
FROM debian:bookworm-slim AS nvidia-smi
ARG TARGETARCH
# 580 from NVIDIA's CUDA repository (Ubuntu 22.04: glibc 2.35, runs on
# bookworm). It renders the full GB200/GB300 NVLink5 surface (`nvlink
# -e/-info/-gLowPwrInfo/-gLWidth`). 580.65.06-0ubuntu1 is the only
# nvidia-utils-580 there that ships the binary (later 580.x are transitional
# dummies) and matches the 580 profiles' driver_version. See
# NVIDIA/k8s-test-infra#371.
ARG NVIDIA_SMI_580_PACKAGE=nvidia-utils-580=580.65.06-0ubuntu1
# 615 from Canonical's archive. NVIDIA's CUDA repositories carry no 615
# nvidia-utils package yet; Canonical's does, repackaging the binaries of
# NVIDIA's 615.71.09 installer (the arm64 nvidia-smi is byte-identical to the
# installer's), in a 640 KB package instead of the whole driver. Launchpad
# keeps every file it has published, so the URL outlives newer 615 uploads;
# the checksums pin the bytes.
ARG NVIDIA_SMI_615_PACKAGE=nvidia-utils-615-server_615.71.09-0ubuntu0.22.04.1
ARG NVIDIA_SMI_615_SHA256_AMD64=9294fabc15010dee248244fb6b4fd7f2dc093fab65508428c86ef404ad3a731e
ARG NVIDIA_SMI_615_SHA256_ARM64=b1646aad3b9acb1439c3b6ea94de8c8946b631ee9edb962ea5e5ea2b29c85b5a
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl gnupg2 patchelf; \
    # stage <extracted package root> <driver version>
    stage() { install -D -m 0755 "$1/usr/bin/nvidia-smi" "/out/$2/nvidia-smi"; }; \
    # Map TARGETARCH to NVIDIA's repository path: amd64 -> x86_64, arm64 -> sbsa.
    repo_arch=$(case "${TARGETARCH}" in arm64) echo sbsa;; *) echo x86_64;; esac); \
    repo="https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2204/${repo_arch}"; \
    curl -fsSL "${repo}/3bf863cc.pub" | gpg --dearmor -o /usr/share/keyrings/nvidia.gpg; \
    echo "deb [signed-by=/usr/share/keyrings/nvidia.gpg] ${repo} /" > /etc/apt/sources.list.d/nvidia.list; \
    apt-get update; \
    cd /tmp; \
    apt-get download "${NVIDIA_SMI_580_PACKAGE}"; \
    dpkg-deb -x /tmp/nvidia-utils-580_*.deb /tmp/580; \
    version="${NVIDIA_SMI_580_PACKAGE#*=}"; \
    stage /tmp/580 "${version%%-*}"; \
    case "${TARGETARCH}" in \
      amd64) sha256="${NVIDIA_SMI_615_SHA256_AMD64}" ;; \
      arm64) sha256="${NVIDIA_SMI_615_SHA256_ARM64}" ;; \
      *) echo "no 615 nvidia-smi pinned for ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    deb="${NVIDIA_SMI_615_PACKAGE}_${TARGETARCH}.deb"; \
    curl -fsSL -o "/tmp/${deb}" "https://launchpad.net/ubuntu/+archive/primary/+files/${deb}"; \
    echo "${sha256}  /tmp/${deb}" | sha256sum -c -; \
    dpkg-deb -x "/tmp/${deb}" /tmp/615; \
    version="${NVIDIA_SMI_615_PACKAGE#*_}"; \
    stage /tmp/615 "${version%%-*}"; \
    # Set RPATH so nvidia-smi finds libs relative to its own location
    # ($ORIGIN/../lib64). This makes it work in every mount context:
    # /run/nvidia/driver/usr/bin/, /var/lib/nvml-mock/driver/usr/bin/, and
    # CDI-injected /usr/bin/.
    for smi in /out/*/nvidia-smi; do patchelf --set-rpath '$ORIGIN/../lib64' "${smi}"; done

FROM debian:bookworm-slim
ARG TARGETARCH
COPY --from=nvidia-smi /out/ /usr/local/libexec/nvml-mock/nvidia-smi/
COPY --from=container-toolkit \
    /artifacts/rpm/usr/bin/nvidia-container-runtime \
    /artifacts/rpm/usr/bin/nvidia-ctk \
    /artifacts/rpm/usr/bin/nvidia-cdi-hook \
    /usr/local/libexec/nvml-mock/container-toolkit/
COPY --from=container-toolkit /licenses/LICENSE /usr/local/libexec/nvml-mock/container-toolkit/LICENSE
COPY --from=builder /src/pkg/gpu/mocknvml/libnvidia-ml.so.* /usr/local/lib/
COPY --from=builder /src/shims/libcuda/libcuda.so.* /usr/local/lib/
COPY --from=builder /src/shims/libibmock/libibmocksys.so.* /usr/local/lib/
COPY --from=builder /src/shims/libibmock/libibmockumad.so.* /usr/local/lib/
COPY --from=builder /src/shims/libibmock/libibmockverbs.so.* /usr/local/lib/
COPY --from=builder /src/shims/libmockfs/libmockfs.so.* /usr/local/lib/
COPY --from=builder /src/dist/nri-plugin /usr/local/bin/nri-plugin
COPY --from=builder /src/dist/nvml-mock-ctl /usr/local/bin/nvml-mock-ctl
COPY --from=builder /src/dist/check-fabric     /usr/local/bin/check-fabric
COPY --from=builder /src/dist/node-agent /usr/local/bin/node-agent
COPY --from=builder /src/dist/nvidia-imex-shim /usr/local/bin/nvidia-imex-shim
# Build-time only: it needs ldd and patchelf, both purged from the final image.
COPY deployments/nvml-mock/build/bundle-ib-tools.sh /usr/local/bin/bundle-ib-tools.sh
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates patchelf \
        pciutils \
        kmod \
        infiniband-diags rdma-core ibverbs-utils ibverbs-providers; \
    # libibverbs prints "couldn't open config directory '/etc/libibverbs.d'"
    # when the dir is missing. ibverbs-providers ships .driver files there,
    # but keep an explicit mkdir as a belt-and-braces guarantee so the
    # warning never appears even if the providers package is purged. \
    mkdir -p /etc/libibverbs.d; \
    # The nvidia-smi on the image's own PATH is the oldest bundled one, as a
    # bare `docker run` gets. In the DaemonSet the node agent points it at the
    # one it stages for the profile's driver.
    ln -s "$(ls -d /usr/local/libexec/nvml-mock/nvidia-smi/*/nvidia-smi | sort -V | head -n 1)" \
        /usr/local/bin/nvidia-smi; \
    # The InfiniBand CLI tools get the same RPATH treatment as nvidia-smi: the node
    # agent's infiniband simulator relocates them into the node overlay, where
    # their libibmad/libibumad/libibverbs/libnl dependencies are not on any
    # default search path. The script stages that closure next to them and bakes
    # in the RPATH. Injected containers therefore run them straight from a
    # minimal image that ships no IB libraries at all.
    # See NVIDIA/k8s-test-infra#438.
    sh /usr/local/bin/bundle-ib-tools.sh; \
    # The loader reaches /usr/local/lib only through ld.so.cache, which apt's
    # triggers happen to rebuild in this layer. Do it explicitly so a client
    # that dlopens libnvidia-ml.so.1, nvidia-smi included, keeps resolving it
    # if these apt steps ever move or go away.
    ldconfig; \
    rm -rf /var/lib/apt/lists/*; \
    # ca-certificates stays: the node agent verifies NVIDIA's TLS certificate
    # when it downloads IMEX (the IMEXDownload feature gate).
    apt-get purge -y --auto-remove patchelf
