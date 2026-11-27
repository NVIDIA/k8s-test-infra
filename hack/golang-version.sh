#!/bin/bash
# Copyright 2025 NVIDIA CORPORATION
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

set -euo pipefail

SCRIPTS_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )"/../hack && pwd )"

DOCKERFILE_ROOT=${SCRIPTS_DIR}/../deployments/devel

# Take the tag verbatim from the only `FROM golang:` line, stopping at the
# variant suffix (`-bookworm`, `-alpine`) so only the toolchain version remains.
#
# Matching digit runs instead (`grep -oE "[0-9\.]+"`) breaks on prerelease tags:
# `1.27rc1` matches twice, and the result reaches the caller as "1.27 1".
# Consumers require one whitespace-free token — variables.yaml writes it into
# $GITHUB_OUTPUT and run.sh passes it as a --build-arg.
DOCKERFILE="${DOCKERFILE_ROOT}/Dockerfile"
if [[ ! -f "${DOCKERFILE}" ]]; then
  echo "error: Go Dockerfile not found: ${DOCKERFILE}" >&2
  exit 1
fi

golang_from_lines=()
while IFS= read -r line; do
  golang_from_lines[${#golang_from_lines[@]}]="${line}"
done < <(grep -E '^FROM[[:space:]]+golang:' "${DOCKERFILE}" || true)
if (( ${#golang_from_lines[@]} != 1 )); then
  echo "error: expected exactly one FROM golang line in ${DOCKERFILE}, found ${#golang_from_lines[@]}" >&2
  exit 1
fi

if [[ "${golang_from_lines[0]}" =~ ^FROM[[:space:]]+golang:([^[:space:]-]+) ]]; then
  GOLANG_VERSION="${BASH_REMATCH[1]}"
else
  echo "error: could not parse the Go version from ${DOCKERFILE}" >&2
  exit 1
fi

if [[ ! "${GOLANG_VERSION}" =~ ^[0-9]+\.[0-9]+((beta|rc)[0-9]+|([.][0-9]+)((beta|rc)[0-9]+)?)?$ ]]; then
  echo "error: invalid Go version '${GOLANG_VERSION}' in ${DOCKERFILE}" >&2
  exit 1
fi

echo "$GOLANG_VERSION"
