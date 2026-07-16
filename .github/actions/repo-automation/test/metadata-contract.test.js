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
  "getDefaultBranchRevision",
  "getContentAtRevision",
  "planPolicyComment",
]);
const writeOperations = new Set([
  "requestReviewers",
  "addIssueLabel",
  "removeIssueLabel",
  "upsertPolicyComment",
]);

function signedCommit(overrides = {}) {
  return {
    sha: "6666666666666666666666666666666666666666",
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
      headOid: "6666666666666666666666666666666666666666",
      state: "open",
      baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    },
    filePages: [
      [{ path: "docs/guide.md", additions: 30, deletions: 10, status: "modified" }],
      [{ path: "pkg/gpu/mocknvml/model.go", additions: 8, deletions: 2, status: "modified" }],
    ],
    commitPages: [[signedCommit()]],
    reviewPages: [
      [{ user: "old-reviewer", state: "APPROVED", commitOid: "old-head" }],
      [{ user: "alice", state: "COMMENTED", commitOid: "6666666666666666666666666666666666666666" }],
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

  assert.equal(result.headOid, "6666666666666666666666666666666666666666");
  assert.equal(result.valid, true);
  assert.deepEqual(result.labels, expectedLabelChanges());
  assert.equal(result.reviewers.request.includes("pr-author"), false);
  assert.equal(result.reviewers.request.every((login) => ["alice", "bob"].includes(login)), true);
  assert.deepEqual(github.calls.getPullRequest, [{ prNumber: 42 }, { prNumber: 42 }]);
  assert.deepEqual(github.calls.getDefaultBranchRevision, [{}]);
  assert.deepEqual(github.calls.getContentAtRevision, [
    { path: "/OWNERS", revision: "base-commit-oid-91ab" },
    { path: "/OWNERS_ALIASES", revision: "base-commit-oid-91ab" },
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
  assert.match(snapshot.comments[0].body, /6666666666666666666666666666666666666666/);
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

  assert.equal(result.headOid, "6666666666666666666666666666666666666666");
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
    ["DCO", metadataState({ pullRequest: {
      ...metadataState().pullRequest,
      headOid: "7".repeat(40),
    }, commitPages: [[signedCommit({
      sha: "7".repeat(40),
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
            state: "open",
            user: { login: "author" },
            head: { sha: "live-head" },
            base: { repo: { name: "k8s-test-infra", owner: { login: "nvidia" } } },
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
          return { data: { type: "file", sha: "alias-blob" } };
        },
        async getBranch(parameters) {
          calls.push({ operation: "branch", parameters });
          return { data: { commit: { sha: "live-base-oid" } } };
        },
      },
      git: {
        async getCommit(parameters) {
          calls.push({ operation: "commit", parameters });
          return { data: { tree: { sha: "root-tree" } } };
        },
        async getTree(parameters) {
          calls.push({ operation: "tree", parameters });
          return { data: { truncated: false, tree: [{
            path: "OWNERS_ALIASES", mode: "100644", type: "blob", sha: "alias-blob",
          }] } };
        },
        async getBlob(parameters) {
          calls.push({ operation: "blob", parameters });
          return { data: { encoding: "base64", content: Buffer.from("aliases: {}\n").toString("base64") } };
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
    state: "open",
    baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
  });
  assert.equal(await github.getContentAtDefaultBranch("/OWNERS_ALIASES"), "aliases: {}\n");
  assert.deepEqual(calls.find(({ operation }) => operation === "content").parameters, {
    owner: "nvidia",
    repo: "k8s-test-infra",
    path: "OWNERS_ALIASES",
    ref: "live-base-oid",
  });
  assertNoEventMutableValue(calls);
});

test("marker upsert paginates, creates once, updates on rerun, and rejects duplicates", async () => {
  const commentPages = [[{ id: 1, body: "unmanaged", user: { login: "someone", type: "User" } }], []];
  const listedPages = [];
  const writes = [];
  const listComments = async ({ page }) => {
    listedPages.push(page);
    return { data: commentPages[page - 1] };
  };
  const octokit = {
    rest: {
      users: { async getAuthenticated() { assert.fail("installation tokens cannot call GET /user"); } },
      issues: {
        listComments,
        async createComment({ body }) {
          writes.push("create");
          const created = { id: 2, body, user: { login: "github-actions[bot]", type: "Bot" } };
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
  commentPages[0].push({
    id: 3,
    body: `${marker}\nduplicate`,
    user: { login: "github-actions[bot]", type: "Bot" },
  });
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
            state: "open",
            user: { login: "author" },
            head: { sha: "head" },
            base: { repo: { name: "k8s-test-infra", owner: { login: "nvidia" } } },
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

  assert.equal(result.headOid, "6666666666666666666666666666666666666666");
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
  assert.equal(summary.headOid, "6666666666666666666666666666666666666666");
  assertNoEventMutableValue(outputs);
});

test("metadata fences the commit snapshot and final live PR before every write", async (t) => {
  const { runMetadata } = require("../src/modes/metadata.js");
  const base = metadataState().pullRequest;
  const cases = [
    ["commit snapshot", metadataState({ commitPages: [[signedCommit({ sha: "stale-head" })]] })],
    ["head changed", metadataState({ pullRequests: [base, { ...base, headOid: "new-head-oid" }] })],
    ["PR closed", metadataState({ pullRequests: [base, { ...base, state: "closed" }] })],
    ["base repository changed", metadataState({ pullRequests: [
      base,
      { ...base, baseRepository: { owner: "attacker", repo: "fork" } },
    ] })],
  ];

  for (const [name, state] of cases) {
    await t.test(name, async () => {
      const github = createFakeGitHub(state);
      await assert.rejects(
        () => runMetadata({ event, github, config: loadConfig(repositoryRoot), dryRun: false }),
        /head|state|repository|snapshot|stale/i,
      );
      assert.deepEqual(mutations(github), []);
    });
  }
});

test("metadata rejects non-PR events and live base-repository mismatch before writes", async (t) => {
  const { runMetadata } = require("../src/modes/metadata.js");
  const cases = [
    ["non-PR event", (() => {
      const value = JSON.parse(JSON.stringify(event));
      delete value.pull_request;
      return value;
    })(), metadataState()],
    ["base mismatch", event, metadataState({ pullRequest: {
      ...metadataState().pullRequest,
      baseRepository: { owner: "attacker", repo: "fork" },
    } })],
  ];
  for (const [name, inputEvent, state] of cases) {
    await t.test(name, async () => {
      const github = createFakeGitHub(state);
      await assert.rejects(
        () => runMetadata({ event: inputEvent, github, config: loadConfig(repositoryRoot), dryRun: false }),
        /pull request|repository/i,
      );
      assert.deepEqual(mutations(github), []);
    });
  }
});

test("marker ownership ignores hostile markers and fails only on owned duplicates", async () => {
  const markerBody = `${marker}\nowned`;
  const pages = [[
    { id: 1, body: `${marker}\nhostile`, user: { login: "attacker", type: "User" } },
    { id: 2, body: markerBody, user: { login: "github-actions[bot]", type: "Bot" } },
  ]];
  const writes = [];
  const octokit = {
    rest: {
      users: { async getAuthenticated() { assert.fail("installation tokens cannot call GET /user"); } },
      issues: {
        async listComments() { return { data: pages[0] }; },
        async updateComment({ comment_id: id }) { writes.push(id); return { data: { id } }; },
        async createComment() { assert.fail("owned comment already exists"); },
      },
    },
    async paginate(fn, parameters) { return (await fn(parameters)).data; },
  };
  const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra");

  assert.deepEqual(await github.upsertPolicyComment(42, marker, markerBody), {
    action: "updated", id: 2,
  });
  assert.deepEqual(writes, [2]);
  pages[0].push({
    id: 3,
    body: markerBody,
    user: { login: "github-actions[bot]", type: "Bot" },
  });
  await assert.rejects(() => github.upsertPolicyComment(42, marker, markerBody), /duplicate/i);
});

test("ambiguous policy-comment creation is not retried and rerun updates the owned comment", async () => {
  const comments = [];
  let creates = 0;
  const octokit = {
    rest: {
      users: { async getAuthenticated() { assert.fail("installation tokens cannot call GET /user"); } },
      issues: {
        async listComments() { return { data: comments }; },
        async createComment({ body }) {
          creates += 1;
          comments.push({
            id: 7,
            body,
            user: { login: "github-actions[bot]", type: "Bot" },
          });
          throw Object.assign(new Error("socket closed"), { code: "ECONNRESET" });
        },
        async updateComment({ comment_id: id, body }) { return { data: { id, body } }; },
      },
    },
    async paginate(fn, parameters) { return (await fn(parameters)).data; },
  };
  const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra", {
    sleep: async () => assert.fail("comment creation must not retry"),
  });
  const body = `${marker}\nhead`;
  await assert.rejects(() => github.upsertPolicyComment(42, marker, body), /createPolicyComment/);
  assert.equal(creates, 1);
  assert.deepEqual(await github.upsertPolicyComment(42, marker, body), { action: "updated", id: 7 });
});

test("real configuration load failures produce bounded diagnostics before failure", async (t) => {
  const { run } = require("../src/index.js");
  for (const dryRun of [true, false]) {
    await t.test(dryRun ? "dry-run" : "apply", async () => {
      const githubClient = createFakeGitHub(metadataState());
      const outputs = [];
      const core = {
        getInput: (name) => name === "mode" ? "metadata" : "",
        getBooleanInput: () => dryRun,
        setOutput: (name, value) => outputs.push({ name, value }),
      };
      await assert.rejects(() => run({
        core,
        owner: "nvidia",
        repo: "k8s-test-infra",
        workspace: path.join(__dirname, "fixtures", "missing-config-root"),
        event,
        githubClient,
      }), /configuration/i);
      assert.equal(outputs.length, 1);
      assert.equal(JSON.parse(outputs[0].value).configuration.valid, false);
      assert.equal(githubClient.calls.upsertPolicyComment.length, dryRun ? 0 : 1);
      assert.equal(mutations(githubClient).every(({ operation }) => operation === "upsertPolicyComment"), true);
    });
  }
});

test("default-branch policy content uses one immutable commit and rejects non-blobs", async () => {
  const calls = [];
  const contents = new Map([
    ["OWNERS", { type: "file", sha: "blob-owners" }],
    ["OWNERS_ALIASES", { type: "file", sha: "blob-aliases" }],
  ]);
  const blobs = new Map([
    ["blob-owners", "reviewers: [alice]\n"],
    ["blob-aliases", "aliases: {}\n"],
  ]);
  const octokit = { rest: {
    repos: {
      async get() { calls.push("repo"); return { data: { default_branch: "main" } }; },
      async getBranch({ branch }) { calls.push(`branch:${branch}`); return { data: { commit: { sha: "base-oid" } } }; },
      async getContent({ path: contentPath, ref }) {
        calls.push(`content:${contentPath}:${ref}`);
        return { data: contents.get(contentPath) };
      },
    },
    git: {
      async getCommit({ commit_sha: sha }) {
        calls.push(`commit:${sha}`);
        return { data: { tree: { sha: "root-tree" } } };
      },
      async getTree({ tree_sha: sha }) {
        calls.push(`tree:${sha}`);
        return { data: { truncated: false, tree: [
          { path: "OWNERS", mode: "100644", type: "blob", sha: "blob-owners" },
          { path: "OWNERS_ALIASES", mode: "100644", type: "blob", sha: "blob-aliases" },
        ] } };
      },
      async getBlob({ file_sha: sha }) {
        calls.push(`blob:${sha}`);
        const text = blobs.get(sha);
        return { data: { encoding: "base64", content: Buffer.from(text).toString("base64"), size: Buffer.byteLength(text) } };
      },
    },
  } };
  const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra");
  const revision = await github.getDefaultBranchRevision();
  assert.equal(revision, "base-oid");
  assert.match(await github.getContentAtRevision("/OWNERS", revision), /alice/);
  assert.match(await github.getContentAtRevision("/OWNERS_ALIASES", revision), /aliases/);
  assert.deepEqual(calls.slice(0, 2), ["repo", "branch:main"]);
  assert.equal(calls.filter((call) => call.startsWith("content:")).every((call) => call.endsWith(":base-oid")), true);

  for (const shape of [
    { type: "file", sha: "blob-owners", target: "secret/OWNERS" },
    { type: "file", sha: "blob-owners", submodule_git_url: "https://example.invalid/repo" },
    { type: "dir", sha: "tree" },
  ]) {
    contents.set("OWNERS", shape);
    await assert.rejects(() => github.getContentAtRevision("/OWNERS", revision), /regular blob/i);
  }
});

function createTreeContentClient(rootEntries, options = {}) {
  const calls = [];
  const contentByPath = options.contentByPath ?? {
    OWNERS: { type: "file", sha: "owners-blob" },
    OWNERS_ALIASES: { type: "file", sha: "aliases-blob" },
  };
  const textBySha = options.textBySha ?? {
    "owners-blob": "reviewers: [alice]\n",
    "aliases-blob": "aliases: {}\n",
    "transparent-target": "reviewers: [attacker]\n",
  };
  const trees = options.trees ?? { "root-tree": rootEntries };
  const octokit = { rest: {
    git: {
      async getCommit({ commit_sha: revision }) {
        calls.push(`commit:${revision}`);
        return { data: { tree: { sha: "root-tree" } } };
      },
      async getTree({ tree_sha: sha }) {
        calls.push(`tree:${sha}`);
        return {
          data: {
            tree: trees[sha] ?? [],
            truncated: options.truncatedTrees?.includes(sha) ?? false,
          },
        };
      },
      async getBlob({ file_sha: sha }) {
        calls.push(`blob:${sha}`);
        const text = textBySha[sha] ?? "aliases: {}\n";
        return { data: {
          encoding: "base64",
          content: Buffer.from(text).toString("base64"),
          size: Buffer.byteLength(text),
        } };
      },
    },
    repos: {
      async getContent({ path: contentPath, ref }) {
        calls.push(`content:${contentPath}:${ref}`);
        return { data: contentByPath[contentPath] };
      },
    },
  } };
  return {
    calls,
    github: createGitHubClient(octokit, "nvidia", "k8s-test-infra"),
  };
}

test("policy paths resolve to exact regular Git tree blobs", async (t) => {
  const regularEntries = [
    { path: "OWNERS", mode: "100644", type: "blob", sha: "owners-blob" },
    { path: "OWNERS_ALIASES", mode: "100644", type: "blob", sha: "aliases-blob" },
  ];
  const regular = createTreeContentClient(regularEntries);
  assert.match(await regular.github.getContentAtRevision("/OWNERS", "base-oid"), /alice/);
  assert.match(await regular.github.getContentAtRevision("/OWNERS_ALIASES", "base-oid"), /aliases/);
  assert.equal(regular.calls.filter((call) => call === "commit:base-oid").length, 1);
  assert.equal(regular.calls.filter((call) => call === "tree:root-tree").length, 1);

  const cases = [
    ["symlink", [{ path: "OWNERS", mode: "120000", type: "blob", sha: "link-blob" }], {
      contentByPath: { OWNERS: { type: "file", sha: "transparent-target" } },
    }],
    ["gitlink", [{ path: "OWNERS", mode: "160000", type: "commit", sha: "submodule" }]],
    ["tree", [{ path: "OWNERS", mode: "040000", type: "tree", sha: "owners-tree" }]],
    ["missing", [{ path: "OTHER", mode: "100644", type: "blob", sha: "other" }]],
    ["duplicate", [
      { path: "OWNERS", mode: "100644", type: "blob", sha: "owners-blob" },
      { path: "OWNERS", mode: "100644", type: "blob", sha: "other-blob" },
    ]],
    ["truncated", regularEntries, { truncatedTrees: ["root-tree"] }],
    ["SHA mismatch", regularEntries, {
      contentByPath: { OWNERS: { type: "file", sha: "different-blob" } },
    }],
  ];
  for (const [name, entries, options] of cases) {
    await t.test(name, async () => {
      const candidate = createTreeContentClient(entries, options);
      await assert.rejects(
        () => candidate.github.getContentAtRevision("/OWNERS", "base-oid"),
        /regular|tree|path|ambiguous|SHA/i,
      );
      if (name === "symlink") {
        assert.equal(candidate.calls.some((call) => call.startsWith("content:")), false);
        assert.equal(candidate.calls.some((call) => call.startsWith("blob:")), false);
      }
    });
  }
});

test("transparent internal OWNERS symlink fails metadata before every mutation", async () => {
  const { runMetadata } = require("../src/modes/metadata.js");
  const fake = createFakeGitHub(metadataState());
  const tree = createTreeContentClient(
    [{ path: "OWNERS", mode: "120000", type: "blob", sha: "link-blob" }],
    { contentByPath: { OWNERS: { type: "file", sha: "transparent-target" } } },
  );
  const github = {
    ...fake,
    getDefaultBranchRevision: async () => "base-oid",
    getContentAtRevision: tree.github.getContentAtRevision,
  };

  await assert.rejects(
    () => runMetadata({ event, github, config: loadConfig(repositoryRoot), dryRun: false }),
    /regular|tree|symlink/i,
  );
  assert.deepEqual(mutations(fake), []);
  assert.equal(tree.calls.some((call) => call.startsWith("content:")), false);
  assert.equal(tree.calls.some((call) => call.startsWith("blob:")), false);
});

test("mutation failures attach bounded partial state and rerun reconciles", async (t) => {
  const { runMetadata } = require("../src/modes/metadata.js");
  for (const operation of ["addIssueLabel", "removeIssueLabel", "requestReviewers", "upsertPolicyComment"]) {
    await t.test(operation, async () => {
      const failure = Object.assign(new Error("hostile\nbody secret"), { status: 422 });
      const github = createFakeGitHub(metadataState({ failures: { [operation]: failure } }));
      let caught;
      try {
        await runMetadata({ event, github, config: loadConfig(repositoryRoot), dryRun: false });
      } catch (error) {
        caught = error;
      }
      assert.ok(caught?.summary, "mutation error must carry a summary");
      assert.equal(caught.summary.apply.status, "partial");
      assert.equal(caught.summary.apply.failed.operation, operation);
      assert.equal(JSON.stringify(caught.summary).includes("secret"), false);

      const rerun = await runMetadata({ event, github, config: loadConfig(repositoryRoot), dryRun: false });
      assert.equal(rerun.apply.status, "complete");
      const snapshot = github.metadataSnapshot();
      for (const label of expectedLabelChanges().add) assert.equal(snapshot.labels.includes(label), true);
      for (const label of expectedLabelChanges().remove) assert.equal(snapshot.labels.includes(label), false);
      assert.equal(snapshot.requestedReviewers.includes("bob"), true);
      assert.equal(snapshot.comments.length, 1);
    });
  }
});

test("retry policy handles transports and server-directed rate limits only", async (t) => {
  const cases = [
    ["network", Object.assign(new Error("reset"), { code: "ECONNRESET" }), 100],
    ["429 retry-after", Object.assign(new Error("limited"), {
      status: 429, response: { headers: { "retry-after": "2" } },
    }), 2000],
    ["403 reset", Object.assign(new Error("limited"), {
      status: 403, response: { headers: { "x-ratelimit-remaining": "0", "x-ratelimit-reset": "1010" } },
    }), 10000],
  ];
  for (const [name, failure, expectedDelay] of cases) {
    await t.test(name, async () => {
      let attempts = 0;
      const sleeps = [];
      const octokit = { rest: { pulls: { async get() {
        attempts += 1;
        if (attempts === 1) throw failure;
        return { data: {
          number: 42, title: "feat: live", body: "", draft: false, state: "open",
          user: { login: "author" }, head: { sha: "head" },
          base: { repo: { name: "k8s-test-infra", owner: { login: "nvidia" } } },
        } };
      } } } };
      const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra", {
        sleep: async (value) => sleeps.push(value), now: () => 1000 * 1000,
      });
      await github.getPullRequest(42);
      assert.equal(attempts, 2);
      assert.deepEqual(sleeps, [expectedDelay]);
    });
  }

  await t.test("422 and sanitized message", async () => {
    let attempts = 0;
    const octokit = { rest: { pulls: { async requestReviewers() {
      attempts += 1;
      throw Object.assign(new Error("identity@example.com\nsecret-body"), { status: 422 });
    } } } };
    const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra", { sleep: async () => {} });
    await assert.rejects(() => github.requestReviewers(42, ["alice"]), (error) => {
      assert.equal(error.message.includes("identity@example.com"), false);
      assert.equal(error.message.includes("secret-body"), false);
      assert.equal(error.message.length < 256, true);
      return true;
    });
    assert.equal(attempts, 1);
  });

  await t.test("remove 404 is success", async () => {
    const octokit = { rest: { issues: { async removeLabel() {
      throw Object.assign(new Error("missing"), { status: 404 });
    } } } };
    const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra");
    await github.removeIssueLabel(42, "Kind/Bug");
  });
});
