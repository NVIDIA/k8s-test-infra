// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");
const { loadConfig } = require("../src/config.js");
const { createEmptyState, serializePolicyState } = require("../src/commands/state.js");
const { runMetadataLabels } = require("../src/modes/metadata-labels.js");
const { POLICY_COMMENT_MARKER, parseMetadataHeadEvidence } = require("../src/policy-comment.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");

const config = loadConfig(path.resolve(__dirname, "../../../.."));
const HEAD = "a".repeat(40);
const POLICY = "b".repeat(40);
const NEXT = "c".repeat(40);
const event = { repository: { name: "k8s-test-infra", full_name: "NVIDIA/k8s-test-infra",
  owner: { login: "NVIDIA" } }, schedule: "17 * * * *" };
const pr = { number: 42, nodeId: "PR_42", state: "open", title: "feat: refresh metadata",
  draft: false, author: "contributor", headOid: HEAD, baseBranch: "main",
  baseRepository: { owner: "nvidia", repo: "k8s-test-infra" } };
const labels = ["area/docs", "kind/feature", "size/S", "approved", "lgtm"];
function state(overrides = {}) {
  return { pullRequest: pr, labels, branches: { main: POLICY }, defaultBranchRevision: POLICY,
    files: [{ path: "docs/guide.md", additions: 1, deletions: 0 }],
    commits: [{ sha: HEAD, author: { login: "contributor" }, parents: [{ sha: POLICY }],
      commit: { author: { name: "Contributor", email: "contributor@example.com" },
        message: "feat: refresh metadata\n\nSigned-off-by: Contributor <contributor@example.com>" } }],
    contents: { "/OWNERS": "reviewers: [alice]\napprovers: [bob]\n", "/OWNERS_ALIASES": "aliases: {}\n" },
    ...overrides };
}
const invoke = (github, overrides = {}) => runMetadataLabels({ event, eventName: "schedule", github,
  config, dryRun: false, policyRevision: POLICY, ...overrides });
function commandState() {
  const value = createEmptyState({ repository: "nvidia/k8s-test-infra", pullRequest: 42,
    policyDigest: "d".repeat(64), headOid: NEXT });
  value.hold = { repository: "nvidia/k8s-test-infra", pullRequest: 42, actor: "alice", actorRole: "owner",
    sourceType: "comment", sourceId: 90, createdAt: "2026-10-02T06:00:00.000Z" };
  return serializePolicyState(value);
}
function writes(github) {
  return github.callOrder.filter(({ operation }) =>
    ["addIssueLabel", "removeIssueLabel", "upsertPolicyComment", "requestReviewers"].includes(operation));
}

test("correct labels still create trusted current-head metadata evidence and replay without writes", async () => {
  const github = createFakeGitHub(state());
  await invoke(github);
  assert.equal(parseMetadataHeadEvidence(github.metadataSnapshot().comments[0]?.body), HEAD);
  assert.equal(github.calls.upsertPolicyComment.length, 1);
  assert.deepEqual(github.calls.requestReviewers, []);
  assert.deepEqual(github.calls.addPolicyLabel, []);
  assert.deepEqual(github.calls.removePolicyLabel, []);
  assert.deepEqual(github.calls.enableAutoMerge, []);
  assert.ok(github.calls.getContentAtRevision.every(({ revision }) => revision === POLICY));
  await invoke(github);
  assert.equal(github.calls.upsertPolicyComment.length, 1);
});

test("stale metadata updates the same trusted comment and preserves exact v2 hold bytes", async () => {
  const serialized = commandState();
  const github = createFakeGitHub(state({ comments: [{ id: 77, body:
    `${POLICY_COMMENT_MARKER}\n${serialized}\n<!-- repo-automation-metadata-head:v1 {"headOid":"${NEXT}"} -->\n` }] }));
  await invoke(github);
  const [comment] = github.metadataSnapshot().comments;
  assert.equal(comment.id, 77);
  assert.ok(comment.body.includes(serialized));
  assert.equal(parseMetadataHeadEvidence(comment.body), HEAD);
});

for (const [name, marker] of [
  ["malformed", "<!-- repo-automation-state:v2 invalid -->"],
  ["duplicate", `${commandState()}\n${commandState()}`],
  ["wrong PR", commandState().replace('"pullRequest":42', '"pullRequest":43')],
  ["wrong repository", commandState().replaceAll("nvidia/k8s-test-infra", "attacker/fork")],
]) test(`${name} command state prevents every scan write`, async () => {
  const github = createFakeGitHub(state({ labels: [], comments: [{ id: 77,
    body: `${POLICY_COMMENT_MARKER}\n${marker}\n` }] }));
  await assert.rejects(() => invoke(github), /initial read failed/);
  assert.deepEqual(writes(github), []);
});

test("invalid same-head policy replaces accepted evidence with a failing comment", async () => {
  const github = createFakeGitHub(state({ pullRequest: { ...pr, title: "invalid" }, comments: [{ id: 77,
    body: `${POLICY_COMMENT_MARKER}\n<!-- repo-automation-metadata-head:v2 {"headOid":"${HEAD}","valid":true} -->\n` }] }));
  await invoke(github);
  const [comment] = github.metadataSnapshot().comments;
  assert.equal(comment.id, 77);
  assert.equal(parseMetadataHeadEvidence(comment.body), null);
  assert.match(comment.body, /Title: \*\*FAIL\*\*/);
});

test("an unresolved author identity cannot grant author-only OWNERS coverage", async () => {
  const github = createFakeGitHub(state({ pullRequest: { ...pr, author: "bob" },
    contents: { "/OWNERS": "reviewers: [bob]\napprovers: [bob]\n", "/OWNERS_ALIASES": "aliases: {}\n" },
    failures: { getUserIdentity: [new Error("identity unavailable"), new Error("identity unavailable")] } }));
  await invoke(github);
  const [comment] = github.metadataSnapshot().comments;
  assert.equal(parseMetadataHeadEvidence(comment.body), null);
  assert.match(comment.body, /Ownership: \*\*FAIL\*\*/);
  assert.equal(github.calls.getUserIdentity.length, 2, "identity is checked in both planning and fresh evidence");
});

test("dry-run computes policy evidence without comment or label writes", async () => {
  const github = createFakeGitHub(state({ labels: [] }));
  await invoke(github, { dryRun: true });
  assert.ok(github.calls.listPullRequestCommits.length > 0);
  assert.ok(github.calls.getContentAtRevision.length > 0);
  assert.deepEqual(writes(github), []);
});

test("a later candidate evidence read failure prevents writes to all candidates", async () => {
  const github = createFakeGitHub(state({ labels: [], openPullRequestNumbers: [42, 43] }));
  const getPr = github.getPullRequest.bind(github);
  github.getPullRequest = async (number) => ({ ...await getPr(number), number, nodeId: `PR_${number}` });
  const getCommits = github.listPullRequestCommits.bind(github);
  github.listPullRequestCommits = async (number) => {
    if (number === 43) throw new Error("commit data unavailable");
    return getCommits(number);
  };
  await assert.rejects(() => invoke(github), /initial read failed for PR 43/);
  assert.deepEqual(writes(github), []);
});

for (const operation of ["upsertPolicyComment", "getPolicyComment"]) {
  test(`${operation} failure cannot certify scan success`, async () => {
    const github = createFakeGitHub(state({ failures: { [operation]: new Error("unavailable") } }));
    await assert.rejects(() => invoke(github), (error) => {
      assert.equal(error.summary.results[0].status, "failed");
      return true;
    });
  });
}

test("an acknowledged comment write without stored evidence fails final verification", async () => {
  const github = createFakeGitHub(state());
  github.upsertPolicyComment = async () => ({ action: "created", id: 77 });
  await assert.rejects(() => invoke(github), /comment verification failed/);
});

test("head change during comment write cannot produce a verified success result", async () => {
  const github = createFakeGitHub(state());
  const upsert = github.upsertPolicyComment.bind(github);
  github.upsertPolicyComment = async (...args) => {
    const result = await upsert(...args);
    github.getPullRequest = async () => ({ ...pr, headOid: NEXT });
    return result;
  };
  const summary = await invoke(github);
  assert.equal(summary.results[0].status, "deferred");
});

test("a hold added during the last input read is preserved without a scan comment write", async () => {
  const github = createFakeGitHub(state({ comments: [{ id: 77, body: POLICY_COMMENT_MARKER }] }));
  const getPr = github.getPullRequest.bind(github);
  const upsert = github.upsertPolicyComment.bind(github);
  let reads = 0;
  github.getPullRequest = async (number) => {
    const value = await getPr(number);
    if (++reads === 3) {
      await upsert(42, POLICY_COMMENT_MARKER, `${POLICY_COMMENT_MARKER}\n${commandState()}\n`,
        { action: "update", id: 77 });
    }
    return value;
  };
  const summary = await invoke(github);
  assert.equal(summary.results[0].status, "deferred");
  assert.equal(github.calls.upsertPolicyComment.length, 1, "only the concurrent hold writer may update");
  assert.ok(github.metadataSnapshot().comments[0].body.includes(commandState()));
});

for (const drift of ["head", "base tip"]) {
  test(`${drift} change during the final label read cannot produce verified scan success`, async () => {
    const github = createFakeGitHub(state());
    const listLabels = github.listIssueLabels.bind(github);
    let reads = 0;
    github.listIssueLabels = async (number) => {
      const value = await listLabels(number);
      if (++reads === 3) {
        if (drift === "head") github.getPullRequest = async () => ({ ...pr, headOid: NEXT });
        else github.getBranch = async () => ({ name: "main", oid: NEXT });
      }
      return value;
    };
    const summary = await invoke(github);
    assert.equal(summary.results[0].status, "deferred");
    assert.equal(summary.status, "deferred");
  });
}
