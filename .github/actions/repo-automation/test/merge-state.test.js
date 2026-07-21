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
    autoMergeMethod: null,
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
  assert.deepEqual(decide({ ...overrides, autoMergeMethod: "SQUASH" }), {
    action: "DISABLE",
    blockers: [blocker],
  });
  for (const autoMergeMethod of ["MERGE", "REBASE"]) {
    assert.deepEqual(decide({ ...overrides, autoMergeMethod }), {
      action: "DISABLE",
      blockers: [blocker, "auto-merge-method-mismatch"],
    });
  }
}

test("converges eligible native auto-merge to squash in two idempotent steps", () => {
  const expected = new Map([
    [null, { action: "ENABLE", blockers: [] }],
    ["SQUASH", { action: "NOOP", blockers: [] }],
    ["MERGE", { action: "DISABLE", blockers: ["auto-merge-method-mismatch"] }],
    ["REBASE", { action: "DISABLE", blockers: ["auto-merge-method-mismatch"] }],
  ]);
  for (const [autoMergeMethod, result] of expected) {
    assert.deepEqual(decide({ autoMergeMethod }), result);
  }
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
    assert.deepEqual(decide({ ciState, autoMergeMethod: "SQUASH" }), {
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

  assert.deepEqual(decideMergeAction({
    ...snapshot(),
    unexpected: true,
    autoMergeMethod: "SQUASH",
  }), {
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

test("unknown and malformed auto-merge methods fail closed according to live evidence", () => {
  for (const autoMergeMethod of ["FAST_FORWARD", 0, false, {}, Symbol("method")]) {
    assert.deepEqual(decide({ autoMergeMethod }), {
      action: "DISABLE",
      blockers: ["invalid-snapshot"],
    });
  }
  assert.deepEqual(decide({ autoMergeMethod: undefined }), {
    action: "NOOP",
    blockers: ["invalid-snapshot"],
  });
});

test("malformed containers disable every trustworthy own enabled method without invoking accessors", () => {
  class LiveSnapshot {
    constructor(autoMergeMethod) {
      this.autoMergeMethod = autoMergeMethod;
    }
  }

  const nullPrototypeEnabled = Object.create(null);
  nullPrototypeEnabled.autoMergeMethod = "SQUASH";
  const nullPrototypeDisabled = Object.create(null);
  nullPrototypeDisabled.autoMergeMethod = null;

  for (const state of [
    nullPrototypeEnabled,
    new LiveSnapshot("MERGE"),
    { autoMergeMethod: "REBASE", unexpected: true },
  ]) {
    assert.deepEqual(decideMergeAction(state), {
      action: "DISABLE",
      blockers: ["invalid-snapshot"],
    });
  }
  for (const state of [
    nullPrototypeDisabled,
    new LiveSnapshot(null),
    { autoMergeMethod: null, unexpected: true },
    null,
  ]) {
    assert.deepEqual(decideMergeAction(state), {
      action: "NOOP",
      blockers: ["invalid-snapshot"],
    });
  }

  let getterCalls = 0;
  const accessor = {};
  Object.defineProperty(accessor, "autoMergeMethod", {
    configurable: true,
    get() {
      getterCalls += 1;
      return "SQUASH";
    },
  });
  assert.deepEqual(decideMergeAction(accessor), {
    action: "NOOP",
    blockers: ["invalid-snapshot"],
  });
  assert.equal(getterCalls, 0);

  const trapFailure = new Proxy({}, {
    getOwnPropertyDescriptor() {
      throw new Error("descriptor trap failed");
    },
  });
  assert.deepEqual(decideMergeAction(trapFailure), {
    action: "NOOP",
    blockers: ["invalid-snapshot"],
  });
});

test("retains the first captured enabled method when later validation fails", () => {
  let methodDescriptorCalls = 0;
  const alternatingMethod = new Proxy(snapshot({
    autoMergeMethod: "SQUASH",
    draft: "invalid",
  }), {
    getOwnPropertyDescriptor(target, property) {
      const descriptor = Reflect.getOwnPropertyDescriptor(target, property);
      if (property !== "autoMergeMethod") return descriptor;
      methodDescriptorCalls += 1;
      return {
        ...descriptor,
        value: methodDescriptorCalls === 1 ? "SQUASH" : null,
      };
    },
  });

  assert.deepEqual(decideMergeAction(alternatingMethod), {
    action: "DISABLE",
    blockers: ["invalid-snapshot"],
  });
  assert.equal(methodDescriptorCalls, 1);
});

test("nested LGTM and label failures preserve the single captured enabled method", () => {
  for (const nestedOverride of [
    { lgtm: { ...lgtm(), actor: "invalid actor" } },
    { labels: ["bad\u0000label"] },
  ]) {
    let methodDescriptorCalls = 0;
    const state = new Proxy(snapshot({
      autoMergeMethod: "SQUASH",
      ...nestedOverride,
    }), {
      getOwnPropertyDescriptor(target, property) {
        if (property === "autoMergeMethod") methodDescriptorCalls += 1;
        return Reflect.getOwnPropertyDescriptor(target, property);
      },
    });

    assert.deepEqual(decideMergeAction(state), {
      action: "DISABLE",
      blockers: ["invalid-snapshot"],
    });
    assert.equal(methodDescriptorCalls, 1);
  }
});

test("an own-key trap failure yields no method evidence and never probes a descriptor", () => {
  let methodDescriptorCalls = 0;
  const unreadableKeys = new Proxy(snapshot({ autoMergeMethod: "SQUASH" }), {
    ownKeys() {
      throw new Error("own keys unavailable");
    },
    getOwnPropertyDescriptor(target, property) {
      if (property === "autoMergeMethod") methodDescriptorCalls += 1;
      return Reflect.getOwnPropertyDescriptor(target, property);
    },
  });

  assert.deepEqual(decideMergeAction(unreadableKeys), {
    action: "NOOP",
    blockers: ["invalid-snapshot"],
  });
  assert.equal(methodDescriptorCalls, 0);
});

test("captures top-level descriptor values once and never consults hostile get traps", () => {
  let getterCalls = 0;
  const hiddenApproval = new Proxy(snapshot({ approvalCoverageComplete: false }), {
    get(target, property, receiver) {
      getterCalls += 1;
      if (property === "approvalCoverageComplete") return true;
      return Reflect.get(target, property, receiver);
    },
  });
  assert.deepEqual(decideMergeAction(hiddenApproval), {
    action: "NOOP",
    blockers: ["approval-coverage-incomplete"],
  });
  assert.equal(getterCalls, 0);

  const hiddenHold = new Proxy(snapshot({ labels: ["do-not-merge/hold"] }), {
    get(target, property, receiver) {
      getterCalls += 1;
      if (property === "labels") return [];
      return Reflect.get(target, property, receiver);
    },
  });
  assert.deepEqual(decideMergeAction(hiddenHold), {
    action: "NOOP",
    blockers: ["do-not-merge-label"],
  });
  assert.equal(getterCalls, 0);
});

test("captures one top-level descriptor map so alternating proxies cannot flip gates", () => {
  let approvalDescriptorCalls = 0;
  const alternatingApproval = new Proxy(snapshot({ approvalCoverageComplete: false }), {
    getOwnPropertyDescriptor(target, property) {
      const descriptor = Reflect.getOwnPropertyDescriptor(target, property);
      if (property !== "approvalCoverageComplete") return descriptor;
      approvalDescriptorCalls += 1;
      return {
        ...descriptor,
        value: approvalDescriptorCalls === 1 ? false : true,
      };
    },
  });

  assert.deepEqual(decideMergeAction(alternatingApproval), {
    action: "NOOP",
    blockers: ["approval-coverage-incomplete"],
  });
  assert.equal(approvalDescriptorCalls, 1);
});

test("captures one label descriptor map before validating length and values", () => {
  let lengthDescriptorCalls = 0;
  let indexDescriptorCalls = 0;
  const alternatingLabel = new Proxy(["do-not-merge/hold"], {
    getOwnPropertyDescriptor(target, property) {
      const descriptor = Reflect.getOwnPropertyDescriptor(target, property);
      if (property === "length") {
        lengthDescriptorCalls += 1;
        return descriptor;
      }
      if (property === "0") {
        indexDescriptorCalls += 1;
        return {
          ...descriptor,
          value: lengthDescriptorCalls === 0 ? "do-not-merge/hold" : "safe",
        };
      }
      return descriptor;
    },
  });

  assert.deepEqual(decide({ labels: alternatingLabel }), {
    action: "NOOP",
    blockers: ["do-not-merge-label"],
  });
  assert.equal(lengthDescriptorCalls, 1);
  assert.equal(indexDescriptorCalls, 1);
});

test("rejects top-level and LGTM accessors without executing them", () => {
  let getterCalls = 0;
  const accessorState = snapshot({ autoMergeMethod: "SQUASH" });
  Object.defineProperty(accessorState, "approvalCoverageComplete", {
    enumerable: true,
    configurable: true,
    get() {
      getterCalls += 1;
      return true;
    },
  });
  assert.deepEqual(decideMergeAction(accessorState), {
    action: "DISABLE",
    blockers: ["invalid-snapshot"],
  });

  const methodAccessorState = snapshot();
  Object.defineProperty(methodAccessorState, "autoMergeMethod", {
    enumerable: true,
    configurable: true,
    get() {
      getterCalls += 1;
      return "SQUASH";
    },
  });
  assert.deepEqual(decideMergeAction(methodAccessorState), {
    action: "NOOP",
    blockers: ["invalid-snapshot"],
  });

  const accessorLgtm = lgtm();
  Object.defineProperty(accessorLgtm, "headOid", {
    enumerable: true,
    configurable: true,
    get() {
      getterCalls += 1;
      return HEAD;
    },
  });
  assert.deepEqual(decide({ lgtm: accessorLgtm }), {
    action: "NOOP",
    blockers: ["invalid-snapshot"],
  });
  assert.equal(getterCalls, 0);
});

test("accepts only bounded dense exact label arrays of own enumerable data values", () => {
  let getterCalls = 0;
  const accessor = [];
  Object.defineProperty(accessor, "0", {
    enumerable: true,
    configurable: true,
    get() {
      getterCalls += 1;
      return "do-not-merge/hold";
    },
  });
  accessor.length = 1;

  const hiddenExtra = ["safe"];
  Object.defineProperty(hiddenExtra, "extra", { value: true });
  const nonEnumerableIndex = [];
  Object.defineProperty(nonEnumerableIndex, "0", { value: "safe" });
  nonEnumerableIndex.length = 1;
  const symbolExtra = ["safe"];
  symbolExtra[Symbol("extra")] = true;
  const enumerableExtra = ["safe"];
  enumerableExtra.extra = true;

  for (const labels of [
    accessor,
    hiddenExtra,
    nonEnumerableIndex,
    symbolExtra,
    enumerableExtra,
    Array(1),
    Array.from({ length: 1001 }, () => "safe"),
  ]) {
    assert.deepEqual(decide({ labels }), {
      action: "NOOP",
      blockers: ["invalid-snapshot"],
    });
  }
  assert.equal(getterCalls, 0);

  let oversizedDescriptorCalls = 0;
  const oversized = new Proxy(Array.from({ length: 1001 }, () => "safe"), {
    getOwnPropertyDescriptor(target, property) {
      oversizedDescriptorCalls += 1;
      return Reflect.getOwnPropertyDescriptor(target, property);
    },
  });
  assert.deepEqual(decide({ labels: oversized }), {
    action: "NOOP",
    blockers: ["invalid-snapshot"],
  });
  assert.equal(oversizedDescriptorCalls, 0);
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
    "state.autoMergeMethod === \"SQUASH\" ? \"NOOP\" : \"ENABLE\"",
  ]) {
    assert.equal(source.includes(fragment), true, `missing explicit gate: ${fragment}`);
  }
  assert.equal(source.includes("state.ciState ==="), false);
});
