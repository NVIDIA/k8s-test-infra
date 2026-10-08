#!/bin/bash
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
#
# Prepares a GitHub Actions runner for an nvml-mock E2E leg: installs pinned
# Kind, Helm and Tilt, then creates the `mokka` Kind cluster.
#
# CI-only: it installs linux/amd64 or linux/arm64 binaries with sudo. Local
# development uses `make cluster-create` directly (see local/README.md).

set -euo pipefail

: "${RUNNER_TEMP:?}"

readonly KIND_VERSION=v0.31.0
readonly HELM_VERSION=v4.3.0
# Pinned so Tilt version drift doesn't silently change rollout semantics
# between runs. Bump alongside local dev.
readonly TILT_VERSION=0.37.5
readonly CLUSTER_NAME=mokka

# Checksums are per release asset, so bumping a version above means updating
# its checksum for both architectures. Tilt names its x86 assets `x86_64`
# where Kind and Helm say `amd64`.
case "$(uname -m)" in
  x86_64|amd64)
    readonly ARCH=amd64 TILT_ARCH=x86_64
    readonly KIND_SHA256=eb244cbafcc157dff60cf68693c14c9a75c4e6e6fedaf9cd71c58117cb93e3fa
    readonly HELM_SHA256=86584a54def73570558f66f5111cc53dfed56689637ae32c1201205d494f54fb
    readonly TILT_SHA256=9efb56969c48ddea4adfb56a604b9ca5ed254924ddf140fc3e068a305cacf558
    ;;
  aarch64|arm64)
    readonly ARCH=arm64 TILT_ARCH=arm64
    readonly KIND_SHA256=8e1014e87c34901cc422a1445866835d1e666f2a61301c27e722bdeab5a1f7e4
    readonly HELM_SHA256=31c5794dd55c66a51e6b7d2e2ac7a114ae8b1de41ff1d9ba51748ac973b06a08
    readonly TILT_SHA256=1f9c62dcce0608aeb0c63533b14e260fc2e8c5797f81589b0914c398ba53d5b0
    ;;
  *) echo "ERROR: unsupported architecture '$(uname -m)'" >&2; exit 1 ;;
esac

# Release downloads hit GitHub's CDN, whose transient 5xx/connection resets
# would otherwise fail a whole matrix leg before any test runs.
readonly CURL_RETRY=(--retry 5 --retry-delay 2 --retry-all-errors)

echo "::group::Install Kind ${KIND_VERSION}"
sudo curl -fsSL "${CURL_RETRY[@]}" -o /usr/local/bin/kind "https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-linux-${ARCH}"
echo "${KIND_SHA256}  /usr/local/bin/kind" | sha256sum --check
sudo chmod 0755 /usr/local/bin/kind
kind version
echo "::endgroup::"

echo "::group::Install Helm ${HELM_VERSION}"
curl -fsSL "${CURL_RETRY[@]}" -o "${RUNNER_TEMP}/helm.tar.gz" "https://get.helm.sh/helm-${HELM_VERSION}-linux-${ARCH}.tar.gz"
echo "${HELM_SHA256}  ${RUNNER_TEMP}/helm.tar.gz" | sha256sum --check
sudo tar -xzf "${RUNNER_TEMP}/helm.tar.gz" -C /usr/local/bin --strip-components=1 "linux-${ARCH}/helm"
rm -f "${RUNNER_TEMP}/helm.tar.gz"
helm version
echo "::endgroup::"

echo "::group::Install Tilt ${TILT_VERSION}"
# Downloaded to a file rather than piped into tar: a retry after a partial
# transfer would append a second copy to the stream and corrupt the archive.
curl -fsSL "${CURL_RETRY[@]}" -o "${RUNNER_TEMP}/tilt.tar.gz" \
  "https://github.com/tilt-dev/tilt/releases/download/v${TILT_VERSION}/tilt.${TILT_VERSION}.linux.${TILT_ARCH}.tar.gz"
echo "${TILT_SHA256}  ${RUNNER_TEMP}/tilt.tar.gz" | sha256sum --check
sudo tar -xzf "${RUNNER_TEMP}/tilt.tar.gz" -C /usr/local/bin tilt
rm -f "${RUNNER_TEMP}/tilt.tar.gz"
tilt version
echo "::endgroup::"

echo "::group::Create Kind cluster ${CLUSTER_NAME}"
# Same command a laptop dev runs (local/kind/default.kind.yaml, context
# kind-mokka).
make cluster-create
echo "::endgroup::"
