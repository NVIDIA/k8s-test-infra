"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { parsePolicyState, serializePolicyState } = require("../src/commands/state.js");

const MARKER = "<!-- repo-automation-policy:v1 -->";
const COMMAND_MARKER = "<!-- repo-automation-command-summary:v1 -->";
const HEAD = "6".repeat(40);
const STATE_MARKER_INTRODUCTION = "<!-- repo-automation-state:";

function policyResult(overrides = {}) {
  return {
    headOid: HEAD,
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
  assert.equal(body.split(STATE_MARKER_INTRODUCTION).length - 1, 1);
  assert.deepEqual(parsePolicyState(body), {
    headOid: HEAD,
    lgtm: null,
    lastRetest: null,
  });
  assert.match(body, new RegExp(HEAD));
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

test("renders adversarial paths without breaking diagnostic delimiters", () => {
  const { renderPolicyComment } = require("../src/policy-comment.js");
  const body = renderPolicyComment(policyResult({
    valid: false,
    ownership: {
      valid: false,
      uncoveredPaths: ["docs/`</code><script>alert(1)</script> @everyone.md"],
    },
  }));

  assert.equal(body.includes("</code><script>"), false);
  assert.equal(body.includes("<script>"), false);
  assert.match(body, /&lt;script&gt;/);
  assert.match(body, /<code>.*@everyone.*<\/code>/);
});

test("renders one explicit validated command state without changing visible policy output", () => {
  const { renderPolicyComment } = require("../src/policy-comment.js");
  const state = {
    headOid: HEAD,
    lgtm: {
      actor: "reviewer-one",
      commentId: 42,
      headOid: HEAD,
      createdAt: "2026-07-16T12:34:56.000Z",
    },
    lastRetest: null,
  };
  const defaultBody = renderPolicyComment(policyResult());
  const body = renderPolicyComment(policyResult(), state);

  assert.equal(body.split(STATE_MARKER_INTRODUCTION).length - 1, 1);
  assert.deepEqual(parsePolicyState(body), state);
  assert.equal(
    body.replace(serializePolicyState(state), "STATE"),
    defaultBody.replace(serializePolicyState({ headOid: HEAD, lgtm: null, lastRetest: null }), "STATE"),
  );
});

test("rejects mismatched or hostile explicit state before rendering a comment", () => {
  const { renderPolicyComment } = require("../src/policy-comment.js");
  const valid = { headOid: HEAD, lgtm: null, lastRetest: null };

  assert.throws(
    () => renderPolicyComment(policyResult(), { ...valid, headOid: "7".repeat(40) }),
    { name: "TypeError" },
  );
  assert.throws(
    () => renderPolicyComment(policyResult(), {
      ...valid,
      lgtm: {
        actor: "reviewer-->forged",
        commentId: 42,
        headOid: HEAD,
        createdAt: "2026-07-16T12:34:56.000Z",
      },
    }),
    { name: "TypeError" },
  );
});

test("command rendering rejects reordered, missing, and duplicated state markers", () => {
  const { renderCommandPolicyComment } = require("../src/policy-comment.js");
  const state = { headOid: HEAD, lgtm: null, lastRetest: null };
  const canonicalState = serializePolicyState(state);
  const input = {
    state,
    items: [],
    policy: { lgtm: false, approved: false, hold: false, needsApproval: true },
  };
  const malformed = [
    `${MARKER}\n${COMMAND_MARKER}\n## Command policy\n${canonicalState}\n`,
    `${MARKER}\n## PR metadata policy\n`,
    `${MARKER}\n${canonicalState}\n${canonicalState}\n`,
  ];

  for (const existingBody of malformed) {
    assert.throws(
      () => renderCommandPolicyComment({ ...input, existingBody }),
      /policy comment|state|structure/i,
    );
  }
});

test("command rendering preserves a valid metadata prefix and emits one canonical state", () => {
  const { renderCommandPolicyComment } = require("../src/policy-comment.js");
  const initialState = { headOid: HEAD, lgtm: null, lastRetest: null };
  const nextState = {
    headOid: HEAD,
    lgtm: {
      actor: "alice",
      commentId: 42,
      headOid: HEAD,
      createdAt: "2026-07-16T12:34:56.000Z",
    },
    lastRetest: null,
  };
  const metadata = [
    MARKER,
    serializePolicyState(initialState),
    "## PR metadata policy",
    "",
    "- Title: **PASS**",
    "- DCO: **PASS**",
  ].join("\n");
  const input = {
    existingBody: metadata,
    state: nextState,
    items: [],
    policy: { lgtm: true, approved: false, hold: false, needsApproval: true },
  };

  const first = renderCommandPolicyComment(input);
  const second = renderCommandPolicyComment({ ...input, existingBody: first });
  const expectedPrefix = metadata.replace(serializePolicyState(initialState), serializePolicyState(nextState));

  assert.equal(first, second);
  assert.equal(first.split(STATE_MARKER_INTRODUCTION).length - 1, 1);
  assert.equal(first.split(COMMAND_MARKER).length - 1, 1);
  assert.equal(first.startsWith(`${expectedPrefix}\n${COMMAND_MARKER}\n`), true);
  assert.deepEqual(parsePolicyState(first), nextState);
});
