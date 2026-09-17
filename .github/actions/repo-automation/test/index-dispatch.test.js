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

test("index accepts only the approved v0.11 mode set", async () => {
  const { run } = require("../src/index.js");
  const core = coreFor({ mode: "release" });
  await assert.rejects(
    () => run({ core, workspace: repositoryRoot, githubClient: createFakeGitHub() }),
    /Unsupported mode: release/,
  );
});
