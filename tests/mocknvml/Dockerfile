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

ARG GOLANG_VERSION=1.25.0
FROM golang:${GOLANG_VERSION} AS lib-builder

WORKDIR /build

# Copy the entire repo to build the mock library
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY pkg/ ./pkg/
COPY cmd/ ./cmd/

# Build the mock NVML library
WORKDIR /build/pkg/gpu/mocknvml
RUN go build -buildmode=c-shared -o libnvidia-ml.so.550.54.15 ./bridge && \
    ln -sf libnvidia-ml.so.550.54.15 libnvidia-ml.so.1 && \
    ln -sf libnvidia-ml.so.1 libnvidia-ml.so

# Build the test binary
FROM golang:${GOLANG_VERSION} AS test-builder

WORKDIR /build

# Copy the test code
COPY tests/mocknvml/go.mod tests/mocknvml/go.sum* ./
RUN go mod download

COPY tests/mocknvml/main.go ./
RUN CGO_ENABLED=1 go build -o mocknvml-test main.go

# Runtime stage
FROM debian:bookworm-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy the test binary
COPY --from=test-builder /build/mocknvml-test /app/

# Copy the mock NVML library to /usr/local/lib (architecture-independent)
COPY --from=lib-builder /build/pkg/gpu/mocknvml/libnvidia-ml.so* /usr/local/lib/

# Update library cache
RUN ldconfig

# Set up library path
ENV LD_LIBRARY_PATH=/usr/local/lib

# Configure mock NVML
ENV MOCK_NVML_NUM_DEVICES=4
ENV MOCK_NVML_DRIVER_VERSION=550.54.15

CMD ["/app/mocknvml-test"]

