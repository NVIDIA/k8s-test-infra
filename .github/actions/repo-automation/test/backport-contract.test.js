"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");

const { loadConfig } = require("../src/config.js");
const {
  BACKPORT_EVIDENCE_MARKER,
  backportBranchName,
  backportPullRequestBody,
  runBackport,
} = require("../src/modes/backport.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const REPOSITORY = "nvidia/k8s-test-infra";
const PR_NUMBER = 42;
const MERGE_OID = "2".repeat(40);
const MERGE_PARENT = "3".repeat(40);
const TARGET_OID = "1".repeat(40);
const BACKPORT_OID = "4".repeat(40);
const TARGET_BRANCH = "release-1.2";

function mergedPullRequest(overrides = {}) {
  return {
    number: PR_NUMBER,
    nodeId: "PR_node_42",
    title: "feat: add gpu probe",
    body: "",
    draft: false,
    author: "orig-author",
    headOid: "7".repeat(40),
    state: "closed",
    merged: true,
    mergeCommitOid: MERGE_OID,
    baseBranch: "main",
    baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    ...overrides,
  };
}

function state(overrides = {}) {
  return {
    pullRequests: overrides.pullRequests ?? [mergedPullRequest(), mergedPullRequest()],
    branches: overrides.branches ?? { [TARGET_BRANCH]: TARGET_OID },
    backportPullRequests: overrides.backportPullRequests ?? [],
  };
}

function gitRecorder(overrides = {}) {
  const calls = [];
  const git = async (args) => {
    calls.push([...args]);
    if (args[0] === "push" && overrides.onPush !== undefined) {
      await overrides.onPush(args);
    }
    const command = args.join(" ");
    if (command === `rev-list --parents --max-count=1 ${MERGE_OID}`) {
      return { stdout: `${MERGE_OID} ${MERGE_PARENT}\n`, stderr: "" };
    }
    if (command === "rev-parse HEAD") {
      return { stdout: `${BACKPORT_OID}\n`, stderr: "" };
    }
    if (overrides.failCherryPick === true && args[0] === "cherry-pick" && args[1] !== "--abort") {
      const error = new Error("cherry-pick stopped");
      error.exitCode = 1;
      throw error;
    }
    if (args[0] === "merge-base") {
      if (overrides.ancestor === true) return { stdout: "", stderr: "" };
      const error = new Error("not ancestor");
      error.exitCode = 1;
      throw error;
    }
    return { stdout: "", stderr: "" };
  };
  return { git, calls };
}

async function execute(options = {}) {
  const github = createFakeGitHub(options.state ?? state());
  const recorder = options.recorder ?? gitRecorder();
  const result = await runBackport({
    github,
    git: recorder.git,
    config: loadConfig(repositoryRoot),
    dryRun: options.dryRun ?? false,
    prNumber: options.prNumber ?? String(PR_NUMBER),
    targetBranch: options.targetBranch ?? TARGET_BRANCH,
    repository: options.repository ?? REPOSITORY,
  });
  return { github, recorder, result };
}

test("creates one deterministic backport branch and pull request", async () => {
  const { github, recorder, result } = await execute();
  const branch = backportBranchName(PR_NUMBER, TARGET_BRANCH);

  assert.deepEqual(result, {
    status: "complete",
    outcome: "created",
    sourcePullRequest: PR_NUMBER,
    sourceCommit: MERGE_OID,
    targetBranch: TARGET_BRANCH,
    backportBranch: branch,
    backportPullRequest: {
      number: 1000,
      url: "https://github.com/NVIDIA/k8s-test-infra/pull/1000",
    },
  });
  assert.deepEqual(github.calls.createBackportPullRequest, [{
    base: TARGET_BRANCH,
    head: branch,
    title: `[${TARGET_BRANCH}] feat: add gpu probe`,
    body: backportPullRequestBody({
      repository: REPOSITORY,
      sourcePullRequest: PR_NUMBER,
      sourceCommit: MERGE_OID,
      targetBranch: TARGET_BRANCH,
      backportBranch: branch,
      author: "orig-author",
    }),
  }]);
  assert.equal(github.calls.createBackportPullRequest[0].body.includes(BACKPORT_EVIDENCE_MARKER), true);
  const push = recorder.calls.find((args) => args[0] === "push");
  assert.deepEqual(push, [
    "push",
    "--porcelain",
    "--atomic",
    `--force-with-lease=refs/heads/${branch}:`,
    "origin",
    `HEAD:refs/heads/${branch}`,
  ]);
  assert.equal(recorder.calls.some((args) => (
    args.some((value) => value === "--force" || value === "-f")
  )), false);
  assert.equal(recorder.calls.some((args) => args.at(-1) === `refs/heads/${TARGET_BRANCH}`), false);
});

test("pull request creation failure removes only the exact leased branch", async () => {
  const branch = backportBranchName(PR_NUMBER, TARGET_BRANCH);
  let github;
  const recorder = gitRecorder({
    onPush(args) {
      if (args.at(-1) === `HEAD:refs/heads/${branch}`) github.setBranch(branch, BACKPORT_OID);
    },
  });
  github = createFakeGitHub({
    ...state(),
    failures: { createBackportPullRequest: new Error("create failed") },
  });

  await assert.rejects(
    () => runBackport({
      github,
      git: recorder.git,
      config: loadConfig(repositoryRoot),
      dryRun: false,
      prNumber: String(PR_NUMBER),
      targetBranch: TARGET_BRANCH,
      repository: REPOSITORY,
    }),
    /pull request creation failed/,
  );
  assert.deepEqual(recorder.calls.at(-1), [
    "push",
    "--porcelain",
    "--atomic",
    `--force-with-lease=refs/heads/${branch}:${BACKPORT_OID}`,
    "origin",
    `:refs/heads/${branch}`,
  ]);
});

test("pull request creation failure preserves a concurrent replacement", async () => {
  const branch = backportBranchName(PR_NUMBER, TARGET_BRANCH);
  let github;
  const recorder = gitRecorder({
    onPush(args) {
      if (args.at(-1) === `HEAD:refs/heads/${branch}`) github.setBranch(branch, "9".repeat(40));
    },
  });
  github = createFakeGitHub({
    ...state(),
    failures: { createBackportPullRequest: new Error("create failed") },
  });

  await assert.rejects(
    () => runBackport({
      github,
      git: recorder.git,
      config: loadConfig(repositoryRoot),
      dryRun: false,
      prNumber: String(PR_NUMBER),
      targetBranch: TARGET_BRANCH,
      repository: REPOSITORY,
    }),
    /manual investigation/,
  );
  assert.equal(recorder.calls.filter((args) => args[0] === "push").length, 1);
});

test("rejects a target outside the checked-in allowlist before any Git command", async () => {
  const recorder = gitRecorder();
  await assert.rejects(
    () => execute({ targetBranch: "feature/not-a-release", recorder }),
    /not allowed/,
  );
  assert.deepEqual(recorder.calls, []);

  await assert.rejects(
    () => execute({ targetBranch: "release-1.2;touch-pwned", recorder }),
    /not allowed/,
  );
  assert.deepEqual(recorder.calls, []);
});

test("rejects an unmerged source and a changed live merge commit", async () => {
  const unmerged = gitRecorder();
  await assert.rejects(
    () => execute({
      state: state({ pullRequests: [mergedPullRequest({ merged: false, state: "open", mergeCommitOid: null })] }),
      recorder: unmerged,
    }),
    /must be merged/,
  );
  assert.deepEqual(unmerged.calls, []);

  const changed = gitRecorder();
  await assert.rejects(
    () => execute({
      state: state({ pullRequests: [
        mergedPullRequest(),
        mergedPullRequest({ mergeCommitOid: "9".repeat(40) }),
      ] }),
      recorder: changed,
    }),
    /changed/,
  );
  assert.deepEqual(changed.calls, []);
});

test("a cherry-pick conflict aborts and creates no remote branch or pull request", async () => {
  const recorder = gitRecorder({ failCherryPick: true, ancestor: false });
  const github = createFakeGitHub(state());

  await assert.rejects(
    () => runBackport({
      github,
      git: recorder.git,
      config: loadConfig(repositoryRoot),
      dryRun: false,
      prNumber: String(PR_NUMBER),
      targetBranch: TARGET_BRANCH,
      repository: REPOSITORY,
    }),
    /conflict/,
  );

  assert.equal(recorder.calls.some((args) => args[0] === "push"), false);
  assert.equal(recorder.calls.some((args) => args.join(" ") === "cherry-pick --abort"), true);
  assert.deepEqual(github.calls.createBackportPullRequest, []);
});

test("an empty cherry-pick is idempotent only when the target contains the source commit", async () => {
  const contained = await execute({ recorder: gitRecorder({ failCherryPick: true, ancestor: true }) });
  assert.equal(contained.result.outcome, "already-contained");
  assert.equal(contained.recorder.calls.some((args) => args[0] === "push"), false);
  assert.deepEqual(contained.github.calls.createBackportPullRequest, []);

  const notContained = gitRecorder({ failCherryPick: true, ancestor: false });
  await assert.rejects(() => execute({ recorder: notContained }), /conflict/);
});

test("duplicate delivery returns the exact existing result only when all evidence matches", async () => {
  const branch = backportBranchName(PR_NUMBER, TARGET_BRANCH);
  const body = backportPullRequestBody({
    repository: REPOSITORY,
    sourcePullRequest: PR_NUMBER,
    sourceCommit: MERGE_OID,
    targetBranch: TARGET_BRANCH,
    backportBranch: branch,
    author: "orig-author",
  });
  const existing = {
    number: 900,
    url: "https://github.com/NVIDIA/k8s-test-infra/pull/900",
    base: TARGET_BRANCH,
    head: branch,
    title: `[${TARGET_BRANCH}] feat: add gpu probe`,
    body,
    state: "open",
  };
  const recorder = gitRecorder();
  const { result } = await execute({
    state: state({
      branches: { [TARGET_BRANCH]: TARGET_OID, [branch]: BACKPORT_OID },
      backportPullRequests: [existing],
    }),
    recorder,
  });
  assert.equal(result.outcome, "already-exists");
  assert.deepEqual(result.backportPullRequest, { number: 900, url: existing.url });
  assert.deepEqual(recorder.calls, []);

  const mismatched = { ...existing, body: body.replace(MERGE_OID, "8".repeat(40)) };
  await assert.rejects(
    () => execute({
      state: state({
        branches: { [TARGET_BRANCH]: TARGET_OID, [branch]: BACKPORT_OID },
        backportPullRequests: [mismatched],
      }),
    }),
    /collision/,
  );

  await assert.rejects(
    () => execute({
      state: state({
        pullRequests: [
          mergedPullRequest(),
          mergedPullRequest({ mergeCommitOid: "9".repeat(40) }),
        ],
        branches: { [TARGET_BRANCH]: TARGET_OID, [branch]: BACKPORT_OID },
        backportPullRequests: [existing],
      }),
    }),
    /changed/,
  );
});

test("the backport path has no direct merge or workflow-dispatch capability", async () => {
  const { github } = await execute();
  assert.equal(typeof github.mergePullRequest, "undefined");
  assert.equal(typeof github.dispatchWorkflow, "undefined");
  assert.equal(typeof github.updateBranch, "undefined");
});
