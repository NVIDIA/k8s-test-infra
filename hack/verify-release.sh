#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

# Verify what a release published: the publish runs for its tag, both images
# and their X.Y and latest tags, both charts' version and appVersion, and every
# signature and SBOM attestation. Read-only: it never pushes, tags or writes
# anything remote.
#
# Usage: hack/verify-release.sh X.Y.Z
#   make verify-release RELEASE_VERSION=0.5.0
#
# Pass the version unprefixed, as image and chart tags carry it; the git tag
# is v<version>. Every check runs and prints one `ok:` or `FAIL:` line, so the
# output can be pasted into the release issue as the record. The exit code is 1
# if any check failed, and 2 if the script could not run at all. A failing
# tool's last output lines go to stderr; a passing tool's output is dropped.
set -uo pipefail

usage() {
  echo "usage: $0 X.Y.Z (the release version without the leading v, e.g. 0.5.0)" >&2
  exit 2
}
[ $# -eq 1 ] && [[ $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || usage

missing=""
for tool in gh docker helm cosign jq; do
  command -v "$tool" > /dev/null || missing="$missing $tool"
done
if [ -n "$missing" ]; then
  echo "$0: missing required tools:$missing" >&2
  exit 2
fi

V=$1
TAG=v$V
XY=${V%.*}
REPO=NVIDIA/k8s-test-infra
ISSUER=https://token.actions.githubusercontent.com
NVML=ghcr.io/nvidia/nvml-mock
CP=ghcr.io/nvidia/mokka-control-plane
CHARTS=ghcr.io/nvidia/k8s-test-infra/chart

# The template is explicit because BSD mktemp ignores TMPDIR without one.
scratch=$(mktemp -d "${TMPDIR:-/tmp}/verify-release.XXXXXX") || exit 2
trap 'rm -rf "$scratch"' EXIT
out=$scratch/out
err=$scratch/err

checks=0
failed=0

ok() {
  checks=$((checks + 1))
  echo "ok: $1"
}

# fail <what> <expected> <actual>
fail() {
  checks=$((checks + 1))
  failed=$((failed + 1))
  echo "FAIL: $1: $2 vs $3"
}

# tool_error <command>: the failing tool's last lines, kept off stdout so the
# report stays one line per check.
tool_error() {
  {
    echo "$1 failed:"
    tail -n 5 "$err" | sed 's/^/  /'
  } >&2
}

# The publish workflows sign with the keyless identity of their own file at the
# release tag, so each artefact is verified against the workflow that built it.
identity() {
  echo "https://github.com/$REPO/.github/workflows/$1@refs/tags/$TAG"
}

# inspect <ref>: print the image index JSON, or fail if the ref does not resolve.
inspect() {
  docker buildx imagetools inspect --format '{{json .Manifest}}' "$1" 2> "$err"
}

index_digest() {
  jq -r '.digest // empty' <<< "$1"
}

# The unknown/unknown entries buildx adds hold attestations, not platforms.
index_platforms() {
  jq -r '[.manifests[]? | .platform | select(. != null and .os != "unknown") | "\(.os)/\(.architecture)"] | unique | join(" ")' <<< "$1"
}

# platform_digest <index JSON> <arch>
platform_digest() {
  jq -r --arg arch "$2" 'first(.manifests[]? | select(.platform.os == "linux" and .platform.architecture == $arch) | .digest) // empty' <<< "$1"
}

# 1. Publish runs. Looked up by workflow file, not display name: the names
# changed between releases (v0.4.1 ran "control-plane Publish").
for workflow in nvml-mock-publish.yaml mokka-control-plane-publish.yaml helm-publish.yaml; do
  what="publish run $workflow for $TAG"
  if ! gh run list --repo "$REPO" --workflow "$workflow" --branch "$TAG" --json conclusion,databaseId,createdAt > "$out" 2> "$err"; then
    tool_error "gh run list --workflow $workflow"
    fail "$what" success "gh run list failed"
    continue
  fi
  latest=$(jq -r 'sort_by(.createdAt) | last | if . == null then empty else "\(.databaseId) \(.conclusion)" end' "$out")
  run=${latest%% *}
  conclusion=${latest#* }
  if [ -z "$latest" ]; then
    fail "$what" success "no run"
  elif [ "$conclusion" = success ]; then
    ok "$what succeeded (run $run)"
  else
    fail "$what" success "${conclusion:-no conclusion yet} (run $run)"
  fi
done

# 2. Both images resolve to a multi-arch index with linux/amd64 and linux/arm64.
# check_platforms <ref>: sets index to the index JSON, empty if unresolved.
check_platforms() {
  local platforms
  if ! index=$(inspect "$1"); then
    tool_error "docker buildx imagetools inspect $1"
    index=""
    fail "$1 platforms" "linux/amd64 linux/arm64" "does not resolve"
    return
  fi
  platforms=$(index_platforms "$index")
  if [[ " $platforms " == *" linux/amd64 "* && " $platforms " == *" linux/arm64 "* ]]; then
    ok "$1 lists linux/amd64 and linux/arm64 ($(index_digest "$index"))"
  else
    fail "$1 platforms" "linux/amd64 linux/arm64" "${platforms:-no platform manifests}"
  fi
}
check_platforms "$NVML:$V"
nvml_index=$index
check_platforms "$CP:$V"
cp_index=$index

# 3. The moving tags point at the release. mokka-control-plane:latest is not
# compared: it follows main builds, and a tag run does not move it.
# check_digest <ref> <index JSON of the release tag>
check_digest() {
  local want got json
  want=$(index_digest "$2")
  if ! json=$(inspect "$1"); then
    tool_error "docker buildx imagetools inspect $1"
    json=""
  fi
  got=$(index_digest "$json")
  if [ -z "$want" ]; then
    fail "$1 digest" "the digest of $V" "$V does not resolve"
  elif [ -z "$got" ]; then
    fail "$1 digest" "$want" "does not resolve"
  elif [ "$got" = "$want" ]; then
    ok "$1 has the digest of $V"
  else
    fail "$1 digest" "$want" "$got"
  fi
}
check_digest "$NVML:$XY" "$nvml_index"
check_digest "$CP:$XY" "$cp_index"
check_digest "$NVML:latest" "$nvml_index"

# 4. The tag sets both chart fields at publish time; a mismatch means the
# chart shipped with the version from Chart.yaml instead.
for chart in nvml-mock mokka-crds; do
  what="chart $chart $V version/appVersion"
  if ! helm show chart "oci://$CHARTS/$chart" --version "$V" > "$out" 2> "$err"; then
    tool_error "helm show chart $chart"
    fail "$what" "$V/$V" "helm show chart failed"
    continue
  fi
  version=$(sed -n 's/^version:[[:space:]]*//p' "$out" | tr -d "\"'")
  app_version=$(sed -n 's/^appVersion:[[:space:]]*//p' "$out" | tr -d "\"'")
  if [ "$version" = "$V" ] && [ "$app_version" = "$V" ]; then
    ok "chart $chart $V has version and appVersion $V"
  else
    fail "$what" "$V/$V" "$version/$app_version"
  fi
done

# 5. Signatures. Each image tag resolves to its multi-arch index, which is the
# digest the workflow signs.
# verify <ref> <workflow>
verify() {
  if cosign verify --certificate-identity "$(identity "$2")" --certificate-oidc-issuer "$ISSUER" "$1" > "$out" 2> "$err"; then
    ok "cosign verify $1, signed by $2"
  else
    tool_error "cosign verify $1"
    fail "cosign verify $1" "signature from $2@refs/tags/$TAG" "verification failed"
  fi
}
verify "$NVML:$V" nvml-mock-publish.yaml
verify "$CP:$V" mokka-control-plane-publish.yaml
verify "$CHARTS/nvml-mock:$V" helm-publish.yaml
verify "$CHARTS/mokka-crds:$V" helm-publish.yaml

# 6. SBOM attestations: nvml-mock attaches its SBOM to the index, the control
# plane attaches one per platform image.
# attest <ref> <workflow> [<platform>]
attest() {
  local what="cosign verify-attestation spdxjson $1${3:+ ($3)}"
  if cosign verify-attestation --type spdxjson --certificate-identity "$(identity "$2")" --certificate-oidc-issuer "$ISSUER" "$1" > "$out" 2> "$err"; then
    ok "$what"
  else
    tool_error "cosign verify-attestation $1"
    fail "$what" "SBOM attestation from $2@refs/tags/$TAG" "verification failed"
  fi
}
attest "$NVML:$V" nvml-mock-publish.yaml
for arch in amd64 arm64; do
  digest=$(platform_digest "$cp_index" "$arch")
  if [ -n "$digest" ]; then
    attest "$CP@$digest" mokka-control-plane-publish.yaml "linux/$arch"
  else
    fail "cosign verify-attestation spdxjson $CP:$V (linux/$arch)" "a linux/$arch digest" "none in the index"
  fi
done

if [ "$failed" -eq 0 ]; then
  echo "summary: $checks of $checks checks passed for $TAG"
  exit 0
fi
echo "summary: $failed of $checks checks failed for $TAG"
exit 1
