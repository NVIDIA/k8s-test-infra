# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

ARG GOLANG_VERSION=1.26.5
FROM golang:${GOLANG_VERSION}-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY cmd/mokka-controller/ cmd/mokka-controller/
COPY internal/mokkacontroller/ internal/mokkacontroller/
COPY pkg/ pkg/
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=vendor -trimpath -ldflags="-s -w" -o /mokka-controller ./cmd/mokka-controller

FROM scratch
COPY --from=builder /mokka-controller /mokka-controller
USER 65532:65532
ENTRYPOINT ["/mokka-controller"]
