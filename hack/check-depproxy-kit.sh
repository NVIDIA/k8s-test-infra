#!/bin/bash
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
#
# Verifies the vendored DGXC Go proxy adoption kit still matches upstream.
# An in-place edit would fork us from NVIDIA-dev/dgxc-depproxy silently; this
# makes it loud. See .github/actions/setup-dgxc-goproxy/UPSTREAM.md.

set -euo pipefail

readonly KIT_DIR=".github/actions/setup-dgxc-goproxy"
readonly MANIFEST="${KIT_DIR}/MANIFEST.sha256"

# The manifest also lists kit files this repo deliberately does not carry, so the
# digest check needs --ignore-missing. That flag errors only when *every* listed
# file is absent, so a deleted action would pass it; these are asserted present
# separately.
readonly VENDORED_FILES=(
  "${KIT_DIR}/action.yml"
  "${KIT_DIR}/oidc-exchange.sh"
)

repo_root="$(git rev-parse --show-toplevel)" || {
  echo "ERROR: not inside a git repository" >&2
  exit 1
}
cd "${repo_root}"

if [ ! -f "${MANIFEST}" ]; then
  echo "ERROR: ${MANIFEST} is missing; the vendored kit cannot be verified" >&2
  exit 1
fi

missing=0
for f in "${VENDORED_FILES[@]}"; do
  if [ ! -f "${f}" ]; then
    echo "ERROR: vendored kit file is missing: ${f}" >&2
    missing=1
  fi
done
if [ "${missing}" -ne 0 ]; then
  echo "Restore it from the upstream release; see ${KIT_DIR}/UPSTREAM.md" >&2
  exit 1
fi

# Manifest paths are repo-root relative, hence the cd above.
if ! shasum -a 256 -c "${MANIFEST}" --ignore-missing >/dev/null 2>&1; then
  echo "ERROR: the vendored DGXC Go proxy kit does not match upstream digests" >&2
  shasum -a 256 -c "${MANIFEST}" --ignore-missing >&2 || true
  echo "" >&2
  echo "The kit is copied verbatim and must not be edited in place." >&2
  echo "To move to a new upstream version, follow ${KIT_DIR}/UPSTREAM.md" >&2
  exit 1
fi

echo "OK: the vendored DGXC Go proxy kit matches upstream digests"
