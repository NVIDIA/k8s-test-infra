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
    configuration: { valid: true },
    labels: {
      add: ["area/docs", "kind/feature", "size/M"],
      remove: ["kind/bug", "size/S"],
    },
    reviewers: { request: ["bob"], preserved: ["alice"] },
    ...overrides,
  };
}

test("renders one stable marker and the current live head OID", () => {
  const {
    POLICY_COMMENT_MARKER,
    parseMetadataHeadEvidence,
    renderPolicyComment,
  } = require("../src/policy-comment.js");

  const body = renderPolicyComment(policyResult());

  assert.equal(POLICY_COMMENT_MARKER, MARKER);
  assert.equal(body.split(MARKER).length - 1, 1);
  assert.match(body, /live-head-oid-6f9d/);
  assert.match(body, /title/i);
  assert.match(body, /DCO/i);
  assert.match(body, /ownership/i);
  assert.equal(body.includes('<!-- repo-automation-metadata-head:v2 {"headOid":"live-head-oid-6f9d","valid":true} -->'), true);
  assert.equal(parseMetadataHeadEvidence(body), "live-head-oid-6f9d");
});

test("rejects missing, ambiguous, or noncanonical validity-bound metadata evidence", async (t) => {
  const { parseMetadataHeadEvidence } = require("../src/policy-comment.js");
  const marker = '<!-- repo-automation-metadata-head:v2 {"headOid":"a","valid":true} -->';
  const legacy = '<!-- repo-automation-metadata-head:v1 {"headOid":"a"} -->';
  const cases = [
    ["missing", "no evidence"],
    ["duplicate", `${marker}\n${marker}`],
    ["mixed versions", `${legacy}\n${marker}`],
    ["mixed versions in reverse order", `${marker}\n${legacy}`],
    ["ambiguous introduction", `${marker}\n<!-- repo-automation-metadata-head:malformed -->`],
    ["malformed JSON", '<!-- repo-automation-metadata-head:v2 {"headOid": -->'],
    ["missing head", '<!-- repo-automation-metadata-head:v2 {"valid":true} -->'],
    ["empty head", '<!-- repo-automation-metadata-head:v2 {"headOid":"","valid":true} -->'],
    ["non-string head", '<!-- repo-automation-metadata-head:v2 {"headOid":1,"valid":true} -->'],
    ["missing validity", '<!-- repo-automation-metadata-head:v2 {"headOid":"a"} -->'],
    ["false validity", '<!-- repo-automation-metadata-head:v2 {"headOid":"a","valid":false} -->'],
    ["null validity", '<!-- repo-automation-metadata-head:v2 {"headOid":"a","valid":null} -->'],
    ["truthy validity", '<!-- repo-automation-metadata-head:v2 {"headOid":"a","valid":"true"} -->'],
    ["extra property", '<!-- repo-automation-metadata-head:v2 {"headOid":"a","valid":true,"extra":true} -->'],
    ["duplicate JSON key", '<!-- repo-automation-metadata-head:v2 {"headOid":"a","valid":false,"valid":true} -->'],
    ["noncanonical key order", '<!-- repo-automation-metadata-head:v2 {"valid":true,"headOid":"a"} -->'],
    ["noncanonical whitespace", '<!-- repo-automation-metadata-head:v2 {"headOid": "a","valid":true} -->'],
  ];
  for (const [name, body] of cases) {
    await t.test(name, () => assert.equal(parseMetadataHeadEvidence(body), null));
  }
});

test("rejects legacy same-head evidence for both successful and failed metadata", async (t) => {
  const { parseMetadataHeadEvidence } = require("../src/policy-comment.js");
  for (const status of ["PASS", "FAIL"]) {
    await t.test(status, () => {
      const body = `${MARKER}\n<!-- repo-automation-metadata-head:v1 {"headOid":"live-head-oid-6f9d"} -->\n- Title: **${status}**\n`;
      assert.equal(parseMetadataHeadEvidence(body), null);
    });
  }
});

test("accepts one canonical validity-bound marker for the current head", () => {
  const { parseMetadataHeadEvidence } = require("../src/policy-comment.js");
  const body = `${MARKER}\n<!-- repo-automation-metadata-head:v2 {"headOid":"live-head-oid-6f9d","valid":true} -->\n`;
  assert.equal(parseMetadataHeadEvidence(body), "live-head-oid-6f9d");
});

test("does not certify inconsistent or incomplete metadata validation", async (t) => {
  const { parseMetadataHeadEvidence, renderPolicyComment } = require("../src/policy-comment.js");
  const cases = [
    ["overall failure with passing components", { valid: false }],
    ["title failure with passing overall result", { title: { valid: false, error: "invalid title" } }],
    ["DCO failure with passing overall result", { dco: { valid: false, failures: [], exempted: [] } }],
    ["ownership failure with passing overall result", { ownership: { valid: false, uncoveredPaths: [] } }],
    ["configuration failure with passing overall result", { configuration: { valid: false } }],
    ["missing explicit configuration", { configuration: undefined }],
  ];
  for (const [name, overrides] of cases) {
    await t.test(name, () => {
      const body = renderPolicyComment(policyResult(overrides));
      assert.equal(parseMetadataHeadEvidence(body), null);
      assert.equal(body.includes("repo-automation-metadata-head:"), false);
    });
  }
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

test("reports validation failures without certifying the head or accepting a raw PR body", () => {
  const { parseMetadataHeadEvidence, renderPolicyComment } = require("../src/policy-comment.js");
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
  assert.equal(parseMetadataHeadEvidence(body), null);
  assert.equal(body.includes("repo-automation-metadata-head:"), false);
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

test("preserves one valid command-state marker during metadata refresh", () => {
  const { renderPolicyComment } = require("../src/policy-comment.js");
  const state = "<!-- repo-automation-state:v2 {\"repository\":\"nvidia/k8s-test-infra\"} -->";
  const existing = `${MARKER}\n${state}\nold visible text\n`;

  const body = renderPolicyComment(policyResult(), existing);

  assert.equal(body.split(state).length - 1, 1);
  assert.equal(body.includes("old visible text"), false);
});

test("failed metadata refresh removes trusted head evidence and preserves a valid hold", () => {
  const { parseMetadataHeadEvidence, renderPolicyComment } = require("../src/policy-comment.js");
  const { createEmptyState, parsePolicyState, serializePolicyState } = require("../src/commands/state.js");
  const state = createEmptyState({
    repository: "nvidia/k8s-test-infra",
    pullRequest: 42,
    policyDigest: "2".repeat(64),
    headOid: "1".repeat(40),
  });
  state.hold = {
    repository: "nvidia/k8s-test-infra",
    pullRequest: 42,
    actor: "alice",
    actorRole: "owner",
    sourceType: "comment",
    sourceId: 99,
    createdAt: "2026-09-17T12:00:00.000Z",
  };
  const stateMarker = serializePolicyState(state);
  const existing = renderPolicyComment(policyResult(), `${MARKER}\n${stateMarker}\n`);

  const body = renderPolicyComment(policyResult({ valid: false }), existing);

  assert.equal(parseMetadataHeadEvidence(body), null);
  assert.equal(body.includes("repo-automation-metadata-head:"), false);
  assert.equal(body.split(stateMarker).length - 1, 1);
  assert.deepEqual(parsePolicyState(body), state);
});

test("renders bounded command results without reflecting raw command text", () => {
  const {
    renderCommandPolicyComment,
  } = require("../src/policy-comment.js");
  const serializedState = "<!-- repo-automation-state:v2 {\"safe\":true} -->";
  const secret = "raw-command-secret-2a91";
  const body = renderCommandPolicyComment({
    existingBody: `${MARKER}\n## PR metadata policy\n\nHead: <code>head</code>\n`,
    serializedState,
    commands: [{ line: 1, name: "lgtm", status: "applied", code: "lgtm-recorded", raw: secret }],
    diagnostics: [],
    policy: { lgtm: true, approved: false, hold: false, needsApproval: true },
  });

  assert.equal(body.split(MARKER).length - 1, 1);
  assert.equal(body.split("repo-automation-state:v2").length - 1, 1);
  assert.match(body, /lgtm-recorded/);
  assert.equal(body.includes(secret), false);
});

function renderWithRejections(rejectedBacklogEvidence) {
  const { renderCommandPolicyComment } = require("../src/policy-comment.js");
  return renderCommandPolicyComment({
    existingBody: null,
    serializedState: "<!-- repo-automation-state:v2 {\"safe\":true} -->",
    commands: [{ line: 1, name: "hold", status: "applied", code: "hold-recorded" }],
    diagnostics: [],
    policy: { lgtm: false, approved: false, hold: true, needsApproval: true },
    rejectedBacklogEvidence,
  });
}

function rejection(commentId, commands) {
  return { commentId, commands, status: "rejected", code: "stale-backlog-evidence" };
}

test("names each caught-up evidence rejection by comment and asks for a re-issue on the current head", () => {
  const body = renderWithRejections([rejection(97, ["approve", "lgtm"]), rejection(98, ["lgtm"])]);
  const lines = body.split("\n");
  const end = lines.indexOf("<!-- repo-automation-command-summary:v1:end -->");

  assert.deepEqual(lines.slice(end - 2, end), [
    "- Comment 97: <code>/approve</code> and <code>/lgtm</code> rejected (<code>stale-backlog-evidence</code>); "
      + "its own run was skipped, so re-issue them on the current head.",
    "- Comment 98: <code>/lgtm</code> rejected (<code>stale-backlog-evidence</code>); "
      + "its own run was skipped, so re-issue it on the current head.",
  ]);
});

test("bounds caught-up evidence rejections in the policy comment", () => {
  const body = renderWithRejections(Array.from({ length: 23 }, (_, index) => rejection(100 + index, ["lgtm"])));
  const lines = body.split("\n").filter((line) => line.includes("stale-backlog-evidence"));

  assert.equal(lines.length, 21);
  assert.equal(lines[19], "- Comment 119: <code>/lgtm</code> rejected (<code>stale-backlog-evidence</code>); "
    + "its own run was skipped, so re-issue it on the current head.");
  assert.equal(lines[20], "- 3 more comments: <code>/lgtm</code> or <code>/approve</code> rejected "
    + "(<code>stale-backlog-evidence</code>); re-issue them on the current head.");
});

test("rejects malformed caught-up evidence rejections", async (t) => {
  for (const [name, value] of [
    ["not an array", {}],
    ["zero comment", [rejection(0, ["lgtm"])]],
    ["no commands", [rejection(98, [])]],
    ["non-evidence command", [rejection(98, ["hold"])]],
    ["duplicate command", [rejection(98, ["lgtm", "lgtm"])]],
    ["markup in command", [rejection(98, ["<b>lgtm</b>"])]],
  ]) await t.test(name, () => {
    assert.throws(() => renderWithRejections(value), {
      name: "TypeError",
      message: "caught-up evidence rejections are invalid",
    });
  });
});

test("rejects caught-up evidence rejections with another status or code", async (t) => {
  for (const [name, value] of [
    ["applied status", [{ ...rejection(98, ["lgtm"]), status: "applied" }]],
    ["other code", [{ ...rejection(98, ["lgtm"]), code: "not-authorized" }]],
  ]) await t.test(name, () => {
    assert.throws(() => renderWithRejections(value), {
      name: "TypeError",
      message: "caught-up evidence rejections are invalid",
    });
  });
});
