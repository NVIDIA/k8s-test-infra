"use strict";

const assert = require("node:assert/strict");
const { Buffer } = require("node:buffer");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const { createGitHubClient } = require("../src/github-client.js");
const { loadConfig } = require("../src/config.js");
const { run } = require("../src/index.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const HEAD = "a".repeat(40);
const BASE = "b".repeat(40);
const NEXT = "c".repeat(40);
const repository = {
  name: "k8s-test-infra",
  full_name: "NVIDIA/k8s-test-infra",
  default_branch: "main",
  owner: { login: "NVIDIA" },
};

function pullRequest(overrides = {}) {
  return {
    number: 42,
    nodeId: "PR_node_42",
    title: "feat: conflict label contract",
    body: "private-body-must-not-enter-the-summary",
    draft: false,
    author: "contributor",
    headOid: HEAD,
    state: "open",
    baseBranch: "main",
    baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    headRepository: { owner: "contributor", repo: "fork" },
    ...overrides,
  };
}

function conflictState(pr, overrides = {}) {
  return {
    number: pr.number,
    nodeId: pr.nodeId,
    repository: "nvidia/k8s-test-infra",
    state: pr.state.toUpperCase(),
    draft: pr.draft,
    headOid: pr.headOid,
    baseBranch: pr.baseBranch,
    baseOid: BASE,
    mergeability: "CONFLICTING",
    ...overrides,
  };
}

function prEvent(overrides = {}) {
  return {
    repository,
    action: "synchronize",
    number: 42,
    pull_request: { number: 42 },
    ...overrides,
  };
}

function pushEvent(branch = "main", overrides = {}) {
  return {
    repository,
    ref: `refs/heads/${branch}`,
    before: NEXT,
    after: BASE,
    deleted: false,
    created: false,
    forced: false,
    ...overrides,
  };
}

function fakeGitHub(options = {}) {
  const prs = options.pullRequests ?? [pullRequest()];
  const byNumber = new Map(prs.map((pr) => [pr.number, pr]));
  const labels = new Map(prs.map((pr) => [
    pr.number,
    new Set(options.labelsByNumber?.[pr.number] ?? options.labels ?? ["maintainer/custom"]),
  ]));
  const calls = [];
  const mutations = [];
  const readFailures = [];
  const counts = new Map();
  const copy = (value) => JSON.parse(JSON.stringify(value));
  function read(operation, key, fallback) {
    calls.push({ operation, key });
    const countKey = `${operation}:${key}`;
    const count = (counts.get(countKey) ?? 0) + 1;
    counts.set(countKey, count);
    if (options.failure?.operation === operation
      && (options.failure.key === undefined || options.failure.key === key)
      && count === (options.failure.at ?? 1)) {
      const error = new Error(`${operation} read unavailable`);
      readFailures.push({ operation, key, error });
      throw error;
    }
    const snapshots = options.snapshots?.[operation];
    return copy(snapshots === undefined ? fallback : snapshots[Math.min(count - 1, snapshots.length - 1)]);
  }
  const github = {
    calls,
    mutations,
    readFailures,
    labels,
    async getPullRequest(number) {
      assert.ok(byNumber.has(number), `unexpected pull request ${number}`);
      return read("getPullRequest", number, byNumber.get(number));
    },
    async getConflictState(number) {
      return read("getConflictState", number, conflictState(
        byNumber.get(number), options.conflicts?.[number] ?? options.conflict ?? {},
      ));
    },
    async getBranch(branch) {
      return read("getBranch", branch, { name: branch, oid: BASE });
    },
    async listIssueLabels(number) {
      return read("listIssueLabels", number, [...labels.get(number)]);
    },
    async listOpenPullRequestNumbers() {
      calls.push({ operation: "listOpenPullRequestNumbers" });
      return options.numbers ?? prs.map((pr) => pr.number);
    },
    async addIssueLabel(number, label) {
      mutations.push({ operation: "addIssueLabel", number, label });
      if (options.mutationFailure === number) throw new Error("label mutation unavailable");
      labels.get(number).add(label);
    },
    async removeIssueLabel(number, label) {
      mutations.push({ operation: "removeIssueLabel", number, label });
      labels.get(number).delete(label);
    },
  };
  for (const operation of [
    "requestReviewers", "upsertPolicyComment", "addPolicyLabel", "removePolicyLabel",
    "setMergePolicyCheck", "disableAutoMerge", "enableAutoMerge", "mergePullRequest",
    "rerunFailedJobs", "createBackportPullRequest",
  ]) {
    github[operation] = async (...args) => {
      mutations.push({ operation, args });
      throw new Error(`conflict reconciliation must not call ${operation}`);
    };
  }
  return github;
}

async function invoke(githubClient, options = {}) {
  const outputs = new Map();
  const core = {
    getInput(name) { return name === "mode" ? "conflict-labels" : ""; },
    getBooleanInput(name) {
      assert.equal(name, "dry-run");
      return options.dryRun ?? false;
    },
    setOutput(name, value) { outputs.set(name, value); },
  };
  let summary;
  try {
    summary = await run({
      core,
      githubClient,
      owner: "NVIDIA",
      repo: "k8s-test-infra",
      octokit: options.octokit,
      workspace: repositoryRoot,
      eventName: options.eventName ?? "pull_request_target",
      event: options.event ?? prEvent(),
    });
  } catch (error) {
    if (!options.captureFailure) throw error;
    assert.doesNotMatch(error.message, /Unsupported mode/);
    assert.ok(error.summary, "a partial failure must retain its operation summary");
    summary = error.summary;
  }
  const serialized = outputs.get("summary");
  assert.equal(typeof serialized, "string", "the action must emit its serialized summary");
  assert.deepEqual(JSON.parse(serialized), summary);
  assert.ok(Buffer.byteLength(serialized, "utf8") <= 64 * 1024);
  assert.equal(serialized.includes("private-body-must-not-enter-the-summary"), false);
  return summary;
}

function reportsOperation(summary, number, outcome) {
  function visit(value, keyPath) {
    if (value === null || typeof value !== "object") return false;
    if ((value.prNumber === number || value.number === number)
      && outcome.test(`${keyPath} ${JSON.stringify(value)}`)) return true;
    return Object.entries(value).some(([key, child]) => visit(child, `${keyPath}.${key}`));
  }
  return visit(summary, "summary");
}

function assertLiveReads(github, minimum = 1) {
  for (const operation of ["getPullRequest", "getConflictState", "getBranch", "listIssueLabels"]) {
    assert.ok(github.calls.filter((call) => call.operation === operation).length >= minimum,
      `${operation} must read live state`);
  }
}

async function assertNoWrite(github, options = {}) {
  try {
    await invoke(github, options);
  } catch (error) {
    assert.doesNotMatch(error.message, /Unsupported mode|is not a function|Cannot read|is not defined|configuration|ENOENT/i,
      "an incidental exception must not satisfy the no-write contract");
    assert.ok(options.expectedFailure, "the guard must declare the expected failure reason");
    const reasons = [error.message, error.cause?.message].filter((reason) => typeof reason === "string");
    const injectedRead = github.readFailures.find((entry) => options.expectedFailure.test(entry.error.message));
    const failedPR = error.message.match(/^conflict label (?:initial|fresh) read failed for PR ([1-9][0-9]*)$/)?.[1];
    const reportedRead = injectedRead !== undefined
      && failedPR !== undefined
      && (typeof injectedRead.key !== "number" || injectedRead.key === Number(failedPR))
      && reportsOperation(error.summary, Number(failedPR), /failed|failure/i);
    assert.ok(reasons.some((reason) => options.expectedFailure.test(reason)) || reportedRead,
      `unexpected no-write failure: ${error.message}`);
  }
  assert.deepEqual(github.mutations, []);
}

test("confirmed conflict adds only the exact needs-rebase label using live PR identity", async () => {
  const github = fakeGitHub({ labels: ["maintainer/custom", "approved"] });
  await invoke(github, { event: prEvent({ pull_request: {
    number: 42,
    head: { sha: NEXT },
    base: { ref: "untrusted-event-branch" },
    mergeable: true,
  } }) });

  assert.deepEqual(github.mutations, [{ operation: "addIssueLabel", number: 42, label: "needs-rebase" }]);
  assert.deepEqual([...github.labels.get(42)].sort(), ["approved", "maintainer/custom", "needs-rebase"]);
  assertLiveReads(github);
  for (const operation of ["getPullRequest", "getConflictState", "getBranch"]) {
    assert.ok(github.calls.filter((call) => call.operation === operation).length >= 2,
      `${operation} must be checked again before a write`);
  }
});

test("confirmed mergeable state removes needs-rebase and preserves other labels", async () => {
  const github = fakeGitHub({
    labels: ["needs-rebase", "approved", "maintainer/custom", "do-not-merge/hold"],
    conflict: { mergeability: "MERGEABLE" },
  });
  await invoke(github);
  assert.deepEqual(github.mutations, [{ operation: "removeIssueLabel", number: 42, label: "needs-rebase" }]);
  assert.deepEqual([...github.labels.get(42)].sort(), ["approved", "do-not-merge/hold", "maintainer/custom"]);
});

for (const present of [false, true]) {
  test(`UNKNOWN preserves needs-rebase ${present ? "present" : "absent"} and reports deferral`, async () => {
    const initial = present ? ["maintainer/custom", "needs-rebase"] : ["maintainer/custom"];
    const github = fakeGitHub({ labels: initial, conflict: { mergeability: "UNKNOWN" } });
    const summary = await invoke(github);
    assert.deepEqual(github.mutations, []);
    assert.deepEqual([...github.labels.get(42)], initial);
    assert.match(JSON.stringify(summary), /unknown|defer/i);
    assertLiveReads(github);
  });
}

for (const [name, restOverrides, graphOverrides] of [
  ["behind the base branch", { mergeable_state: "behind" }, {}],
  ["failed CI", { mergeable_state: "unstable", status: "failure" }, {}],
  ["missing approval", { mergeable_state: "blocked", reviewDecision: "REVIEW_REQUIRED" }, {}],
  ["draft PR", { draft: true }, { draft: true }],
]) {
  test(`${name} does not retain a conflict label when mergeability is MERGEABLE`, async () => {
    const github = fakeGitHub({
      pullRequests: [pullRequest(restOverrides)],
      conflict: { mergeability: "MERGEABLE", ...graphOverrides },
      labels: ["needs-rebase", "maintainer/custom"],
    });
    await invoke(github);
    assert.deepEqual(github.mutations, [{ operation: "removeIssueLabel", number: 42, label: "needs-rebase" }]);
  });
}

test("a draft PR with confirmed conflicts receives needs-rebase", async () => {
  const github = fakeGitHub({ pullRequests: [pullRequest({ draft: true })] });
  await invoke(github);
  assert.deepEqual(github.mutations, [{ operation: "addIssueLabel", number: 42, label: "needs-rebase" }]);
});

test("malformed title and a DCO block do not prevent a confirmed conflict label", async () => {
  const github = fakeGitHub({
    pullRequests: [pullRequest({ title: "not a conventional title", dco: { valid: false } })],
    labels: ["do-not-merge/invalid-title", "do-not-merge/invalid-dco", "maintainer/custom"],
  });
  github.listPullRequestCommits = async () => assert.fail("conflict labels must not evaluate DCO");
  await invoke(github);
  assert.deepEqual(github.mutations, [{ operation: "addIssueLabel", number: 42, label: "needs-rebase" }]);
  assert.deepEqual([...github.labels.get(42)].sort(), [
    "do-not-merge/invalid-dco", "do-not-merge/invalid-title", "maintainer/custom", "needs-rebase",
  ]);
});

test("invalid unrelated labels and areas config cannot block conflict mode through run", async (t) => {
  const readFileSync = fs.readFileSync;
  const invalidPaths = new Set(["labels", "areas"].map((name) =>
    path.join(repositoryRoot, ".github", "repo-automation", `${name}.yml`)));
  t.mock.method(fs, "readFileSync", (filePath, ...options) => {
    if (invalidPaths.has(String(filePath))) return "schemaVersion: [unterminated\n";
    return readFileSync(filePath, ...options);
  });
  assert.throws(() => loadConfig(repositoryRoot), /Invalid repository automation configuration/);
  const github = fakeGitHub();
  await invoke(github);
  assert.deepEqual(github.mutations, [{ operation: "addIssueLabel", number: 42, label: "needs-rebase" }]);
});

test("a current label with different case prevents a redundant add", async () => {
  const github = fakeGitHub({ labels: ["NEEDS-REBASE", "maintainer/custom"] });
  await invoke(github);
  assert.deepEqual(github.mutations, []);
  assert.deepEqual([...github.labels.get(42)], ["NEEDS-REBASE", "maintainer/custom"]);
});

for (const [mergeability, labels] of [
  ["CONFLICTING", ["needs-rebase", "maintainer/custom"]],
  ["MERGEABLE", ["maintainer/custom"]],
]) {
  test(`${mergeability} reconciliation is idempotent`, async () => {
    const github = fakeGitHub({ conflict: { mergeability }, labels });
    await invoke(github);
    await invoke(github);
    assert.deepEqual(github.mutations, []);
    assert.deepEqual([...github.labels.get(42)], labels);
  });
}

for (const [mergeability, labels, operation] of [
  ["CONFLICTING", ["maintainer/custom"], "addIssueLabel"],
  ["MERGEABLE", ["needs-rebase", "maintainer/custom"], "removeIssueLabel"],
]) {
  test(`${mergeability} applies once and replay does not repeat the mutation`, async () => {
    const github = fakeGitHub({ conflict: { mergeability }, labels });
    await invoke(github);
    assert.deepEqual(github.mutations, [{ operation, number: 42, label: "needs-rebase" }]);
    await invoke(github);
    assert.deepEqual(github.mutations, [{ operation, number: 42, label: "needs-rebase" }]);
    assert.deepEqual([...github.labels.get(42)].sort(), mergeability === "CONFLICTING"
      ? ["maintainer/custom", "needs-rebase"] : ["maintainer/custom"]);
  });
}

for (const mergeability of ["CONFLICTING", "MERGEABLE"]) {
  test(`dry-run reports ${mergeability} without label writes`, async () => {
    const github = fakeGitHub({
      conflict: { mergeability },
      labels: mergeability === "MERGEABLE" ? ["needs-rebase"] : [],
    });
    await invoke(github, { dryRun: true });
    assert.deepEqual(github.mutations, []);
    assertLiveReads(github);
  });
}

for (const operation of ["getPullRequest", "getConflictState", "getBranch", "listIssueLabels"]) {
  test(`${operation} read failure prevents a label mutation`, async () => {
    const github = fakeGitHub({ failure: { operation } });
    await assertNoWrite(github, { expectedFailure: new RegExp(`${operation} read unavailable`) });
    assert.ok(github.calls.some((call) => call.operation === operation));
  });
}

const original = pullRequest();
const originalGraph = conflictState(original);
for (const [name, snapshots] of [
  ["PR head", { getPullRequest: [original, pullRequest({ headOid: NEXT })] }],
  ["PR base branch", { getPullRequest: [original, pullRequest({ baseBranch: "release-1.2" })] }],
  ["PR base repository", { getPullRequest: [original, pullRequest({ baseRepository: { owner: "attacker", repo: "fork" } })] }],
  ["PR state", { getPullRequest: [original, pullRequest({ state: "closed" })] }],
  ["base branch tip", { getBranch: [{ name: "main", oid: BASE }, { name: "main", oid: NEXT }] }],
  ["GraphQL mergeability", { getConflictState: [originalGraph, conflictState(original, { mergeability: "MERGEABLE" })] }],
  ["GraphQL head", { getConflictState: [originalGraph, conflictState(original, { headOid: NEXT })] }],
  ["GraphQL base tip", { getConflictState: [originalGraph, conflictState(original, { baseOid: NEXT })] }],
  ["GraphQL state", { getConflictState: [originalGraph, conflictState(original, { state: "CLOSED" })] }],
]) {
  test(`a changed ${name} at the fresh check prevents the planned write`, async () => {
    const github = fakeGitHub({ snapshots });
    await assertNoWrite(github, { expectedFailure: /changed|stale|fresh|identity|inconsistent|closed|base|head|mergeability/i });
    const changedOperation = Object.keys(snapshots)[0];
    assert.ok(github.calls.filter((call) => call.operation === changedOperation).length >= 2);
  });
}

for (const operation of ["getPullRequest", "getConflictState", "getBranch"]) {
  test(`${operation} failure at the fresh check prevents the planned write`, async () => {
    const github = fakeGitHub({ failure: { operation, at: 2 } });
    await assertNoWrite(github, { expectedFailure: new RegExp(`${operation} read unavailable`) });
    assert.ok(github.calls.filter((call) => call.operation === operation).length >= 2);
  });
}

for (const [field, value] of [
  ["number", 43], ["nodeId", "PR_other"], ["repository", "attacker/fork"],
  ["headOid", NEXT], ["baseBranch", "release-1.2"], ["baseOid", NEXT],
  ["state", "CLOSED"], ["draft", true], ["mergeability", "DIRTY"],
]) {
  test(`inconsistent initial GraphQL ${field} prevents writes`, async () => {
    await assertNoWrite(fakeGitHub({ conflict: { [field]: value } }), {
      expectedFailure: /invalid|inconsistent|identity|mismatch|mergeability|state/i,
    });
  });
}

for (const overrides of [
  { state: "closed" },
  { state: "closed", merged: true },
  { baseBranch: "feature/../escape" },
  { baseRepository: { owner: "attacker", repo: "fork" } },
]) {
  test(`unsupported live PR ${JSON.stringify(overrides)} has no label writes`, async () => {
    await assertNoWrite(fakeGitHub({ pullRequests: [pullRequest(overrides)] }), {
      expectedFailure: /unsupported|closed|state|repository|branch/i,
    });
  });
}

for (const [name, numbers] of [
  ["overflow", Array.from({ length: 101 }, (_, index) => index + 1)],
  ["duplicate", [42, 42]], ["string", [42, "43"]], ["zero", [42, 0]],
  ["fraction", [42, 43.5]], ["non-array", { number: 42 }],
]) {
  test(`schedule rejects ${name} scan candidates before any write`, async () => {
    const github = fakeGitHub({ numbers });
    await assertNoWrite(github, {
      eventName: "schedule", event: { repository, schedule: "*/15 * * * *" },
      expectedFailure: /candidate|pull request number|scan|100|array|duplicate/i,
    });
    assert.ok(github.calls.some((call) => call.operation === "listOpenPullRequestNumbers"));
    assert.equal(github.calls.some((call) => call.operation === "getPullRequest"), false,
      "the complete candidate set must be checked before processing it");
  });
}

test("schedule accepts exactly 100 candidates and emits a bounded summary", async () => {
  const prs = Array.from({ length: 100 }, (_, index) => pullRequest({ number: index + 1, nodeId: `PR_${index + 1}` }));
  const github = fakeGitHub({ pullRequests: prs });
  await invoke(github, { eventName: "schedule", event: { repository, schedule: "*/15 * * * *" }, dryRun: true });
  assert.equal(new Set(github.calls.filter((call) => call.operation === "getPullRequest").map((call) => call.key)).size, 100);
  assert.deepEqual(github.mutations, []);
});

for (const branch of ["main", "release-1.2"]) {
  test(`a ${branch} base push repairs matching PRs without a PR payload or new PR head`, async () => {
    const github = fakeGitHub({ pullRequests: [
      pullRequest(),
      pullRequest({ number: 43, nodeId: "PR_43", baseBranch: "release-1.2" }),
      pullRequest({ number: 44, nodeId: "PR_44", baseBranch: "feature/unsupported" }),
    ] });
    await invoke(github, { eventName: "push", event: pushEvent(branch) });
    assert.deepEqual(github.mutations, [{ operation: "addIssueLabel", number: branch === "main" ? 42 : 43, label: "needs-rebase" }]);
    assert.ok(github.calls.some((call) => call.operation === "listOpenPullRequestNumbers"));
    assert.equal(github.calls.some((call) => call.operation === "getBranch" && call.key === "feature/unsupported"), false);
  });
}

test("schedule repairs all open PRs and excludes closed PRs", async () => {
  const github = fakeGitHub({
    pullRequests: [
      pullRequest(),
      pullRequest({ number: 43, nodeId: "PR_43", baseBranch: "release-1.2" }),
      pullRequest({ number: 44, nodeId: "PR_44", baseBranch: "feature/unsupported" }),
      pullRequest({ number: 45, nodeId: "PR_45", state: "closed" }),
    ],
    conflicts: { 43: { mergeability: "MERGEABLE" } },
    labelsByNumber: { 43: ["needs-rebase", "maintainer/custom"] },
  });
  await invoke(github, { eventName: "schedule", event: { repository, schedule: "*/15 * * * *" } });
  assert.deepEqual(github.mutations, [
    { operation: "addIssueLabel", number: 42, label: "needs-rebase" },
    { operation: "removeIssueLabel", number: 43, label: "needs-rebase" },
    { operation: "addIssueLabel", number: 44, label: "needs-rebase" },
  ]);
  assert.deepEqual([...github.labels.get(44)], ["maintainer/custom", "needs-rebase"]);
  assert.deepEqual([...github.labels.get(45)], ["maintainer/custom"]);
});

test("scheduled conflict scans repair PRs on each live stacked feature base", async () => {
  const branches = ["codex/nri-allocation-aware", "codex/nri-plugin-deployment", "roma/improve-ctl-plane-structure"];
  const github = fakeGitHub({ pullRequests: branches.map((baseBranch, index) =>
    pullRequest({ number: 42 + index, nodeId: `PR_${42 + index}`, baseBranch })) });
  await invoke(github, { eventName: "schedule", event: { repository, schedule: "*/15 * * * *" } });
  assert.deepEqual(github.mutations, branches.map((_, index) =>
    ({ operation: "addIssueLabel", number: 42 + index, label: "needs-rebase" })));
  for (const branch of branches) {
    assert.ok(github.calls.some((call) => call.operation === "getBranch" && call.key === branch));
  }
});

for (const baseBranch of ["", "feature/../escape", "unsafe branch"]) {
  test(`scheduled conflict scan rejects unsafe base ${JSON.stringify(baseBranch)} before writes`, async () => {
    await assertNoWrite(fakeGitHub({ pullRequests: [pullRequest({ baseBranch })] }), {
      eventName: "schedule", event: { repository, schedule: "*/15 * * * *" },
      expectedFailure: /branch|base|invalid|unsafe/i,
    });
  });
}

test("an initial read failure on scan candidate 2 prevents all label writes", async () => {
  const github = fakeGitHub({
    pullRequests: [pullRequest(), pullRequest({ number: 43, nodeId: "PR_43" })],
    failure: { operation: "getConflictState", key: 43 },
  });
  await assertNoWrite(github, {
    eventName: "schedule", event: { repository, schedule: "*/15 * * * *" },
    expectedFailure: /getConflictState read unavailable/,
  });
  assert.ok(github.calls.some((call) => call.operation === "getConflictState" && call.key === 43));
});

for (const [name, overrides] of [
  ["prewrite read", { failure: { operation: "getPullRequest", key: 43, at: 2 } }],
  ["label mutation", { mutationFailure: 43 }],
]) {
  test(`candidate 2 ${name} failure reports the applied first candidate and failed operation`, async () => {
    const github = fakeGitHub({
      pullRequests: [pullRequest(), pullRequest({ number: 43, nodeId: "PR_43" })],
      ...overrides,
    });
    const summary = await invoke(github, {
      eventName: "schedule", event: { repository, schedule: "*/15 * * * *" }, captureFailure: true,
    });
    assert.ok(github.labels.get(42).has("needs-rebase"), "candidate 1 remains applied");
    assert.equal(github.labels.get(43).has("needs-rebase"), false);
    assert.ok(reportsOperation(summary, 42, /applied|added|updated/i), "summary must report the applied PR");
    assert.ok(reportsOperation(summary, 43, /failed|failure/i), "summary must identify the failed PR operation");
    assert.doesNotMatch(JSON.stringify(summary), /rolled.?back|rollback complete/i);
  });
}

for (const [eventName, event] of [
  ["schedule", { repository, schedule: "*/15 * * * *" }],
  ["push", pushEvent()],
]) {
  test(`${eventName} scans include a years-old unchanged open PR`, async () => {
    const github = fakeGitHub({ pullRequests: [pullRequest({
      created_at: "2019-01-01T00:00:00Z", updated_at: "2019-01-01T00:00:00Z",
    })] });
    await invoke(github, { eventName, event });
    assert.deepEqual(github.mutations, [{ operation: "addIssueLabel", number: 42, label: "needs-rebase" }]);
    assert.ok(github.calls.some((call) => call.operation === "listOpenPullRequestNumbers"));
  });
}

for (const [name, eventName, event] of [
  ["untrusted PR event", "pull_request", prEvent()],
  ["manual event", "workflow_dispatch", { repository }],
  ["unsupported PR action", "pull_request_target", prEvent({ action: "labeled" })],
  ["conflicting PR numbers", "pull_request_target", prEvent({ number: 43 })],
  ["malformed PR number", "pull_request_target", prEvent({ number: "42", pull_request: { number: "42" } })],
  ["PR and schedule", "pull_request_target", prEvent({ schedule: "*/15 * * * *" })],
  ["PR and push", "pull_request_target", prEvent({ ref: "refs/heads/main", after: BASE })],
  ["schedule and PR", "schedule", { repository, schedule: "*/15 * * * *", pull_request: { number: 42 } }],
  ["missing schedule", "schedule", { repository }],
  ["push and PR", "push", pushEvent("main", { pull_request: { number: 42 } })],
  ["push and schedule", "push", pushEvent("main", { schedule: "*/15 * * * *" })],
  ["deleted base branch", "push", pushEvent("main", { deleted: true })],
  ["zero push head", "push", pushEvent("main", { after: "0".repeat(40) })],
  ["tag push", "push", pushEvent("main", { ref: "refs/tags/v1.0.0" })],
  ["unsupported base push", "push", pushEvent("feature/unrelated")],
  ["mismatched repository", "pull_request_target", prEvent({ repository: { ...repository, full_name: "attacker/fork" } })],
]) {
  test(`${name} cannot route to label mutations`, async () => {
    await assertNoWrite(fakeGitHub(), {
      eventName, event, expectedFailure: /event|payload|action|number|repository|schedule|push|branch|ref|deleted|unsupported/i,
    });
  });
}

async function httpClient(options = {}) {
  const { getOctokit } = await import("@actions/github");
  const requests = [];
  const labels = new Set(options.labels ?? ["needs-rebase"]);
  const octokit = getOctokit("contract-test-token", {
    baseUrl: "https://github-api.example.test",
    request: {
      fetch: async (url, options) => {
        const pathname = new globalThis.URL(url).pathname;
        const body = options.body === undefined ? null : JSON.parse(options.body);
        requests.push({ method: options.method, pathname, body });
        let data;
        if (pathname === "/graphql") {
          data = { data: { repository: { pullRequest: {
            number: 42, id: "PR_node_42", state: "OPEN", isDraft: false,
            headRefOid: HEAD, baseRefName: "main", baseRefOid: BASE,
            mergeable: "CONFLICTING", autoMergeRequest: null,
          } } } };
        } else if (pathname === "/repos/NVIDIA/k8s-test-infra/pulls/42") {
          data = {
            number: 42, node_id: "PR_node_42", title: "feat: conflict transport",
            body: "", draft: false, state: "open", user: { login: "contributor" },
            head: { sha: HEAD },
            base: { ref: "main", repo: { owner: { login: "NVIDIA" }, name: "k8s-test-infra" } },
          };
        } else if (pathname === "/repos/NVIDIA/k8s-test-infra/branches/main") {
          data = { name: "main", commit: { sha: BASE } };
        } else {
          assert.ok(pathname.startsWith("/repos/NVIDIA/k8s-test-infra/issues/42/labels"), pathname);
          if (options.method === "POST") body.labels.forEach((label) => labels.add(label));
          if (options.method === "DELETE") labels.delete(decodeURIComponent(pathname.split("/").at(-1)));
          data = [...labels].map((name) => ({ name }));
        }
        return new globalThis.Response(JSON.stringify(data), {
          status: 200, headers: { "content-type": "application/json" },
        });
      },
    },
  });
  return { octokit, client: createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 }), requests, labels };
}

test("run constructs the real Octokit adapter, applies a label, and replay makes no second write", async () => {
  const { octokit, requests, labels } = await httpClient({ labels: ["maintainer/custom"] });
  await invoke(undefined, { octokit });
  assert.deepEqual([...labels].sort(), ["maintainer/custom", "needs-rebase"]);
  await invoke(undefined, { octokit });
  assert.deepEqual(requests.filter((request) => request.method === "POST" && request.pathname.endsWith("/labels")), [
    { method: "POST", pathname: "/repos/NVIDIA/k8s-test-infra/issues/42/labels", body: { labels: ["needs-rebase"] } },
  ]);
  assert.equal(requests.some((request) => request.method === "DELETE"), false);
  for (const pathname of ["/graphql", "/repos/NVIDIA/k8s-test-infra/pulls/42", "/repos/NVIDIA/k8s-test-infra/branches/main"]) {
    assert.ok(requests.filter((request) => request.pathname === pathname).length >= 2);
  }
});

test("real Octokit HTTP transport maps GraphQL base commit and REST PR identity", async () => {
  const { client, requests } = await httpClient();
  const pr = await client.getPullRequest(42);
  assert.equal(pr.headOid, HEAD);
  assert.equal(pr.baseBranch, "main");
  assert.deepEqual(pr.baseRepository, { owner: "nvidia", repo: "k8s-test-infra" });
  assert.deepEqual(await client.getConflictState(42), conflictState(pullRequest()));
  assert.deepEqual(await client.getBranch("main"), { name: "main", oid: BASE });
  assert.deepEqual(await client.listIssueLabels(42), ["needs-rebase"]);
  const graphRequest = requests.find((request) => request.pathname === "/graphql");
  assert.match(graphRequest.body.query, /\bbaseRefOid\b/);
  assert.deepEqual(graphRequest.body.variables, { owner: "NVIDIA", repo: "k8s-test-infra", number: 42 });
});

test("real Octokit HTTP transport admits only the exact conflict label", async () => {
  const { client, requests } = await httpClient();
  await client.addIssueLabel(42, "needs-rebase");
  await client.removeIssueLabel(42, "needs-rebase");
  assert.deepEqual(requests, [
    { method: "POST", pathname: "/repos/NVIDIA/k8s-test-infra/issues/42/labels", body: { labels: ["needs-rebase"] } },
    { method: "DELETE", pathname: "/repos/NVIDIA/k8s-test-infra/issues/42/labels/needs-rebase", body: null },
  ]);
  for (const label of ["NEEDS-REBASE", "needs-rebase/other", "maintainer/custom"]) {
    await assert.rejects(() => client.addIssueLabel(42, label), /managed/);
    await assert.rejects(() => client.removeIssueLabel(42, label), /managed/);
  }
  assert.equal(requests.length, 2, "rejected labels must not make an HTTP request");
});
