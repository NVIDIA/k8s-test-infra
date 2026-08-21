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

# Runs distroless/static (unlike nvml-mock's debian:bookworm-slim), because the
# control plane is a pure Go binary with no runtime libraries, no shell scripts,
# and no auxiliary tools to launch — the smaller, non-root base is the
# Kubernetes-idiomatic default for stateless services. Revisit if the CP later
# needs CGO (for example for Redis TLS with a custom CA closure).

ARG GOLANG_VERSION=1.26.6
FROM golang:${GOLANG_VERSION}-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY pkg/ pkg/
COPY internal/ internal/
COPY cmd/ cmd/
RUN CGO_ENABLED=0 go build -mod=vendor \
    -ldflags="-s -w" \
    -o /dist/control-plane ./cmd/control-plane

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /dist/control-plane /usr/local/bin/control-plane
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/control-plane"]
