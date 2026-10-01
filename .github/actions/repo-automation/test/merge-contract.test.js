"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const { loadConfig } = require("../src/config.js");
const {
  createEmptyState,
  serializePolicyState,
} = require("../src/commands/state.js");
const { policyDigest } = require("../src/policy-digest.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const config = loadConfig(repositoryRoot);
const HEAD = "6".repeat(40);
const NEXT_HEAD = "7".repeat(40);
const REVISION = "8".repeat(40);
const REPOSITORY = "nvidia/k8s-test-infra";
const OWNER_SOURCE = "reviewers: [alice]\napprovers: [bob]\n";
const ALIASES_SOURCE = "aliases: {}\n";
const POLICY_MARKER = "<!-- repo-automation-policy:v1 -->";
const METADATA = (head = HEAD) => (
  `<!-- repo-automation-metadata-head:v1 {"headOid":"${head}"} -->`
);
const WORKFLOW_EVENT = {
  repository: {
    name: "k8s-test-infra",
    full_name: "NVIDIA/k8s-test-infra",
    owner: { login: "NVIDIA" },
  },
};

function digest() {
  return policyDigest({
    repository: REPOSITORY,
    revision: REVISION,
    policy: config.policy,
    ownerSources: [{ path: "/OWNERS", source: OWNER_SOURCE }],
    aliasesSource: ALIASES_SOURCE,
  });
}

function evidence(command, actor, actorRole, sourceId) {
  return {
    repository: REPOSITORY,
    pullRequest: 42,
    actor,
    actorRole,
    sourceType: "comment",
    sourceId,
    policyDigest: digest(),
    headOid: HEAD,
    createdAt: "2026-09-17T08:00:00.000Z",
    command,
  };
}

function storedEvidence(record) {
  return {
    repository: record.repository,
    pullRequest: record.pullRequest,
    actor: record.actor,
    actorRole: record.actorRole,
    sourceType: record.sourceType,
    sourceId: record.sourceId,
    policyDigest: record.policyDigest,
    headOid: record.headOid,
    createdAt: record.createdAt,
  };
}

function policyBody({
  headOid = HEAD,
  metadataHeadOid = headOid,
  lgtms = [evidence("lgtm", "alice", "reviewer", 8000)],
  approvals = [evidence("approve", "bob", "approver", 8001)],
  hold = null,
} = {}) {
  const state = createEmptyState({
    repository: REPOSITORY,
    pullRequest: 42,
    policyDigest: digest(),
    headOid,
  });
  state.lgtms = lgtms.map(storedEvidence);
  state.approvals = approvals.map(storedEvidence);
  state.hold = hold;
  return [
    POLICY_MARKER,
    serializePolicyState(state),
    METADATA(metadataHeadOid),
    "## Repository policy",
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
    repository: REPOSITORY,
    ...overrides,
  };
}

function liveCommand(id, author, body, overrides = {}) {
  return {
    id,
    issueNumber: 42,
    body,
    author,
    authorType: "User",
    edited: false,
    ...overrides,
  };
}

function evaluatorState(overrides = {}) {
  return {
    pullRequest: pullRequest(),
    files: [{ path: "pkg/gpu.go", additions: 2, deletions: 1, status: "modified" }],
    labels: ["lgtm", "approved", "maintainer/custom"],
    comments: [{ id: 77, author: "github-actions[bot]", body: policyBody() }],
    issueComments: [
      liveCommand(8000, "alice", "/lgtm"),
      liveCommand(8001, "bob", "/approve"),
    ],
    contents: {
      "/OWNERS": OWNER_SOURCE,
      "/OWNERS_ALIASES": ALIASES_SOURCE,
    },
    defaultBranchRevision: REVISION,
    branchProtection: { main: true },
    mergeStates: [mergeState(), mergeState(), mergeState(), mergeState()],
    ...overrides,
  };
}

async function run(state = evaluatorState(), options = {}) {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  const github = createFakeGitHub(state);
  if (options.reviewDetails !== undefined) {
    const getReview = github.getPullRequestReview.bind(github);
    github.getPullRequestReview = async (number, id) => {
      const review = await getReview(number, id);
      const details = typeof options.reviewDetails === "function"
        ? options.reviewDetails(review, github.calls.getPullRequestReview.length)
        : options.reviewDetails[id];
      return { ...review, ...details };
    };
  }
  if (options.onPolicyLabelAdd !== undefined) {
    const addLabel = github.addPolicyLabel.bind(github);
    github.addPolicyLabel = async (number, label) => {
      await addLabel(number, label);
      await options.onPolicyLabelAdd(label);
    };
  }
  if (options.onMergePolicyCheck !== undefined) {
    const setCheck = github.setMergePolicyCheck.bind(github);
    github.setMergePolicyCheck = async (...parameters) => {
      await setCheck(...parameters);
      await options.onMergePolicyCheck(parameters[2]);
    };
  }
  const result = await runMergeEvaluate({
    event: options.event ?? WORKFLOW_EVENT,
    eventName: options.eventName ?? "workflow_dispatch",
    github,
    config,
    dryRun: options.dryRun ?? false,
    prNumber: Object.hasOwn(options, "prNumber") ? options.prNumber : "42",
  });
  return { github, result };
}

function operationIndex(github, operation) {
  return github.callOrder.findIndex((entry) => entry.operation === operation);
}

test("publishes success for the exact head without arming native auto-merge", async () => {
  const { github, result } = await run();

  assert.equal(result.status, "complete");
  assert.deepEqual(github.calls.setMergePolicyCheck, [{
    prNumber: 42,
    headOid: HEAD,
    conclusion: "success",
    summary: "Repository merge policy passed.",
  }]);
  assert.deepEqual(github.calls.enableAutoMerge, []);
  assert.deepEqual(github.calls.disableAutoMerge, []);
  assert.ok(github.calls.getPolicyComment.length >= 2, "gate inputs must be re-read");
  assert.ok(github.calls.getPullRequest.length >= 3, "head must be re-read after success");
  assert.equal(operationIndex(github, "enableAutoMerge"), -1);
});

test("leaves an eligible human SQUASH auto-merge request unchanged", async () => {
  const { github, result } = await run(evaluatorState({
    mergeStates: [
      mergeState({ autoMergeMethod: "SQUASH" }),
      mergeState({ autoMergeMethod: "SQUASH" }),
      mergeState({ autoMergeMethod: "SQUASH" }),
      mergeState({ autoMergeMethod: "SQUASH" }),
    ],
  }));

  assert.equal(result.pullRequests[0].merge.action, "NOOP");
  assert.deepEqual(github.calls.setMergePolicyCheck, [{
    prNumber: 42,
    headOid: HEAD,
    conclusion: "success",
    summary: "Repository merge policy passed.",
  }]);
  assert.deepEqual(github.calls.enableAutoMerge, []);
  assert.deepEqual(github.calls.disableAutoMerge, []);
});

test("disarms an unsupported method armed after the permissive re-read", async (t) => {
  for (const method of ["MERGE", "REBASE"]) await t.test(method, async () => {
    const { github, result } = await run(evaluatorState({
      mergeStates: [
        mergeState(),
        mergeState(),
        mergeState(),
        mergeState({ autoMergeMethod: method }),
        mergeState({ autoMergeMethod: method }),
      ],
    }));

    assert.equal(result.pullRequests[0].merge.action, "DISABLE");
    assert.deepEqual(github.calls.setMergePolicyCheck, [
      {
        prNumber: 42,
        headOid: HEAD,
        conclusion: "success",
        summary: "Repository merge policy passed.",
      },
      {
        prNumber: 42,
        headOid: HEAD,
        conclusion: "action_required",
        summary: "Repository merge policy blocked: auto-merge-method-mismatch.",
      },
    ]);
    assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
    const restrictiveCheck = github.callOrder.findIndex(({ operation, parameters }) => (
      operation === "setMergePolicyCheck" && parameters.conclusion === "action_required"
    ));
    assert.ok(restrictiveCheck < operationIndex(github, "disableAutoMerge"));
  });
});

test("does not disarm an unsupported request that changes before the restrictive write", async () => {
  const { github, result } = await run(evaluatorState({
    mergeStates: [
      mergeState(),
      mergeState(),
      mergeState(),
      mergeState({ autoMergeMethod: "MERGE" }),
      mergeState({ autoMergeMethod: "SQUASH" }),
    ],
  }));

  assert.equal(result.pullRequests[0].merge.action, "DISABLE");
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
  assert.deepEqual(github.calls.disableAutoMerge, []);
});

test("does not disarm an unsupported request when its head changes before the restrictive write", async () => {
  const { github, result } = await run(evaluatorState({
    pullRequests: [
      pullRequest(),
      pullRequest(),
      pullRequest(),
      pullRequest(),
      pullRequest({ headOid: NEXT_HEAD }),
    ],
    mergeStates: [
      mergeState(),
      mergeState(),
      mergeState(),
      mergeState({ autoMergeMethod: "MERGE" }),
      mergeState({ autoMergeMethod: "MERGE", headOid: NEXT_HEAD }),
    ],
  }));

  assert.equal(result.pullRequests[0].merge.action, "DISABLE");
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
  assert.deepEqual(github.calls.disableAutoMerge, []);
});

test("publishes a non-success check before it disables an armed pull request", async () => {
  const state = evaluatorState({
    comments: [{ id: 77, author: "github-actions[bot]", body: policyBody({
      hold: {
        repository: REPOSITORY,
        pullRequest: 42,
        actor: "bob",
        actorRole: "owner",
        sourceType: "comment",
        sourceId: 8002,
        createdAt: "2026-09-17T08:01:00.000Z",
      },
    }) }],
    mergeStates: [
      mergeState({ autoMergeMethod: "SQUASH" }),
      mergeState({ autoMergeMethod: "SQUASH" }),
    ],
  });
  const { github, result } = await run(state);

  assert.equal(result.pullRequests[0].merge.action, "DISABLE");
  assert.ok(result.pullRequests[0].merge.blockers.includes("hold-active"));
  assert.equal(github.calls.setMergePolicyCheck[0].conclusion, "action_required");
  assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
  assert.ok(
    operationIndex(github, "setMergePolicyCheck") < operationIndex(github, "disableAutoMerge"),
    "the non-success check must be visible before auto-merge is disabled",
  );
});

test("visible labels cannot forge missing bot-owned evidence", async () => {
  const { github, result } = await run(evaluatorState({
    labels: ["lgtm", "approved"],
    comments: [{ id: 77, author: "github-actions[bot]", body: policyBody({
      lgtms: [],
      approvals: [],
    }) }],
  }));

  assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
  assert.ok(result.pullRequests[0].merge.blockers.includes("approval-coverage-incomplete"));
  assert.equal(github.calls.setMergePolicyCheck[0].conclusion, "action_required");
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("edited or changed source commands invalidate stored evidence", async () => {
  const { github, result } = await run(evaluatorState({
    issueComments: [
      liveCommand(8000, "alice", "/lgtm", { edited: true }),
      liveCommand(8001, "bob", "/approve changed"),
    ],
  }));

  assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
  assert.ok(result.pullRequests[0].merge.blockers.includes("approval-coverage-incomplete"));
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

function submittedReview(id = 9000, user = "alice", overrides = {}) {
  return {
    id,
    user,
    state: "APPROVED",
    commitOid: HEAD,
    submittedAt: "2026-09-17T09:00:00.000Z",
    ...overrides,
  };
}

function reviewLgtmState(overrides = {}) {
  return evaluatorState({
    labels: ["maintainer/custom"],
    comments: [{ id: 77, author: "github-actions[bot]", body: policyBody({ lgtms: [] }) }],
    reviews: [submittedReview()],
    ...overrides,
  });
}

test("explicit current-head review LGTM accepts current OWNERS reviewers and approvers", async (t) => {
  for (const user of ["alice", "bob"]) {
    for (const state of ["APPROVED", "COMMENTED"]) await t.test(`${user} ${state}`, async () => {
      const { github, result } = await run(reviewLgtmState({
        reviews: [submittedReview(9000, user, { state })],
      }), { reviewDetails: { 9000: { body: "/lgtm" } } });

      assert.deepEqual(result.pullRequests[0].merge.blockers, []);
      assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "success");
      assert.deepEqual(github.calls.addPolicyLabel, [
        { prNumber: 42, label: "approved" },
        { prNumber: 42, label: "lgtm" },
      ]);
      assert.deepEqual(github.calls.enableAutoMerge, []);
    });
  }
});

test("review LGTM is trusted without stored command state but still requires metadata", async () => {
  const { github, result } = await run(reviewLgtmState({ comments: [] }), {
    reviewDetails: { 9000: { body: "/lgtm" } },
  });

  assert.equal(result.pullRequests[0].merge.blockers.includes("lgtm-missing"), false);
  assert.equal(result.pullRequests[0].merge.blockers.includes("lgtm-untrusted"), false);
  assert.ok(result.pullRequests[0].merge.blockers.includes("metadata-stale"));
  assert.ok(github.calls.addPolicyLabel.some(({ label }) => label === "lgtm"));
});

test("metadata-only policy comment accepts current review LGTM and native approval", async () => {
  const { github, result } = await run(reviewLgtmState({
    comments: [{ id: 77, author: "github-actions[bot]", body: `${POLICY_MARKER}\n${METADATA()}\n` }],
    issueComments: [],
    reviews: [submittedReview(9000, "bob")],
  }), { reviewDetails: { 9000: { body: "/lgtm" } } });

  assert.deepEqual(result.pullRequests[0].merge.blockers, []);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "success");
  assert.deepEqual(github.calls.addPolicyLabel, [
    { prNumber: 42, label: "approved" },
    { prNumber: 42, label: "lgtm" },
  ]);
  assert.deepEqual(github.calls.upsertPolicyComment, []);
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("metadata-only policy comment cannot replace missing or negative review authority", async (t) => {
  for (const [name, reviews, body, blocker] of [
    ["missing review", [], "/lgtm", "approval-coverage-incomplete"],
    ["approval without LGTM", [submittedReview(9000, "bob")], "Looks good", "lgtm-missing"],
    ["LGTM without approval", [submittedReview(9000, "bob", { state: "COMMENTED" })], "/lgtm", "approval-coverage-incomplete"],
    ["changes requested", [submittedReview(9000, "bob", { state: "CHANGES_REQUESTED" })], "/lgtm", "lgtm-missing"],
    ["dismissed", [submittedReview(9000, "bob", { state: "DISMISSED" })], "/lgtm", "lgtm-missing"],
    ["stale head", [submittedReview(9000, "bob", { commitOid: NEXT_HEAD })], "/lgtm", "lgtm-missing"],
  ]) await t.test(name, async () => {
    const { github, result } = await run(reviewLgtmState({
      comments: [{ id: 77, author: "github-actions[bot]", body: `${POLICY_MARKER}\n${METADATA()}\n` }],
      issueComments: [],
      reviews,
    }), { reviewDetails: { 9000: { body } } });

    assert.equal(result.pullRequests[0].merge.blockers.includes("metadata-stale"), false);
    assert.ok(result.pullRequests[0].merge.blockers.includes(blocker));
    assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
    assert.deepEqual(github.calls.enableAutoMerge, []);
  });
});

test("metadata-only policy comment rejects invalid metadata or command state", async (t) => {
  const state = policyBody().split("\n")[1];
  for (const [name, body] of [
    ["missing metadata", POLICY_MARKER],
    ["stale metadata", `${POLICY_MARKER}\n${METADATA(NEXT_HEAD)}`],
    ["malformed metadata", `${POLICY_MARKER}\n<!-- repo-automation-metadata-head:v1 {} -->`],
    ["duplicate metadata", `${POLICY_MARKER}\n${METADATA()}\n${METADATA()}`],
    ["duplicate policy marker", `${POLICY_MARKER}\n${POLICY_MARKER}\n${METADATA()}`],
    ["malformed state", `${POLICY_MARKER}\n<!-- repo-automation-state:v2 {} -->\n${METADATA()}`],
    ["duplicate state", `${POLICY_MARKER}\n${state}\n${state}\n${METADATA()}`],
    ["unsupported legacy state", `${POLICY_MARKER}\n${state.replace("state:v2", "state:v1")}\n${METADATA()}`],
    ["wrong state repository", `${POLICY_MARKER}\n${state.replace(REPOSITORY, "nvidia/other")}\n${METADATA()}`],
    ["wrong state pull request", `${POLICY_MARKER}\n${state.replace('"pullRequest":42', '"pullRequest":43')}\n${METADATA()}`],
  ]) await t.test(name, async () => {
    const { github, result } = await run(reviewLgtmState({
      comments: [{ id: 77, author: "github-actions[bot]", body }],
      reviews: [submittedReview(9000, "bob")],
    }), { reviewDetails: { 9000: { body: "/lgtm" } } });

    assert.ok(result.pullRequests[0].merge.blockers.includes("metadata-stale"));
    assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
    assert.deepEqual(github.calls.enableAutoMerge, []);
  });
});

test("COMMENTED review LGTM does not grant native approval or execute review commands", async () => {
  const { github, result } = await run(reviewLgtmState({
    comments: [{ id: 77, body: policyBody({ lgtms: [], approvals: [] }) }],
    reviews: [submittedReview(9000, "bob", { state: "COMMENTED" })],
  }), { reviewDetails: { 9000: { body: "/lgtm\n/approve\n/hold\n/retest" } } });

  assert.equal(result.pullRequests[0].merge.blockers.includes("lgtm-missing"), false);
  assert.ok(result.pullRequests[0].merge.blockers.includes("approval-coverage-incomplete"));
  assert.equal(result.pullRequests[0].merge.blockers.includes("hold-active"), false);
  assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "approved"), false);
  assert.deepEqual(github.calls.rerunFailedJobs, []);
  assert.deepEqual(github.calls.upsertPolicyComment, []);
});

test("native APPROVED without explicit review LGTM remains approval-only", async () => {
  const { github, result } = await run(reviewLgtmState({
    comments: [{ id: 77, body: policyBody({ lgtms: [], approvals: [] }) }],
    reviews: [submittedReview(9000, "bob")],
  }), { reviewDetails: { 9000: { body: "Looks good" } } });

  assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
  assert.equal(result.pullRequests[0].merge.blockers.includes("approval-coverage-incomplete"), false);
  assert.ok(github.calls.addPolicyLabel.some(({ label }) => label === "approved"));
  assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "lgtm"), false);
});

test("review LGTM rejects unsafe live evidence and untrusted actors", async (t) => {
  const cases = [
    ["wrong ID", { id: 9001 }],
    ["wrong reviewer", { user: "bob" }],
    ["stale commit", { commitOid: NEXT_HEAD }],
    ["changed submission", { submittedAt: "2026-09-17T09:01:00.000Z" }],
    ["changed body", { body: "LGTM" }],
    ["missing body", { body: undefined }],
    ["non-text body", { body: null }],
    ["dismissed", { state: "DISMISSED" }],
    ["changes requested", { state: "CHANGES_REQUESTED" }],
    ["pending", { state: "PENDING" }],
    ["quoted", { body: "> /lgtm" }],
    ["fenced", { body: "```\n/lgtm\n```" }],
    ["bad arguments", { body: "/lgtm please" }],
    ["cancel", { body: "/lgtm cancel" }],
    ["grant plus cancel", { body: "/lgtm\n/lgtm cancel" }],
    ["bot", {}, { type: "Bot" }],
    ["deleted reviewer", {}, { deleted: true }],
    ["unresolved reviewer", {}, { resolved: false }],
    ["identity mismatch", {}, { login: "bob" }],
  ];
  for (const [name, details, identity] of cases) await t.test(name, async () => {
    const { github, result } = await run(reviewLgtmState({
      users: identity === undefined ? {} : {
        alice: { login: "alice", type: "User", resolved: true, deleted: false, ...identity },
      },
    }), { reviewDetails: { 9000: { body: "/lgtm", ...details } } });

    assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
    assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
    assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "lgtm"), false);
  });
});

test("review LGTM rejects the PR author and users outside current OWNERS", async (t) => {
  for (const user of ["pr-author", "charlie"]) await t.test(user, async () => {
    const { result } = await run(reviewLgtmState({ reviews: [submittedReview(9000, user)] }), {
      reviewDetails: { 9000: { body: "/lgtm" } },
    });
    assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
  });
});

test("review LGTM fails closed when the live review cannot be read", async () => {
  const { github, result } = await run(reviewLgtmState({
    failures: { getPullRequestReview: new Error("review unavailable") },
  }), { reviewDetails: { 9000: { body: "/lgtm" } } });

  assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
});

test("latest submitted review replaces older LGTM independently of native approval reduction", async (t) => {
  for (const body of ["/lgtm cancel", "Reviewed again without LGTM"]) await t.test(body, async () => {
    const { github, result } = await run(reviewLgtmState({
      reviews: [
        submittedReview(9001, "alice", { state: "COMMENTED", submittedAt: "2026-09-17T10:00:00.000Z" }),
        submittedReview(),
      ],
    }), { reviewDetails: {
      9000: { body: "/lgtm" },
      9001: { body },
    } });

    assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
    assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "lgtm"), false);
  });
});

test("latest COMMENTED review can supply LGTM after an APPROVED review without LGTM", async () => {
  const { github, result } = await run(reviewLgtmState({ reviews: [
    submittedReview(9001, "alice", { state: "COMMENTED", submittedAt: "2026-09-17T10:00:00.000Z" }),
    submittedReview(),
  ] }), { reviewDetails: {
    9000: { body: "Approved" },
    9001: { body: "/lgtm" },
  } });

  assert.deepEqual(result.pullRequests[0].merge.blockers, []);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "success");
});

test("review LGTM revoked during the final authority read cannot publish success", async () => {
  const { github, result } = await run(reviewLgtmState(), {
    reviewDetails: (_review, call) => ({ body: call === 1 ? "/lgtm" : "/lgtm cancel" }),
  });

  assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
  assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
  assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "lgtm"), false);
});

test("latest dismissed or negative review revokes older review LGTM", async (t) => {
  for (const state of ["CHANGES_REQUESTED", "DISMISSED"]) await t.test(state, async () => {
    const { github, result } = await run(reviewLgtmState({ reviews: [
      submittedReview(9001, "alice", { state, submittedAt: "2026-09-17T10:00:00.000Z" }),
      submittedReview(),
    ] }), { reviewDetails: {
      9000: { body: "/lgtm" },
      9001: { body: "/lgtm" },
    } });

    assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
    assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "lgtm"), false);
  });
});

test("higher review ID breaks an equal submission-time tie for review LGTM", async () => {
  const { result } = await run(reviewLgtmState({ reviews: [
    submittedReview(9001, "alice", { state: "COMMENTED" }),
    submittedReview(),
  ] }), { reviewDetails: {
    9000: { body: "/lgtm" },
    9001: { body: "/lgtm cancel" },
  } });

  assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
});

test("a pending review does not replace submitted review LGTM", async () => {
  const pending = submittedReview(9001, "alice", { state: "PENDING" });
  delete pending.submittedAt;
  const { github, result } = await run(reviewLgtmState({
    reviews: [pending, submittedReview()],
  }), { reviewDetails: {
    9000: { body: "/lgtm" },
    9001: { body: "Not submitted" },
  } });

  assert.deepEqual(result.pullRequests[0].merge.blockers, []);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "success");
  assert.equal(github.calls.getPullRequestReview.some(({ reviewId }) => reviewId === 9001), false);
});

test("current live review body can add LGTM without historical edit metadata", async () => {
  const { github, result } = await run(reviewLgtmState(), {
    reviewDetails: { 9000: { body: "Updated review\n/lgtm" } },
  });

  assert.deepEqual(result.pullRequests[0].merge.blockers, []);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "success");
  assert.ok(github.calls.getPullRequestReview.length >= 2);
});

test("human review LGTM and approval can authorize a Dependabot-authored PR", async () => {
  const { github, result } = await run(reviewLgtmState({
    pullRequest: pullRequest({ author: "dependabot[bot]" }),
    reviews: [submittedReview(9000, "bob")],
    comments: [{ id: 77, body: policyBody({ lgtms: [], approvals: [] }) }],
  }), { reviewDetails: { 9000: { body: "/lgtm" } } });

  assert.deepEqual(result.pullRequests[0].merge.blockers, []);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "success");
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("live body removal or replacement revokes review LGTM at the final read", async (t) => {
  for (const body of ["", "LGTM", "/approve"]) await t.test(body || "removed", async () => {
    const { github, result } = await run(reviewLgtmState(), {
      reviewDetails: (_review, call) => ({ body: call === 1 ? "/lgtm" : body }),
    });

    assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
    assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
    assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "lgtm"), false);
  });
});

test("revoked review LGTM removes its visible label and disarms an existing merge request", async (t) => {
  for (const revoked of [{ body: "" }, { body: "/lgtm", state: "DISMISSED" }]) {
    await t.test(revoked.state ?? "body removed", async () => {
      const { github, result } = await run(reviewLgtmState({
        labels: ["lgtm", "approved", "maintainer/custom"],
        mergeStates: Array.from({ length: 4 }, () => mergeState({ autoMergeMethod: "SQUASH" })),
      }), {
        reviewDetails: (_review, call) => (call === 1 ? { body: "/lgtm" } : revoked),
      });

      assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
      assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
      assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
      assert.ok(github.calls.removePolicyLabel.some(({ label }) => label === "lgtm"));
      assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
      assert.equal(github.calls.removePolicyLabel.some(({ label }) => label === "maintainer/custom"), false);
    });
  }
});

test("review LGTM does not authorize a new PR head", async () => {
  const { github, result } = await run(reviewLgtmState({
    pullRequest: pullRequest({ headOid: NEXT_HEAD }),
    mergeStates: Array.from({ length: 4 }, () => mergeState({ headOid: NEXT_HEAD })),
  }), { reviewDetails: { 9000: { body: "/lgtm" } } });

  assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
  assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
  assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "lgtm"), false);
});

test("review LGTM revoked during label writes cannot publish success", async (t) => {
  for (const change of ["body removed", "dismissed", "new negative review"]) {
    await t.test(change, async () => {
      const state = reviewLgtmState({
        mergeStates: Array.from({ length: 6 }, () => mergeState({ autoMergeMethod: "SQUASH" })),
      });
      let body = "/lgtm";
      let changed = false;
      const { github, result } = await run(state, {
        reviewDetails: () => ({ body }),
        onPolicyLabelAdd: () => {
          if (changed) return;
          changed = true;
          if (change === "body removed") body = "";
          if (change === "dismissed") state.reviews[0].state = "DISMISSED";
          if (change === "new negative review") state.reviews.push(submittedReview(
            9001, "alice", { state: "CHANGES_REQUESTED", submittedAt: "2026-09-17T10:00:00.000Z" },
          ));
        },
      });

      assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
      assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
      assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
      assert.ok(github.calls.removePolicyLabel.some(({ label }) => label === "lgtm"));
      assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
    });
  }
});

test("review LGTM revoked during the success check receives a restrictive final check", async () => {
  let body = "/lgtm";
  const { github, result } = await run(reviewLgtmState({
    mergeStates: Array.from({ length: 6 }, () => mergeState({ autoMergeMethod: "SQUASH" })),
  }), {
    reviewDetails: () => ({ body }),
    onMergePolicyCheck: (conclusion) => {
      if (conclusion === "success") body = "";
    },
  });

  assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
  assert.deepEqual(github.calls.setMergePolicyCheck.map(({ conclusion }) => conclusion), [
    "success", "action_required",
  ]);
  assert.ok(github.calls.removePolicyLabel.some(({ label }) => label === "lgtm"));
  assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
  const restrictiveCheck = github.callOrder.findIndex(({ operation, parameters }) => (
    operation === "setMergePolicyCheck" && parameters.conclusion === "action_required"
  ));
  assert.ok(restrictiveCheck < operationIndex(github, "disableAutoMerge"));
});

test("an unprotected target branch fails closed", async () => {
  const { github, result } = await run(evaluatorState({ branchProtection: { main: false } }));

  assert.ok(result.pullRequests[0].merge.blockers.includes("target-branch-not-protected"));
  assert.equal(github.calls.setMergePolicyCheck[0].conclusion, "action_required");
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("an evaluation load error fails the action and forces restrictive state", async () => {
  const github = createFakeGitHub(evaluatorState({
    mergeStates: [
      mergeState({ autoMergeMethod: "SQUASH" }),
      mergeState({ autoMergeMethod: "SQUASH" }),
      mergeState({ autoMergeMethod: "SQUASH" }),
    ],
    failures: { getPolicyComment: new Error("transient authority failure") },
  }));
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");

  await assert.rejects(
    () => runMergeEvaluate({
      event: WORKFLOW_EVENT,
      eventName: "workflow_dispatch",
      github,
      config,
      dryRun: false,
      prNumber: "42",
    }),
    (error) => {
      assert.equal(error.summary.status, "failed");
      assert.deepEqual(error.summary.candidates, [42]);
      return /evaluation failed closed/.test(error.message);
    },
  );
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
  assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
  assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "do-not-merge/needs-approval"), true);
  assert.equal(github.calls.removePolicyLabel.some(({ label }) => label === "lgtm"), true);
  assert.equal(github.calls.removePolicyLabel.some(({ label }) => label === "approved"), true);
});

test("a head change after label writes cannot publish success", async () => {
  const { github, result } = await run(evaluatorState({
    pullRequests: [pullRequest(), pullRequest(), pullRequest({ headOid: NEXT_HEAD })],
  }));

  assert.ok(result.pullRequests[0].merge.blockers.includes("head-changed"));
  assert.deepEqual(github.calls.setMergePolicyCheck, []);
  assert.deepEqual(github.calls.enableAutoMerge, []);
  assert.deepEqual(github.calls.disableAutoMerge, []);
});

test("a head change after the success check fails closed before policy completion", async () => {
  const { github, result } = await run(evaluatorState({
    pullRequests: [pullRequest(), pullRequest(), pullRequest(), pullRequest({ headOid: NEXT_HEAD })],
  }));

  assert.equal(result.pullRequests[0].merge.action, "NOOP");
  assert.ok(result.pullRequests[0].merge.blockers.includes("head-changed"));
  assert.deepEqual(github.calls.setMergePolicyCheck, [{
    prNumber: 42,
    headOid: HEAD,
    conclusion: "success",
    summary: "Repository merge policy passed.",
  }]);
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("dry-run calculates policy but performs no GitHub writes", async () => {
  const { github, result } = await run(evaluatorState(), { dryRun: true });

  assert.equal(result.status, "planned");
  for (const operation of [
    "setMergePolicyCheck",
    "enableAutoMerge",
    "disableAutoMerge",
    "addPolicyLabel",
    "removePolicyLabel",
  ]) assert.deepEqual(github.calls[operation], []);
});

test("workflow completion evaluates only a refetched trusted workflow identity", async () => {
  const state = evaluatorState({
    evaluationWorkflowRuns: [{
      id: 901,
      name: "PR metadata",
      workflowPath: ".github/workflows/pr-metadata.yml",
      workflowSourceRef: "refs/heads/main",
      event: "pull_request_target",
      status: "completed",
      repository: REPOSITORY,
      pullRequestNumbers: [42],
    }],
  });
  const event = {
    ...WORKFLOW_EVENT,
    action: "completed",
    workflow_run: { id: 901, status: "completed" },
  };
  const { github, result } = await run(state, {
    dryRun: true,
    event,
    eventName: "workflow_run",
    prNumber: "",
  });

  assert.deepEqual(github.calls.getEvaluationWorkflowRun, [{ runId: 901 }]);
  assert.deepEqual(result.candidates, [42]);
});

test("workflow completion ignores a refetched workflow with a spoofed path", async () => {
  const state = evaluatorState({
    evaluationWorkflowRuns: [{
      id: 902,
      name: "PR metadata",
      workflowPath: ".github/workflows/spoof.yml",
      workflowSourceRef: "refs/heads/main",
      event: "pull_request_target",
      status: "completed",
      repository: REPOSITORY,
      pullRequestNumbers: [42],
    }],
  });
  const event = {
    ...WORKFLOW_EVENT,
    action: "completed",
    workflow_run: { id: 902, status: "completed" },
  };
  const { github, result } = await run(state, {
    dryRun: true,
    event,
    eventName: "workflow_run",
    prNumber: "",
  });

  assert.deepEqual(github.calls.getEvaluationWorkflowRun, [{ runId: 902 }]);
  assert.deepEqual(result, { status: "planned", candidates: [], pullRequests: [] });
});

test("trusted command completion scans every bounded open pull request", async () => {
  const state = evaluatorState({
    evaluationWorkflowRuns: [{
      id: 903,
      name: "Commands",
      workflowPath: ".github/workflows/commands.yml",
      workflowSourceRef: "refs/heads/main",
      event: "issue_comment",
      status: "completed",
      repository: REPOSITORY,
      pullRequestNumbers: [],
    }],
    openPullRequestNumbers: [42],
  });
  const event = {
    ...WORKFLOW_EVENT,
    action: "completed",
    workflow_run: { id: 903, status: "completed" },
  };
  const { github, result } = await run(state, {
    dryRun: true,
    event,
    eventName: "workflow_run",
    prNumber: "",
  });

  assert.deepEqual(github.calls.listOpenPullRequestNumbers, [{}]);
  assert.deepEqual(result.candidates, [42]);
});

function metadataScanRun(overrides = {}) {
  return {
    id: 904,
    name: "PR metadata",
    workflowPath: ".github/workflows/pr-metadata.yml",
    workflowSourceRef: "refs/heads/main",
    event: "workflow_dispatch",
    status: "completed",
    repository: REPOSITORY,
    pullRequestNumbers: [],
    ...overrides,
  };
}

const METADATA_SCAN_EVENT = {
  ...WORKFLOW_EVENT,
  action: "completed",
  workflow_run: { id: 904, status: "completed" },
};

test("dispatched metadata completion evaluates the bounded open PR scan", async () => {
  const { github, result } = await run(evaluatorState({
    evaluationWorkflowRuns: [metadataScanRun()],
    openPullRequestNumbers: [42],
  }), {
    event: METADATA_SCAN_EVENT,
    eventName: "workflow_run",
    prNumber: "",
  });

  assert.deepEqual(github.calls.getEvaluationWorkflowRun, [{ runId: 904 }]);
  assert.deepEqual(github.calls.listOpenPullRequestNumbers, [{}]);
  assert.deepEqual(result.candidates, [42]);
  assert.deepEqual(result.pullRequests[0].merge.blockers, []);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "success");
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("dispatched metadata completion rejects untrusted live workflow identity", async (t) => {
  for (const [name, overrides] of [
    ["unknown workflow", { name: "CI Pipeline" }],
    ["different trusted workflow", { name: "Commands", workflowPath: ".github/workflows/commands.yml" }],
    ["unsupported event", { event: "push" }],
    ["wrong path", { workflowPath: ".github/workflows/spoof.yml" }],
    ["wrong repository", { repository: "nvidia/other" }],
    ["incomplete run", { status: "in_progress" }],
  ]) await t.test(name, async () => {
    const { github, result } = await run(evaluatorState({
      evaluationWorkflowRuns: [metadataScanRun(overrides)],
      openPullRequestNumbers: [42],
    }), {
      event: METADATA_SCAN_EVENT,
      eventName: "workflow_run",
      prNumber: "",
    });

    assert.deepEqual(result, { status: "complete", candidates: [], pullRequests: [] });
    assert.deepEqual(github.calls.listOpenPullRequestNumbers, []);
    assert.deepEqual(github.calls.getPullRequest, []);
    assert.deepEqual(github.calls.setMergePolicyCheck, []);
  });
});

test("dispatched metadata completion rejects a mismatched refetched run ID", async () => {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  const github = createFakeGitHub(evaluatorState({
    evaluationWorkflowRuns: [metadataScanRun()],
    openPullRequestNumbers: [42],
  }));
  const getRun = github.getEvaluationWorkflowRun.bind(github);
  github.getEvaluationWorkflowRun = async (runId) => ({ ...await getRun(runId), id: 905 });

  const result = await runMergeEvaluate({
    event: METADATA_SCAN_EVENT,
    eventName: "workflow_run",
    github,
    config,
    dryRun: false,
    prNumber: "",
  });

  assert.deepEqual(github.calls.getEvaluationWorkflowRun, [{ runId: 904 }]);
  assert.deepEqual(result, { status: "complete", candidates: [], pullRequests: [] });
  assert.deepEqual(github.calls.listOpenPullRequestNumbers, []);
  assert.deepEqual(github.calls.setMergePolicyCheck, []);
});

test("dispatched metadata completion enforces open scan limits", async (t) => {
  for (const [name, numbers, error] of [
    ["more than 100 candidates", Array.from({ length: 101 }, (_, index) => index + 1), /scan exceeds limit/],
    ["duplicate candidates", [42, 42], /candidate mapping is invalid/],
    ["non-positive candidate", [42, 0], /candidate mapping is invalid/],
  ]) await t.test(name, async () => {
    await assert.rejects(() => run(evaluatorState({
      evaluationWorkflowRuns: [metadataScanRun()],
      openPullRequestNumbers: numbers,
    }), {
      event: METADATA_SCAN_EVENT,
      eventName: "workflow_run",
      prNumber: "",
    }), error);
  });
});

test("merge policy contains no direct merge endpoint", () => {
  const source = fs.readFileSync(
    path.join(__dirname, "..", "src", "modes", "merge-evaluate.js"),
    "utf8",
  );
  const client = fs.readFileSync(
    path.join(__dirname, "..", "src", "github-client.js"),
    "utf8",
  );

  assert.doesNotMatch(source, /\.merge\s*\(|mergePullRequest|pulls\.merge/);
  assert.doesNotMatch(client, /pulls\.merge/);
});
