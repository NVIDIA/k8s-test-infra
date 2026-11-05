"use strict";

const assert = require("node:assert/strict");
const { Buffer } = require("node:buffer");
const { createHash } = require("node:crypto");
const test = require("node:test");

const REQUEST = "12345678-1234-4234-8234-123456789abc";
const SHA = "a".repeat(40);
const repository = { id: 733665780, node_id: "R_kgDOK7rZ9A", name: "k8s-test-infra",
  full_name: "NVIDIA/k8s-test-infra", owner: { login: "NVIDIA" } };
const input = { eventName: "workflow_dispatch", ref: "refs/heads/main", repositoryId: "733665780",
  workflowSha: SHA, requestId: REQUEST, workflowCommitSha: SHA,
  event: { repository, inputs: { request_id: REQUEST, workflow_commit_sha: SHA } } };
const fence = { mode: "conflict-labels", number: 42, nodeId: "PR_42", headOid: SHA,
  baseBranch: "feature/é", baseOid: "b".repeat(40), draft: true, policyRevision: SHA,
  title: "feat: GPU 😀", mergeability: "CONFLICTING" };
function hash(value) { return createHash("sha256").update(value, "utf8").digest("hex"); }
function helpers() { return require("../src/label-scan-report.js"); }

test("dispatch accepts only the fixed identity, exact main SHA and canonical request", () => {
  const context = helpers().validateScanDispatch(input);
  assert.equal(context.requestId, REQUEST);
  assert.equal(context.workflowCommitSha, SHA);
  assert.equal(context.repositoryId, 733665780);
  assert.equal(Object.isFrozen(context), true);
});

test("canonical UUID validation does not impose a UUID version", () => {
  const requestId = "12345678-1234-1234-1234-123456789abc";
  const context = helpers().validateScanDispatch({ ...input, requestId,
    event: { ...input.event, inputs: { ...input.event.inputs, request_id: requestId } } });
  assert.equal(context.requestId, requestId);
});

// Exact vectors from the Task 1 tests/fixtures/label-scan-canonical.json contract.
test("report hashes match the shared Python contract vectors", () => {
  const vectors = [
    [{ mode: "conflict-labels", number: 7, nodeId: "PR_abc123", headOid: "a".repeat(40),
      baseBranch: "feature/é", baseOid: "b".repeat(40), draft: false, policyRevision: "c".repeat(40),
      title: "Fix 🚀", mergeability: "CONFLICTING" }, "f52dbe1a1f62c7a0bbbc1cb75501e45c0c8c28bbeee1d0782e79f9a04e0604db"],
    [{ mode: "metadata-labels", number: 42, nodeId: "PR_def456", headOid: "d".repeat(40),
      baseBranch: "main", baseOid: "e".repeat(40), draft: true, policyRevision: "f".repeat(40),
      title: "多言語 😀", mergeability: null }, "6abdf9dad805ce2ba637362cca5131ed14082a4973bde8e60f1f8741a0159aa9"],
  ];
  for (const [fields, expected] of vectors) assert.equal(helpers().inputSha256(fields), expected);
  for (const [mode, labels, expected] of [
    ["metadata-labels", ["area/Ä", "area/ä"], "683a3d91901d867dbd8b0a9ada0b1d072080698fdb710759531255a034dfc1f8"],
    ["conflict-labels", [], "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"],
    ["conflict-labels", ["NEEDS-REBASE", "unmanaged"], "00260649f65434ddbf45678036378ef49b13cbbf8d74e3f9a671137d1799ac5e"],
    ["metadata-labels", ["size/M", "kind/Ärea", "unmanaged", "area/🚀", "do-not-merge/work-in-progress"],
      "3a88f9e4dee80aa1530f4f77fa08bb85ba7680493782cabf34992698e8862657"],
    ["metadata-labels", ["KIND/bug"], "e49023f4c891a93a70784e533c4106342061747bcf247fb2bc6fe3d5d1cef3da"],
    ["metadata-labels", ["unmanaged"], "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"],
  ]) assert.equal(helpers().labelsSha256(mode, labels), expected);
});

for (const [name, change] of [
  ["wrong ref", { ref: "refs/heads/feature" }],
  ["wrong selected SHA", { workflowSha: "b".repeat(40) }],
  ["uppercase SHA", { workflowCommitSha: "A".repeat(40) }],
  ["zero SHA", { workflowCommitSha: "0".repeat(40) }],
  ["UUID path", { requestId: `../${REQUEST}` }],
  ["uppercase UUID", { requestId: REQUEST.toUpperCase() }],
  ["wrong repository ID", { repositoryId: "1" }],
  ["wrong repository node", { event: { ...input.event, repository: { ...repository, node_id: "R_other" } } }],
  ["wrong repository name", { event: { ...input.event, repository: { ...repository, name: "other" } } }],
  ["unknown input", { event: { ...input.event, inputs: { ...input.event.inputs, path: "/tmp/arbitrary" } } }],
  ["mismatched payload request", { event: { ...input.event, inputs: { ...input.event.inputs, request_id: "22345678-1234-4234-8234-123456789abc" } } }],
  ["mixed PR payload", { event: { ...input.event, pull_request: { number: 42 } } }],
  ["native event", { eventName: "schedule" }],
]) test(`dispatch rejects ${name}`, () => {
  assert.throws(() => helpers().validateScanDispatch({ ...input, ...change }), /dispatch.*invalid/i);
});

test("input fingerprint uses the exact cross-language UTF-8 array and title hash", () => {
  const titleHash = hash("feat: GPU 😀");
  const literal = `[1,733665780,"conflict-labels",42,"PR_42","${SHA}","feature/é","${"b".repeat(40)}",true,"${SHA}","${titleHash}","CONFLICTING"]`;
  assert.equal(helpers().inputSha256(fence), hash(literal));
  assert.notEqual(helpers().inputSha256({ ...fence, mergeability: "MERGEABLE" }), hash(literal));
});

test("managed labels preserve bytes, use ASCII case selection and UTF-8 ordering", () => {
  const { labelsSha256 } = helpers();
  assert.equal(labelsSha256("conflict-labels", ["NEEDS-REBASE", "approved"]), hash('["NEEDS-REBASE"]'));
  assert.notEqual(labelsSha256("conflict-labels", ["NEEDS-REBASE"]), labelsSha256("conflict-labels", []));
  assert.equal(labelsSha256("metadata-labels", ["area/😀", "AREA/É", "kind/feature", "area/é", "needs-rebase"]),
    hash('["AREA/É","area/é","area/😀","kind/feature"]'));
});

test("native metadata planning keeps non-ASCII case distinct and matches ASCII case", () => {
  const { labelPlan } = require("../src/modes/metadata.js");
  assert.deepEqual(labelPlan(["area/Ä", "area/ä", "KIND/feature", "approved"],
    ["area/Ä", "area/ä", "kind/feature"]), { add: [], remove: [] });
  assert.deepEqual(labelPlan(["area/Ä", "area/ä"], ["area/ä"]), { add: [], remove: ["area/Ä"] });
});

test("label bounds count code points and bytes rather than UTF-16 units", () => {
  const label = `area/${"😀".repeat(95)}`;
  assert.equal([...label].length, 100);
  assert.equal(Buffer.byteLength(label), 385);
  assert.equal(helpers().labelsSha256("metadata-labels", [label]), hash(JSON.stringify([label])));
  assert.throws(() => helpers().labelsSha256("metadata-labels", [`area/${"😀".repeat(96)}`]), /label.*invalid|limit/i);
});

for (const [name, labels] of [
  ["case duplicate", ["needs-rebase", "NEEDS-REBASE"]],
  ["empty label", [""]], ["surrogate", ["area/\ud800"]],
  ["1001 labels", Array.from({ length: 1001 }, (_, i) => `custom/${i}`)],
]) test(`complete label collection rejects ${name}`, () => {
  assert.throws(() => helpers().labelsSha256("conflict-labels", labels), /label.*invalid|limit/i);
});

test("maximum reports fit with complete sorted candidates and unvisited failures", () => {
  const context = helpers().validateScanDispatch(input);
  const candidates = Array.from({ length: 100 }, (_, i) => 2147483548 + i);
  const results = candidates.slice(0, 99).map((number) => ({ number, status: "applied", reason: "none",
    inputSha256: "f".repeat(64), labelsSha256: "e".repeat(64) }));
  const report = helpers().buildScanReport(context, "conflict-labels", false, candidates, results);
  assert.equal(report.results.length, 100);
  assert.deepEqual(report.results.at(-1), { number: candidates.at(-1), status: "failed", reason: "not_processed",
    inputSha256: null, labelsSha256: null });
  assert.ok(Buffer.byteLength(JSON.stringify(report)) < 70 * 1024);
  assert.equal(JSON.stringify(report).includes("PR_42"), false);
});

test("reports reject invalid candidate membership and never silently truncate", () => {
  const context = helpers().validateScanDispatch(input);
  for (const candidates of [[1, 1], [0], [2147483648], Array.from({ length: 101 }, (_, i) => i + 1)]) {
    assert.throws(() => helpers().buildScanReport(context, "metadata-labels", false, candidates, []), /candidate.*invalid|limit/i);
  }
});
