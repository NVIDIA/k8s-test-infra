# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

# IMEX payload image: the IMEX archive NVIDIA publishes for the image's
# platform, under /imex, on a base that has sh and cp. The nvml-mock chart's
# imex-payload init container copies /imex into the node agent's provided
# directory (imex.payload.image); the node agent checks the archive against
# the SHA-256 pinned in internal/agent/imex/imex.lock.json and stages it.
#
# Build it with `make imex-payload-image`, which downloads and verifies the
# pinned archive and passes the directory holding it as the build context:
#
#   <context>/<arch>/<archive, named as NVIDIA publishes it>
#
# Mokka does not publish this image. It only copies files, so a build for
# another platform needs no emulation.
FROM debian:bookworm-slim

ARG TARGETARCH
ARG IMEX_VERSION

LABEL org.opencontainers.image.title="mokka-imex" \
      org.opencontainers.image.description="NVIDIA IMEX archive for the Mokka node agent" \
      org.opencontainers.image.version="${IMEX_VERSION}"

COPY ${TARGETARCH}/ /imex/

USER 65534:65534
