"use strict";

const assert = require("node:assert/strict");
const { execFileSync, spawnSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const { runGit } = require("../src/git.js");
const {
  mokkaBranchName,
  runMokkaCherryPick,
} = require("../src/modes/mokka-cherry-pick.js");
const { parseMokkaEvidence } = require("../src/mokka-evidence.js");

const ACTION_ID = "123e4567-e89b-42d3-a456-426614174000";
const REPOSITORY = "NVIDIA/k8s-test-infra";
const REPOSITORY_ID = "733665780";
const SOURCE_PR = 42;
const TARGET_BRANCH = "main";
const WORKFLOW_SHA = "5".repeat(40);

function executeGit(cwd, args) {
  return execFileSync("git", args, {
    cwd,
    encoding: "utf8",
    env: {
      ...process.env,
      GIT_CONFIG_NOSYSTEM: "1",
      GIT_TERMINAL_PROMPT: "0",
    },
  }).trim();
}

function maybeGit(cwd, args) {
  const result = spawnSync("git", args, {
    cwd,
    encoding: "utf8",
    env: {
      ...process.env,
      GIT_CONFIG_NOSYSTEM: "1",
      GIT_TERMINAL_PROMPT: "0",
    },
  });
  return result.status === 0 ? result.stdout.trim() : null;
}

function writeFile(directory, name, contents) {
  fs.writeFileSync(path.join(directory, name), contents, { encoding: "utf8" });
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function createRepository(t, options = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "mokka-node-real-git-"));
  t.after(() => fs.rmSync(root, { force: true, recursive: true }));
  const origin = path.join(root, "origin.git");
  const seed = path.join(root, "seed");
  const target = path.join(root, "target");

  executeGit(root, ["init", "--bare", "--quiet", origin]);
  fs.mkdirSync(seed);
  executeGit(seed, ["init", "--quiet"]);
  executeGit(seed, ["config", "user.name", "fixture"]);
  executeGit(seed, ["config", "user.email", "fixture@example.com"]);
  writeFile(seed, "shared.txt", "base\n");
  executeGit(seed, ["add", "shared.txt"]);
  executeGit(seed, ["commit", "--quiet", "--message", "base"]);
  executeGit(seed, ["branch", "-M", TARGET_BRANCH]);
  executeGit(seed, ["remote", "add", "origin", origin]);
  executeGit(seed, ["push", "--quiet", "--set-upstream", "origin", TARGET_BRANCH]);

  executeGit(seed, ["checkout", "--quiet", "-b", "source"]);
  if (options.conflict === true) {
    writeFile(seed, "shared.txt", "source\n");
    executeGit(seed, ["add", "shared.txt"]);
  } else {
    writeFile(seed, "source.txt", "source change\n");
    executeGit(seed, ["add", "source.txt"]);
  }
  executeGit(seed, ["commit", "--quiet", "--message", "feat: source change"]);
  const sourceSha = executeGit(seed, ["rev-parse", "HEAD"]);
  const sourceParentSha = executeGit(seed, ["rev-parse", "HEAD^"]);
  executeGit(seed, ["push", "--quiet", "--set-upstream", "origin", "source"]);

  executeGit(seed, ["checkout", "--quiet", TARGET_BRANCH]);
  if (options.conflict === true) {
    writeFile(seed, "shared.txt", "target\n");
    executeGit(seed, ["add", "shared.txt"]);
    executeGit(seed, ["commit", "--quiet", "--message", "target change"]);
    executeGit(seed, ["push", "--quiet", "origin", TARGET_BRANCH]);
  }
  const targetSha = executeGit(seed, ["rev-parse", "HEAD"]);
  executeGit(origin, ["symbolic-ref", "HEAD", `refs/heads/${TARGET_BRANCH}`]);
  executeGit(root, ["clone", "--quiet", "--branch", TARGET_BRANCH, origin, target]);

  const pullRequests = [];
  const calls = {
    create: [],
    git: [],
    update: [],
  };
  const headBranch = mokkaBranchName(ACTION_ID);
  const github = {
    async getPullRequest(number) {
      assert.equal(number, SOURCE_PR);
      return {
        number: SOURCE_PR,
        state: "closed",
        merged: true,
        mergeCommitOid: sourceSha,
        baseBranch: "source-base",
        baseRepository: { owner: "NVIDIA", repo: "k8s-test-infra" },
        headRepository: { owner: "NVIDIA", repo: "k8s-test-infra" },
      };
    },
    async getCommit(sha) {
      assert.equal(sha, sourceSha);
      return { sha, parents: [sourceParentSha] };
    },
    async getBranch(name) {
      const oid = maybeGit(origin, ["rev-parse", `refs/heads/${name}`]);
      return oid === null ? null : { name, oid };
    },
    async findMokkaPullRequests(head, base) {
      assert.equal(head, headBranch);
      assert.equal(base, TARGET_BRANCH);
      return clone(pullRequests);
    },
    async createMokkaPullRequest(request) {
      calls.create.push(clone(request));
      if (options.createError !== undefined) {
        if (options.replaceBranchOnCreateFailure === true) {
          executeGit(origin, ["update-ref", `refs/heads/${headBranch}`, targetSha]);
        }
        throw options.createError;
      }
      const headOid = executeGit(origin, ["rev-parse", `refs/heads/${headBranch}`]);
      const created = {
        number: 1000,
        url: `https://github.com/${REPOSITORY}/pull/1000`,
        state: "open",
        draft: true,
        base: request.base,
        head: request.head,
        headOid,
        title: request.title,
        body: request.body,
      };
      pullRequests.push(created);
      return clone(created);
    },
    async updateMokkaPullRequestBody(number, body) {
      calls.update.push({ number, body });
      assert.equal(number, 1000);
      pullRequests[0].body = body;
    },
  };
  const git = async (args) => {
    calls.git.push([...args]);
    return runGit(args, { cwd: target });
  };

  return {
    calls,
    git,
    github,
    headBranch,
    origin,
    pullRequests,
    sourceSha,
    target,
    targetSha,
  };
}

function invocation(repository, overrides = {}) {
  return {
    actionId: ACTION_ID,
    dryRun: false,
    git: repository.git,
    github: repository.github,
    prNumber: String(SOURCE_PR),
    repository: REPOSITORY,
    repositoryId: REPOSITORY_ID,
    sourceSha: repository.sourceSha,
    targetBranch: TARGET_BRANCH,
    workflowSha: WORKFLOW_SHA,
    ...overrides,
  };
}

test("real Git clean cherry-pick preserves the target ref and is exactly idempotent", async (t) => {
  const repository = createRepository(t);
  const first = await runMokkaCherryPick(invocation(repository));

  assert.equal(first.outcome, "created");
  assert.equal(executeGit(repository.target, ["rev-parse", `refs/heads/${TARGET_BRANCH}`]), repository.targetSha);
  assert.equal(executeGit(repository.target, ["show", "HEAD:source.txt"]), "source change");
  assert.equal(
    executeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]),
    executeGit(repository.target, ["rev-parse", "HEAD"]),
  );
  const message = executeGit(repository.target, ["show", "-s", "--format=%B", "HEAD"]);
  assert.match(message, new RegExp(`Mokka-Source-SHA: ${repository.sourceSha}`));
  assert.match(message, new RegExp(`Mokka-Action-ID: ${ACTION_ID}`));
  assert.equal(parseMokkaEvidence(repository.pullRequests[0].body)?.targetBaseSha, repository.targetSha);

  const gitCallCount = repository.calls.git.length;
  const second = await runMokkaCherryPick(invocation(repository));
  assert.equal(second.outcome, "already-exists");
  assert.equal(repository.calls.git.length, gitCallCount, "an exact duplicate must not invoke Git");
  assert.equal(repository.calls.create.length, 1);
});

test("real Git conflict aborts cleanly and does not create a remote branch", async (t) => {
  const repository = createRepository(t, { conflict: true });
  await assert.rejects(
    () => runMokkaCherryPick(invocation(repository)),
    /cherry-pick conflict/,
  );

  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]), null);
  assert.equal(executeGit(repository.target, ["status", "--porcelain"]), "");
  assert.equal(executeGit(repository.target, ["rev-parse", "HEAD"]), repository.targetSha);
  assert.deepEqual(repository.calls.create, []);
});

test("real Git create-only lease rejects a concurrent branch collision", async (t) => {
  const repository = createRepository(t);
  const ordinaryGit = repository.git;
  let injected = false;
  repository.git = async (args) => {
    if (!injected && args[0] === "push" && args.some((value) => value.includes("force-with-lease"))) {
      injected = true;
      executeGit(repository.origin, [
        "update-ref",
        `refs/heads/${repository.headBranch}`,
        repository.targetSha,
      ]);
    }
    return ordinaryGit(args);
  };

  await assert.rejects(
    () => runMokkaCherryPick(invocation(repository)),
    /atomic push failed|failed to push|stale info|rejected/,
  );
  assert.equal(
    executeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]),
    repository.targetSha,
  );
  assert.deepEqual(repository.calls.create, []);
});

test("real Git cleanup removes only the exact leased branch after PR creation fails", async (t) => {
  const repository = createRepository(t, { createError: new Error("create failed") });
  await assert.rejects(
    () => runMokkaCherryPick(invocation(repository)),
    /pull request creation failed/,
  );

  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]), null);
  assert.equal(executeGit(repository.origin, ["rev-parse", `refs/heads/${TARGET_BRANCH}`]), repository.targetSha);
  assert.equal(repository.calls.create.length, 1);
});

test("real Git cleanup preserves a concurrent replacement after PR creation fails", async (t) => {
  const repository = createRepository(t, {
    createError: new Error("create failed"),
    replaceBranchOnCreateFailure: true,
  });
  await assert.rejects(
    () => runMokkaCherryPick(invocation(repository)),
    /manual investigation/,
  );

  assert.equal(
    executeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]),
    repository.targetSha,
  );
});

test("hostile dispatch strings reach neither Git nor GitHub in a real repository", async (t) => {
  const repository = createRepository(t);
  const cases = [
    { sourceSha: "$(touch /tmp/mokka-pwned)" },
    { sourceSha: `${repository.sourceSha};echo pwned` },
    { sourceSha: `-${repository.sourceSha.slice(1)}` },
    { targetBranch: "main " },
    { targetBranch: "main\n" },
    { actionId: `${ACTION_ID};echo pwned` },
    { prNumber: "42 43" },
  ];
  for (const malicious of cases) {
    await assert.rejects(() => runMokkaCherryPick(invocation(repository, malicious)), /invalid/);
  }
  assert.deepEqual(repository.calls.git, []);
  assert.deepEqual(repository.calls.create, []);
});
