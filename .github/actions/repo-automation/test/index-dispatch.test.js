"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");

const { createFakeGitHub } = require("./helpers/fake-github.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const repository = {
  name: "k8s-test-infra",
  full_name: "NVIDIA/k8s-test-infra",
  owner: { login: "NVIDIA" },
};

function coreFor(inputs) {
  const outputs = [];
  return {
    outputs,
    getInput(name) {
      return inputs[name] ?? "";
    },
    getBooleanInput(name) {
      assert.equal(name, "dry-run");
      return true;
    },
    setOutput(name, value) {
      outputs.push({ name, value });
    },
  };
}

test("index dispatches command mode without treating event text as authority", async () => {
  const { run } = require("../src/index.js");
  const core = coreFor({ mode: "command" });
  const githubClient = createFakeGitHub();
  const result = await run({
    core,
    workspace: repositoryRoot,
    githubClient,
    eventName: "issue_comment",
    event: {
      action: "created",
      repository,
      issue: { number: 42 },
      comment: { id: 9001, body: "/approve", user: { login: "attacker" } },
    },
  });

  assert.deepEqual(result, { status: "ignored", reason: "not-pull-request" });
  assert.deepEqual(githubClient.calls.getIssueComment, []);
  assert.deepEqual(core.outputs, [{ name: "summary", value: JSON.stringify(result) }]);
});

test("index passes the event name and explicit PR input to merge evaluation", async () => {
  const { run } = require("../src/index.js");
  const core = coreFor({ mode: "merge-evaluate" });
  const githubClient = createFakeGitHub({ openPullRequestNumbers: [] });
  const result = await run({
    core,
    workspace: repositoryRoot,
    githubClient,
    eventName: "workflow_dispatch",
    event: { repository },
  });

  assert.deepEqual(result, { status: "planned", candidates: [], pullRequests: [] });
  assert.deepEqual(githubClient.calls.listOpenPullRequestNumbers, [{}]);
  assert.deepEqual(core.outputs, [{ name: "summary", value: JSON.stringify(result) }]);
});

test("index passes explicit pull request and target inputs to backport mode", async () => {
  const { run } = require("../src/index.js");
  const core = coreFor({
    mode: "backport",
    "pr-number": "42",
    "target-branch": "release-1.2",
  });
  const mergeOid = "2".repeat(40);
  const githubClient = createFakeGitHub({
    pullRequests: [{
      number: 42,
      nodeId: "PR_node_42",
      title: "feat: add gpu probe",
      body: "",
      draft: false,
      author: "orig-author",
      headOid: "7".repeat(40),
      state: "closed",
      merged: true,
      mergeCommitOid: mergeOid,
      baseBranch: "main",
      baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    }],
    branches: { "release-1.2": "1".repeat(40) },
  });
  const result = await run({
    core,
    workspace: repositoryRoot,
    githubClient,
    owner: "NVIDIA",
    repo: "k8s-test-infra",
  });

  assert.deepEqual(result, {
    status: "planned",
    outcome: "create",
    sourcePullRequest: 42,
    sourceCommit: mergeOid,
    targetBranch: "release-1.2",
    backportBranch: "backport/42-to-release-1.2-61744f7f6745",
  });
  assert.deepEqual(githubClient.calls.getPullRequest, [{ prNumber: 42 }]);
  assert.deepEqual(core.outputs, [{ name: "summary", value: JSON.stringify(result) }]);
});

test("index accepts only the approved v0.11 mode set", async () => {
  const { run } = require("../src/index.js");
  const core = coreFor({ mode: "release" });
  await assert.rejects(
    () => run({ core, workspace: repositoryRoot, githubClient: createFakeGitHub() }),
    /Unsupported mode: release/,
  );
});
