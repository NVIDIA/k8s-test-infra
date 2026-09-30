"use strict";

const assert = require("node:assert/strict");
const { Buffer } = require("node:buffer");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const { loadConfig } = require("../src/config.js");
const { run } = require("../src/index.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const HEAD = "a".repeat(40);
const BASE = "b".repeat(40);
const NEXT = "c".repeat(40);
const POLICY = "d".repeat(40);
const repository = { name: "k8s-test-infra", full_name: "NVIDIA/k8s-test-infra", owner: { login: "NVIDIA" } };
const schedule = { repository, schedule: "*/15 * * * *" };
const preserved = ["needs-rebase", "approved", "do-not-merge/hold", "maintainer/custom"];
const desired = ["area/docs", "kind/feature", "size/M"];
const files = [{ path: "docs/guide.md", additions: 60, deletions: 20, status: "modified" }];

function pullRequest(overrides = {}) {
  return {
    number: 42, nodeId: "PR_42", state: "open", title: "feat: classify old open PRs",
    body: "private-body-must-not-enter-the-summary", draft: false, author: "contributor",
    headOid: HEAD, baseBranch: "main", baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    created_at: "2019-01-01T00:00:00Z", updated_at: "2019-01-01T00:00:00Z",
    ...overrides,
  };
}

function push(branch = "main") {
  return { repository, ref: `refs/heads/${branch}`, before: NEXT, after: BASE, deleted: false };
}

function fakeGitHub(options = {}) {
  const prs = options.pullRequests ?? [pullRequest()];
  const labels = new Map(prs.map((pr) => [pr.number, new Set(options.labelsByNumber?.[pr.number]
    ?? options.labels ?? [...preserved, "kind/bug", "area/ci", "size/S"])]));
  const calls = [];
  const order = [];
  const mutations = [];
  const forbidden = [];
  const injected = [];
  const counts = new Map();
  const copy = (value) => JSON.parse(JSON.stringify(value));
  function read(operation, key, fallback) {
    calls.push({ operation, key });
    order.push({ operation, key });
    const countKey = `${operation}:${key}`;
    const count = (counts.get(countKey) ?? 0) + 1;
    counts.set(countKey, count);
    if (options.failure?.operation === operation
      && (options.failure.key === undefined || options.failure.key === key)
      && count === (options.failure.at ?? 1)) {
      const error = new Error(`${operation} read unavailable`);
      injected.push(error);
      throw error;
    }
    const snapshots = options.snapshots?.[operation];
    return copy(snapshots?.[count - 1] ?? fallback);
  }
  const github = {
    calls, order, mutations, labels, forbidden, injected,
    async listOpenPullRequestNumbers() {
      return read("listOpenPullRequestNumbers", "all", options.numbers ?? prs.map((pr) => pr.number));
    },
    async getPullRequest(number) {
      const pr = prs.find((candidate) => candidate.number === number);
      assert.ok(pr, `unexpected PR ${number}`);
      return read("getPullRequest", number, pr);
    },
    async getBranch(branch) { return read("getBranch", branch, { name: branch, oid: BASE }); },
    async getDefaultBranchRevision() { return read("getDefaultBranchRevision", "policy", POLICY); },
    async listPullRequestFiles(number) { return read("listPullRequestFiles", number, options.files ?? files); },
    async listIssueLabels(number) { return read("listIssueLabels", number, [...labels.get(number)]); },
    async addIssueLabel(number, label) {
      mutations.push({ operation: "addIssueLabel", number, label });
      order.push({ operation: "addIssueLabel", key: number });
      if (options.mutationFailure === number) throw new Error("label mutation unavailable");
      if (!options.stalled) labels.get(number).add(label);
    },
    async removeIssueLabel(number, label) {
      mutations.push({ operation: "removeIssueLabel", number, label });
      order.push({ operation: "removeIssueLabel", key: number });
      if (!options.stalled) labels.get(number).delete(label);
    },
  };
  for (const operation of [
    "requestReviewers", "upsertPolicyComment", "getPolicyComment", "getContentAtRevision",
    "listPullRequestCommits", "listPullRequestReviews", "listRequestedReviewers", "getConflictState",
    "addPolicyLabel", "removePolicyLabel", "setMergePolicyCheck", "disableAutoMerge",
    "enableAutoMerge", "mergePullRequest", "rerunFailedJobs", "createBackportPullRequest",
  ]) {
    github[operation] = async () => {
      forbidden.push(operation);
      throw new Error(`metadata label scans must not call ${operation}`);
    };
  }
  return github;
}

async function invoke(githubClient, options = {}) {
  const outputs = options.outputs ?? new Map();
  const core = {
    summary: options.jobSummary,
    getInput(name) {
      if (name === "mode") return "metadata-labels";
      if (name === "policy-revision") return options.policyRevision === undefined ? POLICY : options.policyRevision;
      return "";
    },
    getBooleanInput() { return options.dryRun ?? false; },
    setOutput(name, value) { outputs.set(name, value); },
    info(message) { options.info?.push(message); },
  };
  let summary;
  try {
    summary = await run({
      core, githubClient, owner: "NVIDIA", repo: "k8s-test-infra", workspace: repositoryRoot,
      eventName: options.eventName ?? "schedule", event: options.event ?? schedule,
    });
  } catch (error) {
    if (!options.captureFailure) throw error;
    assert.doesNotMatch(error.message, /Unsupported mode|is not a function|Cannot read|is not defined/i);
    assert.ok(error.summary, "partial failure must expose its operation summary");
    summary = error.summary;
  }
  const serialized = outputs.get("summary");
  assert.equal(typeof serialized, "string");
  assert.deepEqual(JSON.parse(serialized), summary);
  assert.ok(Buffer.byteLength(serialized, "utf8") <= 64 * 1024);
  assert.equal(serialized.includes("private-body-must-not-enter-the-summary"), false);
  assert.deepEqual(githubClient.forbidden, []);
  assert.ok(githubClient.mutations.every(({ label }) =>
    /^(?:kind\/|size\/|area\/)/i.test(label) || label.toLowerCase() === "do-not-merge/work-in-progress"),
  "scan writes must stay inside the metadata label allowlist");
  return summary;
}

function reports(summary, number, outcome) {
  function visit(value, keyPath) {
    if (value === null || typeof value !== "object") return false;
    if ((value.prNumber === number || value.number === number)
      && outcome.test(`${keyPath} ${JSON.stringify(value)}`)) return true;
    return Object.entries(value).some(([key, child]) => visit(child, `${keyPath}.${key}`));
  }
  return visit(summary, "summary");
}

async function noWrite(github, options = {}) {
  try {
    await invoke(github, options);
  } catch (error) {
    assert.doesNotMatch(error.message, /Unsupported mode|is not a function|Cannot read|is not defined|ENOENT/i);
    const deliberate = options.expectedFailure?.test(error.message) ?? false;
    const injected = github.injected.length > 0 && /metadata.*(?:initial|fresh).*read.*failed/i.test(error.message);
    assert.ok(deliberate || injected, `unexpected no-write failure: ${error.message}`);
  }
  assert.deepEqual(github.mutations, []);
  assert.deepEqual(github.forbidden, []);
}

test("successful metadata scan publishes the exact output JSON to the action log", async () => {
  const github = fakeGitHub({ labels: [] });
  const outputs = new Map();
  const info = [];
  const summary = await invoke(github, { outputs, info });
  assert.deepEqual([...github.labels.get(42)].sort(), desired);
  assert.ok(reports(summary, 42, /applied|updated/i));
  assert.deepEqual(info, [`Repository automation metadata-labels: ${outputs.get("summary")}`]);
});

test("scheduled metadata scan repairs all old open PRs and preserves other labels", async () => {
  const github = fakeGitHub({ pullRequests: [
    pullRequest(),
    pullRequest({ number: 43, nodeId: "PR_43", baseBranch: "release-1.2", draft: true }),
    pullRequest({ number: 44, nodeId: "PR_44", baseBranch: "feature/unrelated" }),
    pullRequest({ number: 45, nodeId: "PR_45", state: "closed" }),
  ] });
  const summary = await invoke(github);
  assert.deepEqual([...github.labels.get(42)].sort(), [...preserved, ...desired].sort());
  assert.deepEqual([...github.labels.get(43)].sort(), [...preserved, ...desired, "do-not-merge/work-in-progress"].sort());
  assert.deepEqual([...github.labels.get(44)].sort(), [...preserved, ...desired].sort());
  assert.deepEqual([...github.labels.get(45)].sort(), [...preserved, "kind/bug", "area/ci", "size/S"].sort());
  assert.ok(reports(summary, 42, /applied|updated/i));
  const firstWrite = github.order.findIndex((call) => /^(?:add|remove)IssueLabel$/.test(call.operation));
  assert.ok(firstWrite > 0);
  for (const operation of ["listOpenPullRequestNumbers", "getDefaultBranchRevision"]) {
    const readIndex = github.order.findIndex((call) => call.operation === operation);
    assert.ok(readIndex >= 0 && readIndex < firstWrite, `${operation} must complete before the first label write`);
  }
  for (const number of [42, 43, 44]) {
    for (const operation of ["getPullRequest", "listPullRequestFiles", "listIssueLabels"]) {
      const readIndex = github.order.findIndex((call) => call.operation === operation && call.key === number);
      assert.ok(readIndex >= 0 && readIndex < firstWrite,
        `${operation} for PR ${number} must complete before the first label write`);
    }
  }
  for (const branch of ["main", "release-1.2", "feature/unrelated"]) {
    const readIndex = github.order.findIndex((call) => call.operation === "getBranch" && call.key === branch);
    assert.ok(readIndex >= 0 && readIndex < firstWrite,
      `base ${branch} must be read before the first label write`);
  }
  for (const operation of ["getPullRequest", "getBranch", "listPullRequestFiles", "listIssueLabels", "getDefaultBranchRevision"]) {
    assert.ok(github.calls.some((call) => call.operation === operation), `${operation} must read live state`);
  }
});

test("scheduled metadata scans label PRs on each live stacked feature base", async () => {
  const branches = ["codex/nri-allocation-aware", "codex/nri-plugin-deployment", "roma/improve-ctl-plane-structure"];
  const github = fakeGitHub({ labels: [], pullRequests: branches.map((baseBranch, index) =>
    pullRequest({ number: 42 + index, nodeId: `PR_${42 + index}`, baseBranch })) });
  await invoke(github);
  for (let index = 0; index < branches.length; index += 1) {
    assert.deepEqual([...github.labels.get(42 + index)].sort(), desired);
    assert.ok(github.calls.some((call) => call.operation === "getBranch" && call.key === branches[index]));
  }
});

test("an old unlabeled PR with an invalid title receives size and area labels", async () => {
  const github = fakeGitHub({ labels: [], pullRequests: [pullRequest({ title: "unfinished change", baseBranch: "feature/stacked" })] });
  await invoke(github);
  assert.deepEqual([...github.labels.get(42)].sort(), ["area/docs", "size/M"]);
});

for (const [author, title, changedFiles, expected] of [
  ["dependabot[bot]", "chore(deps): bump automation dependencies",
    [{ path: ".github/actions/repo-automation/package-lock.json", additions: 60, deletions: 20 }],
    ["area/ci", "kind/dependencies", "size/M"]],
  ["github-actions[bot]", "docs: update generated guidance", files,
    ["area/docs", "kind/documentation", "size/M"]],
]) {
  test(`an old unlabeled PR by ${author} receives useful metadata labels`, async () => {
    const github = fakeGitHub({ labels: [], files: changedFiles, pullRequests: [pullRequest({ author, title })] });
    const summary = await invoke(github);
    assert.deepEqual([...github.labels.get(42)].sort(), expected);
    assert.ok(reports(summary, 42, /applied|updated/i));
  });
}

test("a changed bot author at the fresh metadata read prevents writes", async () => {
  const pr = pullRequest({ author: "dependabot[bot]" });
  const github = fakeGitHub({ pullRequests: [pr], snapshots: {
    getPullRequest: [pr, { ...pr, author: "github-actions[bot]" }],
  } });
  const summary = await invoke(github);
  assert.ok(github.calls.filter((entry) => entry.operation === "getPullRequest").length >= 2,
    "the valid initial bot author must reach the fresh identity check");
  assert.deepEqual(github.mutations, []);
  assert.ok(reports(summary, 42, /changed.*fresh|fresh.*changed/i));
});

for (const baseBranch of ["", "feature/../escape", "unsafe branch"]) {
  test(`scheduled metadata scan rejects unsafe base ${JSON.stringify(baseBranch)} before writes`, async () => {
    const github = fakeGitHub({ pullRequests: [pullRequest({ baseBranch })] });
    await noWrite(github, { expectedFailure: /branch|base|invalid|unsafe/i });
  });
}

test("a live default revision that differs from loaded policy prevents metadata writes", async () => {
  const github = fakeGitHub({ snapshots: { getDefaultBranchRevision: [NEXT] } });
  await noWrite(github, { expectedFailure: /policy|revision|changed|stale/i });
  assert.ok(github.calls.some((call) => call.operation === "getDefaultBranchRevision"));
});

for (const policyRevision of ["", "not-a-commit"]) {
  test(`invalid pinned policy revision ${JSON.stringify(policyRevision)} prevents metadata writes`, async () => {
    const github = fakeGitHub();
    await noWrite(github, { policyRevision, expectedFailure: /policy|revision|invalid|commit/i });
  });
}

for (const branch of ["main", "release-1.2"]) {
  test(`a ${branch} push scans old PRs with unchanged heads and matching bases`, async () => {
    const github = fakeGitHub({ pullRequests: [pullRequest(), pullRequest({ number: 43, nodeId: "PR_43", baseBranch: "release-1.2" })] });
    await invoke(github, { eventName: "push", event: push(branch) });
    assert.ok(github.mutations.length > 0);
    assert.ok(github.mutations.every((entry) => entry.number === (branch === "main" ? 42 : 43)));
  });
}

test("metadata scan apply followed by replay performs no additional label writes", async () => {
  const github = fakeGitHub();
  await invoke(github);
  const firstWrites = [...github.mutations];
  assert.ok(firstWrites.length > 0);
  await invoke(github);
  assert.deepEqual(github.mutations, firstWrites);
});

test("initially correct metadata labels need only one snapshot and no fresh reads or writes", async () => {
  const github = fakeGitHub({ labels: [...preserved, ...desired] });
  const summary = await invoke(github);
  assert.ok(reports(summary, 42, /unchanged/i));
  assert.deepEqual(github.mutations, []);
  for (const operation of ["getPullRequest", "getBranch", "getDefaultBranchRevision", "listPullRequestFiles", "listIssueLabels"]) {
    assert.equal(github.calls.filter((entry) => entry.operation === operation).length, 1,
      `${operation} must not repeat when the initial labels are correct`);
  }
});

test("current labels with different case do not cause repeated writes", async () => {
  const github = fakeGitHub({ labels: [...preserved, "AREA/DOCS", "KIND/FEATURE", "SIZE/M"] });
  await invoke(github);
  assert.deepEqual(github.mutations, []);
});

test("fresh labels already applied by another actor prevent obsolete planned writes", async () => {
  const current = [...preserved, ...desired];
  const github = fakeGitHub({ labels: current, snapshots: { listIssueLabels: [[...preserved, "kind/bug", "size/S"]] } });
  await invoke(github);
  assert.deepEqual(github.mutations, []);
  assert.deepEqual([...github.labels.get(42)], current);
  assert.ok(github.calls.filter((entry) => entry.operation === "listIssueLabels").length >= 2);
});

test("fresh labels introduced after planning are included in metadata reconciliation", async () => {
  const github = fakeGitHub({ labels: [...preserved, "area/ci"], snapshots: { listIssueLabels: [[...preserved]] } });
  await invoke(github);
  assert.deepEqual([...github.labels.get(42)].sort(), [...preserved, ...desired].sort());
  assert.ok(github.mutations.some((entry) => entry.operation === "removeIssueLabel" && entry.label === "area/ci"));
});

test("invalid title preserves current kind labels while size, area, and draft labels are repaired", async () => {
  const github = fakeGitHub({ pullRequests: [pullRequest({ title: "invalid title", draft: true })] });
  await invoke(github);
  assert.deepEqual([...github.labels.get(42)].sort(), [
    ...preserved, "kind/bug", "size/M", "area/docs", "do-not-merge/work-in-progress",
  ].sort());
});

test("DCO and ownership blocks do not gate useful metadata labels", async () => {
  const github = fakeGitHub({ labels: [...preserved, "do-not-merge/invalid-dco"] });
  await invoke(github);
  assert.deepEqual([...github.labels.get(42)].sort(), [...preserved, "do-not-merge/invalid-dco", ...desired].sort());
});

test("invalid configuration defers the scan and preserves every existing label", async (t) => {
  const readFileSync = fs.readFileSync;
  const invalidPath = path.join(repositoryRoot, ".github", "repo-automation", "areas.yml");
  t.mock.method(fs, "readFileSync", (filePath, ...options) =>
    String(filePath) === invalidPath ? "schemaVersion: [unterminated\n" : readFileSync(filePath, ...options));
  assert.throws(() => loadConfig(repositoryRoot), /configuration/);
  const github = fakeGitHub();
  const summary = await invoke(github);
  assert.deepEqual(github.mutations, []);
  assert.deepEqual([...github.labels.get(42)].sort(), [...preserved, "kind/bug", "area/ci", "size/S"].sort());
  assert.match(JSON.stringify(summary), /defer|configuration.*invalid/i);
});

test("dry-run reads and plans old PR labels without writes", async () => {
  const github = fakeGitHub();
  const summary = await invoke(github, { dryRun: true });
  assert.deepEqual(github.mutations, []);
  assert.match(JSON.stringify(summary), /planned|dryRun/);
  assert.ok(github.calls.some((entry) => entry.operation === "listPullRequestFiles"));
});

for (const [name, numbers] of [
  ["overflow", Array.from({ length: 101 }, (_, index) => index + 1)],
  ["duplicate", [42, 42]], ["zero", [42, 0]], ["negative", [42, -1]],
  ["string", [42, "43"]], ["fraction", [42, 43.5]], ["non-array", { number: 42 }],
]) {
  test(`metadata scan rejects ${name} candidates before PR reads and writes`, async () => {
    const github = fakeGitHub({ numbers });
    await noWrite(github, { expectedFailure: /candidate|scan|100|array|number|duplicate/i });
    assert.ok(github.calls.some((entry) => entry.operation === "listOpenPullRequestNumbers"));
    assert.equal(github.calls.some((entry) => entry.operation === "getPullRequest"), false);
  });
}

test("metadata scan accepts exactly 100 candidates with a bounded dry-run summary", async () => {
  const github = fakeGitHub({ pullRequests: Array.from({ length: 100 }, (_, index) =>
    pullRequest({ number: index + 1, nodeId: `PR_${index + 1}` })) });
  await invoke(github, { dryRun: true });
  assert.equal(new Set(github.calls.filter((entry) => entry.operation === "getPullRequest").map((entry) => entry.key)).size, 100);
  assert.deepEqual(github.mutations, []);
});

for (const [name, file] of [
  ["negative additions", { ...files[0], additions: -1 }],
  ["fractional additions", { ...files[0], additions: 1.5 }],
  ["string additions", { ...files[0], additions: "60" }],
  ["missing additions", { path: "docs/guide.md", deletions: 20 }],
  ["unsafe additions", { ...files[0], additions: Number.MAX_SAFE_INTEGER + 1 }],
  ["negative deletions", { ...files[0], deletions: -1 }],
  ["empty path", { ...files[0], path: "" }],
  ["absolute path", { ...files[0], path: "/docs/guide.md" }],
  ["traversal path", { ...files[0], path: "docs/../guide.md" }],
  ["backslash path", { ...files[0], path: "docs\\guide.md" }],
  ["NUL path", { ...files[0], path: "docs/guide\0.md" }],
]) {
  test(`malformed changed file ${name} fails its initial read without writes`, async () => {
    const github = fakeGitHub({ files: [file] });
    const summary = await invoke(github, { captureFailure: true });
    assert.ok(reports(summary, 42, /initial read failed/i));
    assert.deepEqual(github.mutations, []);
    assert.ok(github.calls.some((entry) => entry.operation === "listPullRequestFiles" && entry.key === 42));
  });
}

for (const [name, liveLabels] of [
  ["non-array", { name: "size/M" }],
  ["non-string", ["size/M", null]],
  ["empty", [""]],
  ["control character", ["maintainer/custom\n"]],
  ["duplicate", ["size/M", "SIZE/M"]],
  ["overflow", Array.from({ length: 1001 }, (_, index) => `maintainer/custom-${index}`)],
]) {
  test(`invalid ${name} live labels fail their initial read without writes`, async () => {
    const github = fakeGitHub({ snapshots: { listIssueLabels: [liveLabels] } });
    const summary = await invoke(github, { captureFailure: true });
    assert.ok(reports(summary, 42, /initial read failed/i));
    assert.deepEqual(github.mutations, []);
    assert.ok(github.calls.some((entry) => entry.operation === "listIssueLabels" && entry.key === 42));
  });
}

test("more than 1000 changed files fails the initial read without writes", async () => {
  const github = fakeGitHub({ files: Array.from({ length: 1001 }, (_, index) =>
    ({ path: `docs/file-${index}.md`, additions: 0, deletions: 0 })) });
  const summary = await invoke(github, { captureFailure: true });
  assert.ok(reports(summary, 42, /initial read failed/i));
  assert.deepEqual(github.mutations, []);
});

test("unchanged fresh labels after a successful mutation fail without duplicate writes", async () => {
  const github = fakeGitHub({ stalled: true, labels: [...preserved, "kind/feature", "size/M"] });
  const summary = await invoke(github, { captureFailure: true });
  assert.equal(github.mutations.length, 1, "a stalled write must not be sent a second time");
  assert.deepEqual(github.mutations[0], { operation: "addIssueLabel", number: 42, label: "area/docs" });
  assert.ok(reports(summary, 42, /failed/i));
  assert.ok(reports(summary, 42, /stall|progress|unchanged|repeated/i));
  assert.deepEqual([...github.labels.get(42)], [...preserved, "kind/feature", "size/M"]);
});

const original = pullRequest();
for (const [name, snapshots] of [
  ["head", { getPullRequest: [original, pullRequest({ headOid: NEXT })] }],
  ["title", { getPullRequest: [original, pullRequest({ title: "fix: title changed" })] }],
  ["draft", { getPullRequest: [original, pullRequest({ draft: true })] }],
  ["author", { getPullRequest: [original, pullRequest({ author: "another-contributor" })] }],
  ["base branch", { getPullRequest: [original, pullRequest({ baseBranch: "release-1.2" })] }],
  ["base repository", { getPullRequest: [original, pullRequest({ baseRepository: { owner: "attacker", repo: "fork" } })] }],
  ["state", { getPullRequest: [original, pullRequest({ state: "closed" })] }],
  ["base tip", { getBranch: [{ name: "main", oid: BASE }, { name: "main", oid: NEXT }] }],
  ["trusted policy revision", { getDefaultBranchRevision: [POLICY, NEXT] }],
  ["desired labels", { listPullRequestFiles: [files, [{ path: "pkg/gpu/mocknvml/model.go", additions: 300, deletions: 1 }]] }],
]) {
  test(`changed ${name} at the fresh metadata check prevents all planned writes`, async () => {
    const github = fakeGitHub({ snapshots });
    await noWrite(github, { expectedFailure: /changed|fresh|invalid|identity|state|repository|branch|snapshot/i });
    const changedOperation = Object.keys(snapshots)[0];
    assert.ok(github.calls.filter((entry) => entry.operation === changedOperation).length >= 2);
  });
}

test("each mutation checks fresh identity again and stops when the head changes", async () => {
  const github = fakeGitHub({ snapshots: { getPullRequest: [original, original, pullRequest({ headOid: NEXT })] } });
  const summary = await invoke(github, { captureFailure: true });
  assert.equal(github.mutations.length, 1);
  assert.ok(reports(summary, 42, /applied|partial|changed|defer/i));
});

test("an initial read failure on candidate 2 leaves all labels unchanged", async () => {
  const github = fakeGitHub({
    pullRequests: [pullRequest(), pullRequest({ number: 43, nodeId: "PR_43" })],
    failure: { operation: "listPullRequestFiles", key: 43 },
  });
  await noWrite(github);
  assert.equal(github.injected.length, 1);
});

for (const [name, options] of [
  ["fresh read", { failure: { operation: "getPullRequest", key: 43, at: 2 } }],
  ["mutation", { mutationFailure: 43 }],
]) {
  test(`candidate 2 ${name} failure reports earlier applied labels without rollback`, async () => {
    const github = fakeGitHub({ pullRequests: [pullRequest(), pullRequest({ number: 43, nodeId: "PR_43" })], ...options });
    const summary = await invoke(github, { captureFailure: true });
    assert.deepEqual([...github.labels.get(42)].sort(), [...preserved, ...desired].sort());
    assert.ok(reports(summary, 42, /applied|updated/i));
    assert.ok(reports(summary, 43, /failed|failure/i));
    assert.doesNotMatch(JSON.stringify(summary), /rolled.?back|rollback complete/i);
  });
}

for (const [name, options, expectedError] of [
  ["fresh read", { failure: { operation: "getPullRequest", key: 43, at: 2 } }, /metadata label fresh read failed for PR 43/],
  ["mutation", { mutationFailure: 43 }, /metadata label label mutation failed for PR 43/],
]) {
  test(`candidate 2 ${name} failure publishes exact partial JSON to the job summary and action log and still throws`, async () => {
    const github = fakeGitHub({ pullRequests: [pullRequest(), pullRequest({ number: 43, nodeId: "PR_43" })], ...options });
    const outputs = new Map();
    const info = [];
    const codeBlocks = [];
    const published = [];
    const jobSummary = {
      addHeading() { return this; },
      addCodeBlock(content, language) {
        codeBlocks.push({ content, language });
        published.push("codeBlock");
        return this;
      },
      async write() {
        published.push("write");
        return this;
      },
    };
    let failedSummary;
    await assert.rejects(() => invoke(github, { outputs, jobSummary, info }), (error) => {
      assert.match(error.message, expectedError);
      assert.ok(error.summary);
      failedSummary = error.summary;
      return true;
    });
    assert.deepEqual([...github.labels.get(42)].sort(), [...preserved, ...desired].sort());
    assert.ok(reports(failedSummary, 42, /applied|updated/i));
    assert.ok(reports(failedSummary, 43, /failed|failure/i));
    const serialized = outputs.get("summary");
    assert.equal(typeof serialized, "string");
    assert.deepEqual(JSON.parse(serialized), failedSummary);
    assert.ok(Buffer.byteLength(serialized, "utf8") <= 64 * 1024);
    assert.deepEqual(codeBlocks, [{ content: serialized, language: "json" }]);
    assert.deepEqual(published, ["codeBlock", "write"]);
    assert.deepEqual(info, [`Repository automation metadata-labels: ${serialized}`]);
    assert.equal(serialized.includes("private-body-must-not-enter-the-summary"), false);
    assert.deepEqual(github.forbidden, []);
  });
}

for (const [eventName, event] of [
  ["pull_request_target", { repository, action: "synchronize", number: 42, pull_request: { number: 42 } }],
  ["workflow_dispatch", { repository }],
  ["schedule", { ...schedule, pull_request: { number: 42 } }],
  ["push", { ...push(), schedule: "*/15 * * * *" }],
  ["push", push("feature/unrelated")],
]) {
  test(`${eventName} with an unsupported or ambiguous form cannot run metadata labels`, async () => {
    const github = fakeGitHub();
    await noWrite(github, { eventName, event, expectedFailure: /event|route|ambiguous|unsupported|scan/i });
  });
}
