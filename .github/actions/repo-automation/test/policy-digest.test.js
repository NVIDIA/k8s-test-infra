"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");

const { loadConfig } = require("../src/config.js");
const { runCommand } = require("../src/modes/command.js");
const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
const { policyDigest } = require("../src/policy-digest.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const HEAD = "6".repeat(40);
const MAIN_BEFORE = "8".repeat(40);
const MAIN_AFTER = "9".repeat(40);
const OWNER_SOURCE = "reviewers: [alice]\napprovers: [bob]\n";
const REPOSITORY = {
  owner: { login: "NVIDIA" },
  name: "k8s-test-infra",
  full_name: "NVIDIA/k8s-test-infra",
};

function input(overrides = {}) {
  return {
    repository: "nvidia/k8s-test-infra",
    revision: "a".repeat(40),
    policy: { commands: { historyLimit: 256 }, protectedBranches: ["main"] },
    ownerSources: [{ path: "/OWNERS", source: "reviewers: [alice]\n" }],
    aliasesSource: "aliases: {}\n",
    ...overrides,
  };
}

test("policy digest is deterministic and insensitive to object key order", () => {
  const first = policyDigest(input());
  const second = policyDigest(input({
    policy: { protectedBranches: ["main"], commands: { historyLimit: 256 } },
  }));

  assert.match(first, /^[0-9a-f]{64}$/);
  assert.equal(first, second);
});

test("policy digest binds the repository, policy, owners, and aliases", async (t) => {
  const baseline = policyDigest(input());
  const cases = [
    ["repository", { repository: "nvidia/other" }],
    ["policy", { policy: { commands: { historyLimit: 255 }, protectedBranches: ["main"] } }],
    ["owners", { ownerSources: [{ path: "/OWNERS", source: "reviewers: [bob]\n" }] }],
    ["aliases", { aliasesSource: "aliases: {team: [alice]}\n" }],
  ];
  for (const [name, change] of cases) {
    await t.test(name, () => assert.notEqual(policyDigest(input(change)), baseline));
  }
});

test("policy digest is unchanged when main advances without a policy, owners, or aliases change", () => {
  assert.equal(
    policyDigest(input({ revision: "b".repeat(40) })),
    policyDigest(input({ revision: "a".repeat(40) })),
  );
});

test("policy digest rejects unsafe or ambiguous input", () => {
  assert.throws(() => policyDigest(input({ repository: "NVIDIA/k8s-test-infra" })), TypeError);
  assert.throws(() => policyDigest(input({ ownerSources: [
    { path: "/OWNERS", source: "one" },
    { path: "/OWNERS", source: "two" },
  ] })), TypeError);
  assert.throws(() => policyDigest(input({ policy: { bad: undefined } })), TypeError);
});

function liveCommand(id, author, body) {
  return { id, issueNumber: 42, body, author, authorType: "User", edited: false };
}

function liveState() {
  return {
    pullRequest: {
      number: 42,
      nodeId: "PR_node_42",
      title: "feat: evaluator",
      body: "",
      draft: false,
      author: "pr-author",
      headOid: HEAD,
      state: "open",
      baseBranch: "main",
      baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    },
    files: [{ path: "pkg/gpu.go", additions: 2, deletions: 1, status: "modified" }],
    commits: [{ sha: HEAD, author: { login: "contributor" }, parents: [{ sha: MAIN_BEFORE }],
      commit: { author: { name: "Contributor", email: "contributor@example.com" },
        message: "feat: test\n\nSigned-off-by: Contributor <contributor@example.com>" } }],
    labels: [],
    comments: [],
    issueComments: [
      liveCommand(8000, "alice", "/lgtm"),
      liveCommand(8001, "bob", "/approve"),
      liveCommand(8002, "carol", "/hold"),
    ],
    collaboratorAccess: {
      alice: { liveCollaborator: false, permission: "read" },
      bob: { liveCollaborator: false, permission: "read" },
      carol: { liveCollaborator: true, permission: "write" },
    },
    contents: { "/OWNERS": OWNER_SOURCE, "/OWNERS_ALIASES": "aliases: {}\n" },
    defaultBranchRevision: MAIN_BEFORE,
    branchProtection: { main: true },
    mergeStates: [{
      number: 42,
      nodeId: "PR_node_42",
      state: "OPEN",
      draft: false,
      baseBranch: "main",
      headOid: HEAD,
      mergeability: "MERGEABLE",
      autoMergeMethod: null,
      repository: "nvidia/k8s-test-infra",
    }],
  };
}

function command(github, commentId) {
  return runCommand({
    event: {
      action: "created",
      repository: REPOSITORY,
      issue: { number: 42, pull_request: { url: "event-url" } },
      comment: { id: commentId },
    },
    github,
    config: loadConfig(repositoryRoot),
    dryRun: false,
    now: () => "2026-09-17T12:00:00.000Z",
  });
}

function evaluate(github, policyRevision) {
  return runMergeEvaluate({
    event: { repository: REPOSITORY },
    eventName: "workflow_dispatch",
    github,
    config: loadConfig(repositoryRoot),
    policyRevision,
    dryRun: false,
    prNumber: "42",
  });
}

function labels(github) {
  return github.snapshot().map(({ name }) => name).sort();
}

function removedPolicyLabels(github) {
  return github.calls.removePolicyLabel.map(({ label }) => label).sort();
}

async function approvedOnPreviousMain() {
  const state = liveState();
  const github = createFakeGitHub(state);
  assert.equal((await command(github, 8000)).status, "complete");
  assert.equal((await command(github, 8001)).status, "complete");
  assert.deepEqual(labels(github), ["approved", "lgtm"]);
  github.calls.removePolicyLabel.length = 0;
  return { state, github };
}

test("a later command keeps comment lgtm and approval labels after an unrelated main push", async () => {
  const { state, github } = await approvedOnPreviousMain();
  state.defaultBranchRevision = MAIN_AFTER;

  const result = await command(github, 8002);

  assert.equal(result.status, "complete");
  assert.deepEqual(result.policy, { lgtm: true, approved: true, hold: true, needsApproval: false });
  assert.deepEqual(removedPolicyLabels(github), []);
  assert.deepEqual(labels(github), ["approved", "do-not-merge/hold", "lgtm"]);
});

test("merge evaluation keeps comment lgtm and approval labels after an unrelated main push", async () => {
  const { state, github } = await approvedOnPreviousMain();
  state.defaultBranchRevision = MAIN_AFTER;

  const result = await evaluate(github, MAIN_AFTER);

  assert.equal(result.pullRequests[0].lgtm, true);
  assert.equal(result.pullRequests[0].approved, true);
  assert.deepEqual(removedPolicyLabels(github), []);
  assert.deepEqual(labels(github), ["approved", "lgtm"]);
});

test("merge evaluation still drops comment evidence when the OWNERS content changes", async () => {
  const { state, github } = await approvedOnPreviousMain();
  state.defaultBranchRevision = MAIN_AFTER;
  state.contents["/OWNERS"] = "reviewers: [alice, dave]\napprovers: [bob]\n";

  const result = await evaluate(github, MAIN_AFTER);

  assert.equal(result.pullRequests[0].lgtm, false);
  assert.equal(result.pullRequests[0].approved, false);
  assert.deepEqual(removedPolicyLabels(github), ["approved", "lgtm"]);
  assert.deepEqual(labels(github), ["do-not-merge/needs-approval"]);
});
