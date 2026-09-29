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
  executeGit(origin, ["config", "user.name", "fixture-api"]);
  executeGit(origin, ["config", "user.email", "fixture-api@example.com"]);
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
  executeGit(root, ["clone", "--quiet", "--depth", "1", "--branch", TARGET_BRANCH, `file://${origin}`, target]);

  const pullRequests = [];
  const calls = {
    createCommit: [],
    createRef: [],
    create: [],
    git: [],
    update: [],
  };
  const headBranch = mokkaBranchName(ACTION_ID);
  const uploadBranch = `mokka/cherry-pick-upload/${ACTION_ID}`;
  const verifiedCommits = new Set();
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
      if (sha === sourceSha) return { sha, parents: [sourceParentSha] };
      assert.equal(sha, maybeGit(origin, ["rev-parse", `refs/heads/${headBranch}`]));
      return { sha, parents: [executeGit(origin, ["rev-parse", `${sha}^`])] };
    },
    async getMokkaCommit(sha) {
      const commit = await this.getCommit(sha);
      return { ...commit,
        message: executeGit(origin, ["show", "-s", "--format=%B", sha]),
        tree: executeGit(origin, ["rev-parse", `${sha}^{tree}`]),
        verification: {
        verified: verifiedCommits.has(sha),
        hasSignature: verifiedCommits.has(sha),
      } };
    },
    async getBranch(name) {
      const oid = maybeGit(origin, ["rev-parse", `refs/heads/${name}`]);
      return oid === null ? null : { name, oid };
    },
    async findMokkaPullRequests(head, base) {
      assert.ok(head === headBranch || head === uploadBranch);
      assert.ok(base === undefined || base === TARGET_BRANCH);
      if (options.stalePullRequestList === true && head === headBranch) return [];
      return clone(pullRequests.filter((pullRequest) => (
        pullRequest.head === head && (base === undefined || pullRequest.base === base)
      )));
    },
    async createMokkaCommit(request) {
      calls.createCommit.push(clone(request));
      const provisionalSha = executeGit(origin, ["rev-parse", `refs/heads/${uploadBranch}`]);
      const provisionalTree = executeGit(origin, ["rev-parse", `${provisionalSha}^{tree}`]);
      assert.equal(request.tree, provisionalTree);
      assert.equal(request.parents.length, 1);
      const sha = executeGit(origin, [
        "commit-tree", request.tree, "-p", request.parents[0], "-m", request.message,
      ]);
      if (options.advanceAfterUpload === true) {
        advanceMain("post-upload.txt", "main moved after upload\n");
      }
      if (options.foreignBaseUploadPullRequest === true) {
        pullRequests.push({
          number: 1001,
          base: "release-branch",
          head: uploadBranch,
          headOid: provisionalSha,
        });
      }
      if (options.unverifiedCommit !== true) verifiedCommits.add(sha);
      return {
        sha,
        message: request.message.replace(/\n+$/u, ""),
        tree: request.tree,
        parents: request.parents,
        verification: {
          verified: options.unverifiedCommit !== true,
          hasSignature: options.unverifiedCommit !== true,
        },
      };
    },
    async createMokkaRef(name, sha) {
      calls.createRef.push({ name, sha });
      assert.equal(name, headBranch);
      if (options.createRefCollision === true) {
        executeGit(origin, ["update-ref", `refs/heads/${name}`, targetSha]);
      }
      executeGit(origin, [
        "update-ref",
        `refs/heads/${name}`,
        sha,
        "0".repeat(40),
      ]);
      if (options.ambiguousCreateRef === true) throw new Error("response lost after ref creation");
      return { name, oid: sha };
    },
    async createMokkaPullRequest(request) {
      calls.create.push(clone(request));
      if (options.createError !== undefined) {
        if (options.replaceBranchOnCreateFailure === true) {
          executeGit(origin, ["update-ref", `refs/heads/${headBranch}`, targetSha]);
        }
        if (options.foreignBasePullRequest === true) {
          pullRequests.push({
            number: 1001,
            base: "release-branch",
            head: headBranch,
            headOid: executeGit(origin, ["rev-parse", `refs/heads/${headBranch}`]),
          });
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
      if (options.ambiguousPullRequestCreate === true) {
        throw new Error("response lost after pull request creation");
      }
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
  const advanceMain = (name, contents) => {
    writeFile(seed, name, contents);
    executeGit(seed, ["add", name]);
    executeGit(seed, ["commit", "--quiet", "--message", `advance main: ${name}`]);
    executeGit(seed, ["push", "--quiet", "origin", TARGET_BRANCH]);
    return executeGit(seed, ["rev-parse", "HEAD"]);
  };
  const rewriteMain = () => {
    const tree = executeGit(seed, ["rev-parse", `${TARGET_BRANCH}^{tree}`]);
    const replacement = executeGit(seed, ["commit-tree", tree, "-m", "rewrite main history"]);
    executeGit(seed, ["update-ref", `refs/heads/${TARGET_BRANCH}`, replacement]);
    executeGit(seed, ["push", "--quiet", "--force", "origin", TARGET_BRANCH]);
    return replacement;
  };

  return {
    advanceMain,
    calls,
    git,
    github,
    headBranch,
    uploadBranch,
    origin,
    pullRequests,
    rewriteMain,
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
    parseMokkaEvidence(repository.pullRequests[0].body)?.producedHeadSha,
  );
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.uploadBranch}`]), null);
  assert.equal(
    executeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}^{tree}`]),
    executeGit(repository.target, ["rev-parse", "HEAD^{tree}"]),
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

test("real Git keeps an exact existing draft after main advances", async (t) => {
  const repository = createRepository(t);
  const first = await runMokkaCherryPick(invocation(repository));
  const existingBranch = executeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]);
  const evidence = parseMokkaEvidence(repository.pullRequests[0].body);
  const gitCallCount = repository.calls.git.length;
  const latestMain = repository.advanceMain("unrelated.txt", "new main content\n");

  const second = await runMokkaCherryPick(invocation(repository));

  assert.equal(first.outcome, "created");
  assert.equal(second.outcome, "already-exists");
  assert.equal(executeGit(repository.origin, ["rev-parse", `refs/heads/${TARGET_BRANCH}`]), latestMain);
  assert.equal(executeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]), existingBranch);
  assert.equal(executeGit(repository.origin, ["rev-parse", `${existingBranch}^`]), evidence.targetBaseSha);
  assert.equal(repository.calls.git.length, gitCallCount, "an exact duplicate must not invoke Git");
  assert.equal(repository.calls.git.filter((args) => args[0] === "push").length, 2);
  assert.equal(repository.calls.create.length, 1);
  assert.equal(repository.pullRequests.length, 1);
});

test("real Git starts from current main when checkout became stale before the action", async (t) => {
  const repository = createRepository(t);
  const latestMain = repository.advanceMain("unrelated.txt", "new main content\n");

  const result = await runMokkaCherryPick(invocation(repository));

  assert.equal(result.outcome, "created");
  assert.equal(executeGit(repository.target, ["rev-parse", "HEAD^"]), latestMain);
  assert.equal(executeGit(repository.target, ["show", "HEAD:unrelated.txt"]), "new main content");
  assert.equal(executeGit(repository.target, ["show", "HEAD:source.txt"]), "source change");
  assert.equal(parseMokkaEvidence(repository.pullRequests[0].body)?.targetBaseSha, latestMain);
  assert.equal(repository.calls.git.filter((args) => args[0] === "push").length, 2);
  assert.equal(repository.calls.create.length, 1);
});

test("real Git checks the fetched main when it moves after the first API read", async (t) => {
  const repository = createRepository(t);
  const getBranch = repository.github.getBranch;
  let latestMain;
  repository.github.getBranch = async (name) => {
    const branch = await getBranch(name);
    if (name === TARGET_BRANCH && latestMain === undefined) {
      latestMain = repository.advanceMain("unrelated.txt", "new main content\n");
    }
    return branch;
  };

  const result = await runMokkaCherryPick(invocation(repository));

  assert.equal(result.outcome, "created");
  assert.equal(executeGit(repository.target, ["rev-parse", "HEAD^"]), latestMain);
  assert.equal(executeGit(repository.target, ["show", "HEAD:unrelated.txt"]), "new main content");
  assert.equal(parseMokkaEvidence(repository.pullRequests[0].body)?.targetBaseSha, latestMain);
  assert.equal(repository.calls.git.filter((args) => args[0] === "push").length, 2);
});

test("real Git accepts another fast-forward after a stale shallow checkout", async (t) => {
  const repository = createRepository(t);
  const firstAdvance = repository.advanceMain("first.txt", "first main change\n");
  assert.equal(executeGit(repository.target, ["rev-parse", "--is-shallow-repository"]), "true");
  assert.equal(maybeGit(repository.target, ["cat-file", "-e", `${firstAdvance}^{commit}`]), null);
  const getBranch = repository.github.getBranch;
  let latestMain;
  repository.github.getBranch = async (name) => {
    const branch = await getBranch(name);
    if (name === TARGET_BRANCH && latestMain === undefined) {
      assert.equal(branch.oid, firstAdvance);
      latestMain = repository.advanceMain("second.txt", "second main change\n");
    }
    return branch;
  };

  const result = await runMokkaCherryPick(invocation(repository));

  assert.equal(result.outcome, "created");
  assert.equal(executeGit(repository.target, ["rev-parse", "HEAD^"]), latestMain);
  assert.equal(executeGit(repository.target, ["show", "HEAD:first.txt"]), "first main change");
  assert.equal(executeGit(repository.target, ["show", "HEAD:second.txt"]), "second main change");
  assert.equal(parseMokkaEvidence(repository.pullRequests[0].body)?.targetBaseSha, latestMain);
  assert.equal(repository.calls.git.filter((args) => args[0] === "push").length, 2);
});

test("real Git rejects a main rewind after the first API read and before fetch", async (t) => {
  const repository = createRepository(t);
  const observedMain = repository.advanceMain("unrelated.txt", "new main content\n");
  const getBranch = repository.github.getBranch;
  let rewound = false;
  repository.github.getBranch = async (name) => {
    const branch = await getBranch(name);
    if (name === TARGET_BRANCH && !rewound) {
      assert.equal(branch.oid, observedMain);
      rewound = true;
      executeGit(repository.origin, ["update-ref", `refs/heads/${TARGET_BRANCH}`, repository.targetSha]);
    }
    return branch;
  };

  await assert.rejects(() => runMokkaCherryPick(invocation(repository)), /target branch history changed/);

  assert.equal(executeGit(repository.origin, ["rev-parse", `refs/heads/${TARGET_BRANCH}`]), repository.targetSha);
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]), null);
  assert.equal(repository.calls.git.filter((args) => args[0] === "push").length, 0);
  assert.deepEqual(repository.calls.create, []);
});

test("real Git rebuilds the cherry-pick from a newer main before the first remote write", async (t) => {
  const repository = createRepository(t);
  const ordinaryGit = repository.git;
  let latestMain;
  repository.git = async (args) => {
    const result = await ordinaryGit(args);
    if (latestMain === undefined && args[0] === "commit" && args[1] === "--amend") {
      latestMain = repository.advanceMain("unrelated.txt", "new main content\n");
    }
    return result;
  };

  const result = await runMokkaCherryPick(invocation(repository));

  assert.equal(result.outcome, "created");
  assert.equal(executeGit(repository.target, ["rev-parse", "HEAD^"]), latestMain);
  assert.equal(executeGit(repository.target, ["show", "HEAD:unrelated.txt"]), "new main content");
  assert.equal(executeGit(repository.target, ["show", "HEAD:source.txt"]), "source change");
  assert.equal(parseMokkaEvidence(repository.pullRequests[0].body)?.targetBaseSha, latestMain);
  assert.equal(repository.calls.git.filter((args) => args[0] === "push").length, 2);
  assert.equal(repository.calls.create.length, 1);
});

test("real Git stops without a remote write if a retry conflicts with current main", async (t) => {
  const repository = createRepository(t);
  const ordinaryGit = repository.git;
  let advanced = false;
  repository.git = async (args) => {
    const result = await ordinaryGit(args);
    if (!advanced && args[0] === "commit" && args[1] === "--amend") {
      advanced = true;
      repository.advanceMain("source.txt", "conflicting main content\n");
    }
    return result;
  };

  await assert.rejects(() => runMokkaCherryPick(invocation(repository)), /cherry-pick conflict/);

  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]), null);
  assert.equal(executeGit(repository.target, ["status", "--porcelain"]), "");
  assert.equal(repository.calls.git.filter((args) => args[0] === "push").length, 0);
  assert.deepEqual(repository.calls.create, []);
});

test("real Git stops after repeated main advances without creating a remote branch", async (t) => {
  const repository = createRepository(t);
  const ordinaryGit = repository.git;
  let advances = 0;
  repository.git = async (args) => {
    const result = await ordinaryGit(args);
    if (args[0] === "commit" && args[1] === "--amend") {
      advances += 1;
      repository.advanceMain(`unrelated-${advances}.txt`, `main advance ${advances}\n`);
    }
    return result;
  };

  await assert.rejects(() => runMokkaCherryPick(invocation(repository)), /target branch changed/);

  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]), null);
  assert.equal(repository.calls.git.filter((args) => args[0] === "push").length, 0);
  assert.deepEqual(repository.calls.create, []);
  assert.equal(advances, 3, "three attempts must rebuild on current main, then stop");
});

test("real Git rejects a non-fast-forward main rewrite without a remote write", async (t) => {
  const repository = createRepository(t);
  const ordinaryGit = repository.git;
  let replacement;
  repository.git = async (args) => {
    const result = await ordinaryGit(args);
    if (replacement === undefined && args[0] === "commit" && args[1] === "--amend") {
      replacement = repository.rewriteMain();
    }
    return result;
  };

  await assert.rejects(() => runMokkaCherryPick(invocation(repository)), /target branch history changed/);

  assert.equal(executeGit(repository.origin, ["rev-parse", `refs/heads/${TARGET_BRANCH}`]), replacement);
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]), null);
  assert.equal(repository.calls.git.filter((args) => args[0] === "push").length, 0);
  assert.deepEqual(repository.calls.create, []);
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
    /manual investigation/,
  );
  assert.equal(
    executeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]),
    repository.targetSha,
  );
  assert.deepEqual(repository.calls.create, []);
});

test("real Git removes the exact upload when main advances after signing", async (t) => {
  const repository = createRepository(t, { advanceAfterUpload: true });
  await assert.rejects(
    () => runMokkaCherryPick(invocation(repository)),
    /verified commit creation failed/,
  );
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.uploadBranch}`]), null);
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]), null);
  assert.equal(repository.calls.createRef.length, 0);
  assert.equal(repository.calls.create.length, 0);
});

test("real Git rejects an unverified API commit and removes its upload", async (t) => {
  const repository = createRepository(t, { unverifiedCommit: true });
  await assert.rejects(
    () => runMokkaCherryPick(invocation(repository)),
    /verified commit creation failed/,
  );
  assert.equal(repository.calls.createCommit.length, 1);
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.uploadBranch}`]), null);
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]), null);
  assert.equal(repository.calls.createRef.length, 0);
  assert.equal(repository.calls.create.length, 0);
});

test("real Git reconciles an upload accepted before a lost push response", async (t) => {
  const repository = createRepository(t);
  const ordinaryGit = repository.git;
  let responseLost = false;
  repository.git = async (args) => {
    const result = await ordinaryGit(args);
    if (!responseLost && args[0] === "push" && args.some((arg) => arg.includes(repository.uploadBranch))) {
      responseLost = true;
      throw new Error("transport response lost");
    }
    return result;
  };
  await assert.rejects(
    () => runMokkaCherryPick(invocation(repository)),
    /provisional commit upload failed/,
  );
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.uploadBranch}`]), null);
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]), null);
  assert.equal(repository.calls.createCommit.length, 0);
  assert.equal(repository.calls.create.length, 0);
});

test("real Git preserves a concurrent final ref and removes its exact upload", async (t) => {
  const repository = createRepository(t, { createRefCollision: true });
  await assert.rejects(
    () => runMokkaCherryPick(invocation(repository)),
    /manual investigation/,
  );
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.uploadBranch}`]), null);
  assert.equal(executeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]), repository.targetSha);
  assert.equal(repository.calls.create.length, 0);
});

test("real Git leaves an ambiguous final ref for manual investigation", async (t) => {
  const repository = createRepository(t, { ambiguousCreateRef: true });
  await assert.rejects(
    () => runMokkaCherryPick(invocation(repository)),
    /manual investigation/,
  );
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.uploadBranch}`]), null);
  assert.ok(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]));
  assert.equal(repository.calls.create.length, 0);
});

test("real Git keeps the signed branch when pull request creation fails", async (t) => {
  const repository = createRepository(t, { createError: new Error("create failed") });
  await assert.rejects(
    () => runMokkaCherryPick(invocation(repository)),
    /MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION/,
  );

  assert.ok(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]));
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.uploadBranch}`]), null);
  assert.equal(executeGit(repository.origin, ["rev-parse", `refs/heads/${TARGET_BRANCH}`]), repository.targetSha);
  assert.equal(repository.calls.create.length, 1);
});

test("real Git preserves an upload branch linked to a pull request with another base", async (t) => {
  const repository = createRepository(t, {
    unverifiedCommit: true,
    foreignBaseUploadPullRequest: true,
  });
  await assert.rejects(
    () => runMokkaCherryPick(invocation(repository)),
    /MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION/,
  );
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]), null);
  assert.ok(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.uploadBranch}`]));
  assert.equal(repository.pullRequests.length, 1);
  assert.equal(repository.pullRequests[0].base, "release-branch");
  assert.equal(repository.pullRequests[0].head, repository.uploadBranch);
});

test("real Git keeps a signed branch after a pull request was created but its response was lost", async (t) => {
  const repository = createRepository(t, {
    ambiguousPullRequestCreate: true,
    stalePullRequestList: true,
  });
  await assert.rejects(
    () => runMokkaCherryPick(invocation(repository)),
    /MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION/,
  );
  assert.ok(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.headBranch}`]));
  assert.equal(maybeGit(repository.origin, ["rev-parse", `refs/heads/${repository.uploadBranch}`]), null);
  assert.equal(repository.pullRequests.length, 1);
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
