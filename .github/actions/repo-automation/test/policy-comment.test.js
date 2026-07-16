"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const MARKER = "<!-- repo-automation-policy:v1 -->";

function policyResult(overrides = {}) {
  return {
    headOid: "live-head-oid-6f9d",
    valid: true,
    title: { valid: true, error: null },
    dco: { valid: true, failures: [], exempted: [] },
    ownership: { valid: true, uncoveredPaths: [] },
    labels: {
      add: ["area/docs", "kind/feature", "size/M"],
      remove: ["kind/bug", "size/S"],
    },
    reviewers: { request: ["bob"], preserved: ["alice"] },
    ...overrides,
  };
}

test("renders one stable marker and the current live head OID", () => {
  const { POLICY_COMMENT_MARKER, renderPolicyComment } = require("../src/policy-comment.js");

  const body = renderPolicyComment(policyResult());

  assert.equal(POLICY_COMMENT_MARKER, MARKER);
  assert.equal(body.split(MARKER).length - 1, 1);
  assert.match(body, /live-head-oid-6f9d/);
  assert.match(body, /title/i);
  assert.match(body, /DCO/i);
  assert.match(body, /ownership/i);
});

test("renders deterministically from normalized policy results", () => {
  const { renderPolicyComment } = require("../src/policy-comment.js");
  const first = renderPolicyComment(policyResult());
  const second = renderPolicyComment(policyResult({
    labels: {
      add: ["size/M", "kind/feature", "area/docs"],
      remove: ["size/S", "kind/bug"],
    },
    reviewers: { request: ["bob"], preserved: ["alice"] },
  }));

  assert.equal(first, second);
});

test("reports validation failures without accepting a raw PR body", () => {
  const { renderPolicyComment } = require("../src/policy-comment.js");
  const rawBody = "private-body-secret-sentinel-44d8";
  const body = renderPolicyComment(policyResult({
    valid: false,
    title: { valid: false, error: "title must match the required format" },
    dco: {
      valid: false,
      failures: [{ sha: "unsigned-commit", reason: "missing a matching trailer" }],
      exempted: [],
    },
    ownership: { valid: false, uncoveredPaths: ["unowned/file.go"] },
    rawBody,
  }));

  assert.match(body, /title must match the required format/);
  assert.match(body, /unsigned-commit/);
  assert.match(body, /unowned\/file\.go/);
  assert.equal(body.includes(rawBody), false);
});

test("rejects malformed policy results instead of emitting ambiguous comments", async (t) => {
  const { renderPolicyComment } = require("../src/policy-comment.js");
  const cases = [
    ["missing result", undefined],
    ["missing head", policyResult({ headOid: "" })],
    ["unsafe head", policyResult({ headOid: "head\nforged" })],
    ["missing title result", policyResult({ title: undefined })],
    ["missing DCO result", policyResult({ dco: undefined })],
    ["missing ownership result", policyResult({ ownership: undefined })],
  ];

  for (const [name, input] of cases) {
    await t.test(name, () => {
      assert.throws(() => renderPolicyComment(input), { name: "TypeError" });
    });
  }
});

test("renders adversarial OIDs and paths without breaking diagnostic delimiters", () => {
  const { renderPolicyComment } = require("../src/policy-comment.js");
  const body = renderPolicyComment(policyResult({
    headOid: "head`</code><script>alert(1)</script>",
    valid: false,
    ownership: {
      valid: false,
      uncoveredPaths: ["docs/` @everyone [click](https://example.invalid).md"],
    },
  }));

  assert.equal(body.includes("</code><script>"), false);
  assert.equal(body.includes("<script>"), false);
  assert.match(body, /&lt;script&gt;/);
  assert.match(body, /<code>.*@everyone.*<\/code>/);
});
