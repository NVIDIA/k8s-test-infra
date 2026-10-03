/**
 * Copyright 2026 NVIDIA CORPORATION
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

"use strict";

const assert = require("node:assert/strict");
const { execFileSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const run = require("./backport.js");

// Hermetic git for the fixtures and for backport.js, which inherits this
// environment: no user or system configuration (signing, hooks, templates).
process.env.GIT_CONFIG_GLOBAL = os.devNull;
process.env.GIT_CONFIG_NOSYSTEM = "1";

const BOT_ENV = {
  GIT_AUTHOR_NAME: "github-actions[bot]",
  GIT_AUTHOR_EMAIL: "41898282+github-actions[bot]@users.noreply.github.com",
  GIT_COMMITTER_NAME: "GitHub",
  GIT_COMMITTER_EMAIL: "noreply@github.com",
};
const HUMAN_ENV = {
  GIT_AUTHOR_NAME: "Maintainer",
  GIT_AUTHOR_EMAIL: "maintainer@example.com",
  GIT_COMMITTER_NAME: "Maintainer",
  GIT_COMMITTER_EMAIL: "maintainer@example.com",
};
// A bot commit amended by a human, and a file edited in the GitHub web UI.
const AMENDED_ENV = { ...BOT_ENV, GIT_COMMITTER_NAME: "Maintainer", GIT_COMMITTER_EMAIL: "maintainer@example.com" };
const WEB_EDIT_ENV = { ...HUMAN_ENV, GIT_COMMITTER_NAME: "GitHub", GIT_COMMITTER_EMAIL: "noreply@github.com" };
const CONTEXT = { repo: { owner: "NVIDIA", repo: "k8s-test-infra" } };
const BACKPORT_BRANCH = "backport-42-to-release-0.4";
const PICKED = "ONE\ntwo\nthree\nfour\n";

function git(cwd, args, env = {}) {
  return execFileSync("git", args, { cwd, encoding: "utf8", env: { ...process.env, ...env } }).trim();
}

function reachable(cwd, sha) {
  try {
    git(cwd, ["cat-file", "-e", `${sha}^{commit}`]);
    return true;
  } catch {
    return false;
  }
}

// A squash-only repository with a fork pull request: the PR head commits never
// reach origin, only the squash commit on main does. With merge: true, main
// gets a two-parent merge commit instead.
function repository(t, { merge = false, releaseLine = null, releaseHasChange = false } = {}) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "cherrypick-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const work = path.join(root, "work");
  const origin = path.join(root, "origin.git");
  const clone = path.join(root, "clone");

  git(root, ["init", "-q", "-b", "main", work]);
  git(work, ["config", "user.name", "Developer"]);
  git(work, ["config", "user.email", "developer@example.com"]);
  fs.writeFileSync(path.join(work, "app.txt"), "one\ntwo\nthree\n");
  git(work, ["add", "app.txt"]);
  git(work, ["commit", "-q", "-m", "chore: initial"]);
  git(work, ["branch", "release-0.4"]);
  if (releaseLine !== null) {
    git(work, ["checkout", "-q", "release-0.4"]);
    fs.writeFileSync(path.join(work, "app.txt"), `${releaseLine}\ntwo\nthree\n`);
    git(work, ["commit", "-q", "-am", "fix: release-only change"]);
    git(work, ["checkout", "-q", "main"]);
  }
  if (releaseHasChange) {
    git(work, ["checkout", "-q", "release-0.4"]);
    fs.writeFileSync(path.join(work, "app.txt"), PICKED);
    git(work, ["commit", "-q", "-am", "feat: change app on the release"]);
    git(work, ["checkout", "-q", "main"]);
  }

  git(work, ["checkout", "-q", "-b", "feature", "main"]);
  fs.writeFileSync(path.join(work, "app.txt"), "ONE\ntwo\nthree\n");
  git(work, ["commit", "-q", "-am", "wip: first"]);
  fs.writeFileSync(path.join(work, "app.txt"), PICKED);
  git(work, ["commit", "-q", "-am", "wip: second"]);
  const headCommits = git(work, ["rev-list", "--reverse", "main..feature"]).split("\n");

  git(work, ["checkout", "-q", "main"]);
  if (merge) {
    git(work, ["merge", "-q", "--no-ff", "feature", "-m", "Merge pull request #42"]);
  } else {
    git(work, ["merge", "-q", "--squash", "feature"]);
    git(work, ["commit", "-q", "-m", "feat: change app (#42)"]);
  }
  const mergeCommit = git(work, ["rev-parse", "HEAD"]);

  git(root, ["init", "-q", "--bare", "-b", "main", origin]);
  git(work, ["push", "-q", origin, "main", "release-0.4"]);
  git(root, ["clone", "-q", origin, clone]);
  git(clone, ["config", "user.name", "nvidia-backport-bot"]);
  git(clone, ["config", "user.email", "noreply@nvidia.com"]);

  return {
    origin,
    clone,
    mergeCommit,
    headCommits,
    releaseTip: git(origin, ["rev-parse", "refs/heads/release-0.4"]),
  };
}

function pullRequest(fixture, overrides = {}) {
  return {
    number: 42,
    merged: true,
    merge_commit_sha: fixture.mergeCommit,
    title: "feat: change app",
    user: { login: "contributor" },
    ...overrides,
  };
}

// GitHub one layer deep: the Git Data API acts on the bare origin, and a
// commit it creates is authored by github-actions[bot] like a GITHUB_TOKEN one.
function fakeGitHub(fixture, { pull = pullRequest(fixture), openPulls = [], failCreateCommit = false } = {}) {
  const calls = [];
  const record = (name, params) => calls.push({ name, params });
  let nextNumber = 100;
  const github = {
    rest: {
      pulls: {
        get: async (params) => { record("pulls.get", params); return { data: pull }; },
        listCommits: async (params) => {
          record("pulls.listCommits", params);
          return { data: fixture.headCommits.map((sha) => ({ sha, commit: { message: "wip" } })) };
        },
        list: async (params) => { record("pulls.list", params); return { data: openPulls }; },
        create: async (params) => {
          record("pulls.create", params);
          const number = nextNumber++;
          return { data: { number, html_url: `https://github.com/NVIDIA/k8s-test-infra/pull/${number}` } };
        },
        update: async (params) => { record("pulls.update", params); return { data: {} }; },
      },
      git: {
        getRef: async (params) => {
          record("git.getRef", params);
          return { data: { object: { sha: git(fixture.origin, ["rev-parse", `refs/${params.ref}`]) } } };
        },
        createCommit: async (params) => {
          record("git.createCommit", params);
          if (failCreateCommit) throw new Error("Server Error");
          const parents = params.parents.flatMap((parent) => ["-p", parent]);
          const sha = git(fixture.origin, ["commit-tree", params.tree, ...parents, "-m", params.message], BOT_ENV);
          return { data: { sha } };
        },
        updateRef: async (params) => {
          record("git.updateRef", params);
          git(fixture.origin, ["update-ref", `refs/${params.ref}`, params.sha]);
          return { data: {} };
        },
      },
      issues: {
        addLabels: async (params) => { record("issues.addLabels", params); return { data: [] }; },
        removeLabel: async (params) => { record("issues.removeLabel", params); return { data: [] }; },
        createComment: async (params) => { record("issues.createComment", params); return { data: {} }; },
      },
    },
  };
  return { github, calls };
}

function fakeCore() {
  const failed = [];
  return {
    failed,
    info() {},
    warning() {},
    error() {},
    setFailed(message) { failed.push(message); },
  };
}

async function backport(fixture, github, branches) {
  const previous = process.cwd();
  process.env.PR_NUMBER = "42";
  process.env.BRANCHES_JSON = JSON.stringify(branches);
  const core = fakeCore();
  process.chdir(fixture.clone);
  try {
    return { results: await run({ github, context: CONTEXT, core }), core };
  } finally {
    process.chdir(previous);
  }
}

function named(calls, name) {
  return calls.filter((call) => call.name === name).map((call) => call.params);
}

function pushBranch(fixture, env) {
  const tree = git(fixture.origin, ["rev-parse", "refs/heads/release-0.4^{tree}"]);
  const sha = git(fixture.origin, ["commit-tree", tree, "-p", fixture.releaseTip, "-m", "fix: resolve conflicts"], env);
  git(fixture.origin, ["update-ref", `refs/heads/${BACKPORT_BRANCH}`, sha]);
  return sha;
}

test("cherry-picks the squash merge commit when the PR head commits are unreachable", async (t) => {
  const fixture = repository(t);
  assert.equal(reachable(fixture.clone, fixture.mergeCommit), true);
  for (const sha of fixture.headCommits) assert.equal(reachable(fixture.clone, sha), false, sha);
  const { github, calls } = fakeGitHub(fixture);

  const { results, core } = await backport(fixture, github, ["release-0.4"]);

  assert.deepEqual(results, [{
    branch: "release-0.4",
    success: true,
    prNumber: 100,
    prUrl: "https://github.com/NVIDIA/k8s-test-infra/pull/100",
    hasConflicts: false,
    updated: false,
  }]);
  assert.deepEqual(core.failed, []);
  assert.equal(git(fixture.origin, ["show", `refs/heads/${BACKPORT_BRANCH}:app.txt`]) + "\n", PICKED);
  assert.equal(git(fixture.origin, ["rev-parse", `refs/heads/${BACKPORT_BRANCH}^`]), fixture.releaseTip);
  assert.equal(git(fixture.origin, ["log", "-1", "--format=%an", `refs/heads/${BACKPORT_BRANCH}`]), "github-actions[bot]");
  assert.match(
    git(fixture.origin, ["log", "-1", "--format=%B", `refs/heads/${BACKPORT_BRANCH}`]),
    new RegExp(`\\(cherry picked from commit ${fixture.mergeCommit}\\)`),
  );
  const [created] = named(calls, "pulls.create");
  assert.deepEqual(
    { title: created.title, head: created.head, base: created.base, draft: created.draft },
    { title: "feat: change app [release-0.4]", head: BACKPORT_BRANCH, base: "release-0.4", draft: false },
  );
  assert.deepEqual(named(calls, "issues.createComment").map(({ body }) => body), [
    "Backport PR created for `release-0.4`: #100",
  ]);
});

test("cherry-picks a two-parent merge commit against its first parent", async (t) => {
  const fixture = repository(t, { merge: true });
  assert.equal(git(fixture.clone, ["rev-list", "--parents", "-n", "1", fixture.mergeCommit]).split(" ").length, 3);
  const { github } = fakeGitHub(fixture);

  const { results, core } = await backport(fixture, github, ["release-0.4"]);

  assert.equal(results[0].success, true, results[0].error);
  assert.deepEqual(core.failed, []);
  assert.equal(git(fixture.origin, ["show", `refs/heads/${BACKPORT_BRANCH}:app.txt`]) + "\n", PICKED);
});

test("a conflicting cherry-pick opens a draft PR that keeps the conflict", async (t) => {
  const fixture = repository(t, { releaseLine: "uno" });
  const { github, calls } = fakeGitHub(fixture);

  const { results, core } = await backport(fixture, github, ["release-0.4"]);

  assert.equal(results[0].success, true, results[0].error);
  assert.equal(results[0].hasConflicts, true);
  assert.deepEqual(core.failed, []);
  assert.match(git(fixture.origin, ["show", `refs/heads/${BACKPORT_BRANCH}:app.txt`]), /^<<<<<<< /m);
  assert.equal(named(calls, "pulls.create")[0].draft, true);
});

test("a failed target branch fails the run after every branch is attempted", async (t) => {
  const fixture = repository(t);
  const { github, calls } = fakeGitHub(fixture);

  const { results, core } = await backport(fixture, github, ["release-9.9", "release-0.4"]);

  assert.deepEqual(results.map(({ branch, success }) => ({ branch, success })), [
    { branch: "release-9.9", success: false },
    { branch: "release-0.4", success: true },
  ]);
  const comments = named(calls, "issues.createComment").map(({ body }) => body);
  assert.equal(comments.length, 2);
  assert.match(comments[0], /^Failed to create backport PR for `release-9\.9`\n\nError: /);
  assert.equal(comments[1], "Backport PR created for `release-0.4`: #100");
  assert.deepEqual(core.failed, ["Cherry-pick failed for: release-9.9"]);
});

test("the verified chain is built on the target commit the cherry-pick used", async (t) => {
  const fixture = repository(t);
  // The release branch advances on the server while the backport is running.
  git(fixture.origin, ["config", "user.name", "Release Manager"]);
  git(fixture.origin, ["config", "user.email", "release@example.com"]);
  const hook = path.join(fixture.origin, "hooks", "post-receive");
  fs.writeFileSync(hook, [
    "#!/bin/sh",
    "while read old new ref; do",
    "  case \"$ref\" in refs/heads/backport-*)",
    "    [ -f advanced ] && continue",
    "    tip=$(git rev-parse refs/heads/release-0.4)",
    "    next=$(git commit-tree \"$tip^{tree}\" -p \"$tip\" -m 'chore: advance release')",
    "    git update-ref refs/heads/release-0.4 \"$next\" \"$tip\"",
    "    touch advanced;;",
    "  esac",
    "done",
    "",
  ].join("\n"), { mode: 0o755 });
  const { github, calls } = fakeGitHub(fixture);

  const { results } = await backport(fixture, github, ["release-0.4"]);

  assert.equal(results[0].success, true, results[0].error);
  assert.notEqual(git(fixture.origin, ["rev-parse", "refs/heads/release-0.4"]), fixture.releaseTip);
  assert.deepEqual(named(calls, "git.createCommit").map(({ parents }) => parents), [[fixture.releaseTip]]);
  assert.equal(git(fixture.origin, ["rev-parse", `refs/heads/${BACKPORT_BRANCH}^`]), fixture.releaseTip);
});

test("refuses to overwrite a backport branch with a commit this workflow did not create", async (t) => {
  for (const [name, env] of [["human", HUMAN_ENV], ["amended bot commit", AMENDED_ENV], ["web edit", WEB_EDIT_ENV]]) {
    await t.test(name, async (t) => {
      const fixture = repository(t);
      const foreign = pushBranch(fixture, env);
      const { github, calls } = fakeGitHub(fixture);

      const { results, core } = await backport(fixture, github, ["release-0.4"]);

      assert.deepEqual(results, [{
        branch: "release-0.4",
        success: false,
        error: `${BACKPORT_BRANCH} has commits this workflow did not create; `
          + "merge or delete it before cherry-picking again",
      }]);
      assert.equal(git(fixture.origin, ["rev-parse", `refs/heads/${BACKPORT_BRANCH}`]), foreign);
      assert.deepEqual(named(calls, "git.createCommit"), []);
      assert.deepEqual(named(calls, "pulls.create"), []);
      assert.match(named(calls, "issues.createComment")[0].body, /^Failed to create backport PR for `release-0\.4`/);
      assert.deepEqual(core.failed, ["Cherry-pick failed for: release-0.4"]);
    });
  }
});

test("a retry replaces the unverified branch left by a run that stopped before re-creating it", async (t) => {
  const fixture = repository(t);
  const first = fakeGitHub(fixture, { failCreateCommit: true });
  const stopped = await backport(fixture, first.github, ["release-0.4"]);
  assert.equal(stopped.results[0].success, false);
  assert.equal(git(fixture.origin, ["log", "-1", "--format=%cn", `refs/heads/${BACKPORT_BRANCH}`]), "nvidia-backport-bot");
  const { github, calls } = fakeGitHub(fixture);

  const { results, core } = await backport(fixture, github, ["release-0.4"]);

  assert.equal(results[0].success, true, results[0].error);
  assert.deepEqual(core.failed, []);
  assert.equal(named(calls, "git.createCommit").length, 1);
  assert.equal(git(fixture.origin, ["log", "-1", "--format=%an", `refs/heads/${BACKPORT_BRANCH}`]), "github-actions[bot]");
  assert.equal(git(fixture.origin, ["show", `refs/heads/${BACKPORT_BRANCH}:app.txt`]) + "\n", PICKED);
});

test("a change already on the target is reported without a backport PR", async (t) => {
  const fixture = repository(t, { releaseHasChange: true });
  const { github, calls } = fakeGitHub(fixture);

  const { results, core } = await backport(fixture, github, ["release-0.4"]);

  assert.deepEqual(results, [{ branch: "release-0.4", success: true, alreadyOnTarget: true }]);
  assert.deepEqual(core.failed, []);
  assert.deepEqual(named(calls, "issues.createComment").map(({ body }) => body), [
    "The change from #42 is already on `release-0.4`; no backport PR is needed.",
  ]);
  assert.deepEqual(named(calls, "pulls.create"), []);
  assert.deepEqual(named(calls, "git.createCommit"), []);
  assert.deepEqual(named(calls, "git.updateRef"), []);
  assert.equal(git(fixture.origin, ["for-each-ref", `refs/heads/${BACKPORT_BRANCH}`]), "");
});

test("replaces a backport branch that holds only github-actions[bot] commits", async (t) => {
  const fixture = repository(t);
  pushBranch(fixture, BOT_ENV);
  const { github } = fakeGitHub(fixture);

  const { results, core } = await backport(fixture, github, ["release-0.4"]);

  assert.equal(results[0].success, true, results[0].error);
  assert.deepEqual(core.failed, []);
  assert.equal(git(fixture.origin, ["show", `refs/heads/${BACKPORT_BRANCH}:app.txt`]) + "\n", PICKED);
});

test("refuses an unmerged pull request before any git or GitHub write", async (t) => {
  const fixture = repository(t);
  const refs = git(fixture.origin, ["for-each-ref"]);
  const { github, calls } = fakeGitHub(fixture, { pull: pullRequest(fixture, { merged: false }) });

  await assert.rejects(
    () => backport(fixture, github, ["release-0.4"]),
    { message: "PR #42 is not merged; only merged pull requests are cherry-picked" },
  );
  assert.deepEqual(calls.map(({ name }) => name), ["pulls.get"]);
  assert.equal(git(fixture.origin, ["for-each-ref"]), refs);
});

test("refuses a merged pull request without a merge commit SHA", async (t) => {
  const fixture = repository(t);
  const { github, calls } = fakeGitHub(fixture, { pull: pullRequest(fixture, { merge_commit_sha: null }) });

  await assert.rejects(
    () => backport(fixture, github, ["release-0.4"]),
    { message: "PR #42 has no merge commit to cherry-pick" },
  );
  assert.deepEqual(calls.map(({ name }) => name), ["pulls.get"]);
});
