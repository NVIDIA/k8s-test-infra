/**
 * Copyright 2026 NVIDIA CORPORATION
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

"use strict";

const { COMMANDS, InputError, parseInputs, parseNumber } = require("./inputs.js");

// Applies one Prow command that the Mokka agent dispatched, with the GitHub
// calls Prow's client makes (kubernetes-sigs/prow pkg/github/client.go):
// AssignIssue and UnassignIssue compare the returned assignees, RequestReview
// retries one reviewer at a time after a 422, UnrequestReview compares the
// remaining requested reviewers, and pull requests change state and title
// through the pulls API. The agent has already decided that the requester may
// use the command. A refusal gets one comment and fails the run. A close or
// reopen gets Prow's reply naming the requester; nothing else is written.

// Statuses GitHub uses to refuse a request. Anything else, such as a 5xx, a
// rate limit or a network failure, fails the run without a comment.
const REFUSAL_STATUSES = new Set([403, 404, 410, 422]);
const MAX_DETAIL_LENGTH = 200;

class Refusal extends Error {}

const APPLIED = { status: "applied" };

function codeList(logins) {
  return logins.map((login) => `\`${login}\``).join(", ");
}

function missing(wanted, present) {
  const have = new Set(present.map((login) => login.toLowerCase()));
  return wanted.filter((login) => !have.has(login.toLowerCase()));
}

function remaining(wanted, present) {
  const have = new Set(present.map((login) => login.toLowerCase()));
  return wanted.filter((login) => have.has(login.toLowerCase()));
}

// GitHub's message goes inside a code span on one line, so it can neither
// mention anyone nor start a line the agent would read as a command.
function refusalReason(error) {
  const detail = [...String(error.message ?? "").replace(/[\s`\p{Cc}\p{Cf}\p{Zl}\p{Zp}]+/gu, " ").trim()]
    .slice(0, MAX_DETAIL_LENGTH).join("").trim();
  return `GitHub refused the request with HTTP ${error.status}${detail === "" ? "" : ` (\`${detail}\`)`}`;
}

async function requestReviews(github, target, reviewers) {
  try {
    await github.rest.pulls.requestReviewers({ ...target, reviewers });
    return [];
  } catch (error) {
    if (error?.status !== 422) throw error;
  }
  // GitHub refuses the whole request for one reviewer it cannot request.
  const refused = [];
  for (const reviewer of reviewers) {
    try {
      await github.rest.pulls.requestReviewers({ ...target, reviewers: [reviewer] });
    } catch (error) {
      if (error?.status !== 422) throw error;
      refused.push(reviewer);
    }
  }
  return refused;
}

async function apply(github, repo, request, item) {
  const issue = { ...repo, issue_number: request.number };
  const pull = { ...repo, pull_number: request.number };
  const isPull = item.pull_request != null;
  const users = request.users;
  switch (request.command) {
    case "assign": {
      const { data } = await github.rest.issues.addAssignees({ ...issue, assignees: users });
      const dropped = missing(users, (data.assignees ?? []).map(({ login }) => login));
      if (dropped.length > 0) throw new Refusal(`GitHub did not assign ${codeList(dropped)}`);
      return APPLIED;
    }
    case "unassign": {
      const { data } = await github.rest.issues.removeAssignees({ ...issue, assignees: users });
      const left = remaining(users, (data.assignees ?? []).map(({ login }) => login));
      if (left.length > 0) throw new Refusal(`GitHub left ${codeList(left)} assigned`);
      return APPLIED;
    }
    case "cc":
    case "uncc": {
      if (!isPull) {
        throw new Refusal(`review requests apply only to pull requests, and #${request.number} is an issue`);
      }
      if (request.command === "cc") {
        const refused = await requestReviews(github, pull, users);
        if (refused.length > 0) throw new Refusal(`GitHub refused a review request for ${codeList(refused)}`);
        return APPLIED;
      }
      const { data } = await github.rest.pulls.removeRequestedReviewers({ ...pull, reviewers: users });
      const left = remaining(users, (data.requested_reviewers ?? []).map(({ login }) => login));
      if (left.length > 0) throw new Refusal(`GitHub left the review request for ${codeList(left)} in place`);
      return APPLIED;
    }
    case "close":
      // Prow ignores /close on a closed item; the agent wants it answered.
      if (item.state === "closed") throw new Refusal(`#${request.number} is already closed`);
      if (isPull) await github.rest.pulls.update({ ...pull, state: "closed" });
      else await github.rest.issues.update({ ...issue, state: "closed", state_reason: "completed" });
      return { ...APPLIED, reply: isPull ? "Closed this PR." : "Closing this issue." };
    case "reopen":
      if (isPull && item.pull_request.merged_at != null) {
        throw new Refusal("a merged pull request cannot be reopened");
      }
      // Prow's reopen handler ignores an item that is not closed.
      if (item.state !== "closed") return { status: "unchanged" };
      if (isPull) await github.rest.pulls.update({ ...pull, state: "open" });
      else await github.rest.issues.update({ ...issue, state: "open" });
      return { ...APPLIED, reply: isPull ? "Reopened this PR." : "Reopened this issue." };
    case "retitle":
      if (isPull) await github.rest.pulls.update({ ...pull, title: request.title });
      else await github.rest.issues.update({ ...issue, title: request.title });
      return APPLIED;
    default:
      throw new Error(`unknown command ${JSON.stringify(request.command)}`);
  }
}

// Parses the dispatch inputs. The agent reads no run back, so an invalid
// dispatch on a valid number is answered there with the rule it broke.
async function validate({ github, context, core, inputs }) {
  try {
    return parseInputs(inputs);
  } catch (error) {
    if (!(error instanceof InputError)) throw error;
    const number = parseNumber(inputs.number);
    if (number === null) {
      core.setFailed(error.message);
      return null;
    }
    const known = COMMANDS.includes(inputs.command);
    await github.rest.issues.createComment({
      owner: context.repo.owner,
      repo: context.repo.repo,
      issue_number: number,
      body: `The ${known ? `\`/${inputs.command}\`` : "dispatched"} command did not complete: ${error.detail}.`,
    });
    core.setFailed(`${known ? `/${inputs.command}` : "the dispatched command"} on #${number} did not complete: ${error.message}`);
    return null;
  }
}

module.exports = async ({ github, context, core, request }) => {
  const repo = { owner: context.repo.owner, repo: context.repo.repo };
  // A missing issue cannot take a comment, so this read is not a refusal.
  const { data: item } = await github.rest.issues.get({ ...repo, issue_number: request.number });
  let outcome;
  try {
    outcome = await apply(github, repo, request, item);
  } catch (error) {
    if (!(error instanceof Refusal) && !REFUSAL_STATUSES.has(error?.status)) throw error;
    const reason = error instanceof Refusal ? error.message : refusalReason(error);
    await github.rest.issues.createComment({
      ...repo,
      issue_number: request.number,
      body: `The \`/${request.command}\` command did not complete: ${reason}.`,
    });
    core.setFailed(`/${request.command} on #${request.number} did not complete: ${reason}`);
    return { status: "refused", reason };
  }
  // Prow's lifecycle plugin replies "@<login>: <reply>" after a close or
  // reopen, an audit trail of who asked (close.go:117,132, reopen.go:91,114).
  if (outcome.reply !== undefined) {
    await github.rest.issues.createComment({
      ...repo,
      issue_number: request.number,
      body: `@${request.requester}: ${outcome.reply}`,
    });
  }
  core.info(`/${request.command} on #${request.number} ${outcome.status} for ${request.requester}`);
  return { status: outcome.status };
};

module.exports.validate = validate;
