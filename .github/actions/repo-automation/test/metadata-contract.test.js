"use strict";

const assert = require("node:assert/strict");
const { Buffer } = require("node:buffer");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const { loadConfig } = require("../src/config.js");
const { createGitHubClient } = require("../src/github-client.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const event = JSON.parse(fs.readFileSync(
  path.join(__dirname, "fixtures", "events", "pull-request-target.json"),
  "utf8",
));
const marker = "<!-- repo-automation-policy:v1 -->";
const mutableEventSentinels = [
  "event-default-branch-must-not-be-used",
  "event-title-must-not-execute",
  "event-body-secret-sentinel-7c91",
  "event-command-sentinel",
  "event-author-must-not-be-used",
  "event-head-ref-must-not-be-used",
  "event-head-sha-must-not-be-used",
  "event-base-ref-must-not-be-used",
  "event-path-must-not-be-used",
  "event-executable-must-not-run",
  "event-config-must-not-be-used.yml",
  "refs/pull/42/merge",
  "event-command-must-not-execute",
  "event-artifact-must-not-be-used.tgz",
];
const readOperations = new Set([
  "getPullRequest",
  "listPullRequestFiles",
  "listPullRequestCommits",
  "listPullRequestReviews",
  "listRequestedReviewers",
  "listIssueLabels",
  "getContentAtDefaultBranch",
]);
const writeOperations = new Set([
  "requestReviewers",
  "addIssueLabel",
  "removeIssueLabel",
  "upsertPolicyComment",
]);

function signedCommit(overrides = {}) {
  return {
    sha: "signed-commit",
    commit: {
      author: { name: "Contributor", email: "contributor@example.com" },
      message: "feat: secure metadata\n\nSigned-off-by: Contributor <contributor@example.com>",
    },
    author: { login: "contributor" },
    parents: [{ sha: "parent" }],
    ...overrides,
  };
}

function metadataState(overrides = {}) {
  return {
    pullRequest: {
      number: 42,
      title: "feat: secure metadata automation",
      body: "live-private-body-secret-sentinel-b037",
      draft: true,
      author: "pr-author",
      headOid: "live-head-oid-6f9d",
    },
    filePages: [
      [{ path: "docs/guide.md", additions: 30, deletions: 10, status: "modified" }],
      [{ path: "pkg/gpu/mocknvml/model.go", additions: 8, deletions: 2, status: "modified" }],
    ],
    commitPages: [[signedCommit()]],
    reviewPages: [
      [{ user: "old-reviewer", state: "APPROVED", commitOid: "old-head" }],
      [{ user: "alice", state: "COMMENTED", commitOid: "live-head-oid-6f9d" }],
    ],
    requestedReviewers: ["alice", "outsider"],
    labels: [
      "kind/bug",
      "size/S",
      "area/ci",
      "lgtm",
      "approved",
      "do-not-merge/hold",
      "maintainer/custom",
    ],
    contents: {
      "/OWNERS": [
        "reviewers:",
        "  - pr-author",
        "  - alice",
        "  - bob",
        "approvers:",
        "  - alice",
        "  - bob",
        "",
      ].join("\n"),
      "/OWNERS_ALIASES": "aliases: {}\n",
    },
    comments: [],
    ...overrides,
  };
}

function expectedLabelChanges() {
  return {
    add: [
      "area/docs",
      "area/nvml-mock",
      "do-not-merge/work-in-progress",
      "kind/feature",
      "size/M",
    ],
    remove: ["area/ci", "kind/bug", "size/S"],
  };
}

function mutations(github) {
  return github.callOrder.filter(({ operation }) => writeOperations.has(operation));
}

function assertNoEventMutableValue(value) {
  const text = JSON.stringify(value);
  for (const sentinel of mutableEventSentinels) {
    assert.equal(text.includes(sentinel), false, `must not consume ${sentinel}`);
  }
}

test("metadata re-fetches live PR state and applies only the complete safe plan", async () => {
  const { runMetadata } = require("../src/modes/metadata.js");
  const github = createFakeGitHub(metadataState());

  const result = await runMetadata({
    event,
    github,
    config: loadConfig(repositoryRoot),
    dryRun: false,
  });

  assert.equal(result.headOid, "live-head-oid-6f9d");
  assert.equal(result.valid, true);
  assert.deepEqual(result.labels, expectedLabelChanges());
  assert.equal(result.reviewers.request.includes("pr-author"), false);
  assert.equal(result.reviewers.request.every((login) => ["alice", "bob"].includes(login)), true);
  assert.deepEqual(github.calls.getPullRequest, [{ prNumber: 42 }]);
  assert.deepEqual(github.calls.getContentAtDefaultBranch, [
    { path: "/OWNERS" },
    { path: "/OWNERS_ALIASES" },
  ]);
  assert.deepEqual(github.calls.addIssueLabel.map(({ label }) => label).sort(), expectedLabelChanges().add);
  assert.deepEqual(
    github.calls.removeIssueLabel.map(({ label }) => label).sort(),
    expectedLabelChanges().remove,
  );
  assert.equal(github.calls.requestReviewers.flatMap(({ reviewers }) => reviewers).includes("pr-author"), false);

  const snapshot = github.metadataSnapshot();
  for (const preserved of ["lgtm", "approved", "do-not-merge/hold", "maintainer/custom"] ) {
    assert.equal(snapshot.labels.includes(preserved), true, `${preserved} must be preserved`);
  }
  assert.equal(snapshot.comments.length, 1);
  assert.equal(snapshot.comments[0].body.split(marker).length - 1, 1);
  assert.match(snapshot.comments[0].body, /live-head-oid-6f9d/);
  assert.equal(JSON.stringify(result).includes("live-private-body-secret-sentinel-b037"), false);
  assertNoEventMutableValue({ calls: github.callOrder, result, snapshot });

  const firstWrite = github.callOrder.findIndex(({ operation }) => writeOperations.has(operation));
  assert.notEqual(firstWrite, -1);
  assert.equal(
    github.callOrder.slice(firstWrite).some(({ operation }) => readOperations.has(operation)),
    false,
    "all reads and planning must finish before the first write",
  );
});

test("metadata rerun updates the single marker comment deterministically", async () => {
  const { runMetadata } = require("../src/modes/metadata.js");
  const github = createFakeGitHub(metadataState());
  const options = { event, github, config: loadConfig(repositoryRoot), dryRun: false };

  await runMetadata(options);
  const firstBody = github.metadataSnapshot().comments[0].body;
  await runMetadata(options);
  const second = github.metadataSnapshot();

  assert.equal(second.comments.length, 1);
  assert.equal(second.comments[0].body, firstBody);
  assert.equal(github.calls.upsertPolicyComment.length, 2);
  assert.deepEqual(github.calls.addIssueLabel.length, expectedLabelChanges().add.length);
  assert.deepEqual(github.calls.removeIssueLabel.length, expectedLabelChanges().remove.length);
});

test("dry-run returns the complete plan and performs zero writes", async () => {
  const { runMetadata } = require("../src/modes/metadata.js");
  const github = createFakeGitHub(metadataState());

  const result = await runMetadata({
    event,
    github,
    config: loadConfig(repositoryRoot),
    dryRun: true,
  });

  assert.equal(result.headOid, "live-head-oid-6f9d");
  assert.deepEqual(result.labels, expectedLabelChanges());
  assert.equal(result.reviewers.request.length > 0, true);
  assert.match(result.comment.body, /repo-automation-policy:v1/);
  assert.deepEqual(mutations(github), []);
});

test("a read failure aborts before every mutation", async () => {
  const { runMetadata } = require("../src/modes/metadata.js");
  const failure = Object.assign(new Error("permanent files failure"), { status: 422 });
  const github = createFakeGitHub(metadataState({ failures: { listPullRequestFiles: failure } }));

  await assert.rejects(
    () => runMetadata({ event, github, config: loadConfig(repositoryRoot), dryRun: false }),
    /files failure/i,
  );
  assert.deepEqual(mutations(github), []);
});

test("invalid title, DCO, configuration, and ownership upsert diagnostics then fail apply", async (t) => {
  const validConfig = loadConfig(repositoryRoot);
  const cases = [
    ["title", metadataState({ pullRequest: { ...metadataState().pullRequest, title: "not conventional" } }), validConfig],
    ["DCO", metadataState({ commitPages: [[signedCommit({
      sha: "unsigned-commit",
      commit: {
        author: { name: "Contributor", email: "contributor@example.com" },
        message: "feat: unsigned",
      },
    })]] }), validConfig],
    ["ownership", metadataState({ contents: {
      "/OWNERS": "reviewers: [pr-author]\napprovers: [pr-author]\n",
      "/OWNERS_ALIASES": "aliases: {}\n",
    } }), validConfig],
    ["configuration", metadataState(), {
      ...validConfig,
      policy: { ...validConfig.policy, activeOwnerFiles: [] },
    }],
  ];

  for (const [name, state, config] of cases) {
    await t.test(name, async () => {
      const { runMetadata } = require("../src/modes/metadata.js");
      const github = createFakeGitHub(state);

      await assert.rejects(
        () => runMetadata({ event, github, config, dryRun: false }),
        new RegExp(name, "i"),
      );
      assert.equal(github.calls.upsertPolicyComment.length, 1);
      assert.match(github.calls.upsertPolicyComment[0].body, new RegExp(name, "i"));
      assert.equal(
        mutations(github).every(({ operation }) => operation === "upsertPolicyComment"),
        true,
      );
    });
  }
});

test("invalid dry-run reports failure but performs zero writes", async () => {
  const { runMetadata } = require("../src/modes/metadata.js");
  const github = createFakeGitHub(metadataState({
    pullRequest: { ...metadataState().pullRequest, title: "invalid title" },
  }));

  await assert.rejects(
    () => runMetadata({ event, github, config: loadConfig(repositoryRoot), dryRun: true }),
    /title/i,
  );
  assert.deepEqual(mutations(github), []);
});

test("GitHub boundary paginates and normalizes every metadata list", async () => {
  const endpointCalls = [];
  function endpoint(name, pages) {
    const fn = async (parameters) => {
      endpointCalls.push({ name, parameters });
      return { data: pages[(parameters.page ?? 1) - 1] ?? [] };
    };
    fn.pages = pages;
    fn.operationName = name;
    return fn;
  }
  const listFiles = endpoint("files", [
    [{ filename: "a.go", additions: 1, deletions: 2, status: "modified" }],
    [{ filename: "b.go", additions: 3, deletions: 4, status: "added" }],
  ]);
  const listCommits = endpoint("commits", [[signedCommit()], [signedCommit({ sha: "second" })]]);
  const listReviews = endpoint("reviews", [[{ user: { login: "alice" }, state: "APPROVED", commit_id: "head" }], []]);
  const listRequested = endpoint("requested", [[{ users: [{ login: "alice" }], teams: [] }], [{ users: [{ login: "bob" }], teams: [] }]]);
  const listLabels = endpoint("labels", [[{ name: "kind/bug" }], [{ name: "lgtm" }]]);
  const octokit = {
    rest: {
      pulls: {
        listFiles,
        listCommits,
        listReviews,
        listRequestedReviewers: listRequested,
      },
      issues: { listLabelsOnIssue: listLabels },
    },
    async paginate(fn, parameters) {
      const values = [];
      for (let page = 1; page <= fn.pages.length; page += 1) {
        const response = await fn({ ...parameters, page });
        if (fn.operationName === "requested") {
          values.push(...response.data.flatMap(({ users }) => users));
        } else {
          values.push(...response.data);
        }
      }
      return values;
    },
  };
  const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra");

  assert.deepEqual(await github.listPullRequestFiles(42), [
    { path: "a.go", additions: 1, deletions: 2, status: "modified" },
    { path: "b.go", additions: 3, deletions: 4, status: "added" },
  ]);
  assert.equal((await github.listPullRequestCommits(42)).length, 2);
  assert.deepEqual(await github.listPullRequestReviews(42), [
    { user: "alice", state: "APPROVED", commitOid: "head" },
  ]);
  assert.deepEqual(await github.listRequestedReviewers(42), ["alice", "bob"]);
  assert.deepEqual(await github.listIssueLabels(42), ["kind/bug", "lgtm"]);
  for (const name of ["files", "commits", "reviews", "requested", "labels"]) {
    assert.deepEqual(endpointCalls.filter((call) => call.name === name).map((call) => call.parameters.page), [1, 2]);
  }
});

test("GitHub boundary re-fetches PR and default branch content without an event ref", async () => {
  const calls = [];
  const octokit = {
    rest: {
      pulls: {
        async get(parameters) {
          calls.push({ operation: "pull", parameters });
          return { data: {
            number: 42,
            title: "feat: live",
            body: "private-live-body",
            draft: false,
            user: { login: "author" },
            head: { sha: "live-head" },
          } };
        },
      },
      repos: {
        async get(parameters) {
          calls.push({ operation: "repo", parameters });
          return { data: { default_branch: "live-main" } };
        },
        async getContent(parameters) {
          calls.push({ operation: "content", parameters });
          return { data: { type: "file", encoding: "base64", content: Buffer.from("aliases: {}\n").toString("base64") } };
        },
      },
    },
  };
  const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra");

  assert.deepEqual(await github.getPullRequest(42), {
    number: 42,
    title: "feat: live",
    body: "private-live-body",
    draft: false,
    author: "author",
    headOid: "live-head",
  });
  assert.equal(await github.getContentAtDefaultBranch("/OWNERS_ALIASES"), "aliases: {}\n");
  assert.deepEqual(calls.at(-1).parameters, {
    owner: "nvidia",
    repo: "k8s-test-infra",
    path: "OWNERS_ALIASES",
    ref: "live-main",
  });
  assertNoEventMutableValue(calls);
});

test("marker upsert paginates, creates once, updates on rerun, and rejects duplicates", async () => {
  const commentPages = [[{ id: 1, body: "unmanaged" }], []];
  const listedPages = [];
  const writes = [];
  const listComments = async ({ page }) => {
    listedPages.push(page);
    return { data: commentPages[page - 1] };
  };
  const octokit = {
    rest: {
      issues: {
        listComments,
        async createComment({ body }) {
          writes.push("create");
          const created = { id: 2, body };
          commentPages[1].push(created);
          return { data: created };
        },
        async updateComment({ comment_id: id, body }) {
          writes.push("update");
          commentPages.flat().find((comment) => comment.id === id).body = body;
          return { data: { id, body } };
        },
      },
    },
    async paginate(fn, parameters) {
      const values = [];
      for (let page = 1; page <= commentPages.length; page += 1) {
        values.push(...(await fn({ ...parameters, page })).data);
      }
      return values;
    },
  };
  const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra");
  const firstBody = `${marker}\nhead: one`;
  const secondBody = `${marker}\nhead: two`;

  assert.deepEqual(await github.upsertPolicyComment(42, marker, firstBody), { action: "created", id: 2 });
  assert.deepEqual(await github.upsertPolicyComment(42, marker, secondBody), { action: "updated", id: 2 });
  assert.deepEqual(writes, ["create", "update"]);
  assert.equal(commentPages.flat().length, 2);
  assert.deepEqual(listedPages, [1, 2, 1, 2]);
  commentPages[0].push({ id: 3, body: `${marker}\nduplicate` });
  await assert.rejects(() => github.upsertPolicyComment(42, marker, secondBody), /duplicate/i);
  assert.deepEqual(writes, ["create", "update"]);
});

test("GitHub boundary uses one-label mutations and never replaces the issue label set", async () => {
  const calls = [];
  const octokit = {
    rest: {
      issues: {
        async addLabels(parameters) {
          calls.push({ operation: "add", parameters });
          return { data: [] };
        },
        async removeLabel(parameters) {
          calls.push({ operation: "remove", parameters });
          return { data: {} };
        },
        async setLabels() {
          assert.fail("metadata must never replace the complete issue label set");
        },
      },
      pulls: {
        async requestReviewers(parameters) {
          calls.push({ operation: "reviewers", parameters });
          return { data: {} };
        },
      },
    },
  };
  const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra");

  await github.addIssueLabel(42, "kind/feature");
  await github.removeIssueLabel(42, "kind/bug");
  await github.requestReviewers(42, ["alice"]);

  assert.deepEqual(calls, [
    {
      operation: "add",
      parameters: { owner: "nvidia", repo: "k8s-test-infra", issue_number: 42, labels: ["kind/feature"] },
    },
    {
      operation: "remove",
      parameters: { owner: "nvidia", repo: "k8s-test-infra", issue_number: 42, name: "kind/bug" },
    },
    {
      operation: "reviewers",
      parameters: { owner: "nvidia", repo: "k8s-test-infra", pull_number: 42, reviewers: ["alice"] },
    },
  ]);
});

test("GitHub boundary retries transient idempotent operations only and never unsafe errors", async () => {
  let readAttempts = 0;
  let unsafeWriteAttempts = 0;
  const transient = Object.assign(new Error("transient"), { status: 503 });
  const unsafe = Object.assign(new Error("validation"), { status: 422 });
  const octokit = {
    rest: {
      pulls: {
        async get() {
          readAttempts += 1;
          if (readAttempts < 3) throw transient;
          return { data: {
            number: 42,
            title: "feat: live",
            body: "",
            draft: false,
            user: { login: "author" },
            head: { sha: "head" },
          } };
        },
        async requestReviewers() {
          unsafeWriteAttempts += 1;
          throw unsafe;
        },
      },
    },
  };
  const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra", {
    maxAttempts: 3,
    sleep: async () => {},
  });

  assert.equal((await github.getPullRequest(42)).headOid, "head");
  assert.equal(readAttempts, 3);
  await assert.rejects(() => github.requestReviewers(42, ["alice"]), /validation/i);
  assert.equal(unsafeWriteAttempts, 1);
});

test("index dispatches metadata, writes summary output, and never executes the import entry", async () => {
  const { run } = require("../src/index.js");
  const githubClient = createFakeGitHub(metadataState());
  const outputs = [];
  const core = {
    getInput(name) {
      return { mode: "metadata", "pr-number": "42" }[name] ?? "";
    },
    getBooleanInput(name) {
      assert.equal(name, "dry-run");
      return true;
    },
    setOutput(name, value) {
      outputs.push({ name, value });
    },
  };

  const result = await run({
    core,
    owner: "nvidia",
    repo: "k8s-test-infra",
    workspace: repositoryRoot,
    event,
    githubClient,
  });

  assert.equal(result.headOid, "live-head-oid-6f9d");
  assert.deepEqual(outputs, [{ name: "summary", value: JSON.stringify(result) }]);
  assertNoEventMutableValue(outputs);
});

test("index emits the diagnostic summary before propagating metadata failure", async () => {
  const { run } = require("../src/index.js");
  const outputs = [];
  const core = {
    getInput(name) {
      return { mode: "metadata", "pr-number": "42" }[name] ?? "";
    },
    getBooleanInput() {
      return false;
    },
    setOutput(name, value) {
      outputs.push({ name, value });
    },
  };
  const githubClient = createFakeGitHub(metadataState({
    pullRequest: { ...metadataState().pullRequest, title: "invalid title" },
  }));

  await assert.rejects(() => run({
    core,
    owner: "nvidia",
    repo: "k8s-test-infra",
    workspace: repositoryRoot,
    event,
    githubClient,
  }), /title/i);
  assert.equal(outputs.length, 1);
  assert.equal(outputs[0].name, "summary");
  const summary = JSON.parse(outputs[0].value);
  assert.equal(summary.valid, false);
  assert.equal(summary.title.valid, false);
  assert.equal(summary.headOid, "live-head-oid-6f9d");
  assertNoEventMutableValue(outputs);
});
