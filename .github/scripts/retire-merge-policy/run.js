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

// Supersedes the repository-automation/merge-policy check runs the retired JS
// gate left on open pull request heads. GitHub's status rollup counts their
// action_required conclusion as a failure, and only the app that created a
// check run can post a newer one under that app, so this runs as the
// github-actions app (the workflow's GITHUB_TOKEN) and posts one neutral run
// per stale head. Every read and every guard runs before the first write, and
// any guard that trips refuses the whole run.

const NAME = "repository-automation/merge-policy";
const GITHUB_ACTIONS_APP_ID = 15368;
const SETTLED = new Set(["success", "neutral", "skipped"]);
const PER_PAGE = 100;
const MAX_PAGES = 5;
const MAX_WRITES = 50;
const SUMMARY = "The repository-automation/merge-policy check is retired. mokka/merge-policy has been the merge "
  + "gate since 2026-10-06, so this neutral result replaces the result the retired workflow left on this commit. "
  + "It neither allows nor blocks a merge.";

class Refusal extends Error {}

// Anything but the exact input "false" keeps the dry run on.
function parseDryRun(value) {
  return value !== "false";
}

async function openPulls(github, repo) {
  const pulls = [];
  for (let page = 1; page <= MAX_PAGES; page++) {
    const { data } = await github.rest.pulls.list({ ...repo, state: "open", per_page: PER_PAGE, page });
    pulls.push(...data);
    if (data.length < PER_PAGE) return pulls;
  }
  throw new Refusal(`the page limit of ${MAX_PAGES} pages of ${PER_PAGE} open pull requests was reached`);
}

// Returns the check run to supersede on the pull request's head, or null.
async function staleRun(github, repo, core, pull) {
  const { data } = await github.rest.checks.listForRef({
    ...repo,
    ref: pull.head.sha,
    check_name: NAME,
    filter: "latest",
    per_page: PER_PAGE,
  });
  if (data.total_count > data.check_runs.length) {
    throw new Refusal(`#${pull.number} has ${data.total_count} latest ${NAME} check runs, more than one page of ${PER_PAGE}`);
  }
  const runs = data.check_runs.filter((run) => run.name === NAME);
  if (runs.length === 0) return null;
  const latest = runs.reduce((newest, run) => (run.id > newest.id ? run : newest));
  const prefix = `#${pull.number}: the latest ${NAME} check run ${latest.id}`;
  if (latest.app?.id !== GITHUB_ACTIONS_APP_ID) {
    core.info(`${prefix} belongs to app ${latest.app?.id}, not ${GITHUB_ACTIONS_APP_ID}; left unchanged`);
    return null;
  }
  if (latest.status !== "completed") {
    core.info(`${prefix} is ${latest.status}; left unchanged`);
    return null;
  }
  if (SETTLED.has(latest.conclusion)) {
    core.info(`${prefix} concluded ${latest.conclusion}; nothing to supersede`);
    return null;
  }
  return latest;
}

// A neutral run cannot open a merge only while the check gates nothing, so
// the base branch's classic protection and its active rulesets must both be
// readable and must not require the check. The workflow's GITHUB_TOKEN cannot
// read .../protection/required_status_checks (administration), but the branch
// and its rules are readable with contents and metadata.
async function assertNotRequired(github, repo, branch) {
  const unreadable = (detail) => new Refusal(`cannot read the required checks of ${branch}: ${detail}`);
  let data;
  try {
    ({ data } = await github.rest.repos.getBranch({ ...repo, branch }));
  } catch (error) {
    throw unreadable(error.message);
  }
  const classic = data.protection?.required_status_checks;
  const readable = Array.isArray(classic?.contexts) && Array.isArray(classic?.checks);
  if (data.protected && !readable) {
    throw unreadable("the branch is protected and the response has no required status checks");
  }
  if (readable && (classic.contexts.includes(NAME) || classic.checks.some(({ context }) => context === NAME))) {
    throw new Refusal(`${NAME} is a required check on ${branch} (branch protection)`);
  }

  let rules;
  try {
    ({ data: rules } = await github.rest.repos.getBranchRules({ ...repo, branch, per_page: PER_PAGE }));
  } catch (error) {
    throw unreadable(error.message);
  }
  if (!Array.isArray(rules)) throw unreadable("the rules response is not a list");
  if (rules.length >= PER_PAGE) throw unreadable(`the rules fill a whole page of ${PER_PAGE}`);
  for (const rule of rules) {
    if (rule.type !== "required_status_checks") continue;
    const checks = rule.parameters?.required_status_checks;
    if (!Array.isArray(checks)) throw unreadable(`ruleset ${rule.ruleset_id} has no required status checks list`);
    if (checks.some(({ context }) => context === NAME)) {
      throw new Refusal(`${NAME} is a required check on ${branch} (ruleset ${rule.ruleset_id})`);
    }
  }
}

async function plan(github, repo, core) {
  const stale = [];
  for (const pull of await openPulls(github, repo)) {
    const run = await staleRun(github, repo, core, pull);
    if (run !== null) stale.push({ pull, run });
  }
  if (stale.length > MAX_WRITES) {
    throw new Refusal(`${stale.length} check runs to supersede exceeds the cap of ${MAX_WRITES} per run`);
  }
  for (const branch of new Set(stale.map(({ pull }) => pull.base.ref))) {
    await assertNotRequired(github, repo, branch);
  }
  return stale;
}

module.exports = async ({ github, context, core, dryRun }) => {
  const repo = { owner: context.repo.owner, repo: context.repo.repo };
  let stale;
  try {
    stale = await plan(github, repo, core);
  } catch (error) {
    if (!(error instanceof Refusal)) throw error;
    const reason = `refusing to write: ${error.message}`;
    core.setFailed(reason);
    return { status: "refused", reason };
  }
  for (const { pull, run } of stale) {
    const params = {
      ...repo,
      name: NAME,
      head_sha: pull.head.sha,
      status: "completed",
      conclusion: "neutral",
      output: { title: NAME, summary: SUMMARY },
    };
    if (dryRun) {
      core.info(`dry run: #${pull.number} would get ${JSON.stringify(params)}`);
      continue;
    }
    await github.rest.checks.create(params);
    core.info(`#${pull.number}: superseded ${NAME} check run ${run.id} (${run.conclusion}) with a neutral check run`);
  }
  const superseded = stale.map(({ pull }) => pull.number);
  core.info(`${dryRun ? "dry run: would supersede" : "superseded"} ${superseded.length} check runs`);
  return { status: dryRun ? "planned" : "written", superseded };
};

module.exports.parseDryRun = parseDryRun;
