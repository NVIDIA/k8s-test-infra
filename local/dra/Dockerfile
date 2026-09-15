# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0

# Local-only adapter for exercising the upstream NVIDIA DRA driver against
# Mokka's GPU-less compute-domain simulation. The Mokka image is the sole
# source of IMEX; this build intentionally contains no download metadata.
ARG NVML_MOCK_IMAGE=ghcr.io/nvidia/nvml-mock:latest
# Required: Tilt supplies the image pinned beside the matching OCI chart in
# local/dra/version.tiltfile. Keeping no default here prevents version drift.
ARG DRA_DRIVER_IMAGE

FROM ${NVML_MOCK_IMAGE} AS mokka

# Keep the upstream image as the final stage so its entrypoint and command are
# unchanged. The upstream compute-domain daemon invokes nvidia-imex with a
# fixed argv; Mokka's shim appends --nogpu before starting the real executable.
FROM ${DRA_DRIVER_IMAGE}
COPY --from=mokka /usr/bin/nvidia-imex /usr/bin/nvidia-imex
COPY --from=mokka /usr/bin/nvidia-imex.real /usr/bin/nvidia-imex.real
COPY --from=mokka /usr/bin/nvidia-imex-ctl /usr/bin/nvidia-imex-ctl
COPY --from=mokka /usr/share/doc/nvidia-imex /usr/share/doc/nvidia-imex

# The upstream image is distroless but carries BusyBox outside the standard
# PATH. Use it only for this build check; ENTRYPOINT and CMD remain inherited.
SHELL ["/busybox/sh", "-c"]
RUN set -eu; \
    status=0; \
    output=$(/usr/bin/nvidia-imex-ctl --help 2>&1) || status=$?; \
    test "${status}" -ne 126; \
    test "${status}" -ne 127; \
    printf '%s\n' "${output}" | /busybox/grep -q '^Usage:'
