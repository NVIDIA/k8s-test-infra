"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");

const { loadConfig } = require("../src/config.js");
const { POLICY_COMMENT_MARKER } = require("../src/policy-comment.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const HEAD = "6".repeat(40);
const REVISION = "7".repeat(40);
const event = {
  action: "created",
  repository: {
    owner: { login: "NVIDIA" },
    name: "k8s-test-infra",
    full_name: "NVIDIA/k8s-test-infra",
  },
  issue: { number: 42, pull_request: { url: "untrusted-event-url" } },
  comment: { id: 9001, body: "/approve untrusted-event-body" },
};

function state(overrides = {}) {
  return {
    pullRequest: {
      number: 42,
      nodeId: "PR_kwDOABC42",
      title: "feat: command policy",
      body: "untrusted-pr-body",
      draft: false,
      author: "pr-author",
      headOid: HEAD,
      state: "open",
      baseBranch: "main",
      baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    },
    files: [{ path: "pkg/agent.go", additions: 1, deletions: 0, status: "modified" }],
    labels: [],
    issueComments: [{
      id: 9001,
      issueNumber: 42,
      body: "/lgtm",
      author: "alice",
      authorType: "User",
      edited: false,
    }],
    users: {
      alice: { login: "alice", type: "User", resolved: true, deleted: false },
    },
    collaboratorAccess: {
      alice: { liveCollaborator: false, permission: "read" },
    },
    defaultBranchRevision: REVISION,
    contents: {
      "/OWNERS": "reviewers: [alice]\napprovers: [bob]\n",
      "/OWNERS_ALIASES": "aliases: {}\n",
    },
    comments: [],
    ...overrides,
  };
}

async function run(github, options = {}) {
  const { runCommand } = require("../src/modes/command.js");
  return runCommand({
    event: options.event ?? event,
    github,
    config: options.config ?? loadConfig(repositoryRoot),
    dryRun: options.dryRun ?? false,
    now: () => "2026-09-17T12:00:00.000Z",
  });
}

function writes(github) {
  return github.callOrder.filter(({ operation }) => [
    "upsertPolicyComment",
    "addPolicyLabel",
    "removePolicyLabel",
    "rerunFailedJobs",
  ].includes(operation));
}

test("writes durable command state after policy mutations", async () => {
  const github = createFakeGitHub(state());

  const result = await run(github);

  assert.equal(result.status, "complete");
  assert.deepEqual(writes(github).map(({ operation }) => operation), [
    "addPolicyLabel",
    "addPolicyLabel",
    "upsertPolicyComment",
  ]);
  assert.equal(JSON.stringify(result).includes("untrusted-event-body"), false);
  assert.equal(JSON.stringify(result).includes("untrusted-pr-body"), false);
});

test("a failed mutation leaves the command retryable", async () => {
  const github = createFakeGitHub(state({
    failures: { addPolicyLabel: new Error("transient label failure") },
  }));

  await assert.rejects(() => run(github), /transient label failure/);
  assert.equal(github.calls.upsertPolicyComment.length, 0);

  const result = await run(github);
  assert.equal(result.status, "complete");
  assert.equal(github.calls.upsertPolicyComment.length, 1);
});

test("rejects a source comment change after planning without writes", async () => {
  const github = createFakeGitHub(state());
  const getIssueComment = github.getIssueComment.bind(github);
  let reads = 0;
  github.getIssueComment = async (commentId) => {
    const value = await getIssueComment(commentId);
    reads += 1;
    return reads === 1 ? value : { ...value, body: "/hold" };
  };

  await assert.rejects(() => run(github), /changed after planning/);
  assert.deepEqual(writes(github), []);
});

test("rejects a head change after planning without writes", async () => {
  const github = createFakeGitHub(state({
    pullRequests: [
      state().pullRequest,
      { ...state().pullRequest, headOid: "8".repeat(40) },
    ],
  }));

  await assert.rejects(() => run(github), /changed after planning/);
  assert.deepEqual(writes(github), []);
});

test("rejects malformed or duplicate bot-owned state without writes", async (t) => {
  for (const [name, body] of [
    ["malformed", `${POLICY_COMMENT_MARKER}\n<!-- repo-automation-state:v2 {} -->\n`],
    ["duplicate", `${POLICY_COMMENT_MARKER}\n<!-- repo-automation-state:v2 {} -->\n<!-- repo-automation-state:v2 {} -->\n`],
  ]) {
    await t.test(name, async () => {
      const github = createFakeGitHub(state({
        comments: [{ id: 7, author: "github-actions[bot]", body }],
      }));
      await assert.rejects(() => run(github), /command state/);
      assert.deepEqual(writes(github), []);
    });
  }
});

test("fails closed when changed paths have no current owner authority", async () => {
  const github = createFakeGitHub(state({
    contents: {
      "/OWNERS": "reviewers: []\napprovers: []\n",
      "/OWNERS_ALIASES": "aliases: {}\n",
    },
  }));

  await assert.rejects(() => run(github), /authority is unavailable/);
  assert.deepEqual(writes(github), []);
});

test("ignores an event without a pull-request mapping before live API reads", async () => {
  const github = createFakeGitHub(state());
  const result = await run(github, {
    event: { ...event, issue: { number: 42 } },
  });

  assert.deepEqual(result, { status: "ignored", reason: "not-pull-request" });
  assert.equal(github.callOrder.length, 0);
});
