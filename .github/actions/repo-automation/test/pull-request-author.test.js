"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");

const { loadConfig } = require("../src/config.js");
const { runBackport } = require("../src/modes/backport.js");
const { runCommand } = require("../src/modes/command.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const HEAD = "6".repeat(40);
const MERGE_OID = "2".repeat(40);
const TARGET_BRANCH = "release-1.2";
const BOT_AUTHORS = ["dependabot[bot]", "renovate[bot]"];
const UNSAFE_AUTHORS = ["dependabot\u200b[bot]", "bad--login[bot]", "[bot]", "alice\n"];

function commandState(author) {
  return {
    pullRequest: {
      number: 42,
      nodeId: "PR_kwDOABC42",
      title: "chore(deps): bump yaml",
      body: "",
      draft: false,
      author,
      headOid: HEAD,
      state: "open",
      baseBranch: "main",
      baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    },
    files: [{ path: "go.mod", additions: 1, deletions: 1, status: "modified" }],
    labels: [],
    issueComments: [
      { id: 9001, issueNumber: 42, body: "/lgtm", author: "alice", authorType: "User", edited: false },
      { id: 9002, issueNumber: 42, body: "/hold", author: "carol", authorType: "User", edited: false },
    ],
    collaboratorAccess: {
      alice: { liveCollaborator: false, permission: "read" },
      carol: { liveCollaborator: true, permission: "write" },
    },
    defaultBranchRevision: "7".repeat(40),
    contents: {
      "/OWNERS": "reviewers: [alice]\napprovers: [bob]\n",
      "/OWNERS_ALIASES": "aliases: {}\n",
    },
    comments: [],
  };
}

function command(github, commentId) {
  return runCommand({
    event: {
      action: "created",
      repository: {
        owner: { login: "NVIDIA" },
        name: "k8s-test-infra",
        full_name: "NVIDIA/k8s-test-infra",
      },
      issue: { number: 42, pull_request: { url: "event-url" } },
      comment: { id: commentId },
    },
    github,
    config: loadConfig(repositoryRoot),
    dryRun: false,
    now: () => "2026-09-17T12:00:00.000Z",
  });
}

function mergedPullRequest(author) {
  return {
    number: 42,
    nodeId: "PR_node_42",
    title: "chore(deps): bump yaml",
    body: "",
    draft: false,
    author,
    headOid: "7".repeat(40),
    state: "closed",
    merged: true,
    mergeCommitOid: MERGE_OID,
    baseBranch: "main",
    baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
  };
}

async function git(args) {
  const command = args.join(" ");
  if (command === `rev-list --parents --max-count=1 ${MERGE_OID}`) {
    return { stdout: `${MERGE_OID} ${"3".repeat(40)}\n`, stderr: "" };
  }
  if (command === "rev-parse HEAD") return { stdout: `${"4".repeat(40)}\n`, stderr: "" };
  if (args[0] === "merge-base") {
    const error = new Error("not ancestor");
    error.exitCode = 1;
    throw error;
  }
  return { stdout: "", stderr: "" };
}

function backport(author) {
  const github = createFakeGitHub({
    pullRequests: [mergedPullRequest(author), mergedPullRequest(author)],
    branches: { [TARGET_BRANCH]: "1".repeat(40) },
    backportPullRequests: [],
  });
  return {
    github,
    run: () => runBackport({
      github,
      git,
      config: loadConfig(repositoryRoot),
      dryRun: false,
      prNumber: "42",
      targetBranch: TARGET_BRANCH,
      repository: "nvidia/k8s-test-infra",
    }),
  };
}

test("pull request author context accepts human and app bot logins only", () => {
  const { validAuthorContext } = require("../src/pull-request-author.js");
  for (const login of ["alice", "Pr-Author", "a".repeat(39), ...BOT_AUTHORS, "Dependabot[bot]"]) {
    assert.equal(validAuthorContext(login), true, login);
  }
  for (const login of [...UNSAFE_AUTHORS, "", "-alice", "alice-", "a".repeat(40),
    "dependabot[bot][bot]", "dependabot[BOT]", null, 42]) {
    assert.equal(validAuthorContext(login), false, String(login));
  }
});

for (const author of BOT_AUTHORS) {
  test(`reviewer and hold commands apply on a pull request authored by ${author}`, async () => {
    const github = createFakeGitHub(commandState(author));

    const lgtm = await command(github, 9001);
    const hold = await command(github, 9002);

    assert.equal(lgtm.status, "complete");
    assert.deepEqual(lgtm.commands.map(({ name, status }) => [name, status]), [["lgtm", "applied"]]);
    assert.equal(hold.status, "complete");
    assert.deepEqual(hold.commands.map(({ name, status }) => [name, status]), [["hold", "applied"]]);
    assert.deepEqual(github.snapshot().map(({ name }) => name).sort(), [
      "do-not-merge/hold", "do-not-merge/needs-approval", "lgtm",
    ]);
  });

  test(`backport accepts a merged pull request authored by ${author}`, async () => {
    const { github, run } = backport(author);

    const result = await run();

    assert.equal(result.status, "complete");
    assert.equal(result.outcome, "created");
    assert.equal(github.calls.createBackportPullRequest.length, 1);
    assert.match(github.calls.createBackportPullRequest[0].body, new RegExp(`Original author: @${author.replace(/[[\]]/g, "\\$&")}\n`));
  });
}

for (const author of UNSAFE_AUTHORS) {
  test(`commands and backports reject the unsafe author ${JSON.stringify(author)}`, async () => {
    const github = createFakeGitHub(commandState(author));
    await assert.rejects(() => command(github, 9001), {
      message: "live pull request state or base repository is invalid",
    });
    assert.deepEqual(github.calls.addPolicyLabel, []);

    const source = backport(author);
    await assert.rejects(source.run, { message: "source pull request state is invalid" });
    assert.deepEqual(source.github.calls.createBackportPullRequest, []);
  });
}
