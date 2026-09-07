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
# Resolves every in-repo markdown link against the working tree.
#
# `mkdocs build --strict` only sees docs/, which is a third of the markdown in
# this repo, and it cannot follow a link written as an absolute github.com URL.
# Those two blind spots overlap exactly with where links rot: docs/ pages point
# at in-tree files through absolute self-links because a relative one would
# leave docs_dir and fail the strict build. This closes both.
#
# External URLs are not fetched. This checks only what the working tree can
# prove, so it stays offline and deterministic.

set -euo pipefail

readonly SELF_URL_PREFIX="https://github.com/NVIDIA/k8s-test-infra"

repo_root="$(git rev-parse --show-toplevel)" || {
  echo "ERROR: not inside a git repository" >&2
  exit 1
}
cd "${repo_root}"

# Findings go to a file because the scan runs in a pipeline subshell, so a
# counter incremented inside it would not survive to the exit status.
findings="$(mktemp)"
trap 'rm -f "${findings}"' EXIT

# git ls-files rather than find: gitignored scratch directories such as
# docs/plans/ are not part of the repo and must not fail a contributor's lint.
git ls-files '*.md' | while IFS= read -r file; do
  dir="$(dirname "${file}")"

  # -o prints one match per line and -n prefixes each with its line number, so
  # several links on one line are still reported individually. `|| true`
  # because grep exits 1 on a file with no links, which pipefail would
  # otherwise turn into a failure of the whole scan.
  { grep -n -o ']([^)]*)' "${file}" || true; } | while IFS= read -r hit; do
    line="${hit%%:*}"
    target="${hit#*:](}"
    target="${target%)}"
    # Drop an optional link title: [text](path "title").
    target="${target%% *}"

    case "${target}" in
      '' | '#'* | mailto:*)
        continue
        ;;
      "${SELF_URL_PREFIX}"/blob/* | "${SELF_URL_PREFIX}"/tree/*)
        # A self-link names a path in this repo, so the working tree can
        # verify it. Strip the URL prefix, the blob/tree segment and the ref.
        rel="${target#"${SELF_URL_PREFIX}"/}"
        rel="${rel#blob/}"
        rel="${rel#tree/}"
        rel="${rel#*/}"
        resolved="${rel%%#*}"
        ;;
      http://* | https://*)
        continue
        ;;
      *)
        resolved="${dir}/${target%%#*}"
        ;;
    esac

    [ -z "${resolved}" ] && continue

    if [ ! -e "${resolved}" ]; then
      printf '%s:%s: %s\n' "${file}" "${line}" "${target}" >>"${findings}"
    fi
  done
done

if [ -s "${findings}" ]; then
  echo "ERROR: markdown links that do not resolve in the working tree:" >&2
  sed 's/^/  /' "${findings}" >&2
  echo "" >&2
  echo "Fix the path, or drop the link if its target was deleted." >&2
  exit 1
fi

echo "OK: every in-repo markdown link resolves"
