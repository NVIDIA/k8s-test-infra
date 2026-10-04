"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");
const fs = require("node:fs");
const YAML = require("yaml");

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
      return inputs["dry-run"] === "false" ? false : true;
    },
    setOutput(name, value) {
      outputs.push({ name, value });
    },
  };
}

test("dispatch report action inputs are optional and describe their exact identity", () => {
  const action = YAML.parse(fs.readFileSync(path.resolve(__dirname, "../action.yml"), "utf8"));
  for (const name of ["request-id", "workflow-commit-sha"]) {
    assert.equal(action.inputs[name].required, false);
    assert.match(action.inputs[name].description, /dispatch/i);
    assert.equal(action.inputs[name].default, undefined);
  }
});

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

test("index passes the trusted policy revision to real merge evaluation", async () => {
  const { run } = require("../src/index.js");
  const policyRevision = "2".repeat(40);
  const core = coreFor({ mode: "merge-evaluate", "policy-revision": policyRevision });
  const githubClient = createFakeGitHub({
    openPullRequestNumbers: [],
    defaultBranchRevision: policyRevision,
  });
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

for (const [name, revision] of [
  ["missing", undefined],
  ["empty", ""],
  ["short", "2".repeat(39)],
  ["nonhex", "g".repeat(40)],
  ["zero", "0".repeat(40)],
  ["uppercase", "A".repeat(40)],
  ["whitespace", ` ${"2".repeat(40)}`],
]) {
  test(`index rejects a ${name} merge policy revision before GitHub reads`, async () => {
    const { run } = require("../src/index.js");
    const core = coreFor({ mode: "merge-evaluate", "policy-revision": revision });
    const githubClient = createFakeGitHub({ openPullRequestNumbers: [] });

    await assert.rejects(run({
      core,
      workspace: repositoryRoot,
      githubClient,
      eventName: "workflow_dispatch",
      event: { repository },
    }), /policy revision/);

    assert.deepEqual(githubClient.calls.listOpenPullRequestNumbers, []);
    assert.deepEqual(githubClient.calls.getDefaultBranchRevision, []);
    assert.deepEqual(core.outputs, []);
  });
}

test("index accepts only the approved v0.11 mode set", async () => {
  const { run } = require("../src/index.js");
  for (const mode of ["release", "backport", "mokka-cherry-pick"]) {
    const core = coreFor({ mode });
    await assert.rejects(
      () => run({ core, workspace: repositoryRoot, githubClient: createFakeGitHub() }),
      { message: `Unsupported mode: ${mode}` },
    );
  }
});

test("index rejects a control checkout path that is not the fixed trusted directory", async () => {
  const { run } = require("../src/index.js");
  const core = coreFor({
    mode: "label-sync",
    "control-directory": "../attacker",
  });

  await assert.rejects(
    () => run({ core, workspace: repositoryRoot, githubClient: createFakeGitHub() }),
    /invalid trusted control directory/,
  );
});
