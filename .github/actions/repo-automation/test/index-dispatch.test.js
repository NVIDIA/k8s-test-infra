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
  for (const mode of ["release", "backport"]) {
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

test("index dispatches Mokka only to the fixed target checkout and identity", async () => {
  const { run } = require("../src/index.js");
  const actionId = "123e4567-e89b-42d3-a456-426614174000";
  const sourceSha = "2".repeat(40);
  const targetSha = "1".repeat(40);
  const producedSha = "4".repeat(40);
  const signedSha = "6".repeat(40);
  const treeSha = "7".repeat(40);
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
  githubClient.calls.createMokkaCommit = [];
  githubClient.calls.createMokkaRef = [];
  githubClient.createMokkaCommit = async (request) => {
    githubClient.calls.createMokkaCommit.push(request);
    assert.equal(request.tree, treeSha);
    assert.deepEqual(request.parents, [targetSha]);
    return {
      sha: signedSha,
      message: request.message.replace(/\n+$/u, ""),
      tree: treeSha,
      parents: [targetSha],
      verification: { verified: true, hasSignature: true },
    };
  };
  githubClient.createMokkaRef = async (name, sha) => {
    githubClient.calls.createMokkaRef.push({ name, sha });
    githubClient.setBranch(name, sha);
    return { name, oid: sha };
  };
  const gitCalls = [];
  let revParseCalls = 0;
  let showCalls = 0;
  const git = async (args, options) => {
    gitCalls.push({ args: [...args], options: { ...options } });
    if (args[0] === "rev-parse" && args[1] === "FETCH_HEAD") {
      return { stdout: `${targetSha}\n`, stderr: "" };
    }
    if (args[0] === "rev-parse" && args[1] === "HEAD^{tree}") {
      return { stdout: `${treeSha}\n`, stderr: "" };
    }
    if (args[0] === "rev-parse") {
      revParseCalls += 1;
      return { stdout: `${revParseCalls === 1 ? targetSha : producedSha}\n`, stderr: "" };
    }
    if (args[0] === "show") {
      showCalls += 1;
      return {
        stdout: showCalls === 1
          ? "feat: source\n"
          : `feat: source\n\nMokka-Source-SHA: ${sourceSha}\nMokka-Action-ID: ${actionId}\n`,
        stderr: "",
      };
    }
    if (args[0] === "push" && args.at(-1) === `HEAD:refs/heads/mokka/cherry-pick-upload/${actionId}`) {
      githubClient.setBranch(`mokka/cherry-pick-upload/${actionId}`, producedSha);
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
  assert.deepEqual(githubClient.calls.createMokkaCommit.length, 1);
  assert.deepEqual(githubClient.calls.createMokkaRef, [{ name: branch, sha: signedSha }]);
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
