"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { planRetest } = require("../src/retest.js");

const HEAD = "c".repeat(40);
const OTHER_HEAD = "d".repeat(40);
const NOW = "2026-07-16T12:10:00.000Z";
const PR_NUMBER = 42;
const REPOSITORY = "nvidia/k8s-test-infra";

function run(id, overrides = {}) {
  return {
    id,
    headOid: HEAD,
    status: "completed",
    conclusion: "failure",
    workflowPath: ".github/workflows/automation-ci.yml",
    workflowSourceRef: "main",
    event: "pull_request",
    prNumber: PR_NUMBER,
    repository: REPOSITORY,
    ...overrides,
  };
}

function lastRetest(overrides = {}) {
  return {
    commentId: 100,
    headOid: HEAD,
    createdAt: "2026-07-16T12:00:00.000Z",
    ...overrides,
  };
}

function input(overrides = {}) {
  return {
    runs: [run(10)],
    headOid: HEAD,
    now: NOW,
    lastRetest: null,
    cooldownSeconds: 600,
    commentId: 101,
    prNumber: PR_NUMBER,
    repository: REPOSITORY,
    ...overrides,
  };
}

function noRerun(reason, nextAllowedAt = null) {
  return { rerunRunIds: [], nextAllowedAt, reason };
}

test("plans only completed failed workflow runs for the exact current head", () => {
  const result = planRetest(input({
    runs: [
      run(10),
      run(2),
      run(7, { headOid: OTHER_HEAD }),
      run(8, { status: "in_progress", conclusion: null }),
      run(9, { conclusion: "success" }),
      run(11, { conclusion: "cancelled" }),
      run(12, { conclusion: "skipped" }),
      run(13, { conclusion: "neutral" }),
    ],
  }));

  assert.deepEqual(result, {
    rerunRunIds: [2, 10],
    nextAllowedAt: null,
    reason: "rerun-failed",
  });
});

test("plans only fixed source-controlled pull-request CI for this repository and PR", () => {
  const result = planRetest(input({
    runs: [
      run(1),
      run(2, { workflowPath: ".github/workflows/basic-checks.yaml" }),
      run(3, { workflowPath: ".github/workflows/helm.yaml" }),
      run(4, { workflowPath: ".github/workflows/nvml-mock-publish.yaml", event: "workflow_dispatch" }),
      run(5, { workflowPath: ".github/workflows/release.yaml", event: "push" }),
      run(6, { event: "workflow_dispatch" }),
      run(7, { prNumber: 99 }),
      run(8, { repository: "attacker/fork" }),
      run(9, { workflowPath: ".github/workflows/../workflows/automation-ci.yml" }),
      run(10, { workflowPath: ".github/workflows/nvml-mock-publish.yaml" }),
    ],
  }));

  assert.deepEqual(result, {
    rerunRunIds: [1, 2, 3],
    nextAllowedAt: null,
    reason: "rerun-failed",
  });
});

test("deduplicates identical run records and sorts positive run IDs numerically", () => {
  assert.deepEqual(planRetest(input({ runs: [run(20), run(3), run(20), run(11)] })), {
    rerunRunIds: [3, 11, 20],
    nextAllowedAt: null,
    reason: "rerun-failed",
  });
});

test("does not broaden failure to timed-out or action-required conclusions", () => {
  assert.deepEqual(planRetest(input({
    runs: [
      run(1, { conclusion: "timed_out" }),
      run(2, { conclusion: "action_required" }),
      run(3, { conclusion: "success" }),
    ],
  })), noRerun("no-failed-runs"));
});

test("returns a fixed no-op when no exact-head failed run is rerunnable", () => {
  for (const runs of [
    [],
    [run(1, { headOid: OTHER_HEAD })],
    [run(1, { status: "queued", conclusion: null })],
    [run(1, { status: "in_progress", conclusion: null })],
    [run(1, { conclusion: "success" })],
  ]) {
    assert.deepEqual(planRetest(input({ runs })), noRerun("no-failed-runs"));
  }
});

test("enforces the same-head 600-second cooldown from persisted state", () => {
  assert.deepEqual(planRetest(input({
    now: "2026-07-16T12:09:59.999Z",
    lastRetest: lastRetest(),
  })), noRerun("cooldown", "2026-07-16T12:10:00.000Z"));

  assert.deepEqual(planRetest(input({ lastRetest: lastRetest() })), {
    rerunRunIds: [10],
    nextAllowedAt: null,
    reason: "rerun-failed",
  });

  assert.deepEqual(planRetest(input({
    now: "2026-07-16T12:00:01.000Z",
    lastRetest: lastRetest({ headOid: OTHER_HEAD }),
  })), {
    rerunRunIds: [10],
    nextAllowedAt: null,
    reason: "rerun-failed",
  });
});

test("makes duplicate comment delivery a persisted no-op even after cooldown", () => {
  assert.deepEqual(planRetest(input({
    now: "2026-07-17T12:00:00.000Z",
    commentId: 100,
    lastRetest: lastRetest(),
  })), noRerun("duplicate-delivery"));
});

test("does not accept an event or command supplied workflow run ID", () => {
  assert.deepEqual(
    planRetest({ ...input(), runId: 999 }),
    noRerun("invalid-input"),
  );
  assert.deepEqual(
    planRetest({ ...input(), requestedRunIds: [10] }),
    noRerun("invalid-input"),
  );
});

test("fails closed without a partial plan for malformed or API-error-shaped runs", () => {
  const invalidRunSets = [
    null,
    { status: 500, message: "API unavailable" },
    [run(1), { status: 500, message: "API unavailable" }],
    [run(1), run(0)],
    [run(1), run(Number.MAX_SAFE_INTEGER + 1)],
    [run(1), run(2, { headOid: "not-an-oid" })],
    [run(1), run(2, { status: "completed", conclusion: null })],
    [run(1), run(2, { status: "mystery", conclusion: "failure" })],
    [run(1), { ...run(2), unexpected: true }],
    [run(1), run(1, { conclusion: "success" })],
    [run(1), run(1, { workflowPath: ".github/workflows/basic-checks.yaml" })],
    [run(1), run(1, { workflowSourceRef: "release" })],
  ];

  for (const runs of invalidRunSets) {
    const expectedReason = Array.isArray(runs) ? "invalid-runs" : "invalid-input";
    assert.deepEqual(planRetest(input({ runs })), noRerun(expectedReason));
  }
});

test("validates all planner and persisted-state fields without throwing", () => {
  const invalidInputs = [
    null,
    {},
    input({ headOid: "c".repeat(39) }),
    input({ headOid: "C".repeat(40) }),
    input({ now: "2026-07-16T12:10:00Z" }),
    input({ cooldownSeconds: 599 }),
    input({ cooldownSeconds: "600" }),
    input({ commentId: 0 }),
    input({ commentId: Number.MAX_SAFE_INTEGER + 1 }),
    input({ prNumber: 0 }),
    input({ repository: "NVIDIA/k8s-test-infra" }),
    input({ repository: "nvidia/../k8s-test-infra" }),
    input({ lastRetest: { ...lastRetest(), commentId: -1 } }),
    input({ lastRetest: { ...lastRetest(), createdAt: "2026-07-16T12:00:00Z" } }),
    input({ lastRetest: { ...lastRetest(), extra: true } }),
    input({ now: "2026-07-16T11:59:59.000Z", lastRetest: lastRetest() }),
  ];

  for (const value of invalidInputs) {
    assert.deepEqual(planRetest(value), noRerun("invalid-input"));
  }
});

test("fails closed if computing the cooldown would leave bounded canonical UTC time", () => {
  assert.deepEqual(planRetest(input({
    headOid: "e".repeat(64),
    runs: [run(1, { headOid: "e".repeat(64) })],
    now: "9999-12-31T23:59:59.999Z",
    lastRetest: lastRetest({
      headOid: "e".repeat(64),
      createdAt: "9999-12-31T23:59:59.999Z",
    }),
  })), noRerun("invalid-input"));
});
