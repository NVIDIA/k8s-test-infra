"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const { run } = require("../src/index.js");

const root = path.resolve(__dirname, "../../../..");
const REQUEST = "12345678-1234-4234-8234-123456789abc";
const SHA = "a".repeat(40);
const BASE = "b".repeat(40);
const repository = { id: 733665780, node_id: "R_kgDOK7rZ9A", name: "k8s-test-infra",
  full_name: "NVIDIA/k8s-test-infra", owner: { login: "NVIDIA" } };
function fake(options = {}) {
  const numbers = options.numbers ?? [42];
  const labels = new Map(numbers.map((n) => [n, new Set(options.labels ?? [])]));
  const calls = [];
  const writes = [];
  const counts = new Map();
  const comments = new Map();
  let liveTitle = "feat: private title";
  function read(op, number, value) {
    calls.push({ op, number });
    const key = `${op}:${number}`;
    const count = (counts.get(key) ?? 0) + 1;
    counts.set(key, count);
    if (op === "labels" && options.changeDuringFinalLabels && count > 1
      && (options.mode === "conflict-labels" ? value.includes("needs-rebase")
        : ["kind/feature", "size/S", "area/docs"].every((label) => value.includes(label)))) {
      liveTitle = "fix: changed during final label read";
    }
    if (options.fail?.op === op && options.fail.number === number && count === (options.fail.at ?? 1)) {
      throw new Error("injected read failure");
    }
    if (op === "pr" && options.changedAt === count) return { ...value, title: "fix: changed input" };
    return value;
  }
  return {
    calls, writes, labels,
    async listOpenPullRequestNumbers() {
      if (options.enumerationFailure) throw new Error("catalog unavailable");
      return numbers;
    },
    async getPullRequest(number) { return read("pr", number, {
      number, nodeId: `PR_${number}`, title: liveTitle, body: "private body",
      state: "open", headOid: SHA, draft: false, author: "dependabot[bot]", baseBranch: "main",
      baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    }); },
    async getConflictState(number) { return read("graph", number, {
      number, nodeId: `PR_${number}`, repository: "nvidia/k8s-test-infra", state: "OPEN", draft: false,
      headOid: SHA, baseBranch: "main", baseOid: BASE, mergeability: options.mergeability ?? "CONFLICTING",
    }); },
    async getBranch(branch) { return { name: branch, oid: BASE }; },
    async getDefaultBranchRevision() { return SHA; },
    async listPullRequestFiles(number) { return read("files", number, [{ path: "docs/guide.md", additions: 1, deletions: 0 }]); },
    async listIssueLabels(number) { return read("labels", number, [...labels.get(number)]); },
    async listPullRequestCommits(number) { return read("commits", number, [{ sha: SHA,
      author: { login: "contributor" }, parents: [{ sha: BASE }],
      commit: { author: { name: "Contributor", email: "contributor@example.com" },
        message: "feat: test\n\nSigned-off-by: Contributor <contributor@example.com>" } }]); },
    async getContentAtRevision(filePath, revision) {
      assert.equal(revision, SHA);
      return read("content", filePath, filePath === "/OWNERS_ALIASES"
        ? "aliases: {}\n" : "reviewers: [alice]\napprovers: [bob]\n");
    },
    async getPolicyComment(number) { return read("comment", number, comments.has(number)
      ? { action: "update", ...comments.get(number) } : { action: "create", id: null, body: null }); },
    async upsertPolicyComment(number, marker, body, plan) {
      assert.ok(body.includes(marker));
      const id = plan.id ?? number;
      comments.set(number, { id, body });
      calls.push({ op: "comment-write", number });
      return { action: plan.action === "create" ? "created" : "updated", id };
    },
    async addIssueLabel(number, label) {
      writes.push({ number, label, op: "add" });
      if (options.writeFailure === number) throw new Error("injected write failure");
      if (!options.stalled) labels.get(number).add(label);
    },
    async removeIssueLabel(number, label) { writes.push({ number, label, op: "remove" }); labels.get(number).delete(label); },
  };
}

async function invoke(mode, githubClient, options = {}) {
  const workspace = fs.mkdtempSync(path.join(os.tmpdir(), "mokka-dispatch-test-"));
  fs.mkdirSync(path.join(workspace, "control"));
  fs.cpSync(path.join(root, ".github/repo-automation"), path.join(workspace, "control/.github/repo-automation"), { recursive: true });
  const inputs = { mode, "control-directory": "control", "policy-revision": SHA,
    "request-id": REQUEST, "workflow-commit-sha": SHA, ...options.inputs };
  const event = options.event ?? { repository, inputs: { request_id: REQUEST, workflow_commit_sha: SHA } };
  const outputs = new Map();
  let value;
  let error;
  try {
    value = await run({ core: {
      getInput(name) { return inputs[name] ?? ""; }, getBooleanInput() { return false; },
      setOutput(name, result) { outputs.set(name, result); }, info() {},
    }, githubClient, workspace, owner: "NVIDIA", repo: "k8s-test-infra",
    event, eventName: options.eventName ?? "workflow_dispatch", ref: options.ref ?? "refs/heads/main",
    repositoryId: "733665780", workflowSha: SHA });
  } catch (failure) { error = failure; }
  const file = path.join(workspace, ".mokka-label-scan", REQUEST, `${mode}.json`);
  return { value, error, file, outputs, report: fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, "utf8")) : null };
}

for (const mode of ["conflict-labels", "metadata-labels"]) {
  for (const unchanged of [false, true]) {
    test(`${mode} checks the PR fence after final labels for ${unchanged ? "unchanged" : "applied"} requests`, async () => {
      const labels = unchanged ? (mode === "conflict-labels" ? ["needs-rebase"]
        : ["kind/feature", "size/S", "area/docs"]) : [];
      const github = fake({ mode, labels, changeDuringFinalLabels: true });
      const result = await invoke(mode, github);
      assert.ifError(result.error);
      assert.equal(result.report.results[0].status, "deferred");
      assert.equal(result.report.results[0].reason, "state_changed");
      assert.equal(result.report.results[0].labelsSha256, null);
      if (unchanged) assert.deepEqual(github.writes, []);
      else assert.ok(github.writes.length > 0);
      const lastLabels = github.calls.findLastIndex(({ op }) => op === "labels");
      assert.ok(lastLabels >= 0);
      assert.ok(github.calls.findLastIndex(({ op }) => op === "pr") > lastLabels);
    });
  }
  test(`${mode} accepts distinct non-ASCII live labels without a false duplicate failure`, async () => {
    const result = await invoke(mode, fake({ labels: ["area/Ä", "area/ä"] }));
    assert.ifError(result.error);
    assert.equal(result.report.results[0].status, "applied");
  });
  test(`${mode} dispatch verifies applied labels and emits only the bounded report schema`, async () => {
    const github = fake();
    const result = await invoke(mode, github);
    assert.ifError(result.error);
    assert.ok(result.report, "dispatch must publish its report at the fixed request path");
    assert.deepEqual(Object.keys(result.report), ["schemaVersion", "requestId", "repositoryId", "workflowCommitSha", "mode", "dryRun", "candidates", "results"]);
    assert.deepEqual(result.report.candidates, [42]);
    assert.equal(result.report.results[0].status, "applied");
    assert.equal(result.report.results[0].reason, "none");
    assert.match(result.report.results[0].inputSha256, /^[0-9a-f]{64}$/);
    assert.match(result.report.results[0].labelsSha256, /^[0-9a-f]{64}$/);
    assert.equal(JSON.stringify(result.report).includes("private"), false);
    assert.ok(github.calls.filter(({ op }) => op === "labels").length >= 3);
  });

  test(`${mode} acknowledged but unapplied label updates fail without successful output hash`, async () => {
    const github = fake({ stalled: true });
    const result = await invoke(mode, github);
    assert.ok(result.error, "an acknowledgement alone is not a verified update");
    assert.ok(result.report, "partial failures must publish a report");
    assert.equal(result.report.results[0].status, "failed");
    assert.equal(result.report.results[0].reason, "write_failed");
    assert.equal(result.report.results[0].labelsSha256, null);
    assert.equal(github.writes.length, 1);
  });

  test(`${mode} partial write failure retains all candidates and marks unvisited requests`, async () => {
    const github = fake({ numbers: [42, 43, 44], writeFailure: 43 });
    const result = await invoke(mode, github);
    assert.ok(result.error);
    assert.ok(result.report);
    assert.deepEqual(result.report.candidates, [42, 43, 44]);
    assert.deepEqual(result.report.results.map(({ number, status, reason }) => ({ number, status, reason })), [
      { number: 42, status: "applied", reason: "none" },
      { number: 43, status: "failed", reason: "write_failed" },
      { number: 44, status: "failed", reason: "not_processed" },
    ]);
    assert.ok(github.writes.every(({ number }) => number !== 44));
  });

  test(`${mode} final PR fence change defers after writes`, async () => {
    const result = await invoke(mode, fake({ changedAt: 3 }));
    assert.ifError(result.error);
    assert.ok(result.report);
    assert.equal(result.report.results[0].status, "deferred");
    assert.equal(result.report.results[0].reason, "state_changed");
    assert.equal(result.report.results[0].labelsSha256, null);
  });

  test(`${mode} failed enumeration fails the run without claiming an empty known catalog`, async () => {
    const result = await invoke(mode, fake({ enumerationFailure: true }));
    assert.match(result.error?.message ?? "", /catalog unavailable/);
    assert.equal(result.report, null);
    assert.notEqual(result.outputs.get("summary"), "[]");
  });

  test(`${mode} fresh input changes stop all label writes`, async () => {
    const github = fake({ changedAt: 2 });
    const result = await invoke(mode, github);
    assert.ifError(result.error);
    assert.deepEqual(github.writes, []);
    assert.equal(result.report.results[0].reason, "state_changed");
  });

  for (const at of [1, 2]) test(`${mode} read failure at snapshot ${at} retains known membership`, async () => {
    const github = fake({ numbers: [42, 43, 44], fail: { op: "labels", number: 43, at } });
    const result = await invoke(mode, github);
    assert.match(result.error?.message ?? "", /label (initial|fresh) read failed/);
    assert.deepEqual(result.report.candidates, [42, 43, 44]);
    assert.equal(result.report.results[1].status, "failed");
    assert.equal(result.report.results[1].reason, "read_failed");
    assert.equal(result.report.results[2].reason, "not_processed");
    if (at === 1) assert.deepEqual(github.writes, []);
    else assert.equal(result.report.results[0].status, "applied");
  });
}

test("dispatch verifies an initially unchanged PR again before certifying coverage", async () => {
  const github = fake({ labels: ["needs-rebase"], changedAt: 2 });
  const result = await invoke("conflict-labels", github);
  assert.ifError(result.error);
  assert.equal(result.report.results[0].status, "deferred");
  assert.equal(result.report.results[0].reason, "state_changed");
  assert.deepEqual(github.writes, []);
});

test("UNKNOWN has complete membership and no claimed conflict coverage", async () => {
  const result = await invoke("conflict-labels", fake({ mergeability: "UNKNOWN" }));
  assert.ifError(result.error);
  assert.deepEqual(result.report.candidates, [42]);
  assert.deepEqual(result.report.results, [{ number: 42, status: "deferred", reason: "unknown_mergeability",
    inputSha256: null, labelsSha256: null }]);
});

test("native scans reject dispatch identifiers and never create a report", async () => {
  const github = fake();
  const result = await invoke("conflict-labels", github, { eventName: "schedule", event: { repository, schedule: "17 * * * *" } });
  assert.match(result.error?.message ?? "", /dispatch.*invalid/i);
  assert.equal(result.report, null);
  assert.deepEqual(github.writes, []);
});
