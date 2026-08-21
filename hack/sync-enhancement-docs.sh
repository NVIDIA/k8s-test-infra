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

# Stage enhancements/ into the MkDocs docs_dir so the MEPs publish on the site.
#
# MkDocs builds only what lives under docs_dir. The MEP workflow in
# enhancements/README.md tells contributors to add proposals under
# enhancements/meps/, so the site takes a build-time copy rather than moving the
# source and invalidating that workflow, along with every link already pointing
# at enhancements/ on GitHub.
#
# The copy is gitignored and the destination is deleted before every run, so a
# renamed or removed MEP cannot linger in a later build.
#
# MEP text is written to render on GitHub, so some links reach repo source
# outside enhancements/ (three levels up into pkg/ or tests/). Those have no
# target inside docs_dir and `mkdocs build --strict` fails them. This script
# rewrites them to GitHub blob URLs in the copy; the source keeps its relative
# form and stays correct on GitHub.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC_DIR="${REPO_ROOT}/enhancements"
DST_DIR="${REPO_ROOT}/docs/enhancements"

# Pinned to main, matching edit_uri in mkdocs.yml: the published site tracks
# main, so a link leaving the site should land on main too.
BLOB_URL="https://github.com/NVIDIA/k8s-test-infra/blob/main"

if [[ ! -d "${SRC_DIR}" ]]; then
  echo "ERROR: ${SRC_DIR} does not exist" >&2
  exit 1
fi

rm -rf "${DST_DIR}"
mkdir -p "${DST_DIR}"
cp -R "${SRC_DIR}/." "${DST_DIR}/"

while IFS= read -r -d '' md; do
  rel="${md#"${DST_DIR}/"}"
  dir="$(dirname "${rel}")"

  # How many ../ segments a link in this file needs to reach the repo root: one
  # per directory between the file and enhancements/, plus one for enhancements/
  # itself. A file sitting directly in enhancements/ needs one.
  if [[ "${dir}" == "." ]]; then
    depth=1
  else
    depth=$(($(tr -cd '/' <<<"${dir}" | wc -c) + 2))
  fi

  root_literal=""
  root_regex=""
  for ((i = 0; i < depth; i++)); do
    root_literal+="../"
    root_regex+='\.\./'
  done

  # Inline links `](../../../x)` and reference definitions `]: ../../../x`.
  tmp="${md}.tmp"
  sed -e "s|](${root_regex}|](${BLOB_URL}/|g" \
    -e "s|]:[[:space:]]*${root_regex}|]: ${BLOB_URL}/|g" \
    "${md}" >"${tmp}"
  command mv -f "${tmp}" "${md}"

  # Prove the rewrite landed. A sed that silently matches nothing (a bad escape,
  # a wrong depth, a delimiter collision) would leave the original text here and
  # surface much later as an opaque strict-mode failure.
  if grep -qE "\]\(${root_regex}|\]:[[:space:]]*${root_regex}" "${md}"; then
    echo "ERROR: ${rel} still links above enhancements/ (${root_literal}) after rewriting" >&2
    exit 1
  fi
done < <(find "${DST_DIR}" -type f -name '*.md' -print0)

echo "staged $(find "${DST_DIR}" -type f -name '*.md' | wc -l | tr -d ' ') MEP pages into docs/enhancements/"
