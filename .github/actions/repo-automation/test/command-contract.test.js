"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const { serializePolicyState } = require("../src/commands/state.js");
const { loadConfig } = require("../src/config.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const event = JSON.parse(fs.readFileSync(
  path.join(__dirname, "fixtures", "events", "issue-comment.json"),
  "utf8",
));
const headOid = "6".repeat(40);
const oldOid = "5".repeat(40);
const marker = "<!-- repo-automation-policy:v1 -->";
const commandMarker = "<!-- repo-automation-command-summary:v1 -->";
const writeOperations = new Set([
  "addAssignees",
  "removeAssignees",
  "requestReviewers",
  "rerunFailedJobs",
  "addIssueLabel",
  "removeIssueLabel",
  "addPolicyLabel",
  "removePolicyLabel",
  "upsertPolicyComment",
]);

function stateMarker(state = { headOid, lgtm: null, lastRetest: null }) {
  return `${marker}\n${serializePolicyState(state)}\n## Repository automation policy\n`;
}

function approvedReview(overrides = {}) {
  return {
    id: 101,
    user: "alice",
    state: "APPROVED",
    commitOid: headOid,
    submittedAt: "2026-07-16T08:00:00.000Z",
    ...overrides,
  };
}

function trustedRun(id, overrides = {}) {
  return {
    id,
    headOid,
    status: "completed",
    conclusion: "failure",
    workflowPath: ".github/workflows/automation-ci.yml",
    workflowSourceRef: "main",
    event: "pull_request",
    prNumber: 42,
    repository: "nvidia/k8s-test-infra",
    ...overrides,
  };
}

function apiRun(id, overrides = {}) {
  return {
    id,
    head_sha: headOid,
    status: "completed",
    conclusion: "failure",
    path: ".github/workflows/automation-ci.yml@main",
    event: "pull_request",
    pull_requests: [{ number: 42 }],
    repository: { full_name: "NVIDIA/k8s-test-infra" },
    ...overrides,
  };
}

function commandState(overrides = {}) {
  return {
    pullRequest: {
      number: 42,
      title: "feat: command automation",
      body: "live-pr-body-secret-51d2",
      draft: false,
      author: "pr-author",
      headOid,
      state: "open",
      baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    },
    issueComment: {
      id: 9001,
      issueNumber: 42,
      body: "/lgtm",
      author: "alice",
    },
    identities: {
      alice: { login: "alice", type: "User", resolved: true, deleted: false },
      bob: { login: "bob", type: "User", resolved: true, deleted: false },
      carol: { login: "carol", type: "User", resolved: true, deleted: false },
      "pr-author": { login: "pr-author", type: "User", resolved: true, deleted: false },
    },
    collaboratorAccess: {
      alice: { liveCollaborator: false, permission: "read" },
      bob: { liveCollaborator: true, permission: "triage" },
      carol: { liveCollaborator: true, permission: "write" },
      "pr-author": { liveCollaborator: false, permission: "read" },
    },
    files: [
      { path: "docs/guide.md", additions: 3, deletions: 1, status: "modified" },
      { path: "pkg/device.go", additions: 2, deletions: 2, status: "modified" },
    ],
    reviews: [],
    requestedReviewers: [],
    assignees: [],
    labels: ["do-not-merge/needs-approval"],
    workflowRuns: [
      trustedRun(301),
      trustedRun(302, { conclusion: "success" }),
      trustedRun(303, { headOid: oldOid }),
    ],
    contents: {
      "/OWNERS": [
        "reviewers: [alice, bob]",
        "approvers: [alice, carol]",
        "",
      ].join("\n"),
      "/OWNERS_ALIASES": "aliases: {}\n",
    },
    comments: [{ id: 77, author: "github-actions[bot]", body: stateMarker() }],
    ...overrides,
  };
}

function writes(github) {
  return github.callOrder.filter(({ operation }) => writeOperations.has(operation));
}

async function run(state = commandState(), options = {}) {
  const { runCommand } = require("../src/modes/command.js");
  const github = createFakeGitHub(state);
  const result = await runCommand({
    event: options.event ?? event,
    github,
    config: options.config ?? loadConfig(repositoryRoot),
    dryRun: options.dryRun ?? false,
    now: options.now ?? (() => "2026-07-16T10:00:00.000Z"),
  });
  return { github, result };
}

test("command mode re-fetches live comment authority and current-head policy", async () => {
  const { github, result } = await run();

  assert.equal(result.status, "complete");
  assert.equal(result.commands[0].code, "lgtm-recorded");
  assert.deepEqual(github.calls.getIssueComment, [{ commentId: 9001 }]);
  assert.deepEqual(github.calls.getUserIdentity, [{ login: "alice" }]);
  assert.deepEqual(github.calls.getCollaboratorAccess, [{ login: "alice" }]);
  assert.deepEqual(github.calls.getDefaultBranchRevision, [{}]);
  assert.deepEqual(github.calls.getContentAtRevision, [
    { path: "/OWNERS", revision: "base-commit-oid-91ab" },
    { path: "/OWNERS_ALIASES", revision: "base-commit-oid-91ab" },
  ]);
  assert.deepEqual(github.calls.getPullRequest, [
    { prNumber: 42 },
    { prNumber: 42 },
    { prNumber: 42 },
  ]);
  const snapshot = github.commandSnapshot();
  assert.equal(snapshot.labels.includes("lgtm"), true);
  assert.equal(snapshot.labels.includes("approved"), false);
  assert.equal(snapshot.labels.includes("do-not-merge/needs-approval"), true);
  assert.match(snapshot.comments[0].body, /"actor":"alice","commentId":9001/);
  const serialized = JSON.stringify({ result, calls: github.callOrder });
  for (const secret of [
    "event-comment-body-must-not-be-used",
    "event-actor-must-not-be-used",
    "event-sender-must-not-be-used",
    "live-pr-body-secret-51d2",
  ]) assert.equal(serialized.includes(secret), false);
});

test("non-PR and closed PR comments are ignored without writes", async (t) => {
  await t.test("not a pull request", async () => {
    const state = commandState({ pullRequest: null });
    const { github, result } = await run(state);
    assert.deepEqual(result, { status: "ignored", reason: "not-open-pull-request" });
    assert.deepEqual(writes(github), []);
  });
  await t.test("closed pull request", async () => {
    const state = commandState({
      pullRequest: { ...commandState().pullRequest, state: "closed" },
    });
    const { github, result } = await run(state);
    assert.deepEqual(result, { status: "ignored", reason: "not-open-pull-request" });
    assert.deepEqual(writes(github), []);
  });
});

test("strict event and live comment consistency rejects repository, number, and comment ambiguity", async (t) => {
  const cases = [
    ["repository", { ...event, repository: { ...event.repository, full_name: "other/repo" } }],
    ["number", { ...event, issue: { ...event.issue, number: 43 } }],
    ["comment", { ...event, comment: { ...event.comment, id: 0 } }],
  ];
  for (const [name, malformedEvent] of cases) {
    await t.test(name, async () => {
      const github = createFakeGitHub(commandState());
      const { runCommand } = require("../src/modes/command.js");
      await assert.rejects(
        () => runCommand({
          event: malformedEvent,
          github,
          config: loadConfig(repositoryRoot),
          dryRun: false,
          now: () => "2026-07-16T10:00:00.000Z",
        }),
        /event|mapping/i,
      );
      assert.deepEqual(writes(github), []);
    });
  }
  const mismatch = commandState({
    issueComment: { ...commandState().issueComment, issueNumber: 99 },
  });
  const github = createFakeGitHub(mismatch);
  const { runCommand } = require("../src/modes/command.js");
  await assert.rejects(
    () => runCommand({ event, github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-16T10:00:00.000Z" }),
    /comment/i,
  );
  assert.deepEqual(writes(github), []);
});

test("live edited body wins and commands plus diagnostics retain line order without reflection", async () => {
  const body = [
    "> /hold",
    "```",
    "/assign @event-secret",
    "```",
    "/help",
    "/approve event-secret-raw-13b7",
    "/hold",
    "/hold cancel",
  ].join("\n");
  const { github, result } = await run(commandState({
    issueComment: { ...commandState().issueComment, body, author: "pr-author" },
  }));

  assert.deepEqual(result.items.map(({ line, code }) => [line, code]), [
    [5, "help"],
    [6, "unsupported-command"],
    [7, "hold-recorded"],
    [8, "hold-cleared"],
  ]);
  assert.equal(github.commandSnapshot().labels.includes("do-not-merge/hold"), false);
  assert.match(github.commandSnapshot().comments[0].body, /Supported commands/);
  assert.match(github.commandSnapshot().comments[0].body, /GitHub <code>APPROVED<\/code> review/);
  assert.equal(JSON.stringify({ result, calls: github.callOrder }).includes("event-secret-raw-13b7"), false);
});

test("only created deliveries execute and command fanout is bounded", async (t) => {
  await t.test("edited delivery", async () => {
    const github = createFakeGitHub(commandState());
    const { runCommand } = require("../src/modes/command.js");
    await assert.rejects(
      () => runCommand({
        event: { ...event, action: "edited" },
        github,
        config: loadConfig(repositoryRoot),
        dryRun: false,
        now: () => "2026-07-16T10:00:00.000Z",
      }),
      /created/i,
    );
    assert.deepEqual(writes(github), []);
  });
  await t.test("assignment targets", async () => {
    const users = Array.from({ length: 21 }, (_, index) => `@user-${index + 1}`).join(" ");
    const base = commandState();
    const { github, result } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: `/assign ${users}` },
    }));
    assert.equal(result.commands[0].code, "too-many-targets");
    assert.deepEqual(github.calls.getUserIdentity, [{ login: "pr-author" }]);
    assert.deepEqual(github.calls.addAssignees, []);
  });
  await t.test("assignment targets across commands", async () => {
    const base = commandState();
    const body = Array.from({ length: 21 }, (_, index) => `/assign @user-${index + 1}`).join("\n");
    const { github, result } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body },
    }));
    assert.equal(result.commands.every(({ code }) => code === "too-many-targets"), true);
    assert.deepEqual(github.calls.getUserIdentity, [{ login: "pr-author" }]);
  });
  await t.test("command results", async () => {
    const base = commandState();
    const { result, github } = await run(commandState({
      issueComment: {
        ...base.issueComment,
        author: "pr-author",
        body: Array.from({ length: 101 }, () => "/help").join("\n"),
      },
    }));
    assert.deepEqual(result.items.map(({ code }) => code), ["too-many-commands"]);
    assert.match(github.commandSnapshot().comments[0].body, /too-many-commands/);
  });
});

test("an event without a pull request hint is ignored before API reads", async () => {
  const github = createFakeGitHub(commandState());
  const { runCommand } = require("../src/modes/command.js");
  const issueEvent = { ...event, issue: { number: 42 } };
  const result = await runCommand({
    event: issueEvent,
    github,
    config: loadConfig(repositoryRoot),
    dryRun: false,
    now: () => "2026-07-16T10:00:00.000Z",
  });
  assert.deepEqual(result, { status: "ignored", reason: "not-open-pull-request" });
  assert.deepEqual(github.callOrder, []);
});

test("authorization uses exact live human and collaborator proof boundaries", async (t) => {
  const cases = [
    ["author cannot lgtm", "pr-author", { liveCollaborator: true, permission: "admin" }, "author-cannot-lgtm", false],
    ["applicable non-collaborator may lgtm", "alice", { liveCollaborator: false, permission: "read" }, "lgtm-recorded", true],
    ["public read outsider cannot hold", "alice", { liveCollaborator: false, permission: "read" }, "not-authorized", false, "/hold"],
    ["triage cannot hold", "bob", { liveCollaborator: true, permission: "triage" }, "not-authorized", false, "/hold"],
    ["write may hold", "carol", { liveCollaborator: true, permission: "write" }, "hold-recorded", true, "/hold"],
    ["maintain may retest", "carol", { liveCollaborator: true, permission: "maintain" }, "retest-planned", true, "/retest"],
    ["admin may hold", "carol", { liveCollaborator: true, permission: "admin" }, "hold-recorded", true, "/hold"],
  ];
  for (const [name, actor, access, code, changed, body = "/lgtm"] of cases) {
    await t.test(name, async () => {
      const base = commandState();
      const { github, result } = await run(commandState({
        issueComment: { ...base.issueComment, author: actor, body },
        collaboratorAccess: { ...base.collaboratorAccess, [actor]: access },
      }));
      assert.equal(result.commands[0].code, code);
      const snapshot = github.commandSnapshot();
      assert.equal(
        snapshot.labels.includes(body === "/hold" ? "do-not-merge/hold" : "lgtm")
          || github.calls.rerunFailedJobs.length > 0,
        changed,
      );
    });
  }
});

test("deleted, bot, mismatched, and unavailable actors fail closed", async (t) => {
  const identities = [
    { login: "alice", type: "Bot", resolved: true, deleted: false },
    { login: "alice", type: "User", resolved: true, deleted: true },
    { login: "mallory", type: "User", resolved: true, deleted: false },
    { login: "alice", type: "User", resolved: false, deleted: false },
  ];
  for (const identity of identities) {
    await t.test(JSON.stringify(identity), async () => {
      const { github, result } = await run(commandState({ identities: { alice: identity } }));
      assert.equal(result.commands[0].status, "rejected");
      assert.equal(github.commandSnapshot().labels.includes("lgtm"), false);
    });
  }
});

test("assignment filters every live target and applies only safe deltas", async () => {
  const base = commandState();
  const { github, result } = await run(commandState({
    issueComment: {
      ...base.issueComment,
      author: "pr-author",
      body: "/assign @alice @bob @outsider @robot\n/unassign @bob @pr-author",
    },
    assignees: ["bob", "pr-author"],
    identities: {
      ...base.identities,
      outsider: { login: "outsider", type: "User", resolved: true, deleted: false },
      robot: { login: "robot", type: "Bot", resolved: true, deleted: false },
    },
    collaboratorAccess: {
      ...base.collaboratorAccess,
      outsider: { liveCollaborator: false, permission: "read" },
      robot: { liveCollaborator: true, permission: "write" },
    },
  }));

  assert.deepEqual(github.calls.addAssignees, [{ prNumber: 42, assignees: ["alice"] }]);
  assert.deepEqual(github.calls.removeAssignees, [{ prNumber: 42, assignees: ["bob", "pr-author"] }]);
  assert.deepEqual(result.commands[0].eligible, ["alice", "bob"]);
  assert.deepEqual(result.commands[0].rejected.map(({ login }) => login), ["outsider", "robot"]);
  assert.deepEqual(github.commandSnapshot().assignees, ["alice"]);
});

test("ordinary assignee may unassign only self while triage may manage targets", async (t) => {
  await t.test("self", async () => {
    const base = commandState();
    const { github, result } = await run(commandState({
      issueComment: { ...base.issueComment, author: "alice", body: "/unassign @alice" },
      assignees: ["alice", "bob"],
    }));
    assert.equal(result.commands[0].status, "applied");
    assert.deepEqual(github.commandSnapshot().assignees, ["bob"]);
  });
  await t.test("not another assignee", async () => {
    const base = commandState();
    const { github, result } = await run(commandState({
      issueComment: { ...base.issueComment, author: "alice", body: "/unassign @bob" },
      assignees: ["alice", "bob"],
    }));
    assert.equal(result.commands[0].code, "not-authorized");
    assert.deepEqual(github.commandSnapshot().assignees, ["alice", "bob"]);
  });
  await t.test("triage", async () => {
    const base = commandState();
    const { github } = await run(commandState({
      issueComment: { ...base.issueComment, author: "bob", body: "/unassign @alice" },
      assignees: ["alice"],
    }));
    assert.deepEqual(github.commandSnapshot().assignees, []);
  });
});

test("LGTM requests only minimal uncovered approvers and exact-head reviews derive labels", async (t) => {
  await t.test("minimal request", async () => {
    const { github, result } = await run();
    assert.deepEqual(github.calls.requestReviewers, [{ prNumber: 42, reviewers: ["alice"] }]);
    assert.deepEqual(result.policy.requestApprovers, ["alice"]);
  });
  await t.test("current approval completes coverage", async () => {
    const { github, result } = await run(commandState({ reviews: [approvedReview()] }));
    const labels = github.commandSnapshot().labels;
    assert.equal(result.policy.approved, true);
    assert.equal(labels.includes("approved"), true);
    assert.equal(labels.includes("lgtm"), true);
    assert.equal(labels.includes("do-not-merge/needs-approval"), false);
    assert.deepEqual(github.calls.requestReviewers, []);
  });
  for (const [name, reviews] of [
    ["old head", [approvedReview({ commitOid: oldOid })]],
    ["later changes requested", [approvedReview(), approvedReview({ id: 102, state: "CHANGES_REQUESTED", submittedAt: "2026-07-16T09:00:00.000Z" })]],
    ["dismissed", [approvedReview(), approvedReview({ id: 102, state: "DISMISSED", submittedAt: "2026-07-16T09:00:00.000Z" })]],
    ["pending nullable", [{ id: 102, user: "alice", state: "PENDING", commitOid: null }]],
  ]) {
    await t.test(name, async () => {
      const { github, result } = await run(commandState({ reviews }));
      assert.equal(result.policy.approved, false);
      assert.equal(github.commandSnapshot().labels.includes("approved"), false);
    });
  }
});

test("manual authority labels cannot forge current LGTM or approval", async () => {
  const base = commandState();
  const { github, result } = await run(commandState({
    issueComment: { ...base.issueComment, author: "pr-author", body: "/help" },
    labels: ["lgtm", "approved"],
    reviews: [],
  }));
  assert.equal(result.policy.lgtm, false);
  assert.equal(result.policy.approved, false);
  assert.deepEqual(github.commandSnapshot().labels, ["do-not-merge/needs-approval"]);
});

test("state parsing is current-head, action-owned, singular, and fail closed", async (t) => {
  await t.test("stale state resets", async () => {
    const base = commandState();
    const stale = stateMarker({ headOid: oldOid, lgtm: null, lastRetest: null });
    const { github, result } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/help" },
      comments: [{ id: 77, author: "github-actions[bot]", body: stale }],
    }));
    assert.equal(result.policy.lgtm, false);
    assert.match(github.commandSnapshot().comments[0].body, new RegExp(headOid));
  });
  await t.test("foreign lookalike is ignored and a bot comment is created", async () => {
    const base = commandState();
    const { github } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/help" },
      comments: [{ id: 88, author: "mallory", body: stateMarker() }],
    }));
    assert.equal(github.commandSnapshot().comments.length, 2);
  });
  await t.test("wrong actor type is not action-owned", async () => {
    const base = commandState();
    const { github } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/help" },
      comments: [{ id: 88, author: "github-actions[bot]", type: "User", body: stateMarker() }],
    }));
    assert.equal(github.commandSnapshot().comments.length, 2);
  });
  await t.test("command summary preserves the trusted metadata section", async () => {
    const base = commandState();
    const metadataBody = [
      marker,
      serializePolicyState({ headOid, lgtm: null, lastRetest: null }),
      "## PR metadata policy",
      "",
      "- Title: **PASS**",
      "- DCO: **PASS**",
      "",
    ].join("\n");
    const { github } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/help" },
      comments: [{ id: 77, author: "github-actions[bot]", body: metadataBody }],
    }));
    const body = github.commandSnapshot().comments[0].body;
    assert.match(body, /## PR metadata policy/);
    assert.match(body, /- Title: \*\*PASS\*\*/);
    assert.match(body, /repo-automation-command-summary:v1/);
  });
  await t.test("duplicate", async () => {
    const github = createFakeGitHub(commandState({ comments: [
      { id: 77, author: "github-actions[bot]", body: stateMarker() },
      { id: 78, author: "github-actions[bot]", body: stateMarker() },
    ] }));
    const { runCommand } = require("../src/modes/command.js");
    await assert.rejects(
      () => runCommand({ event, github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-16T10:00:00.000Z" }),
      /policy/i,
    );
    assert.deepEqual(writes(github), []);
  });
  await t.test("malformed state is repaired without granting authority", async () => {
    const github = createFakeGitHub(commandState({
      comments: [{ id: 77, author: "github-actions[bot]", body: `${marker}\n<!-- repo-automation-state:v1 {} -->` }],
    }));
    const { runCommand } = require("../src/modes/command.js");
    const result = await runCommand({ event, github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-16T10:00:00.000Z" });
    assert.equal(result.status, "partial");
    assert.equal(result.commands[0].code, "policy-unavailable");
    assert.equal(github.commandSnapshot().labels.includes("lgtm"), false);
    assert.equal(
      github.commandSnapshot().comments[0].body.includes(serializePolicyState({ headOid, lgtm: null, lastRetest: null })),
      true,
    );
  });

  for (const [name, body] of [
    ["reordered markers", `${marker}\n${commandMarker}\n## Command policy\n${serializePolicyState({ headOid, lgtm: null, lastRetest: null })}\n`],
    ["missing state", `${marker}\n## PR metadata policy\n`],
    ["duplicated state", `${marker}\n${serializePolicyState({ headOid, lgtm: null, lastRetest: null })}\n${serializePolicyState({ headOid, lgtm: null, lastRetest: null })}\n`],
  ]) {
    await t.test(name, async () => {
      const base = commandState();
      const { github, result } = await run(commandState({
        issueComment: { ...base.issueComment, author: "pr-author", body: "/help" },
        comments: [{ id: 77, author: "github-actions[bot]", body }],
      }));
      const rendered = github.commandSnapshot().comments[0].body;
      assert.equal(result.status, "partial");
      assert.equal(result.policy.lgtm, false);
      assert.equal(rendered.split("<!-- repo-automation-state:").length - 1, 1);
      assert.equal(rendered.split(commandMarker).length - 1, 1);
      assert.equal(rendered.startsWith(`${marker}\n${serializePolicyState({ headOid, lgtm: null, lastRetest: null })}\n`), true);
    });
  }
});

test("invalid state authority clears approval and approver requests in apply and dry-run", async (t) => {
  const canonical = serializePolicyState({ headOid, lgtm: null, lastRetest: null });
  for (const [shape, body] of [
    ["malformed", `${marker}\n<!-- repo-automation-state:v1 {} -->\n`],
    ["duplicate", `${marker}\n${canonical}\n${canonical}\n`],
  ]) {
    for (const dryRun of [false, true]) {
      await t.test(`${shape} ${dryRun ? "dry-run" : "apply"}`, async () => {
        const base = commandState();
        const { github, result } = await run(commandState({
          issueComment: { ...base.issueComment, author: "pr-author", body: "/help" },
          comments: [{ id: 77, author: "github-actions[bot]", body }],
          labels: ["lgtm", "approved"],
          reviews: [approvedReview()],
        }), { dryRun });

        assert.equal(result.status, "partial");
        assert.deepEqual(result.policy, {
          lgtm: false,
          approved: false,
          hold: false,
          needsApproval: true,
          uncoveredPaths: [],
          approverUncoveredPaths: [],
          requestApprovers: [],
        });
        assert.deepEqual(result.labels, {
          add: ["do-not-merge/needs-approval"],
          remove: ["lgtm", "approved"],
        });
        if (dryRun) {
          assert.deepEqual(writes(github), []);
        } else {
          assert.deepEqual(github.commandSnapshot().labels, ["do-not-merge/needs-approval"]);
          assert.deepEqual(github.calls.requestReviewers, []);
        }
      });
    }
  }
});

test("same-comment LGTM and cancellation redelivery preserve provenance and converge", async (t) => {
  await t.test("LGTM", async () => {
    const github = createFakeGitHub(commandState());
    const { runCommand } = require("../src/modes/command.js");
    const options = { event, github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-16T10:00:00.000Z" };
    await runCommand(options);
    const first = github.commandSnapshot();
    const firstWrites = writes(github).length;
    await runCommand({ ...options, now: () => "2026-07-16T11:00:00.000Z" });
    const second = github.commandSnapshot();
    assert.equal(writes(github).length, firstWrites);
    assert.equal(second.comments[0].body, first.comments[0].body);
    assert.match(second.comments[0].body, /2026-07-16T10:00:00\.000Z/);
  });
  await t.test("cancel", async () => {
    const base = commandState();
    const lgtm = { actor: "alice", commentId: 8000, headOid, createdAt: "2026-07-16T09:00:00.000Z" };
    const github = createFakeGitHub(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/lgtm cancel" },
      labels: ["lgtm", "do-not-merge/needs-approval"],
      comments: [{ id: 77, author: "github-actions[bot]", body: stateMarker({ headOid, lgtm, lastRetest: null }) }],
    }));
    const { runCommand } = require("../src/modes/command.js");
    const options = { event, github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-16T10:00:00.000Z" };
    await runCommand(options);
    const firstWrites = writes(github).length;
    await runCommand(options);
    assert.equal(writes(github).length, firstWrites);
  });
});

test("retest is exact-head failure-only, cooldown-bound, and duplicate safe", async (t) => {
  await t.test("reruns only failed current run", async () => {
    const base = commandState();
    const { github, result } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/retest" },
    }));
    assert.deepEqual(github.calls.rerunFailedJobs, [{ runId: 301 }]);
    assert.equal(result.commands[0].code, "retest-planned");
    assert.deepEqual(github.calls.listWorkflowRunsForHead, [{ headOid, prNumber: 42 }]);
    assert.deepEqual(github.calls.getWorkflowRun, [{ runId: 301, headOid, prNumber: 42 }]);
  });
  await t.test("never reruns privileged, manual, push, publisher, or wrong-PR runs on the same head", async () => {
    const base = commandState();
    const { github } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/retest" },
      workflowRuns: [
        trustedRun(301),
        trustedRun(304, { workflowPath: ".github/workflows/nvml-mock-publish.yaml", event: "workflow_dispatch" }),
        trustedRun(305, { event: "workflow_dispatch" }),
        trustedRun(306, { event: "push" }),
        trustedRun(307, { prNumber: 99 }),
        trustedRun(308, { workflowPath: ".github/workflows/release.yaml" }),
      ],
    }));
    assert.deepEqual(github.calls.rerunFailedJobs, [{ runId: 301 }]);
  });
  await t.test("no failures", async () => {
    const base = commandState();
    const { github, result } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/retest" },
      workflowRuns: [trustedRun(302, { conclusion: "success" })],
    }));
    assert.equal(result.commands[0].code, "no-failed-runs");
    assert.deepEqual(github.calls.rerunFailedJobs, []);
  });
  const lastRetest = { commentId: 8000, headOid, createdAt: "2026-07-16T09:55:00.000Z" };
  await t.test("cooldown", async () => {
    const base = commandState();
    const { github, result } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/retest" },
      comments: [{ id: 77, author: "github-actions[bot]", body: stateMarker({ headOid, lgtm: null, lastRetest }) }],
    }));
    assert.equal(result.commands[0].code, "cooldown");
    assert.deepEqual(github.calls.rerunFailedJobs, []);
  });
  await t.test("duplicate delivery", async () => {
    const base = commandState();
    const duplicate = { commentId: 9001, headOid, createdAt: "2026-07-16T09:00:00.000Z" };
    const { github, result } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/retest" },
      comments: [{ id: 77, author: "github-actions[bot]", body: stateMarker({ headOid, lgtm: null, lastRetest: duplicate }) }],
    }));
    assert.equal(result.commands[0].code, "duplicate-delivery");
    assert.deepEqual(github.calls.rerunFailedJobs, []);
  });
  await t.test("run changed before rerun", async () => {
    const base = commandState();
    const { github } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/retest" },
      workflowRunReads: { 301: [trustedRun(301, { status: "in_progress", conclusion: null })] },
    }));
    assert.deepEqual(github.calls.rerunFailedJobs, []);
  });
  await t.test("run identity changed between list and final get", async () => {
    const base = commandState();
    const { github } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/retest" },
      workflowRunReads: { 301: [trustedRun(301, { workflowPath: ".github/workflows/basic-checks.yaml" })] },
    }));
    assert.deepEqual(github.calls.rerunFailedJobs, []);
  });
  await t.test("run source ref changed between list and final get", async () => {
    const base = commandState();
    const { github } = await run(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/retest" },
      workflowRunReads: { 301: [trustedRun(301, { workflowSourceRef: "release" })] },
    }));
    assert.deepEqual(github.calls.rerunFailedJobs, []);
  });
  await t.test("partial multi-run delivery is at-most-once", async () => {
    const base = commandState();
    const secondFailure = new Error("second rerun failed");
    const github = createFakeGitHub(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/retest" },
      workflowRuns: [
        trustedRun(301),
        trustedRun(304),
      ],
      failures: { rerunFailedJobs: [null, secondFailure] },
    }));
    const { runCommand } = require("../src/modes/command.js");
    const options = { event, github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-16T10:00:00.000Z" };
    await assert.rejects(() => runCommand(options), /second rerun failed/);
    assert.deepEqual(github.calls.rerunFailedJobs, [{ runId: 301 }, { runId: 304 }]);
    await runCommand(options);
    assert.deepEqual(
      github.calls.rerunFailedJobs,
      [{ runId: 301 }, { runId: 304 }],
      "redelivery must not retry an ambiguous non-idempotent rerun",
    );
  });
});

test("duplicate command delivery and conflicts converge without duplicate mutation", async () => {
  const base = commandState();
  const github = createFakeGitHub(commandState({
    issueComment: {
      ...base.issueComment,
      author: "pr-author",
      body: "/hold\n/hold\n/hold cancel\n/hold cancel\n/assign @alice\n/assign @alice",
    },
  }));
  const { runCommand } = require("../src/modes/command.js");
  const options = { event, github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-16T10:00:00.000Z" };
  await runCommand(options);
  await runCommand(options);
  assert.deepEqual(github.calls.addAssignees, [{ prNumber: 42, assignees: ["alice"] }]);
  assert.deepEqual(github.calls.addIssueLabel.filter(({ label }) => label === "do-not-merge/hold"), []);
  assert.deepEqual(github.calls.removeIssueLabel.filter(({ label }) => label === "do-not-merge/hold"), []);
});

test("final head and PR identity fence aborts every stale write", async (t) => {
  for (const [name, changed] of [
    ["head", { headOid: "7".repeat(40) }],
    ["draft", { draft: true }],
    ["author", { author: "mallory" }],
    ["base", { baseRepository: { owner: "other", repo: "repo" } }],
  ]) {
    await t.test(name, async () => {
      const initial = commandState().pullRequest;
      const github = createFakeGitHub(commandState({
        pullRequests: [initial, { ...initial, ...changed }],
      }));
      const { runCommand } = require("../src/modes/command.js");
      await assert.rejects(
        () => runCommand({ event, github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-16T10:00:00.000Z" }),
        /changed|fence|state/i,
      );
      assert.deepEqual(writes(github), []);
    });
  }
});

test("dry-run returns a complete bounded plan with zero mutations", async () => {
  const { github, result } = await run(commandState(), { dryRun: true });
  assert.equal(result.status, "planned");
  assert.equal(result.policy.lgtm, true);
  assert.deepEqual(writes(github), []);
  assert.equal(github.calls.getPullRequest.length, 1);
});

test("read and partial write failures fail closed with stable safe summaries", async (t) => {
  await t.test("read", async () => {
    const failure = Object.assign(new Error("secret-read-token-912"), { status: 500 });
    const github = createFakeGitHub(commandState({ failures: { listPullRequestReviews: failure } }));
    const { runCommand } = require("../src/modes/command.js");
    await assert.rejects(
      () => runCommand({ event, github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-16T10:00:00.000Z" }),
    );
    assert.deepEqual(writes(github), []);
  });
  await t.test("partial", async () => {
    const base = commandState();
    const failure = new Error("secret-write-token-841");
    const github = createFakeGitHub(commandState({
      issueComment: { ...base.issueComment, author: "pr-author", body: "/assign @alice\n/hold" },
      failures: { addPolicyLabel: failure },
    }));
    const { runCommand } = require("../src/modes/command.js");
    let caught;
    try {
      await runCommand({ event, github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-16T10:00:00.000Z" });
    } catch (error) {
      caught = error;
    }
    assert.equal(caught?.summary?.status, "partial");
    assert.deepEqual(caught?.summary?.apply.applied, [{ operation: "addAssignees", assignees: ["alice"] }]);
    assert.equal(JSON.stringify(caught?.summary).includes("secret-write-token-841"), false);
  });
});

test("invalid or uncovered ownership adds needs-approval and grants no authority", async () => {
  const base = commandState();
  const invalidConfig = loadConfig(repositoryRoot);
  invalidConfig.policy.activeOwnerFiles = [];
  const { github, result } = await run(commandState({
    issueComment: { ...base.issueComment, author: "pr-author", body: "/help" },
    labels: [],
  }), { config: invalidConfig });
  assert.equal(result.status, "partial");
  assert.equal(result.policy.approved, false);
  assert.deepEqual(github.commandSnapshot().labels, ["do-not-merge/needs-approval"]);
});

test("invalid configuration permits only help/diagnostics and fail-closed policy state", async () => {
  const base = commandState();
  const invalidConfig = loadConfig(repositoryRoot);
  invalidConfig.policy.commands.retestCooldownSeconds = 601;
  const { github, result } = await run(commandState({
    issueComment: {
      ...base.issueComment,
      author: "pr-author",
      body: "/hold\n/retest\n/assign @alice\n/help",
    },
    labels: [],
  }), { config: invalidConfig });
  assert.deepEqual(result.commands.map(({ code }) => code), [
    "policy-unavailable",
    "policy-unavailable",
    "policy-unavailable",
    "help",
  ]);
  assert.deepEqual(github.calls.getUserIdentity, [{ login: "pr-author" }]);
  assert.deepEqual(github.calls.listWorkflowRunsForHead, []);
  assert.equal(github.commandSnapshot().labels.includes("do-not-merge/needs-approval"), true);
  assert.equal(github.commandSnapshot().labels.includes("do-not-merge/hold"), false);
  assert.deepEqual(github.calls.rerunFailedJobs, []);
  assert.deepEqual(github.calls.addAssignees, []);
});

test("run dispatches command mode and imports without side effects", async () => {
  const { run: runAction } = require("../src/index.js");
  const githubClient = createFakeGitHub(commandState());
  const outputs = [];
  const core = {
    getInput(name) { return name === "mode" ? "command" : ""; },
    getBooleanInput() { return true; },
    setOutput(name, value) { outputs.push([name, value]); },
  };
  const result = await runAction({
    core,
    githubClient,
    event,
    workspace: repositoryRoot,
    now: () => "2026-07-16T10:00:00.000Z",
  });
  assert.equal(result.status, "planned");
  assert.equal(outputs.length, 1);
  assert.deepEqual(writes(githubClient), []);
});

test("GitHub client exposes fixed validated command endpoint contracts", async () => {
  const { createGitHubClient } = require("../src/github-client.js");
  const calls = [];
  const endpoint = (name, data) => Object.assign(async (parameters) => {
    calls.push([name, parameters]);
    return { data };
  }, { endpointName: name });
  const octokit = {
    paginate: async (handler, parameters, map) => {
      const response = await handler(parameters);
      return map === undefined ? response.data : map(response);
    },
    rest: {
      actions: {
        listWorkflowRunsForRepo: endpoint("listWorkflowRunsForRepo", { workflow_runs: [apiRun(301)] }),
        getWorkflowRun: endpoint("getWorkflowRun", apiRun(301)),
        reRunWorkflowFailedJobs: endpoint("reRunWorkflowFailedJobs", {}),
      },
      issues: {
        getComment: endpoint("getComment", { id: 9001, issue_url: "https://api.github.com/repos/NVIDIA/k8s-test-infra/issues/42", body: "/retest", user: { login: "alice" } }),
        get: endpoint("getIssue", { assignees: [{ login: "alice" }] }),
        addAssignees: endpoint("addAssignees", {}),
        removeAssignees: endpoint("removeAssignees", {}),
      },
      users: { getByUsername: endpoint("getByUsername", { login: "alice", type: "User" }) },
      repos: {
        checkCollaborator: endpoint("checkCollaborator", {}),
        getCollaboratorPermissionLevel: endpoint("getCollaboratorPermissionLevel", { permission: "write" }),
      },
      pulls: {
        listReviews: endpoint("listReviews", [{ id: 101, user: { login: "alice" }, state: "APPROVED", commit_id: headOid, submitted_at: "2026-07-16T08:00:00Z" }]),
        requestReviewers: endpoint("requestReviewers", {}),
      },
    },
  };
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
  assert.deepEqual(await client.getIssueComment(9001), { id: 9001, issueNumber: 42, body: "/retest", author: "alice", edited: false });
  assert.deepEqual(await client.getUserIdentity("alice"), { login: "alice", type: "User", resolved: true, deleted: false });
  assert.deepEqual(await client.getCollaboratorAccess("alice"), { liveCollaborator: true, permission: "write" });
  assert.deepEqual(await client.listIssueAssignees(42), ["alice"]);
  assert.deepEqual(await client.listWorkflowRunsForHead(headOid, 42), [trustedRun(301)]);
  assert.deepEqual(await client.getWorkflowRun(301, headOid, 42), trustedRun(301));
  await client.addAssignees(42, ["alice"]);
  await client.removeAssignees(42, ["alice"]);
  await client.rerunFailedJobs(301);
  assert.deepEqual(await client.listPullRequestReviews(42), [{
    id: 101,
    user: "alice",
    state: "APPROVED",
    commitOid: headOid,
    submittedAt: "2026-07-16T08:00:00Z",
  }]);
  assert.equal(JSON.stringify(calls).includes("/retest"), false, "comment bytes stay data-only");
});

test("GitHub client maps complete review/state authority and never retries reruns", async (t) => {
  const { createGitHubClient } = require("../src/github-client.js");
  await t.test("review shapes", async () => {
    const listReviews = Object.assign(async () => ({ data: [
      { id: 1, user: { login: "alice" }, state: "PENDING", commit_id: null },
      { id: 2, user: { login: "bob" }, state: "COMMENTED", commit_id: null, submitted_at: "2026-07-16T08:00:00Z" },
    ] }), { endpointName: "listReviews" });
    const octokit = {
      paginate: async (handler, parameters) => (await handler(parameters)).data,
      rest: { pulls: { listReviews } },
    };
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
    assert.deepEqual(await client.listPullRequestReviews(42), [
      { id: 1, user: "alice", state: "PENDING", commitOid: null },
      { id: 2, user: "bob", state: "COMMENTED", commitOid: null, submittedAt: "2026-07-16T08:00:00Z" },
    ]);
  });
  await t.test("nullable review user", async () => {
    const listReviews = Object.assign(async () => ({ data: [
      { id: 1, user: null, state: "PENDING", commit_id: null },
    ] }), { endpointName: "listReviews" });
    const octokit = {
      paginate: async (handler, parameters) => (await handler(parameters)).data,
      rest: { pulls: { listReviews } },
    };
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
    await assert.rejects(() => client.listPullRequestReviews(42), /review user/i);
  });
  await t.test("404 collaborator is not public-read membership", async () => {
    let permissionCalls = 0;
    const notFound = Object.assign(new Error("not found"), { status: 404 });
    const octokit = { rest: { repos: {
      checkCollaborator: async () => { throw notFound; },
      getCollaboratorPermissionLevel: async () => { permissionCalls += 1; return { data: { permission: "read" } }; },
    } } };
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
    assert.deepEqual(await client.getCollaboratorAccess("alice"), { liveCollaborator: false, permission: "none" });
    assert.equal(permissionCalls, 0);
  });
  await t.test("rerun is attempted once", async () => {
    let attempts = 0;
    const transient = Object.assign(new Error("network"), { status: 503 });
    const octokit = { rest: { actions: {
      reRunWorkflowFailedJobs: async () => { attempts += 1; throw transient; },
    } } };
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 3, sleep: async () => {} });
    await assert.rejects(() => client.rerunFailedJobs(301), /rerunFailedJobs/);
    assert.equal(attempts, 1);
  });
  await t.test("workflow runs are filtered by fixed path, safe ref, event, PR, head, and repository", async () => {
    const listWorkflowRunsForRepo = Object.assign(async () => ({ data: { workflow_runs: [
      apiRun(1),
      apiRun(2, { path: ".github/workflows/basic-checks.yaml@feature/ci" }),
      apiRun(3, { path: ".github/workflows/helm.yaml@refs/heads/main" }),
      apiRun(4, { path: ".github/workflows/nvml-mock-publish.yaml", event: "workflow_dispatch" }),
      apiRun(5, { event: "workflow_dispatch" }),
      apiRun(6, { event: "push" }),
      apiRun(7, { pull_requests: [{ number: 99 }] }),
      apiRun(8, { repository: { full_name: "attacker/fork" } }),
      apiRun(9, { head_sha: oldOid }),
      apiRun(10, { path: ".github/workflows/automation-ci.yml@refs/pull/42/../../heads/main" }),
      apiRun(11, { path: ".github/workflows/../workflows/automation-ci.yml" }),
      apiRun(12, { path: ".github/workflows/automation-ci.yml@refs/heads/.hidden" }),
      apiRun(13, { path: ".github/workflows/automation-ci.yml@refs/heads/main." }),
      apiRun(14, { path: ".github/workflows/automation-ci.yml@" }),
      apiRun(15, { path: ".github/workflows/automation-ci.yml@main@release" }),
      apiRun(16, { path: ".github/workflows/automation-ci.yml@../main" }),
      apiRun(17, { path: ".github/workflows/automation-ci.yml@main\nforged" }),
      apiRun(18, { path: ".github/workflows/automation-ci.yml" }),
    ] } }), { endpointName: "listWorkflowRunsForRepo" });
    const octokit = {
      paginate: async (handler, parameters, map) => map(await handler(parameters)),
      rest: { actions: { listWorkflowRunsForRepo } },
    };
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
    assert.deepEqual(await client.listWorkflowRunsForHead(headOid, 42), [
      trustedRun(1),
      trustedRun(2, {
        workflowPath: ".github/workflows/basic-checks.yaml",
        workflowSourceRef: "feature/ci",
      }),
      trustedRun(3, {
        workflowPath: ".github/workflows/helm.yaml",
        workflowSourceRef: "refs/heads/main",
      }),
      trustedRun(18, { workflowSourceRef: null }),
    ]);
  });
});

test("GitHub client reads exact action-owned policy body and rejects arbitrary policy labels", async () => {
  const { createGitHubClient } = require("../src/github-client.js");
  const listComments = Object.assign(async () => ({ data: [
    { id: 12, body: stateMarker(), user: { login: "github-actions[bot]", type: "Bot" } },
    { id: 13, body: stateMarker(), user: { login: "github-actions[bot]", type: "User" } },
  ] }), { endpointName: "listComments" });
  const octokit = {
    paginate: async (handler, parameters) => (await handler(parameters)).data,
    rest: { issues: {
      listComments,
      addLabels: async () => { throw new Error("must not call"); },
      removeLabel: async () => { throw new Error("must not call"); },
    } },
  };
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
  assert.deepEqual(await client.getPolicyComment(42, marker), { action: "update", id: 12, body: stateMarker() });
  await assert.rejects(() => client.addPolicyLabel(42, "maintainer/custom"), /policy-managed/);
  await assert.rejects(() => client.removePolicyLabel(42, "kind/bug"), /policy-managed/);
});
