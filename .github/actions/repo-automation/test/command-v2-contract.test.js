"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");

const { loadConfig } = require("../src/config.js");
const { POLICY_COMMENT_MARKER } = require("../src/policy-comment.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const HEAD = "1".repeat(40);
const REVISION = "2".repeat(40);
const event = {
  action: "created",
  repository: {
    owner: { login: "NVIDIA" },
    name: "k8s-test-infra",
    full_name: "NVIDIA/k8s-test-infra",
  },
  issue: { number: 42, pull_request: { url: "event-url-is-untrusted" } },
  comment: { id: 99, body: "/hold event-body-is-untrusted", user: { login: "mallory" } },
};

function state(overrides = {}) {
  return {
    pullRequest: {
      number: 42,
      nodeId: "PR_kwDOABC42",
      title: "feat: command policy",
      body: "",
      draft: false,
      author: "pr-author",
      headOid: HEAD,
      state: "open",
      baseBranch: "main",
      baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    },
    files: [{ path: "pkg/agent.go", additions: 1, deletions: 0, status: "modified" }],
    reviews: [],
    labels: [],
    issueComments: [{
      id: 99,
      issueNumber: 42,
      body: "/lgtm",
      author: "alice",
      authorType: "User",
      edited: false,
    }],
    users: {
      alice: { login: "alice", type: "User", resolved: true, deleted: false },
      "pr-author": { login: "pr-author", type: "User", resolved: true, deleted: false },
    },
    collaboratorAccess: {
      alice: { liveCollaborator: false, permission: "none" },
      "pr-author": { liveCollaborator: false, permission: "none" },
    },
    defaultBranchRevision: REVISION,
    contents: {
      "/OWNERS": [
        "reviewers:",
        "  - alice",
        "approvers:",
        "  - bob",
        "",
      ].join("\n"),
      "/OWNERS_ALIASES": "aliases: {}\n",
    },
    comments: [],
    ...overrides,
  };
}

async function run(github, dryRun = false, config = loadConfig(repositoryRoot)) {
  const { runCommand } = require("../src/modes/command.js");
  return runCommand({
    event,
    github,
    config,
    dryRun,
    now: () => "2026-09-17T12:00:00.000Z",
  });
}

function nativeReview(overrides = {}) {
  return {
    id: 201,
    user: "bob",
    state: "APPROVED",
    commitOid: HEAD,
    submittedAt: "2026-09-17T11:00:00.000Z",
    ...overrides,
  };
}

function assertNoWrites(github) {
  for (const operation of ["addPolicyLabel", "removePolicyLabel", "rerunFailedJobs", "upsertPolicyComment"]) {
    assert.equal(github.calls[operation].length, 0, operation);
  }
}

test("command and native review approvals cover separate OWNERS scopes without forged state", async () => {
  const initial = state({
    files: [{ path: "README.md" }, { path: "pkg/agent.go" }],
    reviews: [nativeReview()],
    labels: ["approved"],
  });
  initial.contents["/OWNERS"] = "reviewers: [alice]\napprovers: [alice]\n";
  initial.contents["/pkg/OWNERS"] = "approvers: [bob]\noptions:\n  no_parent_owners: true\n";
  initial.issueComments[0].body = "/approve\n/lgtm";
  const config = loadConfig(repositoryRoot);
  config.policy.activeOwnerFiles = ["/OWNERS", "/pkg/OWNERS"];
  const github = createFakeGitHub(initial);
  const result = await run(github, false, config);
  assert.deepEqual(result.policy, { lgtm: true, approved: true, hold: false, needsApproval: false });
  assert.equal(github.calls.removePolicyLabel.some(({ label }) => label === "approved"), false);
  const { parsePolicyState } = require("../src/commands/state.js");
  const persisted = parsePolicyState(github.metadataSnapshot().comments[0].body);
  assert.deepEqual(persisted.approvals.map(({ actor }) => actor), ["alice"]);
  assert.ok(persisted.approvals.every(({ sourceType }) => sourceType === "comment"));
});

test("author and native review approvals cover separate OWNERS scopes", async () => {
  const initial = state({ files: [{ path: "README.md" }, { path: "pkg/agent.go" }], reviews: [nativeReview()] });
  initial.contents["/OWNERS"] = "reviewers: [alice]\napprovers: [pr-author]\n";
  initial.contents["/pkg/OWNERS"] = "approvers: [bob]\noptions:\n  no_parent_owners: true\n";
  const config = loadConfig(repositoryRoot);
  config.policy.activeOwnerFiles = ["/OWNERS", "/pkg/OWNERS"];
  const result = await run(createFakeGitHub(initial), false, config);
  assert.equal(result.policy.approved, true);
  assert.equal(result.policy.needsApproval, false);
});

test("hold preserves native approval and review-body LGTM without storing review evidence", async () => {
  const initial = state({ reviews: [nativeReview()], labels: ["approved", "lgtm"] });
  initial.issueComments[0].body = "/hold";
  const github = createFakeGitHub(initial);
  const getReview = github.getPullRequestReview.bind(github);
  github.getPullRequestReview = async (...args) => ({ ...await getReview(...args), body: "/lgtm" });
  const result = await run(github);
  assert.deepEqual(result.policy, { lgtm: true, approved: true, hold: true, needsApproval: false });
  assert.deepEqual(github.calls.removePolicyLabel, []);
  assert.deepEqual(github.calls.addPolicyLabel.map(({ label }) => label), ["do-not-merge/hold"]);
  const { parsePolicyState } = require("../src/commands/state.js");
  const persisted = parsePolicyState(github.metadataSnapshot().comments[0].body);
  assert.deepEqual(persisted.approvals, []);
  assert.deepEqual(persisted.lgtms, []);
});

test("a newer COMMENTED review preserves native approval during command planning", async () => {
  const github = createFakeGitHub(state({ reviews: [nativeReview(), nativeReview({
    id: 202, state: "COMMENTED", submittedAt: "2026-09-17T11:30:00.000Z",
  })] }));
  assert.equal((await run(github)).policy.approved, true);
});

test("a newer review without LGTM revokes review LGTM while preserving native approval", async () => {
  const initial = state({ reviews: [nativeReview(), nativeReview({
    id: 202, state: "COMMENTED", submittedAt: "2026-09-17T11:30:00.000Z",
  })], labels: ["approved", "lgtm"] });
  initial.issueComments[0].body = "/hold";
  const github = createFakeGitHub(initial);
  const getReview = github.getPullRequestReview.bind(github);
  github.getPullRequestReview = async (...args) => ({
    ...await getReview(...args), body: args[1] === 201 ? "/lgtm" : "More review notes.",
  });
  const result = await run(github);
  assert.equal(result.policy.approved, true);
  assert.equal(result.policy.lgtm, false);
  assert.deepEqual(github.calls.removePolicyLabel.map(({ label }) => label), ["lgtm"]);
});

test("invalid native evidence cannot grant approval or review LGTM through commands", async () => {
  for (const override of [
    { review: { commitOid: "3".repeat(40) } },
    { review: { state: "DISMISSED" } },
    { review: { state: "CHANGES_REQUESTED" } },
    { review: { user: "mallory" } },
    { review: { user: "pr-author" } },
    { review: { user: "bob[bot]" } },
    { identity: { type: "Bot" } },
    { identity: { resolved: false } },
    { identity: { deleted: true } },
    { identity: { login: "mallory" } },
  ]) {
    const initial = state({ reviews: [nativeReview(override.review)], labels: ["approved", "lgtm"] });
    initial.issueComments[0].body = "/hold";
    initial.users.bob = { login: "bob", type: "User", resolved: true, deleted: false, ...override.identity };
    const github = createFakeGitHub(initial);
    const getReview = github.getPullRequestReview.bind(github);
    github.getPullRequestReview = async (...args) => ({ ...await getReview(...args), body: "/lgtm" });
    const result = await run(github);
    assert.equal(result.policy.approved, false, JSON.stringify(override));
    assert.equal(result.policy.lgtm, false, JSON.stringify(override));
  }
});

test("a later dismissal or change request revokes native approval during command planning", async () => {
  for (const reviewState of ["DISMISSED", "CHANGES_REQUESTED"]) {
    const github = createFakeGitHub(state({ reviews: [nativeReview(), nativeReview({
      id: 202, state: reviewState, submittedAt: "2026-09-17T11:30:00.000Z",
    })] }));
    assert.equal((await run(github)).policy.approved, false);
  }
});

test("pending reviews cannot grant command-mode approval or LGTM", async () => {
  const pending = nativeReview({ state: "PENDING", commitOid: null });
  delete pending.submittedAt;
  const initial = state({ reviews: [pending] });
  initial.issueComments[0].body = "/hold";
  const result = await run(createFakeGitHub(initial));
  assert.equal(result.policy.approved, false);
  assert.equal(result.policy.lgtm, false);
});

test("changed native reviews stop all command writes even without author approval paths", async () => {
  const github = createFakeGitHub(state({ reviews: [nativeReview()] }));
  const listReviews = github.listPullRequestReviews.bind(github);
  github.listPullRequestReviews = async (...args) => (await listReviews(...args)).map((review) => ({
    ...review, state: github.calls.listPullRequestReviews.length > 1 ? "DISMISSED" : "APPROVED",
  }));
  await assert.rejects(() => run(github), /review evidence changed/);
  assertNoWrites(github);
});

test("changed native reviewer identity stops all command writes", async () => {
  const github = createFakeGitHub(state({ reviews: [nativeReview()] }));
  const getUser = github.getUserIdentity.bind(github);
  github.getUserIdentity = async (login) => {
    const identity = await getUser(login);
    return login === "bob"
      ? { ...identity, deleted: github.calls.listPullRequestReviews.length > 1 }
      : identity;
  };
  await assert.rejects(() => run(github), /review evidence changed/);
  assertNoWrites(github);
});

test("replacement native approval stops writes even when its actor and result are unchanged", async () => {
  const github = createFakeGitHub(state({ reviews: [nativeReview()] }));
  const listReviews = github.listPullRequestReviews.bind(github);
  github.listPullRequestReviews = async (...args) => (await listReviews(...args)).map((review) => ({
    ...review, id: github.calls.listPullRequestReviews.length > 1 ? 202 : 201,
  }));
  await assert.rejects(() => run(github), /review evidence changed/);
  assertNoWrites(github);
});

test("changed PR head stops native review labels before any command writes", async () => {
  const initial = state({ reviews: [nativeReview()] });
  initial.pullRequests = [initial.pullRequest, { ...initial.pullRequest, headOid: "3".repeat(40) }];
  const github = createFakeGitHub(initial);
  await assert.rejects(() => run(github), /live command inputs changed/);
  assertNoWrites(github);
});

test("changed review-body LGTM stops all command writes", async () => {
  const initial = state({ reviews: [nativeReview()] });
  initial.issueComments[0].body = "/hold";
  const github = createFakeGitHub(initial);
  const getReview = github.getPullRequestReview.bind(github);
  github.getPullRequestReview = async (...args) => ({
    ...await getReview(...args),
    body: github.calls.listPullRequestReviews.length > 1 ? "LGTM removed." : "/lgtm",
  });
  await assert.rejects(() => run(github), /review evidence changed/);
  assertNoWrites(github);
});

test("changed trusted OWNERS stops native approval before command writes", async () => {
  const github = createFakeGitHub(state({ reviews: [nativeReview()] }));
  const getContent = github.getContentAtRevision.bind(github);
  let reads = 0;
  github.getContentAtRevision = async (ownerPath, revision) => {
    const source = await getContent(ownerPath, revision);
    return ownerPath === "/OWNERS" && ++reads > 1 ? "reviewers: [alice]\napprovers: [carol]\n" : source;
  };
  await assert.rejects(() => run(github), /authority changed/);
  assertNoWrites(github);
});

test("re-reads the live command and records current reviewer evidence", async () => {
  const github = createFakeGitHub(state());
  const result = await run(github);

  assert.equal(result.status, "complete");
  assert.deepEqual(result.policy, {
    lgtm: true,
    approved: false,
    hold: false,
    needsApproval: true,
  });
  assert.deepEqual(github.calls.addPolicyLabel.map(({ label }) => label), [
    "lgtm",
    "do-not-merge/needs-approval",
  ]);
  assert.equal(github.calls.upsertPolicyComment.length, 1);
  assert.match(github.calls.upsertPolicyComment[0].body, /repo-automation-state:v2/);
  assert.equal(JSON.stringify(result).includes("event-body-is-untrusted"), false);
  assert.deepEqual(github.calls.getIssueComment, [{ commentId: 99 }, { commentId: 99 }]);
  assert.deepEqual(github.calls.getPullRequest, [{ prNumber: 42 }, { prNumber: 42 }]);
});

test("rejects author self-approval and never trusts a manual approval label", async () => {
  const github = createFakeGitHub(state({
    issueComments: [{
      id: 99,
      issueNumber: 42,
      body: "/approve",
      author: "pr-author",
      authorType: "User",
      edited: false,
    }],
    labels: ["approved"],
  }));
  const result = await run(github);

  assert.equal(result.commands[0].code, "author-cannot-provide-evidence");
  assert.equal(result.policy.approved, false);
  assert.deepEqual(github.calls.removePolicyLabel, [{ prNumber: 42, label: "approved" }]);
  assert.deepEqual(github.calls.addPolicyLabel, [{
    prNumber: 42,
    label: "do-not-merge/needs-approval",
  }]);
});

test("independent LGTM preserves implicit approval for a trusted human approver author", async () => {
  const initial = state();
  initial.contents["/OWNERS"] = "reviewers: [alice]\napprovers: [pr-author]\n";
  initial.labels = ["approved", "do-not-merge/needs-approval"];
  const github = createFakeGitHub(initial);
  const result = await run(github);
  assert.deepEqual(result.policy, { lgtm: true, approved: true, hold: false, needsApproval: false });
  assert.equal(github.calls.removePolicyLabel.some(({ label }) => label === "approved"), false);
  assert.ok(github.calls.removePolicyLabel.some(({ label }) => label === "do-not-merge/needs-approval"));
  assert.deepEqual(github.metadataSnapshot().comments[0].body.match(/"approvals":\[\]/)?.[0], '"approvals":[]');
});

test("author implicit approval cannot grant self-LGTM", async () => {
  const initial = state();
  initial.contents["/OWNERS"] = "reviewers: [alice]\napprovers: [pr-author]\n";
  initial.issueComments[0].author = "pr-author";
  const github = createFakeGitHub(initial);
  const result = await run(github);
  assert.equal(result.commands[0].code, "author-cannot-provide-evidence");
  assert.equal(result.policy.approved, true);
  assert.equal(result.policy.lgtm, false);
  assert.equal(result.policy.needsApproval, true);
});

test("non-human or unresolved authors do not receive implicit approval from commands", async () => {
  for (const override of [{ type: "Bot" }, { deleted: true }, { resolved: false }, { login: "mallory" }]) {
    const initial = state();
    initial.contents["/OWNERS"] = "reviewers: [alice]\napprovers: [pr-author]\n";
    Object.assign(initial.users["pr-author"], override);
    const github = createFakeGitHub(initial);
    const result = await run(github);
    assert.equal(result.policy.approved, false);
    assert.equal(result.policy.needsApproval, true);
  }
});

test("changed author identity stops implicit approval before command writes", async () => {
  const initial = state();
  initial.contents["/OWNERS"] = "reviewers: [alice]\napprovers: [pr-author]\n";
  const github = createFakeGitHub(initial);
  const getUser = github.getUserIdentity.bind(github);
  let authorReads = 0;
  github.getUserIdentity = async (login) => {
    const identity = await getUser(login);
    return login === "pr-author" ? { ...identity, deleted: ++authorReads > 1 } : identity;
  };
  await assert.rejects(() => run(github), /authority changed/);
  assert.equal(github.calls.addPolicyLabel.length, 0);
  assert.equal(github.calls.upsertPolicyComment.length, 0);
});

test("changed trusted OWNERS stops implicit approval before command writes", async () => {
  const initial = state();
  initial.contents["/OWNERS"] = "reviewers: [alice]\napprovers: [pr-author]\n";
  const github = createFakeGitHub(initial);
  const getContent = github.getContentAtRevision.bind(github);
  let ownerReads = 0;
  github.getContentAtRevision = async (path, revision) => {
    const source = await getContent(path, revision);
    return path === "/OWNERS" && ++ownerReads > 1 ? "reviewers: [alice]\napprovers: [bob]\n" : source;
  };
  await assert.rejects(() => run(github), /authority changed/);
  assert.equal(github.calls.addPolicyLabel.length, 0);
  assert.equal(github.calls.upsertPolicyComment.length, 0);
});

test("preserves validity-bound metadata evidence in the single bot-owned policy comment", async () => {
  const { parseMetadataHeadEvidence } = require("../src/policy-comment.js");
  const marker = `<!-- repo-automation-metadata-head:v2 {"headOid":"${HEAD}","valid":true} -->`;
  const metadata = [
    POLICY_COMMENT_MARKER,
    marker,
    "## PR metadata policy",
    "",
    "Head is current.",
    "",
  ].join("\n");
  const github = createFakeGitHub(state({
    comments: [{ id: 7, author: "github-actions[bot]", body: metadata }],
  }));

  await run(github);

  const body = github.metadataSnapshot().comments[0].body;
  assert.match(body, /repo-automation-state:v2/);
  assert.equal(body.split(marker).length - 1, 1);
  assert.equal(parseMetadataHeadEvidence(body), HEAD);
  assert.equal(body.split(POLICY_COMMENT_MARKER).length - 1, 1);
});

test("preserves legacy metadata bytes during a hold without trusting legacy evidence", async (t) => {
  const { parseMetadataHeadEvidence } = require("../src/policy-comment.js");
  const { parsePolicyState } = require("../src/commands/state.js");
  for (const validationStatus of ["PASS", "FAIL"]) {
    await t.test(validationStatus, async () => {
      const marker = `<!-- repo-automation-metadata-head:v1 {"headOid":"${HEAD}"} -->`;
      const metadata = `${POLICY_COMMENT_MARKER}\n${marker}\n- Title: **${validationStatus}**\n`;
      const initial = state({ comments: [{ id: 7, author: "github-actions[bot]", body: metadata }] });
      initial.issueComments[0].body = "/hold";
      const github = createFakeGitHub(initial);

      await run(github);

      const body = github.metadataSnapshot().comments[0].body;
      assert.equal(body.split(marker).length - 1, 1);
      assert.equal(parseMetadataHeadEvidence(body), null);
      assert.equal(parsePolicyState(body).hold.actor, "alice");
      assert.equal(body.split(POLICY_COMMENT_MARKER).length - 1, 1);
    });
  }
});

test("duplicate delivery is a strict no-op", async () => {
  const github = createFakeGitHub(state());
  await run(github);
  const firstWrites = {
    comments: github.calls.upsertPolicyComment.length,
    adds: github.calls.addPolicyLabel.length,
    removes: github.calls.removePolicyLabel.length,
  };

  const result = await run(github);

  assert.equal(result.status, "duplicate");
  assert.equal(github.calls.upsertPolicyComment.length, firstWrites.comments);
  assert.equal(github.calls.addPolicyLabel.length, firstWrites.adds);
  assert.equal(github.calls.removePolicyLabel.length, firstWrites.removes);
});

test("plans only allowlisted failed workflow reruns and re-reads each run", async () => {
  const github = createFakeGitHub(state({
    issueComments: [{
      id: 99,
      issueNumber: 42,
      body: "/retest",
      author: "pr-author",
      authorType: "User",
      edited: false,
    }],
    workflowRuns: [{
      id: 501,
      headOid: HEAD,
      status: "completed",
      conclusion: "failure",
      workflowPath: ".github/workflows/automation-ci.yml",
      workflowSourceRef: null,
      event: "pull_request",
      prNumber: 42,
      repository: "nvidia/k8s-test-infra",
    }],
  }));

  const result = await run(github);

  assert.equal(result.commands[0].code, "retest-planned");
  assert.deepEqual(github.calls.getWorkflowRun, [{ runId: 501, headOid: HEAD, prNumber: 42 }]);
  assert.deepEqual(github.calls.rerunFailedJobs, [{ runId: 501 }]);
});

test("an agent-owned /cherry-pick comment is ignored without any write", async () => {
  const stored = storedPolicyComment({ processedCommandIds: [97] });
  const github = createFakeGitHub(state({
    issueComments: [{
      id: 99,
      issueNumber: 42,
      body: "/cherry-pick release-1.2",
      author: "pr-author",
      authorType: "User",
      edited: false,
    }],
    comments: [stored],
  }));

  const result = await run(github);

  assert.deepEqual(result, { status: "ignored", reason: "no-command" });
  assertNoWrites(github);
  assert.equal(github.callOrder.some(({ operation }) => /dispatch/i.test(operation)), false);
  assert.equal(github.metadataSnapshot().comments[0].body, stored.body);
});

test("agent-owned Prow command comments are ignored without any write", async (t) => {
  for (const body of [
    "/assign @octocat", "/unassign", "/cc @octocat", "/uncc @octocat", "/close", "/reopen", "/retitle fix: a new title",
  ]) await t.test(body, async () => {
    const stored = storedPolicyComment({ processedCommandIds: [97] });
    const github = createFakeGitHub(state({
      issueComments: [{ id: 99, issueNumber: 42, body, author: "pr-author", authorType: "User", edited: false }],
      comments: [stored],
    }));

    const result = await run(github);

    assert.deepEqual(result, { status: "ignored", reason: "no-command" });
    assertNoWrites(github);
    assert.equal(github.metadataSnapshot().comments[0].body, stored.body);
  });
});

test("a /cherry-pick line mixed with /lgtm applies exactly the /lgtm command", async () => {
  const github = createFakeGitHub(state({
    issueComments: [{
      id: 99,
      issueNumber: 42,
      body: "/cherry-pick release-1.2\n/lgtm",
      author: "alice",
      authorType: "User",
      edited: false,
    }],
  }));

  const result = await run(github);

  assert.equal(result.status, "complete");
  assert.deepEqual(result.commands, [{ line: 2, name: "lgtm", status: "applied", code: "lgtm-recorded" }]);
  assert.deepEqual(result.diagnostics, []);
  assert.doesNotMatch(github.metadataSnapshot().comments[0].body, /cherry-pick/);
});

test("/backport is rejected exactly like any unsupported command", async () => {
  const results = [];
  for (const body of ["/backport release-1.2", "/foo release-1.2"]) {
    const github = createFakeGitHub(state({
      issueComments: [{
        id: 99,
        issueNumber: 42,
        body,
        author: "pr-author",
        authorType: "User",
        edited: false,
      }],
    }));
    const result = await run(github);
    assert.deepEqual(result.commands, [], body);
    assert.deepEqual(result.diagnostics, [
      { line: 1, name: "diagnostic", status: "rejected", code: "unsupported-command" },
    ], body);
    results.push({ result, policyComment: github.metadataSnapshot().comments[0].body });
  }
  assert.deepEqual(results[0], results[1]);
});

test("edited or non-human source comments fail closed before mutation", async (t) => {
  for (const [name, override] of [
    ["edited", { edited: true, authorType: "User" }],
    ["bot", { edited: false, authorType: "Bot" }],
  ]) {
    await t.test(name, async () => {
      const github = createFakeGitHub(state({
        issueComments: [{
          id: 99,
          issueNumber: 42,
          body: "/lgtm",
          author: "alice",
          ...override,
        }],
      }));
      await assert.rejects(() => run(github), /comment/i);
      assert.equal(github.calls.upsertPolicyComment.length, 0);
      assert.equal(github.calls.addPolicyLabel.length, 0);
    });
  }
});

test("dry-run returns a complete plan and performs no mutations", async () => {
  const github = createFakeGitHub(state());
  const result = await run(github, true);

  assert.equal(result.status, "planned");
  assert.equal(github.calls.upsertPolicyComment.length, 0);
  assert.equal(github.calls.addPolicyLabel.length, 0);
  assert.equal(github.calls.removePolicyLabel.length, 0);
  assert.equal(github.calls.rerunFailedJobs.length, 0);
});

// Created an hour before the fixed run time, inside the 24-hour catch-up window.
function command(id, body, overrides = {}) {
  return {
    id,
    issueNumber: 42,
    body,
    author: "alice",
    authorType: "User",
    edited: false,
    createdAt: "2026-09-17T11:00:00Z",
    ...overrides,
  };
}

async function runEvent(github, commentId) {
  const { runCommand } = require("../src/modes/command.js");
  return runCommand({
    event: { ...event, comment: { ...event.comment, id: commentId } },
    github,
    config: loadConfig(repositoryRoot),
    dryRun: false,
    now: () => "2026-09-17T12:00:00.000Z",
  });
}

function persistedState(github) {
  const { parsePolicyState } = require("../src/commands/state.js");
  return parsePolicyState(github.metadataSnapshot().comments[0].body);
}

function storedPolicyComment({ processedCommandIds, hold = null }) {
  const { createEmptyState, serializePolicyState } = require("../src/commands/state.js");
  const stored = createEmptyState({
    repository: "nvidia/k8s-test-infra",
    pullRequest: 42,
    policyDigest: "a".repeat(64),
    headOid: HEAD,
  });
  stored.hold = hold;
  stored.processedCommandIds = processedCommandIds;
  return { id: 7, author: "github-actions[bot]", body: `${POLICY_COMMENT_MARKER}\n${serializePolicyState(stored)}\n` };
}

test("a command run applies an evicted older command comment before its own", async () => {
  const github = createFakeGitHub(state({
    issueComments: [command(98, "/hold"), command(99, "/lgtm")],
  }));

  const result = await run(github);

  assert.equal(result.status, "complete");
  assert.deepEqual(result.processedCommentIds, [98, 99]);
  assert.deepEqual(result.policy, { lgtm: true, approved: false, hold: true, needsApproval: true });
  assert.deepEqual(github.calls.addPolicyLabel.map(({ label }) => label),
    ["lgtm", "do-not-merge/hold", "do-not-merge/needs-approval"]);
  const persisted = persistedState(github);
  assert.deepEqual(persisted.processedCommandIds, [98, 99]);
  assert.equal(persisted.hold.sourceId, 98);
});

test("caught-up commands apply in comment id order", async (t) => {
  // An /unhold with no hold changes nothing, so the caught-up comment is not recorded.
  for (const [name, older, newer, held, processed] of [
    ["hold then unhold ends unheld", "/hold", "/unhold", false, [98, 99]],
    ["unhold then hold ends held", "/unhold", "/hold", true, [99]],
  ]) await t.test(name, async () => {
    const github = createFakeGitHub(state({
      issueComments: [command(98, older), command(99, newer)],
    }));

    const result = await run(github);

    assert.deepEqual(result.processedCommentIds, processed);
    assert.equal(result.policy.hold, held);
    assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "do-not-merge/hold"), held);
    assert.equal(persistedState(github).hold?.sourceId ?? null, held ? 99 : null);
  });
});

test("several caught-up comments apply in id order even when listed out of order", async (t) => {
  for (const [name, first, second, held, processed] of [
    ["hold then unhold ends unheld", "/hold", "/unhold", false, [97, 98, 99]],
    ["unhold then hold ends held", "/unhold", "/hold", true, [98, 99]],
  ]) await t.test(name, async () => {
    const github = createFakeGitHub(state({
      issueComments: [command(97, first), command(98, second), command(99, "/lgtm")],
    }));
    const listComments = github.listIssueComments.bind(github);
    github.listIssueComments = async (...args) => (await listComments(...args)).reverse();

    const result = await run(github);

    assert.deepEqual(result.processedCommentIds, processed);
    assert.equal(result.policy.hold, held);
    assert.equal(persistedState(github).hold?.sourceId ?? null, held ? 98 : null);
  });
});

test("command catch-up replay is idempotent", async () => {
  const github = createFakeGitHub(state({
    issueComments: [command(98, "/hold"), command(99, "/unhold")],
  }));
  await run(github);
  const writes = github.callOrder.length;

  const replay = await run(github);

  assert.equal(replay.status, "duplicate");
  assert.equal(github.callOrder.slice(writes).some(({ operation }) => (
    ["addPolicyLabel", "removePolicyLabel", "upsertPolicyComment", "rerunFailedJobs"].includes(operation)
  )), false);
  assert.deepEqual(persistedState(github).processedCommandIds, [98, 99]);
});

test("a caught-up comment is authorized as its own live author", async () => {
  const github = createFakeGitHub(state({
    issueComments: [command(98, "/hold", { author: "mallory" }), command(99, "/lgtm")],
  }));

  const result = await run(github);

  assert.deepEqual(result.processedCommentIds, [99]);
  assert.equal(result.policy.hold, false);
  assert.equal(persistedState(github).hold, null);
  assert.deepEqual(github.calls.getCollaboratorAccess.map(({ login }) => login).sort(), ["alice", "mallory"]);
});

test("catch-up skips edited and non-human comments without recording them", async (t) => {
  for (const [name, override] of [
    ["edited", { edited: true }],
    ["bot", { authorType: "Bot", author: "helper" }],
  ]) await t.test(name, async () => {
    const github = createFakeGitHub(state({
      issueComments: [command(98, "/hold", override), command(99, "/lgtm")],
    }));

    const result = await run(github);

    assert.deepEqual(result.processedCommentIds, [99]);
    assert.equal(result.policy.hold, false);
    assert.deepEqual(persistedState(github).processedCommandIds, [99]);
  });
});

test("caught-up /lgtm and /approve are recorded as processed but grant no evidence", async () => {
  const initial = state({
    issueComments: [command(97, "/approve", { author: "bob" }), command(98, "/lgtm"), command(99, "/hold")],
  });
  const github = createFakeGitHub(initial);

  const result = await run(github);

  assert.deepEqual(result.processedCommentIds, [97, 98, 99]);
  assert.deepEqual(result.policy, { lgtm: false, approved: false, hold: true, needsApproval: true });
  const persisted = persistedState(github);
  assert.deepEqual(persisted.lgtms, []);
  assert.deepEqual(persisted.approvals, []);
  assert.deepEqual(persisted.processedCommandIds, [97, 98, 99]);
  assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "lgtm" || label === "approved"), false);
});

test("comments newer than the event comment are left to their own run", async () => {
  const github = createFakeGitHub(state({
    issueComments: [command(99, "/hold"), command(100, "/lgtm")],
  }));

  const first = await run(github);
  assert.deepEqual(first.processedCommentIds, [99]);
  assert.equal(first.policy.lgtm, false);

  const second = await runEvent(github, 100);
  assert.deepEqual(second.processedCommentIds, [100]);
  assert.deepEqual(second.policy, { lgtm: true, approved: false, hold: true, needsApproval: true });
  assert.deepEqual(persistedState(github).processedCommandIds, [99, 100]);
});

test("unprocessed comments older than a processed command are never replayed", async () => {
  const github = createFakeGitHub(state({
    issueComments: [command(96, "/unhold"), command(97, "/hold"), command(99, "/lgtm")],
    comments: [storedPolicyComment({
      processedCommandIds: [97],
      hold: {
        repository: "nvidia/k8s-test-infra",
        pullRequest: 42,
        actor: "alice",
        actorRole: "owner",
        sourceType: "comment",
        sourceId: 97,
        createdAt: "2026-09-17T11:00:00.000Z",
      },
    })],
  }));

  const result = await run(github);

  assert.deepEqual(result.processedCommentIds, [99]);
  assert.equal(result.policy.hold, true);
  assert.deepEqual(persistedState(github).processedCommandIds, [97, 99]);
});

test("a comment without commands still catches up an evicted command", async () => {
  const github = createFakeGitHub(state({
    issueComments: [command(98, "/hold"), command(99, "see https://example.com/a/b")],
  }));

  const result = await run(github);

  assert.deepEqual(result.processedCommentIds, [98]);
  assert.equal(result.policy.hold, true);
  assert.deepEqual(persistedState(github).processedCommandIds, [98]);
});

test("a comment without commands and nothing to catch up is ignored before pull request reads", async () => {
  const github = createFakeGitHub(state({
    issueComments: [command(99, "see https://example.com/a/b")],
  }));

  const result = await run(github);

  assert.deepEqual(result, { status: "ignored", reason: "no-command" });
  assert.deepEqual(github.calls.getPullRequest, []);
  assertNoWrites(github);
});

test("a caught-up comment edited after planning stops all command writes", async () => {
  const github = createFakeGitHub(state({
    issueComments: [command(98, "/hold"), command(99, "/lgtm")],
  }));
  const listComments = github.listIssueComments.bind(github);
  github.listIssueComments = async (...args) => (await listComments(...args)).map((comment) => (
    comment.id === 98 && github.calls.listIssueComments.length > 1 ? { ...comment, edited: true } : comment
  ));

  await assert.rejects(() => run(github), /changed after planning/);
  assertNoWrites(github);
});

test("a caught-up /lgtm rejection names its comment in the policy comment", async () => {
  const github = createFakeGitHub(state({
    issueComments: [command(97, "/approve\n/lgtm", { author: "bob" }), command(98, "/lgtm"), command(99, "/hold")],
  }));

  const result = await run(github);

  assert.deepEqual(result.rejectedBacklogEvidence, [
    { commentId: 97, commands: ["approve", "lgtm"], status: "rejected", code: "stale-backlog-evidence" },
    { commentId: 98, commands: ["lgtm"], status: "rejected", code: "stale-backlog-evidence" },
  ]);
  const lines = github.metadataSnapshot().comments[0].body.split("\n");
  assert.ok(lines.includes("- Comment 97: <code>/approve</code> and <code>/lgtm</code> rejected "
    + "(<code>stale-backlog-evidence</code>); its own run was skipped, so re-issue them on the current head."));
  assert.ok(lines.includes("- Comment 98: <code>/lgtm</code> rejected (<code>stale-backlog-evidence</code>); "
    + "its own run was skipped, so re-issue it on the current head."));
});

function failedRun() {
  return {
    id: 501,
    headOid: HEAD,
    status: "completed",
    conclusion: "failure",
    workflowPath: ".github/workflows/automation-ci.yml",
    workflowSourceRef: null,
    event: "pull_request",
    prNumber: 42,
    repository: "nvidia/k8s-test-infra",
  };
}

test("several caught-up /retest comments rerun failed jobs at most once", async () => {
  const github = createFakeGitHub(state({
    issueComments: [
      command(97, "/retest", { author: "pr-author" }),
      command(98, "/retest", { author: "pr-author" }),
      command(99, "/retest", { author: "pr-author" }),
    ],
    workflowRuns: [failedRun()],
  }));

  const result = await run(github);

  assert.deepEqual(result.processedCommentIds, [97, 99]);
  assert.deepEqual(github.calls.rerunFailedJobs, [{ runId: 501 }]);
  assert.deepEqual(result.commands, [{ line: 1, name: "retest", status: "noop", code: "cooldown" }]);
  assert.equal(persistedState(github).lastRetest.commentId, 97);
});

test("a caught-up /retest inside the stored cooldown reruns nothing", async () => {
  const { createEmptyState, serializePolicyState } = require("../src/commands/state.js");
  const stored = createEmptyState({
    repository: "nvidia/k8s-test-infra",
    pullRequest: 42,
    policyDigest: "a".repeat(64),
    headOid: HEAD,
  });
  stored.processedCommandIds = [90];
  stored.lastRetest = { commentId: 90, headOid: HEAD, createdAt: "2026-09-17T11:55:00.000Z" };
  const github = createFakeGitHub(state({
    issueComments: [command(98, "/retest", { author: "pr-author" }), command(99, "/hold")],
    workflowRuns: [failedRun()],
    comments: [{ id: 7, author: "github-actions[bot]", body: `${POLICY_COMMENT_MARKER}\n${serializePolicyState(stored)}\n` }],
  }));

  const result = await run(github);

  assert.deepEqual(result.processedCommentIds, [99]);
  assert.deepEqual(github.calls.rerunFailedJobs, []);
  assert.deepEqual(result.rerunRunIds, []);
  assert.equal(persistedState(github).lastRetest.commentId, 90);
});

test("a first run replays only command comments from the last 24 hours, in id order", async () => {
  const github = createFakeGitHub(state({
    issueComments: [
      command(95, "/hold", { createdAt: "2026-09-15T11:00:00Z" }),
      command(96, "/retest", { author: "pr-author", createdAt: "2026-09-16T08:00:00Z" }),
      command(97, "/unhold", { createdAt: "2026-09-17T09:00:00Z" }),
      command(98, "/hold", { createdAt: "2026-09-17T10:00:00Z" }),
      command(99, "/lgtm"),
    ],
    workflowRuns: [failedRun()],
  }));

  const result = await run(github);

  assert.deepEqual(result.processedCommentIds, [98, 99]);
  assert.equal(result.policy.hold, true);
  assert.deepEqual(github.calls.rerunFailedJobs, []);
  const persisted = persistedState(github);
  assert.deepEqual(persisted.processedCommandIds, [98, 99]);
  assert.equal(persisted.hold.sourceId, 98);
  assert.equal(persisted.lastRetest, null);
});

test("the catch-up window is 24 hours of comment creation time", async (t) => {
  for (const [name, createdAt, replayed] of [
    ["exactly 24 hours old", "2026-09-16T12:00:00Z", true],
    ["one second older", "2026-09-16T11:59:59Z", false],
    ["missing creation time", null, false],
    ["unparseable creation time", "yesterday", false],
  ]) await t.test(name, async () => {
    const github = createFakeGitHub(state({
      issueComments: [command(98, "/hold", { createdAt }), command(99, "/lgtm")],
    }));

    const result = await run(github);

    assert.deepEqual(result.processedCommentIds, replayed ? [98, 99] : [99]);
    assert.equal(result.policy.hold, replayed);
  });
});

test("an old comment is not replayed even when it is newer than the newest processed command", async () => {
  const github = createFakeGitHub(state({
    issueComments: [command(98, "/hold", { createdAt: "2026-09-10T12:00:00Z" }), command(99, "/lgtm")],
    comments: [storedPolicyComment({ processedCommandIds: [97] })],
  }));

  const result = await run(github);

  assert.deepEqual(result.processedCommentIds, [99]);
  assert.equal(result.policy.hold, false);
  assert.deepEqual(persistedState(github).processedCommandIds, [97, 99]);
});

test("caught-up comments that change nothing cannot fill the command history", async () => {
  const config = loadConfig(repositoryRoot);
  config.policy.commands.historyLimit = 3;
  const github = createFakeGitHub(state({
    issueComments: [
      command(95, "/foo", { author: "stranger" }),
      command(96, "/foo", { author: "stranger" }),
      command(97, "/foo", { author: "stranger" }),
      command(98, "/foo", { author: "stranger" }),
      command(99, "/hold"),
    ],
  }));

  const result = await run(github, false, config);

  assert.equal(result.status, "complete");
  assert.deepEqual(result.processedCommentIds, [99]);
  assert.equal(result.policy.hold, true);
  assert.deepEqual(persistedState(github).processedCommandIds, [99]);
});

test("a comment without commands whose catch-up changes nothing writes nothing", async () => {
  const github = createFakeGitHub(state({
    issueComments: [command(98, "/foo", { author: "stranger" }), command(99, "see https://example.com/a/b")],
  }));

  const result = await run(github);

  assert.deepEqual(result, { status: "ignored", reason: "no-command" });
  assertNoWrites(github);
});

test("caught-up /backport and /cherry-pick comments are not replayed", async () => {
  const github = createFakeGitHub(state({
    issueComments: [
      command(97, "/cherry-pick release-0.11", { author: "pr-author" }),
      command(98, "/backport release-0.13\n/hold"),
      command(99, "/backport release-0.12", { author: "pr-author" }),
    ],
  }));

  const result = await run(github);

  assert.deepEqual(result.processedCommentIds, [98, 99]);
  assert.equal(result.policy.hold, true);
  assert.deepEqual(result.commands, []);
  assert.deepEqual(result.diagnostics, [
    { line: 1, name: "diagnostic", status: "rejected", code: "unsupported-command" },
  ]);
});

test("caught-up /lgtm or /approve from someone not allowed to give it cannot fill the history", async (t) => {
  for (const evidenceCommand of ["/lgtm", "/approve"]) await t.test(evidenceCommand, async () => {
    const config = loadConfig(repositoryRoot);
    config.policy.commands.historyLimit = 3;
    const github = createFakeGitHub(state({
      issueComments: [
        command(95, evidenceCommand, { author: "stranger" }),
        command(96, evidenceCommand, { author: "stranger" }),
        command(97, evidenceCommand, { author: "stranger" }),
        command(98, evidenceCommand, { author: "stranger" }),
        command(99, "/hold"),
      ],
    }));

    const result = await run(github, false, config);

    assert.equal(result.status, "complete");
    assert.deepEqual(result.processedCommentIds, [99]);
    assert.deepEqual(result.rejectedBacklogEvidence, []);
    assert.equal(result.policy.hold, true);
    assert.equal(github.metadataSnapshot().comments[0].body.includes("stale-backlog-evidence"), false);
  });
});

test("only a caught-up /lgtm its author may give is rejected as stale and shown", async () => {
  const github = createFakeGitHub(state({
    issueComments: [
      command(96, "/lgtm", { author: "pr-author" }),
      command(97, "/lgtm", { author: "stranger" }),
      command(98, "/lgtm"),
      command(99, "/hold"),
    ],
  }));

  const result = await run(github);

  assert.deepEqual(result.processedCommentIds, [98, 99]);
  assert.deepEqual(result.rejectedBacklogEvidence, [
    { commentId: 98, commands: ["lgtm"], status: "rejected", code: "stale-backlog-evidence" },
  ]);
  const lines = github.metadataSnapshot().comments[0].body.split("\n")
    .filter((line) => line.includes("stale-backlog-evidence"));
  assert.deepEqual(lines, ["- Comment 98: <code>/lgtm</code> rejected (<code>stale-backlog-evidence</code>); "
    + "its own run was skipped, so re-issue it on the current head."]);
});

test("a caught-up self-/lgtm by an approver author is neither recorded nor shown", async () => {
  const initial = state({
    issueComments: [command(98, "/lgtm", { author: "pr-author" }), command(99, "/hold")],
  });
  initial.contents["/OWNERS"] = "reviewers: [alice]\napprovers: [bob, pr-author]\n";
  const github = createFakeGitHub(initial);

  const result = await run(github);

  assert.deepEqual(result.processedCommentIds, [99]);
  assert.deepEqual(result.rejectedBacklogEvidence, []);
  assert.equal(github.metadataSnapshot().comments[0].body.includes("stale-backlog-evidence"), false);
});
