# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION
# Build context contains the statically linked binaries under linux/<arch>/.
FROM scratch
ARG TARGETARCH
COPY linux/${TARGETARCH}/mokka-kwok /mokka-kwok
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/mokka-kwok"]
