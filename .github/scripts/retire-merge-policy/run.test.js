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

const assert = require("node:assert/strict");
const test = require("node:test");

const run = require("./run.js");

const REPO = { owner: "NVIDIA", repo: "k8s-test-infra" };
const CONTEXT = { repo: REPO };
const NAME = "repository-automation/merge-policy";
const SHA = "c73f565a00247016253090079e8cec21951ff42d";
const SUMMARY = "The repository-automation/merge-policy check is retired and is no longer a required check; "
  + "mokka/merge-policy has been the merge gate since 2026-10-06. This neutral result replaces the result the "
  + "retired workflow left on this commit. If this check were required again, GitHub would count this neutral "
  + "result as passing.";

// The parameters of the one check run the script may write for #889.
const NEUTRAL = {
  ...REPO,
  name: NAME,
  head_sha: SHA,
  status: "completed",
  conclusion: "neutral",
  output: { title: NAME, summary: SUMMARY },
};

function pull(number, { sha = SHA, base = "main" } = {}) {
  return { number, head: { sha }, base: { ref: base } };
}

function checkRun(id, { name = NAME, app = 15368, status = "completed", conclusion = "action_required" } = {}) {
  return { id, name, status, conclusion, app: { id: app } };
}

// The live shape of main, release-0.4 and release-0.3 on 2026-10-07.
const PROTECTED = {
  protected: true,
  protection: {
    enabled: true,
    required_status_checks: {
      enforcement_level: "non_admins",
      contexts: ["mokka/merge-policy"],
      checks: [{ context: "mokka/merge-policy", app_id: 4416309 }],
    },
  },
};
const RULES = [
  { type: "repository_delete", ruleset_source_type: "Enterprise", ruleset_source: "nvidia", ruleset_id: 12715276 },
  { type: "repository_transfer", ruleset_source_type: "Enterprise", ruleset_source: "nvidia", ruleset_id: 12715293 },
];

function httpError(status, message) {
  return Object.assign(new Error(message), { status });
}

// GitHub one layer deep: each REST method records its call, then answers with
// the handler's data or throws the handler's error.
function fakeGitHub({ pulls = [pull(889)], runs = () => [checkRun(112245042964)], handlers = {} } = {}) {
  const calls = [];
  const method = (name, fallback) => async (params) => {
    calls.push({ name, params });
    return { data: await (handlers[name] ?? fallback)(params) };
  };
  return {
    calls,
    rest: {
      pulls: {
        list: method("pulls.list", ({ page }) => (page === 1 ? pulls : [])),
      },
      checks: {
        listForRef: method("checks.listForRef", ({ ref }) => {
          const found = runs(ref);
          return { total_count: found.length, check_runs: found };
        }),
        create: method("checks.create", () => ({ id: 1 })),
      },
      repos: {
        getBranch: method("repos.getBranch", () => PROTECTED),
        getBranchRules: method("repos.getBranchRules", () => RULES),
      },
    },
  };
}

function fakeCore() {
  const failed = [];
  const lines = [];
  return { failed, lines, setFailed: (message) => failed.push(message), info: (message) => lines.push(message) };
}

async function execute(client = {}, { dryRun = false } = {}) {
  const github = fakeGitHub(client);
  const core = fakeCore();
  const result = await run({ github, context: CONTEXT, core, dryRun });
  return { github, core, result };
}

function writes(github) {
  return github.calls.filter(({ name }) => name === "checks.create");
}

function refusal(reason) {
  return { status: "refused", reason };
}

test("a real run supersedes the stale run with one neutral check run on the same head", async () => {
  const { github, core, result } = await execute();

  assert.deepEqual(github.calls, [
    { name: "pulls.list", params: { ...REPO, state: "open", per_page: 100, page: 1 } },
    { name: "checks.listForRef", params: { ...REPO, ref: SHA, check_name: NAME, filter: "latest", per_page: 100 } },
    { name: "repos.getBranch", params: { ...REPO, branch: "main" } },
    { name: "repos.getBranchRules", params: { ...REPO, branch: "main", per_page: 100 } },
    { name: "checks.create", params: NEUTRAL },
  ]);
  assert.deepEqual(core.failed, []);
  assert.deepEqual(result, { status: "written", superseded: [889] });
});

test("a dry run prints the check run it would write and writes nothing", async () => {
  const { github, core, result } = await execute({}, { dryRun: true });

  assert.deepEqual(writes(github), []);
  assert.ok(core.lines.includes(`dry run: #889 would get ${JSON.stringify(NEUTRAL)}`), core.lines.join("\n"));
  assert.deepEqual(core.failed, []);
  assert.deepEqual(result, { status: "planned", superseded: [889] });
});

test("the latest run is the newest one, so a second run after a neutral write writes nothing", async () => {
  const { github, core, result } = await execute({
    runs: () => [checkRun(112245042964), checkRun(112300000000, { conclusion: "neutral" })],
  });

  assert.deepEqual(writes(github), []);
  assert.ok(core.lines.includes(`#889: the latest ${NAME} check run 112300000000 concluded neutral; nothing to supersede`));
  assert.deepEqual(result, { status: "written", superseded: [] });
});

test("the latest run is the newest one when the API lists runs newest first", async () => {
  const { github, core, result } = await execute({
    runs: () => [checkRun(112300000000, { conclusion: "neutral" }), checkRun(112245042964)],
  });

  assert.deepEqual(writes(github), []);
  assert.ok(core.lines.includes(`#889: the latest ${NAME} check run 112300000000 concluded neutral; nothing to supersede`));
  assert.deepEqual(result, { status: "written", superseded: [] });
});

test("a run that concluded success or skipped is left alone", async () => {
  for (const conclusion of ["success", "skipped"]) {
    const { github, result } = await execute({ runs: () => [checkRun(7, { conclusion })] });
    assert.deepEqual(writes(github), [], conclusion);
    assert.deepEqual(result, { status: "written", superseded: [] }, conclusion);
  }
});

test("a run that has not completed is left alone", async () => {
  const { github, core, result } = await execute({
    runs: () => [checkRun(7, { status: "in_progress", conclusion: null })],
  });

  assert.deepEqual(writes(github), []);
  assert.ok(core.lines.includes(`#889: the latest ${NAME} check run 7 is in_progress; left unchanged`));
  assert.deepEqual(result, { status: "written", superseded: [] });
});

test("a latest run from another app is never touched, even over an older stale github-actions run", async () => {
  const { github, core, result } = await execute({
    runs: () => [checkRun(5), checkRun(9, { app: 4416309 })],
  });

  assert.deepEqual(writes(github), []);
  assert.ok(core.lines.includes(`#889: the latest ${NAME} check run 9 belongs to app 4416309, not 15368; left unchanged`));
  assert.deepEqual(result, { status: "written", superseded: [] });
});

test("check runs of any other name are ignored even when the API returns them", async () => {
  const { github, result } = await execute({
    runs: () => [checkRun(9, { name: "mokka/merge-policy" })],
  });

  assert.deepEqual(writes(github), []);
  assert.deepEqual(result, { status: "written", superseded: [] });
});

test("a required repository-automation/merge-policy check in branch protection refuses every write", async () => {
  const required = (protection) => ({ protected: true, protection: { enabled: true, required_status_checks: protection } });
  const cases = [
    { contexts: [NAME], checks: [] },
    { contexts: [], checks: [{ context: NAME, app_id: 15368 }] },
  ];
  for (const protection of cases) {
    const { github, core, result } = await execute({
      pulls: [pull(889), pull(890, { sha: "a".repeat(40), base: "release-0.4" })],
      handlers: { "repos.getBranch": ({ branch }) => (branch === "release-0.4" ? required(protection) : PROTECTED) },
    });
    const reason = `refusing to write: ${NAME} is a required check on release-0.4 (branch protection)`;
    assert.deepEqual(writes(github), []);
    assert.deepEqual(core.failed, [reason]);
    assert.deepEqual(result, refusal(reason));
  }
});

test("a required repository-automation/merge-policy check in a ruleset refuses every write, in a dry run too", async () => {
  const { github, core, result } = await execute({
    handlers: {
      "repos.getBranchRules": () => [...RULES, {
        type: "required_status_checks",
        ruleset_id: 42,
        parameters: { required_status_checks: [{ context: NAME, integration_id: 15368 }] },
      }],
    },
  }, { dryRun: true });

  const reason = `refusing to write: ${NAME} is a required check on main (ruleset 42)`;
  assert.deepEqual(writes(github), []);
  assert.ok(!core.lines.some((line) => line.startsWith("dry run: #889 would get")), core.lines.join("\n"));
  assert.deepEqual(core.failed, [reason]);
  assert.deepEqual(result, refusal(reason));
});

test("an unreadable branch refuses every write", async () => {
  const { github, core, result } = await execute({
    handlers: { "repos.getBranch": () => { throw httpError(403, "Resource not accessible by integration"); } },
  });

  const reason = "refusing to write: cannot read the required checks of main: Resource not accessible by integration";
  assert.deepEqual(writes(github), []);
  assert.deepEqual(core.failed, [reason]);
  assert.deepEqual(result, refusal(reason));
});

test("a branch response that does not say whether the branch is protected refuses every write", async () => {
  for (const [label, branch] of [
    ["missing", { name: "main", protection: PROTECTED.protection }],
    ["null", { name: "main", protected: null, protection: PROTECTED.protection }],
    ["a string", { name: "main", protected: "true", protection: PROTECTED.protection }],
  ]) {
    const { github, core, result } = await execute({ handlers: { "repos.getBranch": () => branch } });

    const reason = "refusing to write: cannot read the required checks of main: "
      + "the response does not say whether the branch is protected";
    assert.deepEqual(writes(github), [], label);
    assert.deepEqual(core.failed, [reason], label);
    assert.deepEqual(result, refusal(reason), label);
  }
});

test("a protected branch whose required checks are missing from the response refuses every write", async () => {
  const { github, core, result } = await execute({
    handlers: { "repos.getBranch": () => ({ protected: true, protection: { enabled: true } }) },
  });

  const reason = "refusing to write: cannot read the required checks of main: "
    + "the branch is protected and the response has no required status checks";
  assert.deepEqual(writes(github), []);
  assert.deepEqual(core.failed, [reason]);
  assert.deepEqual(result, refusal(reason));
});

test("unreadable branch rules refuse every write", async () => {
  const { github, core, result } = await execute({
    handlers: { "repos.getBranchRules": () => { throw httpError(500, "Server Error"); } },
  });

  const reason = "refusing to write: cannot read the required checks of main: Server Error";
  assert.deepEqual(writes(github), []);
  assert.deepEqual(core.failed, [reason]);
  assert.deepEqual(result, refusal(reason));
});

test("branch rules that are not a list refuse every write", async () => {
  const { github, core, result } = await execute({
    handlers: { "repos.getBranchRules": () => ({ message: "Not Found" }) },
  });

  const reason = "refusing to write: cannot read the required checks of main: the rules response is not a list";
  assert.deepEqual(writes(github), []);
  assert.deepEqual(core.failed, [reason]);
  assert.deepEqual(result, refusal(reason));
});

test("branch rules that fill a whole page refuse every write", async () => {
  const { github, core, result } = await execute({
    handlers: { "repos.getBranchRules": () => Array.from({ length: 100 }, () => RULES[0]) },
  });

  const reason = "refusing to write: cannot read the required checks of main: the rules fill a whole page of 100";
  assert.deepEqual(writes(github), []);
  assert.deepEqual(core.failed, [reason]);
  assert.deepEqual(result, refusal(reason));
});

test("a required status checks rule without a readable list refuses every write", async () => {
  const { github, core, result } = await execute({
    handlers: { "repos.getBranchRules": () => [{ type: "required_status_checks", ruleset_id: 42 }] },
  });

  const reason = "refusing to write: cannot read the required checks of main: ruleset 42 has no required status checks list";
  assert.deepEqual(writes(github), []);
  assert.deepEqual(core.failed, [reason]);
  assert.deepEqual(result, refusal(reason));
});

test("only the base branches of pull requests it would touch are read", async () => {
  const { github, core, result } = await execute({
    pulls: [pull(889), pull(450, { sha: "b".repeat(40), base: "release-0.3" })],
    runs: (ref) => (ref === SHA ? [checkRun(7)] : [checkRun(8, { conclusion: "neutral" })]),
  });

  assert.deepEqual(github.calls.filter(({ name }) => name.startsWith("repos.")).map(({ params }) => params.branch), ["main", "main"]);
  assert.deepEqual(writes(github).map(({ params }) => params.head_sha), [SHA]);
  assert.deepEqual(core.failed, []);
  assert.deepEqual(result, { status: "written", superseded: [889] });
});

test("open pull requests are read page by page and the fifth full page refuses every write", async () => {
  const page = (n) => Array.from({ length: 100 }, (_, i) => pull(n * 1000 + i, { sha: `${n}`.repeat(40) }));
  const { github, core, result } = await execute({ handlers: { "pulls.list": ({ page: n }) => page(n) } });

  const reason = "refusing to write: the page limit of 5 pages of 100 open pull requests was reached";
  assert.deepEqual(github.calls.filter(({ name }) => name === "pulls.list").map(({ params }) => params.page), [1, 2, 3, 4, 5]);
  assert.deepEqual(writes(github), []);
  assert.deepEqual(core.failed, [reason]);
  assert.deepEqual(result, refusal(reason));
});

test("a short page ends the pull request listing", async () => {
  const { github, result } = await execute({
    handlers: { "pulls.list": ({ page }) => (page === 1 ? Array.from({ length: 100 }, (_, i) => pull(i + 1)) : [pull(889)]) },
    runs: () => [checkRun(7, { conclusion: "neutral" })],
  });

  assert.deepEqual(github.calls.filter(({ name }) => name === "pulls.list").map(({ params }) => params.page), [1, 2]);
  assert.deepEqual(result, { status: "written", superseded: [] });
});

test("more planned writes than the cap refuses every write", async () => {
  const pulls = Array.from({ length: 51 }, (_, i) => pull(i + 1, { sha: i.toString(16).padStart(40, "0") }));
  const { github, core, result } = await execute({ pulls });

  const reason = "refusing to write: 51 check runs to supersede exceeds the cap of 50 per run";
  assert.deepEqual(writes(github), []);
  assert.deepEqual(core.failed, [reason]);
  assert.deepEqual(result, refusal(reason));

  const atCap = await execute({ pulls: pulls.slice(0, 50) });
  assert.equal(writes(atCap.github).length, 50);
  assert.deepEqual(atCap.core.failed, []);
});

test("more latest check runs than one page holds refuses every write", async () => {
  const { github, core, result } = await execute({
    handlers: { "checks.listForRef": () => ({ total_count: 101, check_runs: [checkRun(7)] }) },
  });

  const reason = `refusing to write: #889 has 101 latest ${NAME} check runs, more than one page of 100`;
  assert.deepEqual(writes(github), []);
  assert.deepEqual(core.failed, [reason]);
  assert.deepEqual(result, refusal(reason));
});

test("only the exact input false turns the dry run off", () => {
  assert.equal(run.parseDryRun("false"), false);
  for (const value of ["true", "", undefined, "False", "0", "no"]) {
    assert.equal(run.parseDryRun(value), true, String(value));
  }
});
