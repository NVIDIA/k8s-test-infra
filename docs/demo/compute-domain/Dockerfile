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

# LOCAL BUILD ONLY — never publish this image: it repackages the proprietary
# nvidia-imex binary installed from Ubuntu jammy multiverse.

ARG GOLANG_VERSION=1.26.6

# Jammy carries the 595 driver branch under this package name. Fail loudly if
# the repository ever resolves it to a different branch.
FROM ubuntu:22.04 AS imex
RUN export DEBIAN_FRONTEND=noninteractive \
 && apt-get update \
 && apt-get install -y --no-install-recommends nvidia-imex-595 \
 && rm -rf /var/lib/apt/lists/* \
 && dpkg-query -W -f='${Version}\n' nvidia-imex-595 | tee /imex-version \
 && grep -q '^595\.' /imex-version

FROM golang:${GOLANG_VERSION} AS shim
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY shims/imex-nogpu/ shims/imex-nogpu/
RUN CGO_ENABLED=0 go build -mod=vendor -o /out/imex-nogpu-shim ./shims/imex-nogpu

# Retain the package configuration and runtime dependencies while replacing
# the daemon entry point with the NO GPU mode shim.
FROM imex
RUN mv /usr/bin/nvidia-imex /usr/bin/nvidia-imex.real
COPY --from=shim /out/imex-nogpu-shim /usr/bin/nvidia-imex
