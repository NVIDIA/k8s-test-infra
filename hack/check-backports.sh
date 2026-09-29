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
# Report which PRs marked for backport have not reached their release branch.
#
# While a release candidate is open, main keeps taking new work and the release
# line lives on release-X.Y. A fix that belongs in the open release is marked
# two ways, either of which counts:
#
#   - the `backport/release-X.Y` label on the PR
#   - the line "This PR SHOULD BE BACKPORTED TO release-X.Y BRANCH" in its body
#
# A marked PR has landed on the release branch when a commit there carries
# `(cherry picked from commit <main-sha>)`, which `git cherry-pick -x` writes.
#
# Usage:  hack/check-backports.sh [release-branch] [remote]
# Exit:   0 nothing outstanding, 1 outstanding backports, 2 usage/environment
set -uo pipefail

BRANCH="${1:-release-0.4}"
REMOTE="${2:-upstream}"
REPO="${REPO:-NVIDIA/k8s-test-infra}"

command -v gh  >/dev/null 2>&1 || { echo "error: gh is required" >&2; exit 2; }
command -v jq  >/dev/null 2>&1 || { echo "error: jq is required" >&2; exit 2; }

git rev-parse --verify "${REMOTE}/${BRANCH}" >/dev/null 2>&1 || {
    echo "error: ${REMOTE}/${BRANCH} not found; fetch it first" >&2; exit 2; }
git rev-parse --verify "${REMOTE}/main" >/dev/null 2>&1 || {
    echo "error: ${REMOTE}/main not found" >&2; exit 2; }

# Where the release line diverged from main. Everything after this on main is a
# backport candidate; everything before it is already in the release.
BASE=$(git merge-base "${REMOTE}/main" "${REMOTE}/${BRANCH}") || exit 2

# Commits already cherry-picked onto the release branch, by their origin sha.
PICKED=$(git log "${BASE}..${REMOTE}/${BRANCH}" --format=%B \
         | sed -n 's/.*cherry picked from commit \([0-9a-f]\{7,40\}\).*/\1/p')

label="backport/${BRANCH}"
marker="SHOULD BE BACKPORTED TO ${BRANCH} BRANCH"

# Marked PRs, merged, whose merge commit is on main after the divergence point.
# gh's --jq takes only a filter, so pipe to real jq where --arg is available.
prs=$(gh pr list --repo "$REPO" --state merged --limit 200 \
        --json number,title,mergeCommit,labels,body \
      | jq -r --arg l "$label" --arg m "$marker" '
          .[] | select(.mergeCommit.oid != null)
              | select(
                  ([.labels[].name] | index($l)) != null
                  or ((.body // "") | ascii_upcase | contains($m | ascii_upcase))
                )
              | "\(.number)\t\(.mergeCommit.oid)\t\(.title)"') || exit 2

outstanding=0; landed=0
printf '%-7s %-10s %s\n' "PR" "SHA" "STATUS / TITLE"
while IFS=$'\t' read -r num sha title; do
    [ -z "${num:-}" ] && continue
    # only consider commits that are on main after the branch point
    git merge-base --is-ancestor "$sha" "${REMOTE}/main" 2>/dev/null || continue
    git merge-base --is-ancestor "$sha" "$BASE" 2>/dev/null && continue

    if echo "$PICKED" | grep -q "^${sha:0:9}" || echo "$PICKED" | grep -qF "$sha"; then
        landed=$((landed+1))
        printf '%-7s %-10s %s\n' "#$num" "${sha:0:9}" "landed    ${title:0:56}"
    else
        outstanding=$((outstanding+1))
        printf '%-7s %-10s %s\n' "#$num" "${sha:0:9}" "MISSING   ${title:0:56}"
    fi
done <<< "$prs"

echo
echo "release branch : ${REMOTE}/${BRANCH}"
echo "diverged at    : ${BASE:0:9}"
echo "landed         : $landed"
echo "outstanding    : $outstanding"

if [ "$outstanding" -gt 0 ]; then
    echo
    echo "Cherry-pick each MISSING commit onto ${BRANCH}, preserving provenance:"
    echo "  git cherry-pick -x -s -S <sha>"
    exit 1
fi
exit 0
