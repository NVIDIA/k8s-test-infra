"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");

const { loadConfig } = require("../src/config.js");
const { runCommand } = require("../src/modes/command.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const HEAD = "6".repeat(40);
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
}

for (const author of UNSAFE_AUTHORS) {
  test(`commands reject the unsafe author ${JSON.stringify(author)}`, async () => {
    const github = createFakeGitHub(commandState(author));
    await assert.rejects(() => command(github, 9001), {
      message: "live pull request state or base repository is invalid",
    });
    assert.deepEqual(github.calls.addPolicyLabel, []);
  });
}
