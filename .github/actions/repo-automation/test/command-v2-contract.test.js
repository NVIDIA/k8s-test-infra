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

async function run(github, dryRun = false) {
  const { runCommand } = require("../src/modes/command.js");
  return runCommand({
    event,
    github,
    config: loadConfig(repositoryRoot),
    dryRun,
    now: () => "2026-09-17T12:00:00.000Z",
  });
}

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

test("preserves metadata evidence in the single bot-owned policy comment", async () => {
  const metadata = [
    POLICY_COMMENT_MARKER,
    `<!-- repo-automation-metadata-head:v1 {"headOid":"${HEAD}"} -->`,
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
  assert.match(body, /repo-automation-metadata-head:v1/);
  assert.equal(body.split(POLICY_COMMENT_MARKER).length - 1, 1);
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

test("returns bounded backport requests without dispatching remote work", async () => {
  const github = createFakeGitHub(state({
    issueComments: [{
      id: 99,
      issueNumber: 42,
      body: "/backport release-0.11\n/cherry-pick release-0.12",
      author: "pr-author",
      authorType: "User",
      edited: false,
    }],
  }));

  const result = await run(github);

  assert.deepEqual(result.backportRequests, [
    { command: "backport", targetBranch: "release-0.11", sourceCommentId: 99 },
    { command: "cherry-pick", targetBranch: "release-0.12", sourceCommentId: 99 },
  ]);
  assert.equal(github.callOrder.some(({ operation }) => /dispatch/i.test(operation)), false);
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
