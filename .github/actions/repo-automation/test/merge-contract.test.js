"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const { loadConfig } = require("../src/config.js");
const { serializePolicyState } = require("../src/commands/state.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");
const {
  renderCommandPolicyComment,
  renderPolicyComment,
} = require("../src/policy-comment.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const workflowEvent = JSON.parse(fs.readFileSync(
  path.join(__dirname, "fixtures", "events", "workflow-run.json"),
  "utf8",
));
const HEAD = "6".repeat(40);
const NEXT_HEAD = "7".repeat(40);
const MARKER = "<!-- repo-automation-policy:v1 -->";
const METADATA = (head = HEAD) => `<!-- repo-automation-metadata-head:v1 {"headOid":"${head}"} -->`;
const lgtm = (head = HEAD) => ({
  actor: "alice",
  commentId: 8000,
  headOid: head,
  createdAt: "2026-07-16T09:00:00.000Z",
});

function policyBody({ head = HEAD, metadataHead = head, approval = lgtm(head) } = {}) {
  return [
    MARKER,
    serializePolicyState({ headOid: head, lgtm: approval, lastRetest: null }),
    METADATA(metadataHead),
    "## PR metadata policy",
    "",
  ].join("\n");
}

function pullRequest(overrides = {}) {
  return {
    number: 42,
    nodeId: "PR_node_42",
    title: "feat: evaluator",
    body: "live body is irrelevant",
    draft: false,
    author: "pr-author",
    headOid: HEAD,
    state: "open",
    baseBranch: "main",
    baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    ...overrides,
  };
}

function mergeState(overrides = {}) {
  return {
    number: 42,
    nodeId: "PR_node_42",
    state: "OPEN",
    draft: false,
    baseBranch: "main",
    headOid: HEAD,
    mergeability: "MERGEABLE",
    autoMergeMethod: null,
    repository: "nvidia/k8s-test-infra",
    ...overrides,
  };
}

function approvedReview(overrides = {}) {
  return {
    id: 101,
    user: "alice",
    state: "APPROVED",
    commitOid: HEAD,
    submittedAt: "2026-07-16T08:00:00.000Z",
    ...overrides,
  };
}

function evaluatorState(overrides = {}) {
  return {
    pullRequest: pullRequest(),
    files: [{ path: "pkg/gpu.go", additions: 2, deletions: 1, status: "modified" }],
    reviews: [approvedReview()],
    labels: ["lgtm", "do-not-merge/needs-approval", "maintainer/custom"],
    comments: [{ id: 77, author: "github-actions[bot]", type: "Bot", body: policyBody() }],
    contents: {
      "/OWNERS": "reviewers: [alice]\napprovers: [alice]\n",
      "/OWNERS_ALIASES": "aliases: {}\n",
    },
    branchProtection: { main: true },
    mergeStates: [mergeState(), mergeState()],
    ...overrides,
  };
}

const writeOperations = new Set([
  "addPolicyLabel",
  "removePolicyLabel",
  "enableAutoMerge",
  "disableAutoMerge",
]);

function writes(github) {
  return github.callOrder.filter(({ operation }) => writeOperations.has(operation));
}

async function run(state = evaluatorState(), options = {}) {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  const github = createFakeGitHub(state);
  const result = await runMergeEvaluate({
    event: options.event ?? {
      repository: {
        name: "k8s-test-infra",
        full_name: "NVIDIA/k8s-test-infra",
        owner: { login: "NVIDIA" },
      },
    },
    github,
    config: options.config ?? loadConfig(repositoryRoot),
    dryRun: options.dryRun ?? false,
    prNumber: options.prNumber ?? "42",
    eventName: options.eventName ?? (
      options.event?.workflow_run !== undefined
        ? "workflow_run"
        : options.event?.schedule !== undefined ? "schedule" : "workflow_dispatch"
    ),
  });
  return { github, result };
}

test("explicit evaluation recomputes authority, repairs display labels, and enables native SQUASH", async () => {
  const { github, result } = await run();

  assert.equal(result.status, "complete");
  assert.deepEqual(result.pullRequests, [{
    number: 42,
    headOid: HEAD,
    attempts: 1,
    lgtm: true,
    approved: true,
    labels: { add: ["approved"], remove: ["do-not-merge/needs-approval"] },
    merge: { action: "ENABLE", blockers: [] },
  }]);
  assert.deepEqual(github.calls.enableAutoMerge, [{ pullRequestId: "PR_node_42", mergeMethod: "SQUASH" }]);
  assert.deepEqual(github.calls.disableAutoMerge, []);
  assert.deepEqual(github.calls.addPolicyLabel, [{ prNumber: 42, label: "approved" }]);
  assert.deepEqual(github.calls.removePolicyLabel, [{ prNumber: 42, label: "do-not-merge/needs-approval" }]);
  assert.deepEqual(github.calls.getBranchProtection, [{ branch: "main" }]);
});

test("labels never grant authority and invalid current-head approval disables existing auto-merge", async () => {
  const { github, result } = await run(evaluatorState({
    labels: ["lgtm", "approved"],
    reviews: [
      approvedReview({ id: 100, commitOid: "5".repeat(40) }),
      approvedReview({ id: 101, state: "CHANGES_REQUESTED", submittedAt: "2026-07-16T09:00:00.000Z" }),
    ],
    mergeStates: [mergeState({ autoMergeMethod: "SQUASH" }), mergeState({ autoMergeMethod: "SQUASH" })],
  }));

  assert.equal(result.pullRequests[0].approved, false);
  assert.deepEqual(result.pullRequests[0].labels, {
    add: ["do-not-merge/needs-approval"],
    remove: ["approved"],
  });
  assert.equal(result.pullRequests[0].merge.action, "DISABLE");
  assert.deepEqual(github.calls.disableAutoMerge, [{ pullRequestId: "PR_node_42" }]);
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("Task 8 partial reset states cannot enable native auto-merge", async (t) => {
  await t.test("pre-comment partial reset has conservative labels and stale comment evidence", async () => {
    const { github, result } = await run(evaluatorState({
      labels: ["do-not-merge/needs-approval", "maintainer/custom"],
      comments: [{
        id: 77,
        author: "github-actions[bot]",
        type: "Bot",
        body: policyBody({
          head: NEXT_HEAD,
          metadataHead: NEXT_HEAD,
          approval: lgtm(NEXT_HEAD),
        }),
      }],
    }));

    assert.equal(result.pullRequests[0].merge.action, "NOOP");
    assert.deepEqual(result.pullRequests[0].merge.blockers, [
      "lgtm-missing",
      "metadata-stale",
    ]);
    assert.deepEqual(github.calls.enableAutoMerge, []);
  });

  await t.test("post-comment canonical reset has null current-head authority", async () => {
    const { github, result } = await run(evaluatorState({
      labels: ["do-not-merge/needs-approval", "maintainer/custom"],
      comments: [{
        id: 77,
        author: "github-actions[bot]",
        type: "Bot",
        body: policyBody({ approval: null }),
      }],
    }));

    assert.equal(result.pullRequests[0].merge.action, "NOOP");
    assert.deepEqual(result.pullRequests[0].merge.blockers, ["lgtm-missing"]);
    assert.deepEqual(github.calls.enableAutoMerge, []);
  });
});

test("workflow completion uses only a re-fetched strict identity", async (t) => {
  const trusted = {
    id: 7001,
    name: "Review observer",
    workflowPath: ".github/workflows/review-observer.yml",
    event: "pull_request_review",
    status: "completed",
    repository: "nvidia/k8s-test-infra",
    pullRequestNumbers: [42],
  };

  await t.test("observer maps its live PR list and ignores hostile payload fields", async () => {
    const { github, result } = await run(evaluatorState({ evaluationWorkflowRuns: { 7001: trusted } }), {
      event: workflowEvent,
      prNumber: "",
    });
    assert.deepEqual(result.candidates, [42]);
    assert.deepEqual(github.calls.getEvaluationWorkflowRun, [{ runId: 7001 }]);
    assert.equal(JSON.stringify({ result, calls: github.callOrder }).includes("event-artifact"), false);
  });

  await t.test("empty observer mapping evaluates nothing", async () => {
    const { github, result } = await run(evaluatorState({
      evaluationWorkflowRuns: { 7001: { ...trusted, pullRequestNumbers: [] } },
    }), { event: workflowEvent, prNumber: "" });
    assert.deepEqual(result.candidates, []);
    assert.deepEqual(github.calls.getPullRequest, []);
    assert.deepEqual(writes(github), []);
  });

  await t.test("identity mismatch is rejected before PR reads", async () => {
    const { github, result } = await run(evaluatorState({
      evaluationWorkflowRuns: { 7001: { ...trusted, name: "Publisher" } },
    }), { event: workflowEvent, prNumber: "" });
    assert.deepEqual(result.candidates, []);
    assert.deepEqual(github.calls.getPullRequest, []);
  });

  await t.test("metadata completion uses the exact workflow and source event", async () => {
    const metadata = {
      ...trusted,
      name: "PR metadata",
      workflowPath: ".github/workflows/pr-metadata.yml",
      event: "pull_request_target",
    };
    const { result } = await run(evaluatorState({ evaluationWorkflowRuns: { 7001: metadata } }), {
      event: workflowEvent,
      prNumber: "",
    });
    assert.deepEqual(result.candidates, [42]);
  });

  for (const [name, change] of [
    ["path", { workflowPath: ".github/workflows/publish.yml" }],
    ["source event", { event: "workflow_dispatch" }],
    ["status", { status: "in_progress" }],
    ["repository", { repository: "other/repo" }],
  ]) {
    await t.test(`${name} mismatch`, async () => {
      const { result } = await run(evaluatorState({
        evaluationWorkflowRuns: { 7001: { ...trusted, ...change } },
      }), { event: workflowEvent, prNumber: "" });
      assert.deepEqual(result.candidates, []);
    });
  }
});

test("event name authenticates exactly one trigger class before PR reads", async (t) => {
  const cases = [
    ["workflow run with explicit PR", "workflow_run", workflowEvent, "42"],
    ["schedule with explicit PR", "schedule", { repository: workflowEvent.repository, schedule: "*/15 * * * *" }, "42"],
    ["unsupported review", "pull_request_review", { repository: workflowEvent.repository }, ""],
    ["unsupported empty", "", { repository: workflowEvent.repository }, ""],
    ["workflow run carrying schedule", "workflow_run", { ...workflowEvent, schedule: "*/15 * * * *" }, ""],
    ["schedule carrying workflow run", "schedule", { ...workflowEvent, schedule: "*/15 * * * *" }, ""],
  ];
  for (const [name, eventName, selectedEvent, prNumber] of cases) {
    await t.test(name, async () => {
      const github = createFakeGitHub(evaluatorState({ openPullRequestNumbers: [42] }));
      const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
      await assert.rejects(() => runMergeEvaluate({
        event: selectedEvent,
        eventName,
        github,
        config: loadConfig(repositoryRoot),
        dryRun: false,
        prNumber,
      }), /event|explicit|trigger/i);
      assert.deepEqual(github.calls.getPullRequest, []);
    });
  }

  await t.test("dispatch without PR scans open", async () => {
    const { result } = await run(evaluatorState({ openPullRequestNumbers: [42] }), {
      event: { repository: workflowEvent.repository },
      eventName: "workflow_dispatch",
      prNumber: "",
    });
    assert.deepEqual(result.candidates, [42]);
  });
});

test("Commands completion and schedule use a bounded all-open scan", async (t) => {
  const commands = {
    id: 7001,
    name: "Commands",
    workflowPath: ".github/workflows/commands.yml",
    event: "issue_comment",
    status: "completed",
    repository: "nvidia/k8s-test-infra",
    pullRequestNumbers: [999],
  };
  await t.test("Commands ignores plausible run mapping", async () => {
    const { github, result } = await run(evaluatorState({
      openPullRequestNumbers: [42],
      evaluationWorkflowRuns: { 7001: commands },
    }), { event: workflowEvent, prNumber: "" });
    assert.deepEqual(result.candidates, [42]);
    assert.deepEqual(github.calls.listOpenPullRequestNumbers, [{}]);
  });

  await t.test("overflow fails closed", async () => {
    const numbers = Array.from({ length: 101 }, (_, index) => index + 1);
    await assert.rejects(
      () => run(evaluatorState({ openPullRequestNumbers: numbers }), {
        event: {
          repository: workflowEvent.repository,
          schedule: "*/15 * * * *",
        },
        prNumber: "",
      }),
      /open pull request scan.*limit/i,
    );
  });
});

test("missing, duplicate, malformed, or stale metadata-head evidence blocks enable", async (t) => {
  const bodies = [
    policyBody().replace(`${METADATA()}\n`, ""),
    policyBody().replace(METADATA(), `${METADATA()}\n${METADATA()}`),
    policyBody().replace(METADATA(), "<!-- repo-automation-metadata-head:v1 {} -->"),
    policyBody({ metadataHead: NEXT_HEAD }),
  ];
  for (const body of bodies) {
    await t.test(body.slice(0, 32), async () => {
      const { github, result } = await run(evaluatorState({
        comments: [{ id: 77, author: "github-actions[bot]", type: "Bot", body }],
      }));
      assert.notEqual(result.pullRequests[0].merge.action, "ENABLE");
      assert.equal(result.pullRequests[0].merge.blockers.includes("metadata-stale"), true);
      assert.deepEqual(github.calls.enableAutoMerge, []);
    });
  }
});

test("live protected branch and merge state are mandatory", async (t) => {
  const cases = [
    ["unprotected", { branchProtection: { main: false } }],
    ["disallowed", { pullRequest: pullRequest({ baseBranch: "experimental" }), mergeStates: [mergeState({ baseBranch: "experimental" }), mergeState({ baseBranch: "experimental" })] }],
    ["draft", { pullRequest: pullRequest({ draft: true }), mergeStates: [mergeState({ draft: true }), mergeState({ draft: true })] }],
    ["conflict", { mergeStates: [mergeState({ mergeability: "CONFLICTING" }), mergeState({ mergeability: "CONFLICTING" })] }],
    ["unknown", { mergeStates: [mergeState({ mergeability: "UNKNOWN" }), mergeState({ mergeability: "UNKNOWN" })] }],
  ];
  for (const [name, overrides] of cases) {
    await t.test(name, async () => {
      const { github, result } = await run(evaluatorState(overrides));
      assert.notEqual(result.pullRequests[0].merge.action, "ENABLE");
      assert.deepEqual(github.calls.enableAutoMerge, []);
    });
  }

  await t.test("closed live REST state fails before every mutation", async () => {
    const { github, result } = await run(evaluatorState({
      pullRequest: pullRequest({ state: "closed" }),
    }));
    assert.equal(result.status, "partial");
    assert.deepEqual(writes(github), []);
  });

  await t.test("fork head fields are never read as trusted content", async () => {
    const fork = pullRequest({
      headRepository: { owner: "attacker", repo: "fork" },
      headRef: "hostile-ref",
    });
    const { github, result } = await run(evaluatorState({ pullRequest: fork }));
    assert.equal(result.pullRequests[0].merge.action, "ENABLE");
    assert.equal(JSON.stringify(github.callOrder).includes("hostile-ref"), false);
  });
});

test("wrong native method is disabled without enabling until a later pass", async () => {
  const { github, result } = await run(evaluatorState({
    mergeStates: [mergeState({ autoMergeMethod: "MERGE" }), mergeState({ autoMergeMethod: "MERGE" })],
  }));
  assert.equal(result.pullRequests[0].merge.action, "DISABLE");
  assert.deepEqual(github.calls.disableAutoMerge, [{ pullRequestId: "PR_node_42" }]);
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("head races restart once from a fresh snapshot and a second race performs no merge mutation", async (t) => {
  await t.test("race before labels converges from one restart", async () => {
    const next = pullRequest({ headOid: NEXT_HEAD });
    const { github, result } = await run(evaluatorState({
      pullRequests: [pullRequest(), next, next, next],
      comments: [{ id: 77, author: "github-actions[bot]", type: "Bot", body: policyBody({ head: NEXT_HEAD }) }],
      reviews: [approvedReview({ commitOid: NEXT_HEAD })],
      mergeStates: [mergeState({ headOid: NEXT_HEAD }), mergeState({ headOid: NEXT_HEAD })],
    }));
    assert.equal(result.pullRequests[0].attempts, 2);
    assert.deepEqual(github.calls.enableAutoMerge, [{ pullRequestId: "PR_node_42", mergeMethod: "SQUASH" }]);
  });

  await t.test("second race fails closed", async () => {
    const second = pullRequest({ headOid: NEXT_HEAD });
    const third = pullRequest({ headOid: "8".repeat(40) });
    const { github, result } = await run(evaluatorState({
      pullRequests: [pullRequest(), second, second, third],
      comments: [{ id: 77, author: "github-actions[bot]", type: "Bot", body: policyBody({ head: NEXT_HEAD }) }],
      reviews: [approvedReview({ commitOid: NEXT_HEAD })],
      mergeStates: [mergeState({ headOid: NEXT_HEAD }), mergeState({ headOid: "8".repeat(40) })],
    }));
    assert.equal(result.pullRequests[0].merge.action, "NOOP");
    assert.equal(result.pullRequests[0].merge.blockers.includes("head-changed"), true);
    assert.deepEqual(github.calls.enableAutoMerge, []);
    assert.deepEqual(github.calls.disableAutoMerge, []);
  });

  await t.test("race after label reconciliation but before GraphQL mutation restarts", async () => {
    const next = pullRequest({ headOid: NEXT_HEAD });
    const { github, result } = await run(evaluatorState({
      pullRequests: [pullRequest(), pullRequest(), pullRequest(), next, next, next],
      comments: [{ id: 77, author: "github-actions[bot]", type: "Bot", body: policyBody({ head: NEXT_HEAD }) }],
      reviews: [approvedReview({ commitOid: NEXT_HEAD })],
      mergeStates: [
        mergeState(),
        mergeState({ headOid: NEXT_HEAD }),
        mergeState({ headOid: NEXT_HEAD }),
        mergeState({ headOid: NEXT_HEAD }),
      ],
    }));
    assert.equal(result.pullRequests[0].attempts, 2);
    assert.deepEqual(github.calls.enableAutoMerge, [{ pullRequestId: "PR_node_42", mergeMethod: "SQUASH" }]);
  });
});

test("dry-run performs complete explicit and all-open reads with exactly zero mutations", async (t) => {
  for (const [name, options, state] of [
    ["explicit", { prNumber: "42" }, evaluatorState()],
    ["all-open", { prNumber: "", event: { repository: workflowEvent.repository, schedule: "*/15 * * * *" } }, evaluatorState({ openPullRequestNumbers: [42] })],
  ]) {
    await t.test(name, async () => {
      const { github, result } = await run(state, { ...options, dryRun: true });
      assert.equal(result.status, "planned");
      assert.deepEqual(writes(github), []);
      assert.equal(github.calls.getPullRequest.length > 0, true);
      assert.equal(github.calls.getMergeState.length > 0, true);
    });
  }
});

test("source contains fixed GraphQL documents and never builds them from event bytes", () => {
  const source = fs.readFileSync(path.join(__dirname, "..", "src", "github-client.js"), "utf8");
  assert.match(source, /const MERGE_STATE_QUERY =/);
  assert.match(source, /const ENABLE_AUTO_MERGE_MUTATION =/);
  assert.match(source, /const DISABLE_AUTO_MERGE_MUTATION =/);
  assert.doesNotMatch(source, /graphql\s*\(\s*`[^`]*\$\{/s);
});

test("metadata rendering emits separate exact head evidence and command rendering preserves it", () => {
  const metadata = renderPolicyComment({
    headOid: HEAD,
    valid: true,
    configuration: { valid: true },
    title: { valid: true, error: null },
    dco: { valid: true, failures: [], exempted: [] },
    ownership: { valid: true, uncoveredPaths: [] },
    labels: { add: [], remove: [] },
    reviewers: { request: [], preserved: [] },
  });
  assert.equal(metadata.split(METADATA()).length - 1, 1);
  const rendered = renderCommandPolicyComment({
    existingBody: metadata,
    state: { headOid: HEAD, lgtm: lgtm(), lastRetest: null },
    items: [],
    policy: { lgtm: true, approved: true, hold: false, needsApproval: false },
  });
  assert.equal(rendered.split(METADATA()).length - 1, 1);
});

test("GitHub client uses exact GraphQL documents and variables and validates live responses", async () => {
  const { createGitHubClient } = require("../src/github-client.js");
  const calls = [];
  const apiRun = {
    id: 7001,
    name: "Review observer",
    path: ".github/workflows/review-observer.yml@main",
    event: "pull_request_review",
    status: "completed",
    repository: { full_name: "NVIDIA/k8s-test-infra" },
    pull_requests: [{ number: 42 }],
  };
  const endpoint = (data) => async () => ({ data });
  const octokit = {
    paginate: async (handler, parameters) => (await handler(parameters)).data,
    rest: {
      actions: { getWorkflowRun: endpoint(apiRun) },
      pulls: { list: endpoint([{ number: 42 }]) },
      repos: { getBranch: endpoint({ protected: true }) },
    },
    async graphql(document, variables) {
      calls.push({ document, variables });
      if (document.includes("RepositoryAutomationMergeState")) return { repository: {
        nameWithOwner: "NVIDIA/k8s-test-infra",
        pullRequest: {
          id: "PR_node_42",
          number: 42,
          state: "OPEN",
          isDraft: false,
          baseRefName: "main",
          headRefOid: HEAD,
          mergeable: "MERGEABLE",
          autoMergeRequest: null,
        },
      } };
      if (document.includes("RepositoryAutomationEnableAutoMerge")) return {
        enablePullRequestAutoMerge: { pullRequest: { id: variables.pullRequestId } },
      };
      return { disablePullRequestAutoMerge: { pullRequest: { id: variables.pullRequestId } } };
    },
  };
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
  assert.deepEqual(await client.getEvaluationWorkflowRun(7001), {
    id: 7001,
    name: "Review observer",
    workflowPath: ".github/workflows/review-observer.yml",
    workflowSourceRef: "main",
    event: "pull_request_review",
    status: "completed",
    repository: "nvidia/k8s-test-infra",
    pullRequestNumbers: [42],
  });
  assert.deepEqual(await client.listOpenPullRequestNumbers(), [42]);
  assert.equal(await client.getBranchProtection("main"), true);
  assert.deepEqual(await client.getMergeState(42), mergeState());
  await client.enableAutoMerge("PR_node_42", "SQUASH");
  await client.disableAutoMerge("PR_node_42");
  assert.deepEqual(calls.map(({ variables }) => variables), [
    { owner: "NVIDIA", repo: "k8s-test-infra", number: 42 },
    { pullRequestId: "PR_node_42", mergeMethod: "SQUASH" },
    { pullRequestId: "PR_node_42" },
  ]);
  assert.equal(calls.every(({ document }) => !document.includes("event-")), true);
});

test("live evaluator workflow source suffix is retained only when structurally safe", async (t) => {
  const { createGitHubClient } = require("../src/github-client.js");
  for (const [suffix, expected] of [
    ["main", "main"],
    ["release/v1", "release/v1"],
    ["refs/heads/main", "refs/heads/main"],
    ["", null],
    ["../main", null],
    ["main@evil", null],
    ["refs//heads/main", null],
  ]) {
    await t.test(JSON.stringify(suffix), async () => {
      const octokit = { rest: { actions: { getWorkflowRun: async () => ({ data: {
        id: 7001,
        name: "Review observer",
        path: `.github/workflows/review-observer.yml@${suffix}`,
        event: "pull_request_review",
        status: "completed",
        repository: { full_name: "NVIDIA/k8s-test-infra" },
        pull_requests: [{ number: 42 }],
      } }) } } };
      const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
      const run = await client.getEvaluationWorkflowRun(7001);
      if (expected === null) assert.equal(run, null);
      else assert.equal(run.workflowSourceRef, expected);
    });
  }
});

test("armed native auto-merge is safely disabled after later authority failures", async (t) => {
  const methods = ["SQUASH", "MERGE", "REBASE"];
  const failures = [
    "listPullRequestFiles",
    "listPullRequestReviews",
    "getPolicyComment",
    "getContentAtRevision",
    "getBranchProtection",
  ];
  for (const method of methods) {
    for (const operation of failures) {
      await t.test(`${method} ${operation}`, async () => {
        const failure = new Error(`${operation} unavailable`);
        const { github, result } = await run(evaluatorState({
          failures: { [operation]: failure },
          mergeStates: [mergeState({ autoMergeMethod: method }), mergeState({ autoMergeMethod: method })],
        }));
        assert.equal(result.status, "partial");
        assert.deepEqual(github.calls.addPolicyLabel, []);
        assert.deepEqual(github.calls.removePolicyLabel, []);
        assert.deepEqual(github.calls.disableAutoMerge, [{ pullRequestId: "PR_node_42" }]);
        assert.equal(github.calls.getMergeState.length, 2);
      });
    }
  }

  await t.test("invalid policy disables without authority-label mutation", async () => {
    const config = loadConfig(repositoryRoot);
    const { github, result } = await run(evaluatorState({
      mergeStates: [mergeState({ autoMergeMethod: "SQUASH" }), mergeState({ autoMergeMethod: "SQUASH" })],
    }), { config: { ...config, policy: { ...config.policy, protectedBranches: [] } } });
    assert.equal(result.status, "partial");
    assert.deepEqual(github.calls.disableAutoMerge, [{ pullRequestId: "PR_node_42" }]);
    assert.deepEqual(github.calls.addPolicyLabel, []);
  });

  for (const [name, state] of [
    ["file bound", { files: Array.from({ length: 1001 }, (_, index) => ({ path: `file-${index}`, additions: 1, deletions: 0, status: "modified" })) }],
    ["review bound", { reviews: Array.from({ length: 1001 }, (_, index) => approvedReview({ id: index + 1 })) }],
    ["default branch API", { failures: { getDefaultBranchRevision: new Error("revision unavailable") } }],
  ]) {
    await t.test(name, async () => {
      const { github, result } = await run(evaluatorState({
        ...state,
        mergeStates: [mergeState({ autoMergeMethod: "SQUASH" }), mergeState({ autoMergeMethod: "SQUASH" })],
      }));
      assert.equal(result.status, "partial");
      assert.deepEqual(github.calls.disableAutoMerge, [{ pullRequestId: "PR_node_42" }]);
      assert.deepEqual(github.calls.addPolicyLabel, []);
    });
  }

  await t.test("changed head during safe-disable fence mutates nothing", async () => {
    const { github } = await run(evaluatorState({
      failures: { listPullRequestFiles: new Error("files unavailable") },
      pullRequests: [pullRequest(), pullRequest({ headOid: NEXT_HEAD })],
      mergeStates: [mergeState({ autoMergeMethod: "SQUASH" })],
    }));
    assert.deepEqual(writes(github), []);
  });

  await t.test("ambiguous final GraphQL read mutates nothing", async () => {
    const { github } = await run(evaluatorState({
      failures: { listPullRequestFiles: new Error("files unavailable"), getMergeState: [null, new Error("ambiguous")] },
      mergeStates: [mergeState({ autoMergeMethod: "SQUASH" })],
    }));
    assert.deepEqual(writes(github), []);
  });
});

test("armed native auto-merge fail-safe covers every post-authority failure", async (t) => {
  async function assertFailSafe(state, expectedOperation) {
    const { github, result } = await run(evaluatorState(state));
    assert.equal(result.status, "partial");
    assert.equal(github.calls[expectedOperation].length > 0, true);
    assert.deepEqual(github.calls.disableAutoMerge, [{ pullRequestId: "PR_node_42" }]);
    assert.deepEqual(github.calls.enableAutoMerge, []);
    return github;
  }

  await t.test("known hold plus final label read failure disables SQUASH once", async () => {
    const github = await assertFailSafe({
      labels: ["lgtm", "approved", "do-not-merge/hold"],
      failures: { listIssueLabels: [null, new Error("final labels unavailable")] },
      mergeStates: [mergeState({ autoMergeMethod: "SQUASH" }), mergeState({ autoMergeMethod: "SQUASH" })],
    }, "listIssueLabels");
    assert.equal(github.calls.listIssueLabels.length, 2);
  });

  for (const [name, method, labels, operation] of [
    ["add display label", "MERGE", ["do-not-merge/hold"], "addPolicyLabel"],
    ["remove stale display label", "REBASE", ["lgtm", "approved", "do-not-merge/needs-approval", "do-not-merge/hold"], "removePolicyLabel"],
  ]) {
    await t.test(`${name} failure disables ${method}`, async () => {
      const github = await assertFailSafe({
        labels,
        failures: { [operation]: new Error(`${operation} unavailable`) },
        mergeStates: [mergeState({ autoMergeMethod: method }), mergeState({ autoMergeMethod: method })],
      }, operation);
      assert.equal(github.calls.disableAutoMerge.length, 1);
    });
  }

  await t.test("pre-write REST failure disables proven request", async () => {
    const github = await assertFailSafe({
      failures: { getPullRequest: [null, new Error("pre-write PR unavailable")] },
      mergeStates: [mergeState({ autoMergeMethod: "SQUASH" }), mergeState({ autoMergeMethod: "SQUASH" })],
    }, "getPullRequest");
    assert.equal(github.calls.getPullRequest.length, 3);
  });

  for (const method of ["SQUASH", "MERGE", "REBASE"]) {
    await t.test(`final GraphQL failure disables ${method}`, async () => {
      const github = await assertFailSafe({
        failures: { getMergeState: [null, new Error("final GraphQL unavailable")] },
        mergeStates: [mergeState({ autoMergeMethod: method }), mergeState({ autoMergeMethod: method })],
      }, "getMergeState");
      assert.equal(github.calls.getMergeState.length, 3);
    });
  }

  await t.test("persistent final ambiguity performs no mutation", async () => {
    const { github, result } = await run(evaluatorState({
      failures: { getMergeState: [null, new Error("final GraphQL unavailable"), new Error("still ambiguous")] },
      mergeStates: [mergeState({ autoMergeMethod: "SQUASH" })],
    }));
    assert.equal(result.status, "partial");
    assert.deepEqual(github.calls.enableAutoMerge, []);
    assert.deepEqual(github.calls.disableAutoMerge, []);
  });

  await t.test("a concurrently changed native method is not the same request", async () => {
    const { github } = await run(evaluatorState({
      failures: { listIssueLabels: [null, new Error("final labels unavailable")] },
      mergeStates: [mergeState({ autoMergeMethod: "SQUASH" }), mergeState({ autoMergeMethod: "MERGE" })],
    }));
    assert.deepEqual(github.calls.disableAutoMerge, []);
  });

  await t.test("failed fail-safe disable is attempted exactly once without retry", async () => {
    const { github, result } = await run(evaluatorState({
      failures: {
        listIssueLabels: [null, new Error("final labels unavailable")],
        disableAutoMerge: new Error("disable unavailable"),
      },
      mergeStates: [mergeState({ autoMergeMethod: "SQUASH" }), mergeState({ autoMergeMethod: "SQUASH" })],
    }));
    assert.equal(result.status, "partial");
    assert.equal(github.calls.disableAutoMerge.length, 1);
  });

  await t.test("failed normal disable never falls through to fail-safe retry", async () => {
    const { github, result } = await run(evaluatorState({
      labels: ["lgtm", "approved", "do-not-merge/hold"],
      failures: { disableAutoMerge: new Error("normal disable unavailable") },
      mergeStates: [mergeState({ autoMergeMethod: "SQUASH" }), mergeState({ autoMergeMethod: "SQUASH" })],
    }));
    assert.equal(result.status, "partial");
    assert.equal(github.calls.disableAutoMerge.length, 1);
  });

  await t.test("dry-run post-authority failure never invokes fail-safe disable", async () => {
    const { github, result } = await run(evaluatorState({
      failures: { listIssueLabels: [null, new Error("final labels unavailable")] },
      mergeStates: [mergeState({ autoMergeMethod: "SQUASH" })],
    }), { dryRun: true });
    assert.equal(result.status, "partial");
    assert.deepEqual(writes(github), []);
  });

  await t.test("redelivery converges after fail-safe disable and a partial display write", async () => {
    const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
    const github = createFakeGitHub(evaluatorState({
      labels: ["do-not-merge/hold"],
      failures: { addPolicyLabel: [new Error("first add unavailable")] },
      mergeStates: [
        mergeState({ autoMergeMethod: "MERGE" }),
        mergeState({ autoMergeMethod: "MERGE" }),
        mergeState(),
        mergeState(),
      ],
    }));
    const options = {
      event: { repository: workflowEvent.repository },
      eventName: "workflow_dispatch",
      github,
      config: loadConfig(repositoryRoot),
      dryRun: false,
      prNumber: "42",
    };
    const first = await runMergeEvaluate(options);
    const second = await runMergeEvaluate(options);
    assert.equal(first.status, "partial");
    assert.equal(second.status, "complete");
    assert.equal(github.calls.disableAutoMerge.length, 1);
    assert.equal(github.calls.enableAutoMerge.length, 0);
  });
});

test("Task 7 bounded pagination stops at limit plus one without requesting later pages", async (t) => {
  const { createGitHubClient } = require("../src/github-client.js");
  function endpoint(name, pages, calls) {
    return async (parameters) => {
      calls.push({ name, page: parameters.page ?? 1 });
      return { data: pages[(parameters.page ?? 1) - 1] ?? [] };
    };
  }
  async function assertStops(name, pages, invoke, restFactory) {
    const calls = [];
    const octokit = {
      rest: restFactory(endpoint(name, pages, calls)),
      async paginate(handler, parameters, map) {
        const values = [];
        let stopped = false;
        const done = () => { stopped = true; };
        for (let page = 1; page <= pages.length && !stopped; page += 1) {
          const response = await handler({ ...parameters, page });
          values.push(...map(response, done));
        }
        return values;
      },
    };
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
    await assert.rejects(() => invoke(client), /exceeds limit/i);
    assert.equal(calls.at(-1).page < pages.length, true, `${name} must stop before the sentinel page`);
  }

  await t.test("open PRs", () => assertStops(
    "pulls",
    [Array.from({ length: 100 }, (_, index) => ({ number: index + 1 })), [{ number: 101 }], [{ number: 102 }]],
    (client) => client.listOpenPullRequestNumbers(),
    (handler) => ({ pulls: { list: handler } }),
  ));
  await t.test("files", () => assertStops(
    "files",
    Array.from({ length: 12 }, (_, page) => Array.from({ length: 100 }, (_, index) => ({ filename: `${page}-${index}`, additions: 1, deletions: 0, status: "modified" }))),
    (client) => client.listPullRequestFiles(42),
    (handler) => ({ pulls: { listFiles: handler } }),
  ));
  await t.test("reviews", () => assertStops(
    "reviews",
    Array.from({ length: 12 }, (_, page) => Array.from({ length: 100 }, (_, index) => ({ id: page * 100 + index + 1 }))),
    (client) => client.listPullRequestReviews(42),
    (handler) => ({ pulls: { listReviews: handler } }),
  ));
  await t.test("comments", () => assertStops(
    "comments",
    Array.from({ length: 12 }, (_, page) => Array.from({ length: 100 }, (_, index) => ({ id: page * 100 + index + 1, body: "ordinary" }))),
    (client) => client.getPolicyComment(42, MARKER),
    (handler) => ({ issues: { listComments: handler } }),
  ));
});

test("candidate failures are isolated and bounded API ambiguity never enables", async () => {
  const state = evaluatorState({
    openPullRequestNumbers: [42, 43],
    pullRequests: [pullRequest(), pullRequest(), pullRequest(), { ...pullRequest(), number: 43 }],
  });
  const { github, result } = await run(state, {
    event: { repository: workflowEvent.repository, schedule: "*/15 * * * *" },
    prNumber: "",
  });
  assert.equal(result.status, "partial");
  assert.equal(result.pullRequests.length, 2);
  assert.deepEqual(github.calls.enableAutoMerge, [{ pullRequestId: "PR_node_42", mergeMethod: "SQUASH" }]);
});

test("index dispatches merge-evaluate with explicit inputs and preserves dry-run zero writes", async () => {
  const { run: runAction } = require("../src/index.js");
  const githubClient = createFakeGitHub(evaluatorState());
  const outputs = [];
  const core = {
    getInput(name) {
      if (name === "mode") return "merge-evaluate";
      if (name === "pr-number") return "42";
      return "";
    },
    getBooleanInput() { return true; },
    setOutput(name, value) { outputs.push([name, value]); },
  };
  const result = await runAction({
    core,
    githubClient,
    event: { repository: workflowEvent.repository },
    workspace: repositoryRoot,
    eventName: "workflow_dispatch",
  });
  assert.equal(result.status, "planned");
  assert.deepEqual(writes(githubClient), []);
  assert.equal(outputs.length, 1);
});
