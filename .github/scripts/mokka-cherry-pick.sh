#!/usr/bin/env bash
# Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.

set -Eeuo pipefail

readonly repository="NVIDIA/k8s-test-infra"
readonly repository_id="733665780"
readonly target_branch="main"
readonly bot_name="mokka[bot]"
readonly bot_email="mokka[bot]@users.noreply.github.com"

die() {
  printf '%s\n' "$1" >&2
  exit 1
}

inputs="$(jq -cers '
  if length != 1 then error("invalid event document count") else .[0] end |
  .inputs as $inputs |
  if type != "object" or ($inputs | type) != "object" then error("invalid inputs")
  elif ($inputs | keys | sort) != ["action_id", "pull_request_number", "source_sha", "target_branch"] then error("unexpected inputs")
  elif ($inputs | to_entries | all(.value | type == "string")) | not then error("non-string input")
  elif ($inputs.pull_request_number | test("\\A[1-9][0-9]*\\z") | not) then error("invalid pull request number")
  elif (($inputs.pull_request_number | length) > 10 or (($inputs.pull_request_number | length) == 10 and $inputs.pull_request_number > "2147483647")) then error("invalid pull request number")
  elif ($inputs.source_sha | test("\\A[0-9a-f]{40}\\z") | not) then error("invalid source SHA")
  elif $inputs.target_branch != "main" then error("invalid target branch")
  elif ($inputs.action_id | test("\\A[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\\z") | not) then error("invalid action ID")
  else $inputs end
' "${GITHUB_EVENT_PATH:?GITHUB_EVENT_PATH is required}")" || die "invalid event inputs"

pull_request_number="$(jq -er '.pull_request_number' <<<"$inputs")" || die "invalid pull request number"
source_sha="$(jq -er '.source_sha' <<<"$inputs")" || die "invalid source SHA"
requested_target="$(jq -er '.target_branch' <<<"$inputs")" || die "invalid target branch"
action_id="$(jq -er '.action_id' <<<"$inputs")" || die "invalid action ID"

[[ "$pull_request_number" =~ ^[1-9][0-9]*$ ]] || die "invalid pull request number"
awk -v value="$pull_request_number" 'BEGIN { exit !(length(value) < 10 || (length(value) == 10 && sprintf("x%s", value) <= "x2147483647")) }' || die "invalid pull request number"
[[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || die "invalid source SHA"
[[ "$requested_target" == "$target_branch" ]] || die "invalid target branch"
[[ "$action_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]] || die "invalid action ID"
[[ "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}" == "$repository" ]] || die "invalid repository"
[[ "${GITHUB_REPOSITORY_ID:?GITHUB_REPOSITORY_ID is required}" == "$repository_id" ]] || die "invalid repository ID"
[[ "${GITHUB_WORKFLOW_SHA:?GITHUB_WORKFLOW_SHA is required}" =~ ^[0-9a-f]{40}$ ]] || die "invalid workflow SHA"

source_pull_request="$(gh api "/repos/$repository/pulls/$pull_request_number")" || die "source pull request lookup failed"
jq -es --arg repository "$repository" --arg source_sha "$source_sha" '
  length == 1 and (.[0] |
    type == "object" and
    (.commits | type) == "number" and .commits == 1 and
    .state == "closed" and .merged == true and .merge_commit_sha == $source_sha and
    .head.repo.full_name == $repository and .base.repo.full_name == $repository)
' >/dev/null <<<"$source_pull_request" || die "source pull request is not eligible"

source_commit="$(gh api "/repos/$repository/commits/$source_sha")" || die "source commit lookup failed"
jq -es --arg source_sha "$source_sha" '
  length == 1 and (.[0] |
    type == "object" and
    (.sha | type) == "string" and .sha == $source_sha and (.sha | test("\\A[0-9a-f]{40}\\z")) and
    (.parents | type) == "array" and (.parents | length) == 1 and
    (.parents[0] | type) == "object" and
    (.parents[0].sha | type) == "string" and (.parents[0].sha | test("\\A[0-9a-f]{40}\\z")))
' >/dev/null <<<"$source_commit" || die "source commit must match the requested SHA and have one canonical parent"

head_branch="mokka/cherry-pick/$action_id"
if branch_result="$(gh api --include "/repos/$repository/git/ref/heads/$head_branch" 2>&1)"; then
  die "derived branch already exists"
fi
[[ "$branch_result" =~ ^HTTP/[0-9.]+[[:space:]]404([[:space:]]|$) ]] || die "derived branch lookup failed"

existing_pull_requests="$(gh api "/repos/$repository/pulls?state=all&per_page=1&head=NVIDIA:$head_branch&base=$target_branch")" || die "derived pull request lookup failed"
jq -es 'length == 1 and (.[0] | type == "array" and length == 0)' >/dev/null <<<"$existing_pull_requests" || die "derived pull request already exists"

target_base_sha="$(git rev-parse HEAD)"
git config user.name "$bot_name"
git config user.email "$bot_email"
git fetch origin "$source_sha"
if ! git cherry-pick "$source_sha"; then
  git cherry-pick --abort
  die "cherry-pick conflict"
fi
git show -s --format=%B HEAD |
  awk '
    tolower($0) ~ /^mokka-source-sha:/ || tolower($0) ~ /^mokka-action-id:/ { skip_continuation = 1; next }
    /^[[:space:]]/ && skip_continuation { next }
    { skip_continuation = 0; print }
  ' |
  git commit --amend --file - \
  --trailer "Mokka-Source-SHA: $source_sha" \
  --trailer "Mokka-Action-ID: $action_id"
produced_head_sha="$(git rev-parse HEAD)"
# The empty expected value makes this an atomic create-only operation: the
# server rejects the push if a concurrent actor creates the derived branch.
git push --porcelain --atomic --force-with-lease="refs/heads/$head_branch:" origin "HEAD:refs/heads/$head_branch"

title="Mokka: cherry-pick #$pull_request_number to $target_branch"
created_pull_request="$(gh api --method POST "/repos/$repository/pulls" \
  --raw-field "title=$title" \
  --raw-field "head=$head_branch" \
  --raw-field "base=$target_branch" \
  -F draft=true \
  --raw-field "body=<!-- mokka-cherry-pick-action-id: $action_id -->")" || die "pull request creation failed"
jq -es --arg repository "$repository" --arg head_branch "$head_branch" --arg produced_head_sha "$produced_head_sha" '
  length == 1 and (.[0] |
    type == "object" and
    (.number as $number |
      ($number | type) == "number" and
      $number >= 1 and $number <= 2147483647 and $number == ($number | floor) and
      .html_url == ("https://github.com/" + $repository + "/pull/" + ($number | tostring))) and
    .draft == true and .head.ref == $head_branch and .head.sha == $produced_head_sha and .base.ref == "main")
' >/dev/null <<<"$created_pull_request" || die "pull request response is not the requested draft"
pull_request_number_created="$(jq -er '.number | tostring' <<<"$created_pull_request")" || die "pull request response missing number"
pull_request_url="$(jq -er '.html_url' <<<"$created_pull_request")" || die "pull request response missing URL"
[[ "$pull_request_number_created" =~ ^[1-9][0-9]*$ ]] || die "pull request response has invalid number"
awk -v value="$pull_request_number_created" 'BEGIN { exit !(length(value) < 10 || (length(value) == 10 && sprintf("x%s", value) <= "x2147483647")) }' || die "pull request response has invalid number"
[[ "$pull_request_url" == "https://github.com/$repository/pull/$pull_request_number_created" ]] || die "pull request response has invalid URL"

evidence_payload="$(printf 'action_id: %s\nsource_pull_request: %s\nsource_sha: %s\ntarget_branch: %s\ntarget_base_sha: %s\nproduced_head_sha: %s\nworkflow_commit_sha: %s\nhead_branch: %s\npull_request_url: %s\n' \
  "$action_id" "$pull_request_number" "$source_sha" "$target_branch" "$target_base_sha" "$produced_head_sha" "$GITHUB_WORKFLOW_SHA" "$head_branch" "$pull_request_url")"
evidence_digest="$(printf '%s' "$evidence_payload" | shasum -a 256 | awk '{print $1}')"
printf -v evidence '<!-- mokka-cherry-pick-evidence/v1\n%s\nsha256: %s\n-->\n' "$evidence_payload" "$evidence_digest"
if ! gh api --method PATCH "/repos/$repository/pulls/$pull_request_number_created" --raw-field "body=$evidence" >/dev/null; then
  printf 'MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION action_id=%s pull_request_url=%s\n' "$action_id" "$pull_request_url" >&2
  exit 1
fi
