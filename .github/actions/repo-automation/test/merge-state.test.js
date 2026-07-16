"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const { decideMergeAction } = require("../src/merge-state.js");

const sourcePath = path.join(__dirname, "..", "src", "merge-state.js");
const HEAD = "a".repeat(40);
const OTHER_HEAD = "b".repeat(40);
const HEAD_64 = "c".repeat(64);

function lgtm(headOid = HEAD) {
  return {
    actor: "reviewer-one",
    commentId: 123,
    headOid,
    createdAt: "2026-07-16T10:20:30.000Z",
  };
}

function snapshot(overrides = {}) {
  return {
    pullRequestState: "OPEN",
    draft: false,
    baseBranch: "main",
    baseBranchAllowed: true,
    baseBranchProtected: true,
    headOid: HEAD,
    finalHeadOid: HEAD,
    metadataHeadOid: HEAD,
    approvalHeadOid: HEAD,
    lgtm: lgtm(),
    lgtmStateOwnedByBot: true,
    approvalCoverageComplete: true,
    mergeability: "MERGEABLE",
    labels: [],
    loadError: false,
    ciState: "SUCCESS",
    autoMergeEnabled: false,
    ...overrides,
  };
}

function decide(overrides = {}) {
  return decideMergeAction(snapshot(overrides));
}

function assertBlocked(overrides, blocker) {
  assert.deepEqual(decide(overrides), {
    action: "NOOP",
    blockers: [blocker],
  });
  assert.deepEqual(decide({ ...overrides, autoMergeEnabled: true }), {
    action: "DISABLE",
    blockers: [blocker],
  });
}

test("enables native auto-merge only when every repository prerequisite is proven", () => {
  assert.deepEqual(decide(), { action: "ENABLE", blockers: [] });
  assert.deepEqual(decide({ autoMergeEnabled: true }), {
    action: "NOOP",
    blockers: [],
  });
});

test("blocks closed and draft pull requests independently", () => {
  assertBlocked({ pullRequestState: "CLOSED" }, "pr-not-open");
  assertBlocked({ draft: true }, "pr-draft");
});

test("requires the target branch to be both allowed and protected", () => {
  assertBlocked({ baseBranchAllowed: false }, "target-branch-not-allowed");
  assertBlocked({ baseBranchProtected: false }, "target-branch-not-protected");
});

test("requires exact current-head bot-owned LGTM provenance", () => {
  assertBlocked({ lgtm: null }, "lgtm-missing");
  assertBlocked({ lgtmStateOwnedByBot: false }, "lgtm-untrusted");
  assertBlocked({ lgtm: lgtm(OTHER_HEAD) }, "lgtm-stale");
});

test("requires complete approval coverage evaluated for the current head", () => {
  assertBlocked({ approvalCoverageComplete: false }, "approval-coverage-incomplete");
  assertBlocked({ approvalHeadOid: OTHER_HEAD }, "approval-stale");
});

test("blocks every current and future do-not-merge label case-insensitively", async (t) => {
  for (const label of [
    "do-not-merge/hold",
    "do-not-merge/needs-approval",
    "do-not-merge/work-in-progress",
    "DO-NOT-MERGE/Future-Policy",
  ]) {
    await t.test(label, () => {
      assertBlocked({ labels: [label] }, "do-not-merge-label");
    });
  }

  assert.deepEqual(decide({
    labels: [
      "DO-NOT-MERGE/HOLD",
      "do-not-merge/hold",
      "kind/feature",
      "KIND/FEATURE",
    ],
  }), {
    action: "NOOP",
    blockers: ["do-not-merge-label"],
  });
});

test("requires known non-conflicting mergeability", () => {
  assertBlocked({ mergeability: "CONFLICTING" }, "mergeability-conflicting");
  assertBlocked({ mergeability: "UNKNOWN" }, "mergeability-unknown");
});

test("requires metadata bound to the current head and no upstream load error", () => {
  assertBlocked({ metadataHeadOid: OTHER_HEAD }, "metadata-stale");
  assertBlocked({ loadError: true }, "load-error");
});

test("lets GitHub enforce required checks for success, pending, and failed CI", () => {
  for (const ciState of ["SUCCESS", "PENDING", "FAILED"]) {
    assert.deepEqual(decide({ ciState }), { action: "ENABLE", blockers: [] });
    assert.deepEqual(decide({ ciState, autoMergeEnabled: true }), {
      action: "NOOP",
      blockers: [],
    });
  }
});

test("blocks an observed or final head mismatch immediately before mutation", () => {
  assertBlocked({ finalHeadOid: OTHER_HEAD }, "head-changed");
});

test("display labels cannot forge LGTM or approval authority", () => {
  assert.deepEqual(decide({ labels: ["lgtm", "approved"] }), {
    action: "ENABLE",
    blockers: [],
  });

  assert.deepEqual(decide({
    lgtm: null,
    approvalCoverageComplete: false,
    labels: ["lgtm", "approved"],
  }), {
    action: "NOOP",
    blockers: ["lgtm-missing", "approval-coverage-incomplete"],
  });
});

test("reports all failed prerequisites once in a deterministic fixed order", () => {
  assert.deepEqual(decide({
    pullRequestState: "CLOSED",
    draft: true,
    baseBranchAllowed: false,
    baseBranchProtected: false,
    finalHeadOid: OTHER_HEAD,
    metadataHeadOid: OTHER_HEAD,
    approvalHeadOid: OTHER_HEAD,
    lgtm: null,
    lgtmStateOwnedByBot: false,
    approvalCoverageComplete: false,
    mergeability: "UNKNOWN",
    labels: ["do-not-merge/hold", "DO-NOT-MERGE/HOLD"],
    loadError: true,
  }), {
    action: "NOOP",
    blockers: [
      "load-error",
      "pr-not-open",
      "pr-draft",
      "target-branch-not-allowed",
      "target-branch-not-protected",
      "lgtm-missing",
      "lgtm-untrusted",
      "approval-coverage-incomplete",
      "approval-stale",
      "metadata-stale",
      "mergeability-unknown",
      "do-not-merge-label",
      "head-changed",
    ],
  });
});

test("normalizes hexadecimal OID case and supports exact 40- and 64-character heads", () => {
  assert.deepEqual(decide({
    headOid: HEAD.toUpperCase(),
    finalHeadOid: HEAD,
    metadataHeadOid: HEAD.toUpperCase(),
    approvalHeadOid: HEAD,
    lgtm: lgtm(HEAD.toUpperCase()),
  }), { action: "ENABLE", blockers: [] });

  assert.deepEqual(decide({
    headOid: HEAD_64.toUpperCase(),
    finalHeadOid: HEAD_64,
    metadataHeadOid: HEAD_64.toUpperCase(),
    approvalHeadOid: HEAD_64,
    lgtm: lgtm(HEAD_64.toUpperCase()),
  }), { action: "ENABLE", blockers: [] });

  assertBlocked({
    headOid: HEAD_64,
    finalHeadOid: HEAD_64,
    metadataHeadOid: HEAD_64,
    approvalHeadOid: HEAD_64,
    lgtm: lgtm(HEAD),
  }, "lgtm-stale");
  assertBlocked({
    headOid: HEAD_64,
    finalHeadOid: HEAD,
    metadataHeadOid: HEAD_64,
    approvalHeadOid: HEAD_64,
    lgtm: lgtm(HEAD_64),
  }, "head-changed");
});

test("fails closed for unknown keys, missing fields, and non-plain snapshots", () => {
  const invalidSnapshots = [
    { ...snapshot(), unexpected: true },
    Object.fromEntries(Object.entries(snapshot()).filter(([key]) => key !== "headOid")),
    null,
    [],
    Object.assign(Object.create(null), snapshot()),
  ];

  for (const state of invalidSnapshots) {
    assert.deepEqual(decideMergeAction(state), {
      action: "NOOP",
      blockers: ["invalid-snapshot"],
    });
  }

  assert.deepEqual(decideMergeAction({ ...snapshot(), unexpected: true, autoMergeEnabled: true }), {
    action: "DISABLE",
    blockers: ["invalid-snapshot"],
  });
});

test("fails closed for invalid booleans, enums, OIDs, labels, branches, and provenance", () => {
  const invalidOverrides = [
    { draft: 0 },
    { baseBranchAllowed: "true" },
    { baseBranchProtected: null },
    { lgtmStateOwnedByBot: 1 },
    { approvalCoverageComplete: "yes" },
    { loadError: "false" },
    { autoMergeEnabled: 0 },
    { pullRequestState: "MERGED" },
    { mergeability: "CLEAN" },
    { ciState: "CANCELLED" },
    { headOid: "a".repeat(39) },
    { finalHeadOid: "g".repeat(40) },
    { metadataHeadOid: "a".repeat(41) },
    { approvalHeadOid: "" },
    { baseBranch: "main\nunsafe" },
    { labels: "lgtm" },
    { labels: ["safe", "bad\u0000label"] },
    { lgtm: { ...lgtm(), extra: true } },
    { lgtm: { ...lgtm(), actor: "bad actor" } },
    { lgtm: { ...lgtm(), commentId: 0 } },
    { lgtm: { ...lgtm(), createdAt: "yesterday" } },
  ];

  for (const overrides of invalidOverrides) {
    assert.deepEqual(decide(overrides), {
      action: "NOOP",
      blockers: ["invalid-snapshot"],
    });
  }
});

test("never reflects unsafe attacker-controlled text in blockers", () => {
  const unsafe = "<script>alert(1)</script>\n";
  for (const state of [
    snapshot({ labels: [unsafe] }),
    snapshot({ baseBranch: unsafe }),
    snapshot({ lgtm: { ...lgtm(), actor: unsafe } }),
  ]) {
    const result = decideMergeAction(state);
    assert.deepEqual(result, { action: "NOOP", blockers: ["invalid-snapshot"] });
    assert.equal(JSON.stringify(result).includes(unsafe), false);
  }
});

test("source keeps CI diagnostic-only and all high-risk enable gates explicit", () => {
  const source = fs.readFileSync(sourcePath, "utf8");
  for (const fragment of [
    "state.pullRequestState !== \"OPEN\"",
    "state.draft",
    "!state.baseBranchAllowed",
    "!state.baseBranchProtected",
    "state.lgtm === null",
    "!state.lgtmStateOwnedByBot",
    "state.lgtm.headOid !== state.headOid",
    "!state.approvalCoverageComplete",
    "state.approvalHeadOid !== state.headOid",
    "state.metadataHeadOid !== state.headOid",
    "state.mergeability === \"CONFLICTING\"",
    "state.mergeability === \"UNKNOWN\"",
    "state.finalHeadOid !== state.headOid",
    "state.autoMergeEnabled ? \"NOOP\" : \"ENABLE\"",
  ]) {
    assert.equal(source.includes(fragment), true, `missing explicit gate: ${fragment}`);
  }
  assert.equal(source.includes("state.ciState ==="), false);
});
