#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

# Render the GitHub release notes for a published Mokka release: the image and
# chart digests as published, the install and cosign commands, the release's
# CHANGELOG.md section and its compare link. The opening summary is left as a
# SUMMARY placeholder because it needs judgment, not text processing.
#
# Usage: release-notes.sh X.Y.Z [CHANGELOG.md]
#
# Read-only: it resolves digests from GHCR and never writes anything remote.
# Run it after the tag's publish runs succeed, because the digests are those of
# the published artefacts. Exits non-zero if any digest does not resolve, so
# notes are never written with a missing digest.
set -euo pipefail

version="${1:-}"
changelog="${2:-CHANGELOG.md}"
if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: $0 X.Y.Z [CHANGELOG.md]" >&2
  exit 2
fi
minor="${version%.*}"
repo="https://github.com/NVIDIA/k8s-test-infra"

image_digest() {
  local d
  d="$(docker buildx imagetools inspect "$1" --format '{{json .Manifest}}' | jq -r .digest)"
  [[ "$d" =~ ^sha256:[0-9a-f]{64}$ ]] || { echo "no digest for $1" >&2; exit 1; }
  echo "$d"
}

chart_digest() {
  local dir d
  dir="$(mktemp -d)"
  d="$(helm pull "oci://ghcr.io/nvidia/k8s-test-infra/chart/$1" --version "$version" -d "$dir" 2>&1 | awk '/^Digest:/ {print $2}')"
  rm -rf "$dir"
  [[ "$d" =~ ^sha256:[0-9a-f]{64}$ ]] || { echo "no digest for chart $1 $version" >&2; exit 1; }
  echo "$d"
}

section="$(awk -v v="## [$version]" '
  index($0, v) == 1 { found = 1; next }
  found && /^## \[/ { exit }
  found { print }
' "$changelog" | sed -e '/./,$!d')"
if ! grep -q '^- ' <<<"$section"; then
  echo "no '## [$version]' section with entries in $changelog" >&2
  exit 1
fi
compare="$(grep -m1 -F "[$version]: " "$changelog" | sed 's/^[^ ]* //')"

nvml_mock="$(image_digest "ghcr.io/nvidia/nvml-mock:$version")"
control_plane="$(image_digest "ghcr.io/nvidia/mokka-control-plane:$version")"
chart_nvml_mock="$(chart_digest nvml-mock)"
chart_crds="$(chart_digest mokka-crds)"

cat <<EOF
SUMMARY

## Artifacts

- \`ghcr.io/nvidia/nvml-mock:$version\` (\`$nvml_mock\`) and \`ghcr.io/nvidia/mokka-control-plane:$version\` (\`$control_plane\`): linux/amd64 and linux/arm64, signed with cosign (keyless), with SPDX SBOM attestations
- \`oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock\` (\`$chart_nvml_mock\`) and \`oci://ghcr.io/nvidia/k8s-test-infra/chart/mokka-crds\` (\`$chart_crds\`), version $version, signed with cosign

\`\`\`bash
helm upgrade --install nvml-mock oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock \\
  --version $version -n mokka --create-namespace
\`\`\`

\`\`\`bash
cosign verify ghcr.io/nvidia/nvml-mock:$version \\
  --certificate-identity $repo/.github/workflows/nvml-mock-publish.yaml@refs/tags/v$version \\
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
\`\`\`

This tag moves \`nvml-mock:latest\` and both images' \`$minor\` tags to $version. \`mokka-control-plane:latest\` follows \`main\` builds.

## Changelog

$section
EOF
if [ -n "$compare" ]; then
  printf '\n**Full diff**: %s\n' "$compare"
fi
