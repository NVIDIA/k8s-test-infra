"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { planCommandExecution } = require("../src/commands/executor.js");
const { createEmptyState } = require("../src/commands/state.js");

const HEAD = "a".repeat(40);
const DIGEST = "b".repeat(64);
const CONTEXT = {
  repository: "nvidia/k8s-test-infra",
  pullRequest: 42,
  policyDigest: DIGEST,
  headOid: HEAD,
};

function command(name, line, targetBranch = null) {
  return { name, targetBranch, line, raw: targetBranch === null ? `/${name}` : `/${name} ${targetBranch}` };
}

function input(overrides = {}) {
  return {
    parsed: { commands: [command("lgtm", 1)], diagnostics: [] },
    state: createEmptyState(CONTEXT),
    context: CONTEXT,
    actor: {
      resolved: true,
      deleted: false,
      login: "approver",
      type: "User",
      liveCollaborator: false,
      permission: "read",
    },
    author: "author",
    reviewers: ["reviewer", "approver"],
    approvers: ["approver"],
    owners: ["reviewer", "approver"],
    commentId: 100,
    now: "2026-09-17T10:00:00.000Z",
    historyLimit: 32,
    runs: [],
    cooldownSeconds: 600,
    retestWorkflowAllowlist: [".github/workflows/automation-ci.yml"],
    allowedBackportBranches: ["release-*"],
    ...overrides,
  };
}

test("records current-head LGTM and approval evidence with full provenance", () => {
  const result = planCommandExecution(input({
    parsed: {
      commands: [command("lgtm", 1), command("approve", 2)],
      diagnostics: [],
    },
  }));
  assert.equal(result.commands.every((entry) => entry.status === "applied"), true);
  assert.deepEqual(result.state.processedCommandIds, [100]);
  for (const evidence of [result.state.lgtms[0], result.state.approvals[0]]) {
    assert.equal(evidence.repository, CONTEXT.repository);
    assert.equal(evidence.pullRequest, CONTEXT.pullRequest);
    assert.equal(evidence.actor, "approver");
    assert.equal(evidence.sourceType, "comment");
    assert.equal(evidence.sourceId, 100);
    assert.equal(evidence.policyDigest, DIGEST);
    assert.equal(evidence.headOid, HEAD);
  }
  assert.equal(result.policy.lgtm, true);
  assert.equal(result.policy.approved, true);
});

test("keeps holds across heads and clears them only through authorized unhold", () => {
  const held = planCommandExecution(input({
    parsed: { commands: [command("hold", 1)], diagnostics: [] },
  }));
  assert.equal(held.state.hold.actorRole, "owner");
  const newContext = { ...CONTEXT, headOid: "c".repeat(40) };
  const cleared = planCommandExecution(input({
    context: newContext,
    state: { ...held.state, headOid: newContext.headOid, policyDigest: DIGEST, processedCommandIds: [] },
    parsed: { commands: [command("unhold", 1)], diagnostics: [] },
    commentId: 101,
  }));
  assert.equal(cleared.state.hold, null);
});

test("plans only policy-allowed backport requests and preserves the alias", () => {
  const result = planCommandExecution(input({
    parsed: {
      commands: [
        command("backport", 1, "release-1.2"),
        command("cherry-pick", 2, "release-1.3"),
        command("backport", 3, "main"),
      ],
      diagnostics: [],
    },
  }));
  assert.deepEqual(result.mutations.backportRequests, [
    { command: "backport", prNumber: 42, targetBranch: "release-1.2", sourceCommentId: 100 },
    { command: "cherry-pick", prNumber: 42, targetBranch: "release-1.3", sourceCommentId: 100 },
  ]);
  assert.equal(result.commands[2].code, "target-branch-not-allowed");
});

test("uses the configured retest allowlist and records cooldown state only for reruns", () => {
  const run = {
    id: 9,
    headOid: HEAD,
    status: "completed",
    conclusion: "failure",
    workflowPath: ".github/workflows/automation-ci.yml",
    workflowSourceRef: "main",
    event: "pull_request",
    prNumber: 42,
    repository: CONTEXT.repository,
  };
  const result = planCommandExecution(input({
    parsed: { commands: [command("retest", 1)], diagnostics: [] },
    actor: {
      resolved: true,
      deleted: false,
      login: "author",
      type: "User",
      liveCollaborator: false,
      permission: "read",
    },
    runs: [
      run,
      { ...run, id: 10, workflowPath: ".github/workflows/untrusted.yml" },
    ],
  }));
  assert.deepEqual(result.mutations.rerunRunIds, [9]);
  assert.deepEqual(result.state.lastRetest, {
    commentId: 100,
    headOid: HEAD,
    createdAt: "2026-09-17T10:00:00.000Z",
  });
});

test("makes a duplicate delivery an exact no-op", () => {
  const first = planCommandExecution(input());
  const duplicate = planCommandExecution(input({ state: first.state }));
  assert.equal(duplicate.duplicate, true);
  assert.deepEqual(duplicate.mutations, {
    addLabels: [],
    removeLabels: [],
    rerunRunIds: [],
    backportRequests: [],
  });
  assert.deepEqual(duplicate.state, first.state);
});

test("fails closed when command history is full", () => {
  assert.throws(() => planCommandExecution(input({
    state: { ...createEmptyState(CONTEXT), processedCommandIds: [1, 2] },
    historyLimit: 2,
  })), /history limit reached/);
});

test("reports parser diagnostics without creating authority", () => {
  const result = planCommandExecution(input({
    parsed: {
      commands: [],
      diagnostics: [{ line: 1, code: "unsupported-command", message: "ignored" }],
    },
  }));
  assert.equal(result.diagnostics[0].code, "unsupported-command");
  assert.equal(result.state.lgtms.length, 0);
});
