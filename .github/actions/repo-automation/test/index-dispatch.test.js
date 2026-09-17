"use strict";

const assert = require("node:assert/strict");
const { Buffer } = require("node:buffer");
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
      return inputs["dry-run"] === "false" ? false : true;
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
  assert.deepEqual(core.outputs, [
    { name: "backport-requests", value: "[]" },
    { name: "summary", value: JSON.stringify(result) },
  ]);
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
    "working-directory": "target",
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
  const gitCalls = [];
  const result = await run({
    core,
    workspace: repositoryRoot,
    githubClient,
    owner: "NVIDIA",
    repo: "k8s-test-infra",
    git: async (args, options) => {
      gitCalls.push({ args, options });
      return { stdout: "", stderr: "" };
    },
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
  assert.equal(gitCalls.length, 0, "dry-run must not invoke Git");
});

test("index dispatches generic backport Git only in the fixed target checkout", async () => {
  const { run } = require("../src/index.js");
  const core = coreFor({
    mode: "backport",
    "pr-number": "42",
    "target-branch": "release-1.2",
    "working-directory": "target",
    "dry-run": "false",
  });
  const mergeOid = "2".repeat(40);
  const targetOid = "1".repeat(40);
  const producedOid = "4".repeat(40);
  const githubClient = createFakeGitHub({
    pullRequest: {
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
    },
    branches: { "release-1.2": targetOid },
  });
  const gitCalls = [];
  const git = async (args, options) => {
    gitCalls.push({ args: [...args], options: { ...options } });
    if (args[0] === "rev-list") {
      return { stdout: `${mergeOid} ${"3".repeat(40)}\n`, stderr: "" };
    }
    if (args[0] === "rev-parse") {
      return { stdout: `${producedOid}\n`, stderr: "" };
    }
    if (args[0] === "push") githubClient.setBranch("backport/42-to-release-1.2-61744f7f6745", producedOid);
    return { stdout: "", stderr: "" };
  };

  await run({
    core,
    workspace: repositoryRoot,
    githubClient,
    git,
    owner: "NVIDIA",
    repo: "k8s-test-infra",
  });

  assert.ok(gitCalls.length > 0);
  assert.equal(gitCalls.every(({ options }) => options.cwd === path.join(repositoryRoot, "target")), true);
});

test("index publishes bounded backport requests for the workflow matrix", async () => {
  const { run } = require("../src/index.js");
  const core = coreFor({ mode: "command", "dry-run": "true" });
  const githubClient = createFakeGitHub({
    pullRequest: {
      number: 42,
      nodeId: "PR_42",
      title: "feat: request backport",
      body: "",
      draft: false,
      author: "author",
      headOid: "1".repeat(40),
      state: "open",
      baseBranch: "main",
      baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    },
    files: [{ path: "pkg/gpu.go", additions: 1, deletions: 0, status: "modified" }],
    issueComments: [{
      id: 99,
      issueNumber: 42,
      body: "/backport release-0.11",
      author: "author",
      authorType: "User",
      edited: false,
    }],
    contents: {
      "/OWNERS": "reviewers: [alice]\napprovers: [bob]\n",
      "/OWNERS_ALIASES": "aliases: {}\n",
    },
    defaultBranchRevision: "2".repeat(40),
  });
  const event = {
    action: "created",
    repository,
    issue: { number: 42, pull_request: {} },
    comment: { id: 99 },
  };

  await run({ core, workspace: repositoryRoot, githubClient, event });

  const output = core.outputs.find(({ name }) => name === "backport-requests");
  assert.deepEqual(JSON.parse(output.value), [{ prNumber: 42, targetBranch: "release-0.11" }]);
  assert.ok(Buffer.byteLength(output.value, "utf8") < 4096);
});

test("index accepts only the approved v0.11 mode set", async () => {
  const { run } = require("../src/index.js");
  const core = coreFor({ mode: "release" });
  await assert.rejects(
    () => run({ core, workspace: repositoryRoot, githubClient: createFakeGitHub() }),
    /Unsupported mode: release/,
  );
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

test("index dispatches Mokka only to the fixed target checkout and identity", async () => {
  const { run } = require("../src/index.js");
  const actionId = "123e4567-e89b-42d3-a456-426614174000";
  const sourceSha = "2".repeat(40);
  const targetSha = "1".repeat(40);
  const producedSha = "4".repeat(40);
  const workflowSha = "5".repeat(40);
  const branch = `mokka/cherry-pick/${actionId}`;
  const core = coreFor({
    mode: "mokka-cherry-pick",
    pull_request_number: "42",
    source_sha: sourceSha,
    "target-branch": "main",
    action_id: actionId,
    "working-directory": "target",
    "dry-run": "false",
  });
  const githubClient = createFakeGitHub({
    pullRequest: {
      number: 42,
      state: "closed",
      merged: true,
      mergeCommitOid: sourceSha,
      baseBranch: "source-base",
      baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
      headRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    },
    commitsBySha: {
      [sourceSha]: { sha: sourceSha, parents: ["3".repeat(40)] },
    },
    branches: { main: targetSha },
  });
  const gitCalls = [];
  let revParseCalls = 0;
  const git = async (args, options) => {
    gitCalls.push({ args: [...args], options: { ...options } });
    if (args[0] === "rev-parse") {
      revParseCalls += 1;
      return { stdout: `${revParseCalls === 1 ? targetSha : producedSha}\n`, stderr: "" };
    }
    if (args[0] === "show") return { stdout: "feat: source\n", stderr: "" };
    if (args[0] === "push" && args.at(-1) === `HEAD:refs/heads/${branch}`) {
      githubClient.setBranch(branch, producedSha);
    }
    return { stdout: "", stderr: "" };
  };

  const result = await run({
    core,
    workspace: "/trusted/workspace",
    githubClient,
    git,
    owner: "NVIDIA",
    repo: "k8s-test-infra",
    repositoryId: "733665780",
    workflowSha,
  });

  assert.equal(result.outcome, "created");
  assert.equal(gitCalls.every(({ options }) => options.cwd === "/trusted/workspace/target"), true);
  assert.deepEqual(githubClient.calls.createMokkaPullRequest.length, 1);
  assert.deepEqual(githubClient.calls.updateMokkaPullRequestBody.length, 1);
});

test("index rejects every Mokka working directory except target", async () => {
  const { run } = require("../src/index.js");
  const core = coreFor({
    mode: "mokka-cherry-pick",
    "working-directory": "control",
    "dry-run": "false",
  });
  await assert.rejects(
    () => run({
      core,
      workspace: "/trusted/workspace",
      githubClient: createFakeGitHub(),
      owner: "NVIDIA",
      repo: "k8s-test-infra",
    }),
    /working directory/,
  );
});
