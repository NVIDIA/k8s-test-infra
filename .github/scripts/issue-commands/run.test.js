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

const { parseInputs } = require("./inputs.js");
const run = require("./run.js");

const REPO = { owner: "NVIDIA", repo: "k8s-test-infra" };
const CONTEXT = { repo: REPO };
const ISSUE = { number: 42, state: "open" };
const PULL = { number: 42, state: "open", pull_request: { merged_at: null } };
const MERGED_PULL = { number: 42, state: "closed", pull_request: { merged_at: "2026-10-01T12:00:00Z" } };

function request(command, { users = "", title = "" } = {}) {
  return parseInputs({ command, number: "42", users, title, requester: "alice" });
}

function httpError(status, message) {
  return Object.assign(new Error(message), { status });
}

// GitHub one layer deep: each REST method records its call, then answers with
// the handler's data or throws the handler's error.
function fakeGitHub({ issue = ISSUE, handlers = {} } = {}) {
  const calls = [];
  const method = (name, fallback) => async (params) => {
    calls.push({ name, params });
    return { data: await (handlers[name] ?? fallback)(params) };
  };
  return {
    calls,
    rest: {
      issues: {
        get: method("issues.get", () => issue),
        addAssignees: method("issues.addAssignees", ({ assignees }) => ({ assignees: assignees.map((login) => ({ login })) })),
        removeAssignees: method("issues.removeAssignees", () => ({ assignees: [] })),
        update: method("issues.update", () => ({})),
        createComment: method("issues.createComment", () => ({})),
      },
      pulls: {
        requestReviewers: method("pulls.requestReviewers", () => ({})),
        removeRequestedReviewers: method("pulls.removeRequestedReviewers", () => ({ requested_reviewers: [] })),
        update: method("pulls.update", () => ({})),
      },
    },
  };
}

function fakeCore() {
  const failed = [];
  return { failed, setFailed: (message) => failed.push(message), info: () => {} };
}

async function execute(command, options = {}, client = {}) {
  const github = fakeGitHub(client);
  const core = fakeCore();
  const result = await run({ github, context: CONTEXT, core, request: request(command, options) });
  return { github, core, result };
}

const GET = { name: "issues.get", params: { ...REPO, issue_number: 42 } };

function comment(body) {
  return { name: "issues.createComment", params: { ...REPO, issue_number: 42, body } };
}

test("/assign adds the assignees and accepts GitHub's login case", async () => {
  const { github, core, result } = await execute("assign", { users: "bob,carol" }, {
    handlers: { "issues.addAssignees": () => ({ assignees: [{ login: "Bob" }, { login: "carol" }, { login: "dave" }] }) },
  });

  assert.deepEqual(github.calls, [
    GET, { name: "issues.addAssignees", params: { ...REPO, issue_number: 42, assignees: ["bob", "carol"] } },
  ]);
  assert.deepEqual(core.failed, []);
  assert.deepEqual(result, { status: "applied" });
});

test("/assign reports the users GitHub silently did not assign", async () => {
  const { github, core, result } = await execute("assign", { users: "bob,carol,erin" }, {
    handlers: { "issues.addAssignees": () => ({ assignees: [{ login: "bob" }] }) },
  });

  const reason = "GitHub did not assign `carol`, `erin`";
  assert.deepEqual(github.calls.slice(2), [comment(`The \`/assign\` command did not complete: ${reason}.`)]);
  assert.deepEqual(core.failed, [`/assign on #42 did not complete: ${reason}`]);
  assert.deepEqual(result, { status: "refused", reason });
});

test("/unassign removes the assignees and reports any GitHub left assigned", async () => {
  const applied = await execute("unassign", { users: "bob" }, {
    handlers: { "issues.removeAssignees": () => ({ assignees: [{ login: "carol" }] }) },
  });
  assert.deepEqual(applied.github.calls, [
    GET, { name: "issues.removeAssignees", params: { ...REPO, issue_number: 42, assignees: ["bob"] } },
  ]);
  assert.deepEqual(applied.core.failed, []);

  const refused = await execute("unassign", { users: "bob,carol" }, {
    handlers: { "issues.removeAssignees": () => ({ assignees: [{ login: "Carol" }, { login: "dave" }] }) },
  });
  const reason = "GitHub left `carol` assigned";
  assert.deepEqual(refused.github.calls.slice(2), [comment(`The \`/unassign\` command did not complete: ${reason}.`)]);
  assert.deepEqual(refused.core.failed, [`/unassign on #42 did not complete: ${reason}`]);
});

test("/cc requests reviews on a pull request", async () => {
  const { github, core, result } = await execute("cc", { users: "bob,carol" }, { issue: PULL });

  assert.deepEqual(github.calls, [
    GET, { name: "pulls.requestReviewers", params: { ...REPO, pull_number: 42, reviewers: ["bob", "carol"] } },
  ]);
  assert.deepEqual(core.failed, []);
  assert.deepEqual(result, { status: "applied" });
});

test("/cc retries one reviewer at a time when GitHub refuses the batch, as Prow does", async () => {
  const { github, core } = await execute("cc", { users: "bob,carol,erin" }, {
    issue: PULL,
    handlers: {
      "pulls.requestReviewers": ({ reviewers }) => {
        if (reviewers.length > 1 || reviewers[0] === "carol") throw httpError(422, "Reviews may only be requested from collaborators.");
        return {};
      },
    },
  });

  const reason = "GitHub refused a review request for `carol`";
  const requested = (reviewers) => ({ name: "pulls.requestReviewers", params: { ...REPO, pull_number: 42, reviewers } });
  assert.deepEqual(github.calls, [
    GET, requested(["bob", "carol", "erin"]), requested(["bob"]), requested(["carol"]), requested(["erin"]),
    comment(`The \`/cc\` command did not complete: ${reason}.`),
  ]);
  assert.deepEqual(core.failed, [`/cc on #42 did not complete: ${reason}`]);
});

test("/cc reports a review request on the pull request author", async () => {
  const { github, core } = await execute("cc", { users: "pr-author,bob" }, {
    issue: PULL,
    handlers: {
      "pulls.requestReviewers": ({ reviewers }) => {
        if (reviewers.includes("pr-author")) throw httpError(422, "Review cannot be requested from pull request author.");
        return {};
      },
    },
  });

  const reason = "GitHub refused a review request for `pr-author`";
  assert.deepEqual(github.calls.at(-1), comment(`The \`/cc\` command did not complete: ${reason}.`));
  assert.deepEqual(github.calls.filter(({ name }) => name === "pulls.requestReviewers").map(({ params }) => params.reviewers),
    [["pr-author", "bob"], ["pr-author"], ["bob"]]);
  assert.deepEqual(core.failed, [`/cc on #42 did not complete: ${reason}`]);
});

test("/cc fails without a comment when a single-reviewer retry fails for another reason", async () => {
  const github = fakeGitHub({
    issue: PULL,
    handlers: {
      "pulls.requestReviewers": ({ reviewers }) => {
        throw reviewers.length > 1 ? httpError(422, "Validation Failed") : httpError(502, "Bad Gateway");
      },
    },
  });
  const core = fakeCore();

  await assert.rejects(run({ github, context: CONTEXT, core, request: request("cc", { users: "bob,carol" }) }),
    { status: 502, message: "Bad Gateway" });
  assert.equal(github.calls.some(({ name }) => name === "issues.createComment"), false);
});

test("/cc retries one reviewer at a time only after a 422 on the batch", async () => {
  const requested = { name: "pulls.requestReviewers", params: { ...REPO, pull_number: 42, reviewers: ["bob", "carol"] } };
  const refused = await execute("cc", { users: "bob,carol" }, {
    issue: PULL,
    handlers: { "pulls.requestReviewers": () => { throw httpError(403, "Resource not accessible by integration"); } },
  });
  const reason = "GitHub refused the request with HTTP 403 (`Resource not accessible by integration`)";
  assert.deepEqual(refused.github.calls, [GET, requested, comment(`The \`/cc\` command did not complete: ${reason}.`)]);
  assert.deepEqual(refused.core.failed, [`/cc on #42 did not complete: ${reason}`]);

  const github = fakeGitHub({
    issue: PULL,
    handlers: { "pulls.requestReviewers": () => { throw httpError(502, "Bad Gateway"); } },
  });
  await assert.rejects(run({ github, context: CONTEXT, core: fakeCore(), request: request("cc", { users: "bob,carol" }) }),
    { status: 502 });
  assert.deepEqual(github.calls, [GET, requested]);
});

test("/uncc removes review requests and reports any GitHub left in place", async () => {
  const applied = await execute("uncc", { users: "bob" }, {
    issue: PULL,
    handlers: { "pulls.removeRequestedReviewers": () => ({ requested_reviewers: [{ login: "carol" }] }) },
  });
  assert.deepEqual(applied.github.calls, [
    GET, { name: "pulls.removeRequestedReviewers", params: { ...REPO, pull_number: 42, reviewers: ["bob"] } },
  ]);
  assert.deepEqual(applied.core.failed, []);

  const refused = await execute("uncc", { users: "bob,carol" }, {
    issue: PULL,
    handlers: { "pulls.removeRequestedReviewers": () => ({ requested_reviewers: [{ login: "BOB" }] }) },
  });
  const reason = "GitHub left the review request for `bob` in place";
  assert.deepEqual(refused.github.calls.slice(2), [comment(`The \`/uncc\` command did not complete: ${reason}.`)]);
  assert.deepEqual(refused.core.failed, [`/uncc on #42 did not complete: ${reason}`]);
});

test("/cc and /uncc on an issue are refused before any write", async () => {
  for (const command of ["cc", "uncc"]) {
    const { github, core, result } = await execute(command, { users: "bob" });
    const reason = "review requests apply only to pull requests, and #42 is an issue";
    assert.deepEqual(github.calls, [GET, comment(`The \`/${command}\` command did not complete: ${reason}.`)], command);
    assert.deepEqual(core.failed, [`/${command} on #42 did not complete: ${reason}`], command);
    assert.deepEqual(result, { status: "refused", reason }, command);
  }
});

test("/close closes an issue as completed and a pull request through the pulls API", async () => {
  const issue = await execute("close");
  assert.deepEqual(issue.github.calls, [
    GET, { name: "issues.update", params: { ...REPO, issue_number: 42, state: "closed", state_reason: "completed" } },
  ]);
  assert.deepEqual(issue.core.failed, []);

  const pull = await execute("close", {}, { issue: PULL });
  assert.deepEqual(pull.github.calls, [GET, { name: "pulls.update", params: { ...REPO, pull_number: 42, state: "closed" } }]);
  assert.deepEqual(pull.core.failed, []);
});

test("/close refuses an item that is already closed without a state write", async () => {
  for (const issue of [{ ...ISSUE, state: "closed" }, { ...PULL, state: "closed" }, MERGED_PULL]) {
    const { github, core, result } = await execute("close", {}, { issue });
    const reason = "#42 is already closed";
    assert.deepEqual(github.calls, [GET, comment(`The \`/close\` command did not complete: ${reason}.`)]);
    assert.deepEqual(core.failed, [`/close on #42 did not complete: ${reason}`]);
    assert.deepEqual(result, { status: "refused", reason });
  }
});

test("/reopen reopens an issue and an unmerged pull request", async () => {
  const issue = await execute("reopen", {}, { issue: { ...ISSUE, state: "closed" } });
  assert.deepEqual(issue.github.calls, [GET, { name: "issues.update", params: { ...REPO, issue_number: 42, state: "open" } }]);
  assert.deepEqual(issue.core.failed, []);

  const pull = await execute("reopen", {}, { issue: { ...PULL, state: "closed" } });
  assert.deepEqual(pull.github.calls, [GET, { name: "pulls.update", params: { ...REPO, pull_number: 42, state: "open" } }]);
  assert.deepEqual(pull.core.failed, []);
});

test("/reopen refuses a merged pull request without a state write", async () => {
  const { github, core, result } = await execute("reopen", {}, { issue: MERGED_PULL });

  const reason = "a merged pull request cannot be reopened";
  assert.deepEqual(github.calls, [GET, comment(`The \`/reopen\` command did not complete: ${reason}.`)]);
  assert.deepEqual(core.failed, [`/reopen on #42 did not complete: ${reason}`]);
  assert.deepEqual(result, { status: "refused", reason });
});

test("/retitle updates the title through the API that owns the item", async () => {
  const title = "fix(ci): pin actionlint";
  const issue = await execute("retitle", { title });
  assert.deepEqual(issue.github.calls, [GET, { name: "issues.update", params: { ...REPO, issue_number: 42, title } }]);
  assert.deepEqual(issue.core.failed, []);

  const pull = await execute("retitle", { title }, { issue: PULL });
  assert.deepEqual(pull.github.calls, [GET, { name: "pulls.update", params: { ...REPO, pull_number: 42, title } }]);
  assert.deepEqual(pull.core.failed, []);
});

test("a GitHub refusal of the write gets one plain comment and fails the run", async () => {
  const cases = [
    ["reopen", {}, { ...PULL, state: "closed" }, "pulls.update",
      httpError(422, "Validation Failed: state cannot be changed. The head branch was deleted.")],
    ["close", {}, ISSUE, "issues.update", httpError(403, "Resource not accessible by integration")],
    ["retitle", { title: "fix: x" }, PULL, "pulls.update", httpError(404, "Not Found")],
    ["assign", { users: "bob" }, ISSUE, "issues.addAssignees", httpError(410, "Issues are disabled")],
  ];
  for (const [command, options, issue, failing, error] of cases) {
    const { github, core, result } = await execute(command, options, { issue, handlers: { [failing]: () => { throw error; } } });
    const reason = `GitHub refused the request with HTTP ${error.status} (\`${error.message}\`)`;
    assert.deepEqual(github.calls.slice(2), [comment(`The \`/${command}\` command did not complete: ${reason}.`)], command);
    assert.deepEqual(core.failed, [`/${command} on #42 did not complete: ${reason}`], command);
    assert.deepEqual(result, { status: "refused", reason }, command);
  }
});

test("GitHub's refusal text cannot break out of its code span or start a command line", async () => {
  const error = httpError(422, "Validation `Failed`\n/close\n@nvidia/maintainers​ " + "x".repeat(300));
  const { github } = await execute("close", {}, { handlers: { "issues.update": () => { throw error; } } });

  const body = github.calls.at(-1).params.body;
  assert.equal(body,
    `The \`/close\` command did not complete: GitHub refused the request with HTTP 422 (\`Validation Failed /close @nvidia/maintainers ${"x".repeat(155)}\`).`);
  for (const line of body.split("\n")) assert.doesNotMatch(line, /^\s*\//);
});

test("errors that are not refusals fail the run without a comment", async () => {
  for (const [failing, error] of [
    ["issues.update", httpError(500, "Server Error")],
    ["issues.update", httpError(429, "Too Many Requests")],
    ["issues.update", new TypeError("fetch failed")],
    ["issues.get", httpError(404, "Not Found")],
  ]) {
    const github = fakeGitHub({ handlers: { [failing]: () => { throw error; } } });
    const core = fakeCore();
    await assert.rejects(run({ github, context: CONTEXT, core, request: request("close") }), error, failing);
    assert.equal(github.calls.some(({ name }) => name === "issues.createComment"), false, `${failing}: ${error.message}`);
    assert.deepEqual(core.failed, []);
  }
});
