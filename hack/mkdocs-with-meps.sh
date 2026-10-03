#!/usr/bin/env bash
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
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
destination="$root/docs/meps"
stamp="$root/.meps-stage-stamp.$$"

if [[ -e "$destination" ]]; then
  echo "ERROR: refusing to overwrite existing generated directory: $destination" >&2
  exit 1
fi

cleanup() {
  rm -rf -- "$destination"
  rm -f -- "$stamp"
}
trap cleanup EXIT

stage_meps() {
  rm -rf -- "$destination"
  mkdir -p "$destination"
  cp -R "$root/enhancements/meps/." "$destination/"

  # MEPs are browsable from the repository and from the generated site. A link
  # from an enhancement directory to docs/ needs three parent traversals in
  # the source tree, but only two after staging below docs/meps/.
  find "$destination" -type f -name '*.md' -exec perl -pi -e \
    's#\(\.\./\.\./\.\./docs/#(../../#g' {} +
  find "$destination" -type f -name '*.md' -exec perl -pi -e \
    's#\(\.\./\.\./\.\./local/#(https://github.com/NVIDIA/k8s-test-infra/blob/main/local/#g' {} +
  touch "$stamp"
}

stage_meps
cd "$root"

if [[ "${1:-}" == "serve" ]]; then
  # MkDocs watches the generated tree so changes copied from enhancements/meps
  # trigger reloads. The polling synchronizer also removes staged files when a
  # source proposal is deleted, without requiring another documentation tool.
  sync_meps() {
    while [[ -e "$stamp" ]]; do
      source_count=$(find "$root/enhancements/meps" -type f | wc -l | tr -d ' ')
      staged_count=$(find "$destination" -type f | wc -l | tr -d ' ')
      if [[ "$source_count" != "$staged_count" ]] ||
        find "$root/enhancements/meps" -type f -newer "$stamp" -print -quit | grep -q .; then
        stage_meps
      fi
      sleep 1
    done
  }

  sync_meps &
  sync_pid=$!
  set +e
  "${MKDOCS:-mkdocs}" "$@" --watch "$destination"
  status=$?
  set -e
  kill "$sync_pid" 2>/dev/null || true
  wait "$sync_pid" 2>/dev/null || true
  exit "$status"
fi

cd "$root"
"${MKDOCS:-mkdocs}" "$@"
