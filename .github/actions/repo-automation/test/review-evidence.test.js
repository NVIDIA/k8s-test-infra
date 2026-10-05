"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { loadReviewEvidence } = require("../src/review-evidence.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");

const HEAD = "1".repeat(40);
const SUBMITTED_AT = "2026-09-17T11:00:00.000Z";
const LISTED_REVIEW = { id: 301, user: "alice", state: "COMMENTED", commitOid: HEAD, submittedAt: SUBMITTED_AT };
const ALICE_LGTM = {
  repository: "nvidia/k8s-test-infra",
  pullRequest: 42,
  actor: "alice",
  actorRole: "reviewer",
  sourceType: "review",
  sourceId: 301,
  createdAt: SUBMITTED_AT,
};

function evidenceFor(body) {
  return loadReviewEvidence({
    github: createFakeGitHub({ reviews: [{ ...LISTED_REVIEW, body }] }),
    reviews: [LISTED_REVIEW],
    ownership: {
      files: [{ path: "pkg/agent.go", approvers: ["bob"] }],
      reviewerCandidates: ["alice"],
      approverCandidates: ["bob"],
    },
    pullRequest: { number: 42, headOid: HEAD, author: "pr-author" },
    context: { repository: "nvidia/k8s-test-infra", pullRequest: 42 },
  });
}

test("a review body LGTM is granted for a plain /lgtm", async () => {
  assert.deepEqual(await evidenceFor("/lgtm"), { approvals: [], lgtms: [ALICE_LGTM] });
});

test("an agent-owned /cherry-pick line does not change review body evaluation", async () => {
  for (const [withLine, withoutLine] of [
    ["/lgtm\n/cherry-pick release-1.2", "/lgtm"],
    ["/cherry-pick release-1.2\n/lgtm", "/lgtm"],
    ["/lgtm\n/cherry-pick", "/lgtm"],
    ["/lgtm\n/cherry-pick release branch", "/lgtm"],
    ["/cherry-pick release-1.2", ""],
  ]) {
    assert.deepEqual(await evidenceFor(withLine), await evidenceFor(withoutLine), withLine);
  }
  assert.deepEqual(await evidenceFor("/cherry-pick release-1.2\n/lgtm"), { approvals: [], lgtms: [ALICE_LGTM] });
  assert.deepEqual(await evidenceFor("/cherry-pick release-1.2"), { approvals: [], lgtms: [] });
});

test("agent-owned Prow command lines do not change review body evaluation", async () => {
  for (const line of ["/assign", "/unassign @alice", "/cc @bob", "/uncc @bob", "/close", "/reopen", "/retitle fix: x"]) {
    assert.deepEqual(await evidenceFor(`/lgtm\n${line}`), { approvals: [], lgtms: [ALICE_LGTM] }, line);
    assert.deepEqual(await evidenceFor(line), { approvals: [], lgtms: [] }, line);
  }
});

test("a /backport line blocks a review body LGTM like any unsupported command", async () => {
  assert.deepEqual(await evidenceFor("/lgtm\n/foo release-1.2"), { approvals: [], lgtms: [] });
  assert.deepEqual(await evidenceFor("/lgtm\n/backport release-1.2"), { approvals: [], lgtms: [] });
});
