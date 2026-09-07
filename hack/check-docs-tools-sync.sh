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

# Keep docs/tools/ and cmd/ in agreement.
#
# The failure this exists to catch: a binary is deleted or renamed and its
# reference page is left behind, or a new binary lands with no page at all.
# Neither shows up in a docs build, because a page nothing links to still
# renders and a missing page is not a broken link. The drift is only visible
# when a reader follows the documentation and finds the binary is not there.
#
# A binary may be documented either at docs/tools/<name>.md or at the top level
# as docs/<name>.md, which is where the pages aimed at operators rather than at
# contributors live.

set -euo pipefail
shopt -s nullglob

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

orphan_pages=()
for page in docs/tools/*.md; do
  name="$(basename "${page}" .md)"
  if [ "${name}" = "README" ]; then
    continue
  fi
  if [ ! -d "cmd/${name}" ]; then
    orphan_pages+=("${page}")
  fi
done

undocumented=()
for dir in cmd/*/; do
  name="$(basename "${dir}")"
  if [ ! -f "docs/tools/${name}.md" ] && [ ! -f "docs/${name}.md" ]; then
    undocumented+=("${name}")
  fi
done

status=0

if [ "${#orphan_pages[@]}" -gt 0 ]; then
  echo "ERROR: documented under docs/tools/ but no matching cmd/<name>:" >&2
  printf '  %s\n' "${orphan_pages[@]}" >&2
  status=1
fi

if [ "${#undocumented[@]}" -gt 0 ]; then
  echo "ERROR: present in cmd/ but documented in neither docs/tools/<name>.md nor docs/<name>.md:" >&2
  printf '  %s\n' "${undocumented[@]}" >&2
  status=1
fi

if [ "${status}" -ne 0 ]; then
  exit "${status}"
fi

echo "docs/tools is in sync with cmd/"
