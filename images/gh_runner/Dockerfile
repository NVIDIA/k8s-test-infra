# Cache Holodeck image
FROM ghcr.io/nvidia/holodeck:v0.2.6 AS holodeck

# https://github.com/actions/runner/blob/main/images/Dockerfile
FROM ghcr.io/actions/actions-runner:2.322.0 AS runner

USER root

RUN apt-get update -y \
  && apt-get install -y --no-install-recommends \
  ca-certificates \
  curl \
  git \
  make \
  gpg \
  iptables \
  jq \
  lsb-release \
  python3 \
  sudo \
  unzip \
  zip \
  && rm -rf /var/lib/apt/lists/*

COPY --from=holodeck /go/bin/holodeck /usr/local/bin/holodeck

USER runner
