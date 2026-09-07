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

# Keep the component reference pages and cmd/ in agreement.
#
# The failure this exists to catch: a binary is deleted or renamed and its
# reference page is left behind, or a new binary lands with no page at all.
# Neither shows up in a docs build, because a page nothing links to still
# renders and a missing page is not a broken link. The drift is only visible
# when a reader follows the documentation and finds the binary is not there.

set -euo pipefail
shopt -s nullglob

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# Binaries documented at the top level instead of under docs/tools/, because
# they are aimed at operators rather than at contributors. Listed explicitly
# because docs/ is full of pages that are about no binary at all, so it cannot
# be scanned the way docs/tools/ can. Both directions are checked against this
# list, so a page here that outlives its binary fails too.
top_level_pages=(nvml-mock-ctl)

# is_component reports whether cmd/<name> builds a binary. Keyed on a main.go
# rather than on the directory, because `make build` finds its packages the
# same way and a helper package under cmd/ produces nothing to document.
#
# A nested cmd/foo/bar/main.go is deliberately not handled: `make build` names
# that binary foo-bar, which this would look for at docs/tools/foo.md. Nothing
# under cmd/ is nested today, and guessing at the shape of a layout nobody has
# adopted would be the wrong thing to encode.
is_component() {
  [ -n "$(find "cmd/$1" -name main.go -print -quit 2>/dev/null)" ]
}

# has_top_level_page reports whether name is registered above AND its page is
# on disk. Registration is required so that adding docs/<name>.md is a visible
# edit here, which is what earns that page the reverse check below.
has_top_level_page() {
  local name="$1" listed
  # Guarded because bash 3.2, which is what macOS ships, treats "${empty[@]}"
  # under `set -u` as an unbound variable and aborts.
  if [ "${#top_level_pages[@]}" -eq 0 ]; then
    return 1
  fi
  for listed in "${top_level_pages[@]}"; do
    if [ "${listed}" = "${name}" ]; then
      [ -f "docs/${name}.md" ]
      return
    fi
  done
  return 1
}

orphan_pages=()
for page in docs/tools/*.md; do
  name="$(basename "${page}" .md)"
  if [ "${name}" = "README" ]; then
    continue
  fi
  if ! is_component "${name}"; then
    orphan_pages+=("${page}")
  fi
done

undocumented=()
for dir in cmd/*/; do
  name="$(basename "${dir}")"
  if ! is_component "${name}"; then
    continue
  fi
  if [ ! -f "docs/tools/${name}.md" ] && ! has_top_level_page "${name}"; then
    undocumented+=("${name}")
  fi
done

stale_top_level=()
missing_top_level=()
if [ "${#top_level_pages[@]}" -gt 0 ]; then
  for name in "${top_level_pages[@]}"; do
    if ! is_component "${name}"; then
      stale_top_level+=("${name}")
    elif [ ! -f "docs/${name}.md" ]; then
      missing_top_level+=("${name}")
    fi
  done
fi

status=0

if [ "${#orphan_pages[@]}" -gt 0 ]; then
  echo "ERROR: documented under docs/tools/ but no cmd/<name> builds them:" >&2
  printf '  %s\n' "${orphan_pages[@]}" >&2
  status=1
fi

if [ "${#undocumented[@]}" -gt 0 ]; then
  echo "ERROR: built from cmd/ but documented nowhere. Add docs/tools/<name>.md," >&2
  echo "       or add the name to top_level_pages in $0 and write docs/<name>.md:" >&2
  printf '  %s\n' "${undocumented[@]}" >&2
  status=1
fi

if [ "${#stale_top_level[@]}" -gt 0 ]; then
  echo "ERROR: listed in top_level_pages with no cmd/<name> behind them." >&2
  echo "       Delete docs/<name>.md and the list entry:" >&2
  printf '  %s\n' "${stale_top_level[@]}" >&2
  status=1
fi

if [ "${#missing_top_level[@]}" -gt 0 ]; then
  echo "ERROR: listed in top_level_pages but docs/<name>.md is absent:" >&2
  printf '  %s\n' "${missing_top_level[@]}" >&2
  status=1
fi

if [ "${status}" -ne 0 ]; then
  exit "${status}"
fi

echo "component pages are in sync with cmd/"
