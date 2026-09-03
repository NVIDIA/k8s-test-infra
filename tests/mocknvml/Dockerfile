# Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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
# Named by both the build and the runtime stage.
ARG LIB_VERSION=550.54.15
ARG GOPROXY=https://proxy.golang.org,direct
FROM golang:${GOLANG_VERSION} AS lib-builder

ARG GOPROXY
ENV GOPROXY=${GOPROXY} \
    GOSUMDB=sum.golang.org \
    GONOPROXY=none \
    GOTOOLCHAIN=local

WORKDIR /build

# The mock library is a cgo build against the root module, so it needs the root
# module's dependency graph, not this sub-module's.
COPY go.mod go.sum ./
RUN --mount=type=secret,id=goauth,mode=0500 \
    if [ -f /run/secrets/goauth ]; then export GOAUTH=/run/secrets/goauth; fi; \
    go mod download
COPY pkg/ ./pkg/
COPY cmd/ ./cmd/

# Build via the Makefile, not a bare `go build`: it owns the --defsym aliases
# for the unversioned entry points (nvmlEventSetWait, ...), the SONAME and the
# symlinks. nopadding keeps the test image small.
# `clean` first: the library target has no prerequisites, so a host-built
# libnvidia-ml.so.* carried in by `COPY pkg/` would satisfy it unrebuilt.
WORKDIR /build/pkg/gpu/mocknvml
ARG LIB_VERSION
RUN make clean && make LIB_VERSION=${LIB_VERSION} BUILD_TAGS=nopadding

# Build the test binary
FROM golang:${GOLANG_VERSION} AS test-builder

ARG GOPROXY
ENV GOPROXY=${GOPROXY} \
    GOSUMDB=sum.golang.org \
    GONOPROXY=none \
    GOTOOLCHAIN=local

WORKDIR /build

# Copy the test code
COPY tests/mocknvml/go.mod tests/mocknvml/go.sum* ./
RUN --mount=type=secret,id=goauth,mode=0500 \
    if [ -f /run/secrets/goauth ]; then export GOAUTH=/run/secrets/goauth; fi; \
    go mod download

COPY tests/mocknvml/*.go ./
RUN CGO_ENABLED=1 go build -o mocknvml-test .

# Runtime stage
FROM debian:bookworm-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy the test binary
COPY --from=test-builder /build/mocknvml-test /app/

# Copy the test config fixture (a process with utilization, for the populated path).
# The Makefile points MOCK_NVML_CONFIG at this so the integration test runs against
# a real profile config rather than the env-var fallback.
COPY tests/mocknvml/util-test-config.yaml /app/util-test-config.yaml

# The real file only: a wildcard also drags in the cgo header (ldconfig rejects
# it) and flattens the sonames into three 12 MiB copies.
ARG LIB_VERSION
COPY --from=lib-builder /build/pkg/gpu/mocknvml/libnvidia-ml.so.${LIB_VERSION} /usr/local/lib/
RUN ln -sf libnvidia-ml.so.${LIB_VERSION} /usr/local/lib/libnvidia-ml.so.1 \
    && ln -sf libnvidia-ml.so.1 /usr/local/lib/libnvidia-ml.so

# Update library cache
RUN ldconfig

# Set up library path
ENV LD_LIBRARY_PATH=/usr/local/lib

# Configure mock NVML
ENV MOCK_NVML_NUM_DEVICES=4
ENV MOCK_NVML_DRIVER_VERSION=550.54.15

CMD ["/app/mocknvml-test"]

