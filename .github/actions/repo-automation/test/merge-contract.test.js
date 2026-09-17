"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const { loadConfig } = require("../src/config.js");
const {
  createEmptyState,
  serializePolicyState,
} = require("../src/commands/state.js");
const { policyDigest } = require("../src/policy-digest.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const config = loadConfig(repositoryRoot);
const HEAD = "6".repeat(40);
const NEXT_HEAD = "7".repeat(40);
const REVISION = "8".repeat(40);
const REPOSITORY = "nvidia/k8s-test-infra";
const OWNER_SOURCE = "reviewers: [alice]\napprovers: [bob]\n";
const ALIASES_SOURCE = "aliases: {}\n";
const POLICY_MARKER = "<!-- repo-automation-policy:v1 -->";
const METADATA = (head = HEAD) => (
  `<!-- repo-automation-metadata-head:v1 {"headOid":"${head}"} -->`
);

function digest() {
  return policyDigest({
    repository: REPOSITORY,
    revision: REVISION,
    policy: config.policy,
    ownerSources: [{ path: "/OWNERS", source: OWNER_SOURCE }],
    aliasesSource: ALIASES_SOURCE,
  });
}

function evidence(command, actor, actorRole, sourceId) {
  return {
    repository: REPOSITORY,
    pullRequest: 42,
    actor,
    actorRole,
    sourceType: "comment",
    sourceId,
    policyDigest: digest(),
    headOid: HEAD,
    createdAt: "2026-09-17T08:00:00.000Z",
    command,
  };
}

function storedEvidence(record) {
  return {
    repository: record.repository,
    pullRequest: record.pullRequest,
    actor: record.actor,
    actorRole: record.actorRole,
    sourceType: record.sourceType,
    sourceId: record.sourceId,
    policyDigest: record.policyDigest,
    headOid: record.headOid,
    createdAt: record.createdAt,
  };
}

function policyBody({
  headOid = HEAD,
  metadataHeadOid = headOid,
  lgtms = [evidence("lgtm", "alice", "reviewer", 8000)],
  approvals = [evidence("approve", "bob", "approver", 8001)],
  hold = null,
} = {}) {
  const state = createEmptyState({
    repository: REPOSITORY,
    pullRequest: 42,
    policyDigest: digest(),
    headOid,
  });
  state.lgtms = lgtms.map(storedEvidence);
  state.approvals = approvals.map(storedEvidence);
  state.hold = hold;
  return [
    POLICY_MARKER,
    serializePolicyState(state),
    METADATA(metadataHeadOid),
    "## Repository policy",
    "",
  ].join("\n");
}

function pullRequest(overrides = {}) {
  return {
    number: 42,
    nodeId: "PR_node_42",
    title: "feat: evaluator",
    body: "live body is irrelevant",
    draft: false,
    author: "pr-author",
    headOid: HEAD,
    state: "open",
    baseBranch: "main",
    baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    ...overrides,
  };
}

function mergeState(overrides = {}) {
  return {
    number: 42,
    nodeId: "PR_node_42",
    state: "OPEN",
    draft: false,
    baseBranch: "main",
    headOid: HEAD,
    mergeability: "MERGEABLE",
    autoMergeMethod: null,
    repository: REPOSITORY,
    ...overrides,
  };
}

function liveCommand(id, author, body, overrides = {}) {
  return {
    id,
    issueNumber: 42,
    body,
    author,
    authorType: "User",
    edited: false,
    ...overrides,
  };
}

function evaluatorState(overrides = {}) {
  return {
    pullRequest: pullRequest(),
    files: [{ path: "pkg/gpu.go", additions: 2, deletions: 1, status: "modified" }],
    labels: ["lgtm", "approved", "maintainer/custom"],
    comments: [{ id: 77, author: "github-actions[bot]", body: policyBody() }],
    issueComments: [
      liveCommand(8000, "alice", "/lgtm"),
      liveCommand(8001, "bob", "/approve"),
    ],
    contents: {
      "/OWNERS": OWNER_SOURCE,
      "/OWNERS_ALIASES": ALIASES_SOURCE,
    },
    defaultBranchRevision: REVISION,
    branchProtection: { main: true },
    mergeStates: [mergeState(), mergeState(), mergeState(), mergeState()],
    ...overrides,
  };
}

async function run(state = evaluatorState(), options = {}) {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  const github = createFakeGitHub(state);
  const result = await runMergeEvaluate({
    event: {
      repository: {
        name: "k8s-test-infra",
        full_name: "NVIDIA/k8s-test-infra",
        owner: { login: "NVIDIA" },
      },
    },
    eventName: "workflow_dispatch",
    github,
    config,
    dryRun: options.dryRun ?? false,
    prNumber: "42",
  });
  return { github, result };
}

function operationIndex(github, operation) {
  return github.callOrder.findIndex((entry) => entry.operation === operation);
}

test("publishes success for the exact head, re-reads gates and head, then enables native SQUASH", async () => {
  const { github, result } = await run();

  assert.equal(result.status, "complete");
  assert.deepEqual(github.calls.setMergePolicyCheck, [{
    prNumber: 42,
    headOid: HEAD,
    conclusion: "success",
    summary: "Repository merge policy passed.",
  }]);
  assert.deepEqual(github.calls.enableAutoMerge, [{
    nodeId: "PR_node_42",
    mergeMethod: "SQUASH",
  }]);
  assert.deepEqual(github.calls.disableAutoMerge, []);
  assert.ok(github.calls.getPolicyComment.length >= 2, "gate inputs must be re-read");
  assert.ok(github.calls.getPullRequest.length >= 3, "head must be re-read after success");
  assert.ok(
    operationIndex(github, "setMergePolicyCheck") < operationIndex(github, "enableAutoMerge"),
    "success must be visible before auto-merge is enabled",
  );
});

test("publishes a non-success check before it disables an armed pull request", async () => {
  const state = evaluatorState({
    comments: [{ id: 77, author: "github-actions[bot]", body: policyBody({
      hold: {
        repository: REPOSITORY,
        pullRequest: 42,
        actor: "bob",
        actorRole: "owner",
        sourceType: "comment",
        sourceId: 8002,
        createdAt: "2026-09-17T08:01:00.000Z",
      },
    }) }],
    mergeStates: [
      mergeState({ autoMergeMethod: "SQUASH" }),
      mergeState({ autoMergeMethod: "SQUASH" }),
    ],
  });
  const { github, result } = await run(state);

  assert.equal(result.pullRequests[0].merge.action, "DISABLE");
  assert.ok(result.pullRequests[0].merge.blockers.includes("hold-active"));
  assert.equal(github.calls.setMergePolicyCheck[0].conclusion, "action_required");
  assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
  assert.ok(
    operationIndex(github, "setMergePolicyCheck") < operationIndex(github, "disableAutoMerge"),
    "the non-success check must be visible before auto-merge is disabled",
  );
});

test("visible labels cannot forge missing bot-owned evidence", async () => {
  const { github, result } = await run(evaluatorState({
    labels: ["lgtm", "approved"],
    comments: [{ id: 77, author: "github-actions[bot]", body: policyBody({
      lgtms: [],
      approvals: [],
    }) }],
  }));

  assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
  assert.ok(result.pullRequests[0].merge.blockers.includes("approval-coverage-incomplete"));
  assert.equal(github.calls.setMergePolicyCheck[0].conclusion, "action_required");
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("edited or changed source commands invalidate stored evidence", async () => {
  const { github, result } = await run(evaluatorState({
    issueComments: [
      liveCommand(8000, "alice", "/lgtm", { edited: true }),
      liveCommand(8001, "bob", "/approve changed"),
    ],
  }));

  assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
  assert.ok(result.pullRequests[0].merge.blockers.includes("approval-coverage-incomplete"));
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("an unprotected target branch fails closed", async () => {
  const { github, result } = await run(evaluatorState({ branchProtection: { main: false } }));

  assert.ok(result.pullRequests[0].merge.blockers.includes("target-branch-not-protected"));
  assert.equal(github.calls.setMergePolicyCheck[0].conclusion, "action_required");
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("a head change after the success check stops auto-merge enablement", async () => {
  const { github, result } = await run(evaluatorState({
    pullRequests: [pullRequest(), pullRequest(), pullRequest({ headOid: NEXT_HEAD })],
  }));

  assert.equal(result.pullRequests[0].merge.action, "NOOP");
  assert.ok(result.pullRequests[0].merge.blockers.includes("head-changed"));
  assert.deepEqual(github.calls.setMergePolicyCheck, [{
    prNumber: 42,
    headOid: HEAD,
    conclusion: "success",
    summary: "Repository merge policy passed.",
  }]);
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("dry-run calculates policy but performs no GitHub writes", async () => {
  const { github, result } = await run(evaluatorState(), { dryRun: true });

  assert.equal(result.status, "planned");
  for (const operation of [
    "setMergePolicyCheck",
    "enableAutoMerge",
    "disableAutoMerge",
    "addPolicyLabel",
    "removePolicyLabel",
  ]) assert.deepEqual(github.calls[operation], []);
});

test("merge policy contains no direct merge endpoint", () => {
  const source = fs.readFileSync(
    path.join(__dirname, "..", "src", "modes", "merge-evaluate.js"),
    "utf8",
  );
  const client = fs.readFileSync(
    path.join(__dirname, "..", "src", "github-client.js"),
    "utf8",
  );

  assert.doesNotMatch(source, /\.merge\s*\(|mergePullRequest|pulls\.merge/);
  assert.doesNotMatch(client, /pulls\.merge/);
});
