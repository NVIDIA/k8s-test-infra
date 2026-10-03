"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
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
  `<!-- repo-automation-metadata-head:v2 {"headOid":"${head}","valid":true} -->`
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

function signedCommit(sha = HEAD) {
  return { sha, author: { login: "contributor" }, parents: [{ sha: REVISION }],
    commit: { author: { name: "Contributor", email: "contributor@example.com" },
      message: "feat: test\n\nSigned-off-by: Contributor <contributor@example.com>" } };
}

function evaluatorState(overrides = {}) {
  return {
    pullRequest: pullRequest(),
    files: [{ path: "pkg/gpu.go", additions: 2, deletions: 1, status: "modified" }],
    commits: [signedCommit(overrides.pullRequest?.headOid ?? HEAD)],
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
  if (options.authorIdentity !== undefined) {
    const getUser = github.getUserIdentity.bind(github);
    let reads = 0;
    github.getUserIdentity = async (login) => {
      const identity = await getUser(login);
      return login === "bob" ? options.authorIdentity(login, ++reads) : identity;
    };
  }
  if (options.ownerSource !== undefined) {
    const getContent = github.getContentAtRevision.bind(github);
    let reads = 0;
    github.getContentAtRevision = async (path, revision) => {
      const source = await getContent(path, revision);
      return path === "/OWNERS" ? options.ownerSource(path, ++reads) : source;
    };
  }
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
  if (options.commitSnapshot !== undefined) {
    const listCommits = github.listPullRequestCommits.bind(github);
    github.listPullRequestCommits = async (number) => {
      const commits = await listCommits(number);
      return options.commitSnapshot(commits, github.calls.getPullRequest.length);
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
    config: options.config ?? config,
    policyRevision: REVISION,
    dryRun: options.dryRun ?? false,
    prNumber: Object.hasOwn(options, "prNumber") ? options.prNumber : "42",
  });
  return { github, result };
}

function operationIndex(github, operation) {
  return github.callOrder.findIndex((entry) => entry.operation === operation);
}

test("an invalid live title cannot reuse valid same-head metadata evidence", async () => {
  const { github, result } = await run(evaluatorState({ pullRequest: pullRequest({ title: "invalid" }) }));
  assert.ok(result.pullRequests[0].merge.blockers.includes("metadata-stale"));
  assert.deepEqual(github.calls.enableAutoMerge, []);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
});

test("current policy removal of a bot DCO exemption blocks same-head metadata", async () => {
  const currentConfig = { ...config, policy: { ...config.policy,
    bots: config.policy.bots.filter(({ login }) => login !== "dependabot[bot]") } };
  const unsignedBotCommit = { sha: HEAD, author: { login: "dependabot[bot]" },
    commit: { author: { name: "Dependabot", email: "49699333+dependabot[bot]@users.noreply.github.com" },
      message: "chore(deps): bump package" } };
  const { github, result } = await run(evaluatorState({ commits: [unsignedBotCommit],
    comments: [{ id: 77, body: `${POLICY_MARKER}\n${METADATA()}\n` }],
    issueComments: [], reviews: [submittedReview(9000, "bob")] }),
  { config: currentConfig, reviewDetails: { 9000: { body: "/lgtm" } } });
  assert.ok(result.pullRequests[0].merge.blockers.includes("metadata-stale"));
  assert.deepEqual(github.calls.enableAutoMerge, []);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
});

for (const [name, overrides] of [
  ["head-ending commit collection drift", { commits: [signedCommit(NEXT_HEAD)] }],
  ["current commit collection read failure", { failures: { listPullRequestCommits: new Error("unavailable") } }],
]) test(`${name} fails closed before native merge enable`, async () => {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  const github = createFakeGitHub(evaluatorState(overrides));
  await assert.rejects(() => runMergeEvaluate({ event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github,
    config, policyRevision: REVISION, dryRun: false, prNumber: "42" }), /failed closed/);
  assert.deepEqual(github.calls.enableAutoMerge, []);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
});

for (const changedRead of [1, 2]) {
  test(`default policy revision drift on read ${changedRead} fails closed before native merge enable`, async () => {
    const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
    const github = createFakeGitHub(evaluatorState());
    const getRevision = github.getDefaultBranchRevision.bind(github);
    let reads = 0;
    github.getDefaultBranchRevision = async () => {
      const value = await getRevision();
      return ++reads < changedRead ? value : NEXT_HEAD;
    };
    await assert.rejects(() => runMergeEvaluate({ event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github,
      config, policyRevision: REVISION, dryRun: false, prNumber: "42" }), /failed closed/);
    assert.deepEqual(github.calls.enableAutoMerge, []);
    assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
  });
}

test("policy revision drift during CI after native enable fails closed and disarms", async () => {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  const github = createFakeGitHub(evaluatorState());
  const getRevision = github.getDefaultBranchRevision.bind(github);
  const getCIState = github.getCIState.bind(github);
  let revisionChanged = false;
  github.getDefaultBranchRevision = async () => {
    const revision = await getRevision();
    return revisionChanged ? NEXT_HEAD : revision;
  };
  github.getCIState = async (parameters) => {
    const ci = await getCIState(parameters);
    if (github.calls.enableAutoMerge.length > 0) revisionChanged = true;
    return ci;
  };
  await assert.rejects(() => runMergeEvaluate({ event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github,
    config, policyRevision: REVISION, dryRun: false, prNumber: "42" }), /failed closed/);
  assert.deepEqual(github.calls.enableAutoMerge, [{
    nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
  }]);
  assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
  assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
});

test("a commit read failure after native enable fails closed and disarms", async () => {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  const github = createFakeGitHub(evaluatorState({ failures: {
    listPullRequestCommits: [null, null, null, new Error("post-enable commits unavailable")],
  } }));
  await assert.rejects(() => runMergeEvaluate({ event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github,
    config, policyRevision: REVISION, dryRun: false, prNumber: "42" }), /failed closed/);
  assert.deepEqual(github.calls.enableAutoMerge, [{
    nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
  }]);
  assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
  assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
});

for (const [name, commits] of [
  ["empty", []],
  ["non-array", {}],
  ["over-limit", Array.from({ length: 1001 }, () => signedCommit())],
  ["malformed", [{ ...signedCommit(), commit: { ...signedCommit().commit, message: 42 } }]],
]) test(`${name} current commit evidence fails closed without a SUCCESS check`, async () => {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  const github = createFakeGitHub(evaluatorState());
  const listCommits = github.listPullRequestCommits.bind(github);
  github.listPullRequestCommits = async (number) => {
    await listCommits(number);
    return commits;
  };
  await assert.rejects(() => runMergeEvaluate({ event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github,
    config, policyRevision: REVISION, dryRun: false, prNumber: "42" }), /failed closed/);
  assert.deepEqual(github.calls.enableAutoMerge, []);
  assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
});

async function dispatchMetadataScan(github) {
  const { run: runAction } = require("../src/index.js");
  const workspace = fs.mkdtempSync(path.join(os.tmpdir(), "mokka-scan-merge-"));
  fs.mkdirSync(path.join(workspace, "control"));
  fs.cpSync(path.join(repositoryRoot, ".github/repo-automation"),
    path.join(workspace, "control/.github/repo-automation"), { recursive: true });
  const requestId = "12345678-1234-4234-8234-123456789abc";
  const inputs = { mode: "metadata-labels", "control-directory": "control", "policy-revision": REVISION,
    "request-id": requestId, "workflow-commit-sha": REVISION };
  await runAction({ githubClient: github, workspace, owner: "NVIDIA", repo: "k8s-test-infra",
    eventName: "workflow_dispatch", ref: "refs/heads/main", repositoryId: "733665780", workflowSha: REVISION,
    event: { repository: { ...WORKFLOW_EVENT.repository, id: 733665780, node_id: "R_kgDOK7rZ9A" },
      inputs: { request_id: requestId, workflow_commit_sha: REVISION } },
    core: { getInput: (name) => inputs[name] ?? "", getBooleanInput: () => false, setOutput() {}, info() {} } });
  return JSON.parse(fs.readFileSync(path.join(workspace, ".mokka-label-scan", requestId, "metadata-labels.json"), "utf8"));
}

function scanMergeState(overrides = {}) {
  return evaluatorState({ comments: [], issueComments: [],
    files: [{ path: "docs/guide.md", additions: 1, deletions: 0 }],
    labels: ["area/docs", "kind/feature", "size/S"], branches: { main: REVISION },
    commits: [signedCommit()],
    reviews: [submittedReview(9000, "bob")], ...overrides });
}

function scanMergeGitHub(overrides = {}) {
  const github = createFakeGitHub(scanMergeState(overrides));
  const getReview = github.getPullRequestReview.bind(github);
  github.getPullRequestReview = async (number, id) => ({ ...await getReview(number, id), body: "/lgtm" });
  return github;
}

test("trusted dispatch creates real metadata evidence accepted by native merge evaluation", async () => {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  const github = scanMergeGitHub();
  const report = await dispatchMetadataScan(github);
  assert.equal(report.results[0].status, "applied", "the unchanged-label comment refresh is an applied scan");
  assert.equal(report.results[0].reason, "none");
  assert.equal(JSON.stringify(report).includes("Repository policy"), false);
  assert.equal(github.calls.upsertPolicyComment.length, 1);
  assert.deepEqual(github.calls.addIssueLabel, []);
  assert.deepEqual(github.calls.requestReviewers, []);
  const result = await runMergeEvaluate({ event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github,
    config, policyRevision: REVISION, dryRun: false, prNumber: "42" });
  assert.deepEqual(result.pullRequests[0].merge.blockers, []);
  assert.equal(github.calls.enableAutoMerge.length, 1);
  assert.equal(github.calls.setMergePolicyCheck[0].conclusion, "action_required");
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "success");
});

for (const [name, overrides, blocker] of [
  ["missing authority", { reviews: [] }, "lgtm-missing"],
  ["negative review", { reviews: [submittedReview(9000, "bob", { state: "CHANGES_REQUESTED" })] }, "approval-coverage-incomplete"],
  ["pending CI", { ciStates: ["PENDING"] }, "ci-pending"],
  ["failed CI", { ciStates: ["FAILED"] }, "ci-failed"],
]) test(`real scan evidence cannot override ${name}`, async () => {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  const github = scanMergeGitHub(overrides);
  await dispatchMetadataScan(github);
  const result = await runMergeEvaluate({ event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github,
    config, policyRevision: REVISION, dryRun: false, prNumber: "42" });
  assert.ok(result.pullRequests[0].merge.blockers.includes(blocker));
  assert.deepEqual(github.calls.enableAutoMerge, []);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
});

test("a real failed renderer cannot certify same-head metadata to the merge evaluator", async () => {
  const { renderPolicyComment } = require("../src/policy-comment.js");
  const body = renderPolicyComment({ headOid: HEAD, valid: false, configuration: { valid: true },
    title: { valid: true, error: null }, dco: { valid: false, failures: [{ sha: HEAD }], exempted: [] },
    ownership: { valid: true, uncoveredPaths: [] }, labels: { add: [], remove: [] },
    reviewers: { request: [], preserved: [] } }, policyBody());
  const { github, result } = await run(evaluatorState({ comments: [{ id: 77, body }] }));
  assert.ok(result.pullRequests[0].merge.blockers.includes("metadata-stale"));
  assert.deepEqual(github.calls.enableAutoMerge, []);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
});

function approverAuthorState(overrides = {}) {
  return evaluatorState({
    pullRequest: pullRequest({ author: "BoB" }),
    labels: ["lgtm", "do-not-merge/needs-approval"],
    comments: [{ id: 77, author: "github-actions[bot]", body: policyBody({ approvals: [] }) }],
    ...overrides,
  });
}

test("a trusted human approver author needs independent LGTM but no additional OWNERS approval", async () => {
  const { github, result } = await run(approverAuthorState());
  assert.deepEqual(result.pullRequests[0].merge.blockers, []);
  assert.ok(github.calls.addPolicyLabel.some(({ label }) => label === "approved"));
  assert.ok(github.calls.removePolicyLabel.some(({ label }) => label === "do-not-merge/needs-approval"));
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "success");
  assert.ok(github.calls.getUserIdentity.some(({ login }) => login === "bob"));
  assert.equal(github.calls.enableAutoMerge.length, 1);
});

test("implicit author approval never grants LGTM or clears a hold", async () => {
  for (const held of [false, true]) {
    const { github, result } = await run(approverAuthorState({
      comments: [{ id: 77, author: "github-actions[bot]", body: policyBody({
        lgtms: [], approvals: [],
        hold: held ? {
          repository: REPOSITORY, pullRequest: 42, actor: "alice", actorRole: "owner",
          sourceType: "comment", sourceId: 8000, createdAt: "2026-09-17T08:00:00.000Z",
        } : null,
      }) }],
    }));
    assert.ok(github.calls.addPolicyLabel.some(({ label }) => label === "approved"));
    assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
    if (held) assert.ok(result.pullRequests[0].merge.blockers.includes("hold-active"));
    assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
  }
});

test("unresolved, deleted, bot, or mismatched author identities cannot grant approval", async (t) => {
  for (const identity of [
    { resolved: false }, { deleted: true }, { type: "Bot" }, { login: "mallory" },
  ]) await t.test(JSON.stringify(identity), async () => {
    const { github, result } = await run(approverAuthorState({ users: {
      bob: { login: "bob", type: "User", resolved: true, deleted: false, ...identity },
    } }));
    assert.ok(result.pullRequests[0].merge.blockers.includes("approval-coverage-incomplete"));
    assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "approved"), false);
  });
});

test("an unavailable author identity cannot grant approval", async () => {
  const { github, result } = await run(approverAuthorState(), {
    authorIdentity: () => { throw new Error("identity unavailable"); },
  });
  assert.ok(result.pullRequests[0].merge.blockers.includes("approval-coverage-incomplete"));
  assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "approved"), false);
});

test("author approval is revoked if identity changes during the final authority read", async () => {
  const { github, result } = await run(approverAuthorState(), {
    authorIdentity: (_login, call) => ({ login: "bob", type: "User", resolved: true, deleted: call > 1 }),
  });
  assert.ok(result.pullRequests[0].merge.blockers.includes("approval-coverage-incomplete"));
  assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
});

test("OWNERS approval comes from the trusted base and is revoked when it removes the author", async () => {
  const { github, result } = await run(approverAuthorState(), {
    ownerSource: (_path, call) => call > 1 ? "reviewers: [alice]\napprovers: [carol]\n" : OWNER_SOURCE,
  });
  assert.ok(result.pullRequests[0].merge.blockers.includes("approval-coverage-incomplete"));
  assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
  assert.ok(github.calls.getContentAtRevision.every(({ revision }) => revision === REVISION));
});

test("a new head keeps applicable author approval but invalidates the old LGTM", async () => {
  const { github, result } = await run(approverAuthorState({
    pullRequest: pullRequest({ author: "bob", headOid: NEXT_HEAD }),
    comments: [{ id: 77, author: "github-actions[bot]", body: policyBody({ approvals: [], metadataHeadOid: NEXT_HEAD }) }],
    mergeStates: [mergeState({ headOid: NEXT_HEAD })],
  }));
  assert.ok(github.calls.addPolicyLabel.some(({ label }) => label === "approved"));
  assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
  assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
});

test("implicit author approval cannot restore dismissed or negative review LGTM", async (t) => {
  for (const state of ["CHANGES_REQUESTED", "DISMISSED"]) await t.test(state, async () => {
    const { github, result } = await run(approverAuthorState({
      comments: [{ id: 77, author: "github-actions[bot]", body: policyBody({ lgtms: [], approvals: [] }) }],
      reviews: [
        submittedReview(9001, "alice", { state, submittedAt: "2026-09-17T10:00:00.000Z" }),
        submittedReview(),
      ],
    }), { reviewDetails: { 9000: { body: "/lgtm" }, 9001: { body: "/lgtm" } } });
    assert.ok(github.calls.addPolicyLabel.some(({ label }) => label === "approved"));
    assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
    assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
    assert.equal(github.calls.addPolicyLabel.some(({ label }) => label === "lgtm"), false);
  });
});

test("arms native SQUASH while policy blocks, then publishes success for the exact head", async () => {
  const { github, result } = await run();

  assert.equal(result.status, "complete");
  assert.deepEqual(github.calls.setMergePolicyCheck, [{
    prNumber: 42,
    headOid: HEAD,
    conclusion: "action_required",
    summary: "Repository merge policy is preparing native SQUASH auto-merge.",
  }, {
    prNumber: 42,
    headOid: HEAD,
    conclusion: "success",
    summary: "Repository merge policy passed.",
  }]);
  assert.deepEqual(github.calls.enableAutoMerge, [{
    nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
  }]);
  assert.equal(result.pullRequests[0].merge.action, "ENABLE");
  assert.deepEqual(github.calls.disableAutoMerge, []);
  assert.equal(github.calls.getPolicyComment.length, 5);
  assert.equal(github.calls.getPullRequest.length, 5);
  assert.ok(operationIndex(github, "enableAutoMerge") > operationIndex(github, "setMergePolicyCheck"));
  const successIndex = github.callOrder.findIndex(({ operation, parameters }) => (
    operation === "setMergePolicyCheck" && parameters.conclusion === "success"
  ));
  assert.ok(operationIndex(github, "enableAutoMerge") < successIndex);
  assert.equal(github.callOrder.slice(operationIndex(github, "enableAutoMerge"), successIndex)
    .filter(({ operation }) => operation === "getCIState").length, 1);
  assert.deepEqual(github.calls.getCIState.at(-1), {
    prNumber: 42, headOid: HEAD, baseBranch: "main",
    files: [{ path: "pkg/gpu.go", additions: 2, deletions: 1, status: "modified" }],
    requiredCI: config.policy.merge.requiredCI,
  });
});

test("pending and failed source CI block policy success and native arming", async (t) => {
  for (const [ciState, blocker] of [["PENDING", "ci-pending"], ["FAILED", "ci-failed"]]) {
    await t.test(ciState, async () => {
      const { github, result } = await run(evaluatorState({ ciStates: [ciState] }));
      assert.ok(result.pullRequests[0].merge.blockers.includes(blocker));
      assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
      assert.deepEqual(github.calls.enableAutoMerge, []);
    });
  }
});

test("each CI read receives the live source repository and branch", async () => {
  const source = { headRepository: { owner: "ArangoGutierrez", repo: "k8s-test-infra" }, headBranch: "codex/fork" };
  const { github } = await run(evaluatorState({ pullRequests: [pullRequest(source)] }));
  assert.equal(github.calls.getCIState.length, 5);
  for (const parameters of github.calls.getCIState) {
    assert.equal(parameters.headRepository, "arangogutierrez/k8s-test-infra");
    assert.equal(parameters.headBranch, "codex/fork");
  }
});

test("a source repository or branch change prevents policy success", async (t) => {
  const source = { headRepository: { owner: "fork-owner", repo: "k8s-test-infra" }, headBranch: "codex/fork" };
  for (const changed of [
    { ...source, headRepository: { owner: "other-owner", repo: "k8s-test-infra" } },
    { ...source, headBranch: "codex/other" },
    { ...source, headRepository: null },
  ]) await t.test(JSON.stringify(changed), async () => {
    const { github, result } = await run(evaluatorState({
      pullRequests: [pullRequest(source), pullRequest(changed)],
    }));
    assert.ok(result.pullRequests[0].merge.blockers.includes("head-changed"));
    assert.deepEqual(github.calls.enableAutoMerge, []);
    assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
  });
});

test("malformed live source identities fail closed before CI reads", async (t) => {
  for (const source of [
    { headRepository: { owner: "bad/owner", repo: "k8s-test-infra" } },
    { headRepository: { owner: "fork-owner", repo: "." } },
    { headBranch: "/invalid" },
    { headBranch: {} },
  ]) await t.test(JSON.stringify(source), async () => {
    const github = createFakeGitHub(evaluatorState({ pullRequests: [pullRequest(source)] }));
    const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
    await assert.rejects(() => runMergeEvaluate({
      event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github, config, policyRevision: REVISION,
      dryRun: false, prNumber: "42",
    }), /evaluation failed closed/);
    assert.deepEqual(github.calls.getCIState, []);
    assert.deepEqual(github.calls.enableAutoMerge, []);
    assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
  });
});

test("CI revocation before native arming prevents enable and policy success", async (t) => {
  for (const revokedRead of [2, 3]) await t.test(`read ${revokedRead}`, async () => {
    const states = Array.from({ length: 3 }, (_, index) => index + 1 < revokedRead ? "SUCCESS" : "FAILED");
    const { github, result } = await run(evaluatorState({ ciStates: states }));
    assert.ok(result.pullRequests[0].merge.blockers.includes("ci-failed"));
    assert.deepEqual(github.calls.enableAutoMerge, []);
    assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
    assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
  });
});

test("CI revocation after native enable disarms the request", async (t) => {
  for (const [name, states, conclusions] of [
    ["before success", ["SUCCESS", "SUCCESS", "SUCCESS", "FAILED"], ["action_required", "action_required"]],
    ["after success", ["SUCCESS", "SUCCESS", "SUCCESS", "SUCCESS", "FAILED"], ["action_required", "success", "action_required"]],
  ]) await t.test(name, async () => {
    const { github, result } = await run(evaluatorState({ ciStates: states }));
    assert.ok(result.pullRequests[0].merge.blockers.includes("ci-failed"));
    assert.deepEqual(github.calls.enableAutoMerge, [{
      nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
    }]);
    assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
    assert.deepEqual(github.calls.setMergePolicyCheck.map(({ conclusion }) => conclusion), conclusions);
  });
});

test("authority revoked after native enable cannot publish success", async () => {
  const { github, result } = await run(reviewLgtmState(), {
    reviewDetails: (_review, call) => ({ body: call < 4 ? "/lgtm" : "" }),
  });
  assert.ok(result.pullRequests[0].merge.blockers.includes("lgtm-missing"));
  assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
  assert.deepEqual(github.calls.enableAutoMerge, [{
    nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
  }]);
  assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
});

test("malformed or unavailable CI after native enable fails closed and disarms", async (t) => {
  for (const name of ["malformed", "unavailable"]) await t.test(name, async () => {
    const github = createFakeGitHub(evaluatorState({
      ciStates: ["SUCCESS", "SUCCESS", "SUCCESS", name === "malformed" ? "UNKNOWN" : "SUCCESS"],
      failures: name === "unavailable" ? { getCIState: [null, null, null, new Error("CI read failed")] } : {},
    }));
    const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
    await assert.rejects(() => runMergeEvaluate({
      event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github, config, policyRevision: REVISION,
      dryRun: false, prNumber: "42",
    }), /evaluation failed closed/);
    assert.deepEqual(github.calls.enableAutoMerge, [{
      nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
    }]);
    assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
    assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
    assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
  });
});

test("a second evaluation preserves the SQUASH request armed by the first", async () => {
  const { github } = await run();
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  const result = await runMergeEvaluate({
    event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github, config, policyRevision: REVISION,
    dryRun: false, prNumber: "42",
  });
  assert.equal(result.pullRequests[0].merge.action, "NOOP");
  assert.deepEqual(github.calls.enableAutoMerge, [{
    nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
  }]);
  assert.deepEqual(github.calls.disableAutoMerge, []);
  assert.deepEqual(github.calls.setMergePolicyCheck.map(({ conclusion }) => conclusion), [
    "action_required", "success", "action_required", "success",
  ]);
});

test("CI revocation disarms an existing SQUASH request", async () => {
  const { github, result } = await run(evaluatorState({
    ciStates: ["PENDING"],
    mergeStates: [mergeState({ autoMergeMethod: "SQUASH" })],
  }));
  assert.ok(result.pullRequests[0].merge.blockers.includes("ci-pending"));
  assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("malformed or unavailable CI fails closed without arming", async (t) => {
  for (const name of ["malformed", "unavailable"]) await t.test(name, async () => {
    const github = createFakeGitHub(evaluatorState({
      ciStates: ["UNKNOWN"],
      failures: name === "unavailable" ? { getCIState: new Error("CI read failed") } : {},
    }));
    const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
    await assert.rejects(() => runMergeEvaluate({
      event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github, config, policyRevision: REVISION,
      dryRun: false, prNumber: "42",
    }), /evaluation failed closed/);
    assert.deepEqual(github.calls.enableAutoMerge, []);
    assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
  });
});

test("native enable rejection is not retried and cannot publish policy success", async () => {
  const github = createFakeGitHub(evaluatorState({
    failures: { enableAutoMerge: new Error("head changed at GitHub") },
  }));
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  await assert.rejects(() => runMergeEvaluate({
    event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github, config, policyRevision: REVISION,
    dryRun: false, prNumber: "42",
  }), /evaluation failed closed/);
  assert.equal(github.calls.enableAutoMerge.length, 1);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
  assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
});

test("an acknowledged enable without a retained SQUASH request cannot publish policy success", async () => {
  const github = createFakeGitHub(evaluatorState());
  const getMergeState = github.getMergeState.bind(github);
  github.getMergeState = async (number) => ({
    ...await getMergeState(number), autoMergeMethod: null,
  });
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  await assert.rejects(() => runMergeEvaluate({
    event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github, config, policyRevision: REVISION,
    dryRun: false, prNumber: "42",
  }), /evaluation failed closed/);
  assert.equal(github.calls.enableAutoMerge.length, 1);
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
  assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
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
    conclusion: "action_required",
    summary: "Repository merge policy is preparing native SQUASH auto-merge.",
  }, {
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
        mergeState({ autoMergeMethod: "SQUASH" }),
        mergeState({ autoMergeMethod: "SQUASH" }),
        mergeState({ autoMergeMethod: "SQUASH" }),
        mergeState({ autoMergeMethod: method }),
        mergeState({ autoMergeMethod: method }),
      ],
    }));

    assert.equal(result.pullRequests[0].merge.action, "DISABLE");
    assert.deepEqual(github.calls.setMergePolicyCheck, [
      {
        prNumber: 42,
        headOid: HEAD,
        conclusion: "action_required",
        summary: "Repository merge policy is preparing native SQUASH auto-merge.",
      },
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
      assert.deepEqual(github.calls.enableAutoMerge, [{
        nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
      }]);
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
  assert.deepEqual(github.calls.enableAutoMerge, [{
    nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
  }]);
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
  assert.deepEqual(github.calls.enableAutoMerge, [{
    nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
  }]);
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
    "action_required", "success", "action_required",
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
      policyRevision: REVISION,
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
  }), { commitSnapshot: (_commits, read) => [signedCommit(read < 3 ? HEAD : NEXT_HEAD)] });

  assert.ok(result.pullRequests[0].merge.blockers.includes("head-changed"));
  assert.deepEqual(github.calls.setMergePolicyCheck.map(({ conclusion }) => conclusion), ["action_required"]);
  assert.deepEqual(github.calls.enableAutoMerge, []);
  assert.deepEqual(github.calls.disableAutoMerge, []);
});

test("a head change after the success check fails closed before policy completion", async () => {
  const { github, result } = await run(evaluatorState({
    pullRequests: [pullRequest(), pullRequest(), pullRequest(), pullRequest({ headOid: NEXT_HEAD })],
    mergeStates: [mergeState({ autoMergeMethod: "SQUASH" })],
  }), { commitSnapshot: (_commits, read) => [signedCommit(read < 4 ? HEAD : NEXT_HEAD)] });

  assert.equal(result.pullRequests[0].merge.action, "NOOP");
  assert.ok(result.pullRequests[0].merge.blockers.includes("head-changed"));
  assert.deepEqual(github.calls.setMergePolicyCheck, [{
    prNumber: 42,
    headOid: HEAD,
    conclusion: "action_required",
    summary: "Repository merge policy is preparing native SQUASH auto-merge.",
  }, {
    prNumber: 42,
    headOid: HEAD,
    conclusion: "success",
    summary: "Repository merge policy passed.",
  }]);
  assert.deepEqual(github.calls.enableAutoMerge, []);
});

test("native completion after policy success is reported without a head-change failure", async () => {
  const { github, result } = await run(evaluatorState({
    pullRequests: [pullRequest(), pullRequest(), pullRequest(), pullRequest(),
      pullRequest({ state: "closed", merged: true })],
    mergeStates: [mergeState(), mergeState(), mergeState(), mergeState(),
      mergeState({ state: "MERGED", autoMergeMethod: null })],
  }));
  assert.equal(result.status, "complete");
  assert.deepEqual(result.pullRequests[0].merge, { action: "ENABLE", blockers: [] });
  assert.deepEqual(github.calls.setMergePolicyCheck.map(({ conclusion }) => conclusion), [
    "action_required", "success",
  ]);
  assert.deepEqual(github.calls.disableAutoMerge, []);
});

function completedMergeGitHub({ armed = false, terminalOverrides = {} } = {}) {
  const source = { headRepository: { owner: "fork-owner", repo: "k8s-test-infra" }, headBranch: "codex/fork" };
  const priorReads = armed ? 3 : 4;
  const completedPullRequest = pullRequest({ ...source, state: "closed", merged: true, ...terminalOverrides });
  const github = createFakeGitHub(evaluatorState({
    pullRequests: [...Array.from({ length: priorReads }, () => pullRequest(source)), completedPullRequest],
    mergeStates: [...Array.from({ length: priorReads }, () => mergeState({ autoMergeMethod: armed ? "SQUASH" : null })),
      mergeState({ state: "MERGED", autoMergeMethod: null, headOid: completedPullRequest.headOid,
        baseBranch: completedPullRequest.baseBranch, nodeId: completedPullRequest.nodeId })],
    branchProtection: { main: true, "release-test": true },
  }));
  const getRevision = github.getDefaultBranchRevision.bind(github);
  const setCheck = github.setMergePolicyCheck.bind(github);
  let completed = false;
  github.getDefaultBranchRevision = async () => {
    const revision = await getRevision();
    return completed ? NEXT_HEAD : revision;
  };
  github.setMergePolicyCheck = async (...parameters) => {
    await setCheck(...parameters);
    if (parameters[2] === "success") completed = true;
  };
  return { github, priorReads };
}

test("completed exact-head native merge accepts the new default revision without another authority read", async (t) => {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  for (const armed of [false, true]) await t.test(armed ? "already armed" : "newly armed", async () => {
    const { github, priorReads } = completedMergeGitHub({ armed });
    const result = await runMergeEvaluate({ event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github,
      config, policyRevision: REVISION, dryRun: false, prNumber: "42" });
    assert.equal(result.status, "complete");
    assert.deepEqual(result.pullRequests[0].merge, { action: armed ? "NOOP" : "ENABLE", blockers: [] });
    assert.deepEqual(github.calls.setMergePolicyCheck.map(({ conclusion }) => conclusion), [
      "action_required", "success",
    ]);
    assert.deepEqual(github.calls.disableAutoMerge, []);
    assert.equal(github.calls.listPullRequestCommits.length, priorReads);
  });
});

test("completed native merge with a changed identity cannot bypass the policy revision fence", async (t) => {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  for (const terminalOverrides of [
    { headOid: NEXT_HEAD },
    { nodeId: "PR_other_42" },
    { baseBranch: "release-test" },
    { headBranch: "codex/other" },
    { headRepository: { owner: "other-owner", repo: "k8s-test-infra" } },
  ]) await t.test(JSON.stringify(terminalOverrides), async () => {
    const { github } = completedMergeGitHub({ terminalOverrides });
    await assert.rejects(() => runMergeEvaluate({ event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github,
      config, policyRevision: REVISION, dryRun: false, prNumber: "42" }), /failed closed/);
    assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
    assert.deepEqual(github.calls.enableAutoMerge, [{
      nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
    }]);
  });
});

test("same-head identity changes after policy success block and disarm native auto-merge", async (t) => {
  const source = { headRepository: { owner: "fork-owner", repo: "k8s-test-infra" }, headBranch: "codex/fork" };
  for (const armed of [false, true]) {
    for (const change of [
      { headBranch: "codex/other" },
      { headRepository: { owner: "other-owner", repo: "k8s-test-infra" } },
      { baseBranch: "release-test" },
    ]) await t.test(`${armed ? "armed" : "new"} ${JSON.stringify(change)}`, async () => {
      const priorReads = armed ? 3 : 4;
      const changed = pullRequest({ ...source, ...change });
      const { github, result } = await run(evaluatorState({
        pullRequests: [...Array.from({ length: priorReads }, () => pullRequest(source)), changed],
        mergeStates: [...Array.from({ length: priorReads }, () => mergeState({
          autoMergeMethod: armed ? "SQUASH" : null,
        })), mergeState({ autoMergeMethod: "SQUASH", baseBranch: changed.baseBranch })],
        branchProtection: { main: true, "release-test": true },
      }));
      assert.ok(result.pullRequests[0].merge.blockers.includes("head-changed"));
      assert.deepEqual(github.calls.setMergePolicyCheck.map(({ conclusion }) => conclusion), [
        "action_required", "success", "action_required",
      ]);
      assert.equal(github.calls.setMergePolicyCheck.at(-1).headOid, HEAD);
      assert.deepEqual(github.calls.disableAutoMerge, [{ nodeId: "PR_node_42" }]);
      const lastBlock = github.callOrder.findLastIndex(({ operation, parameters }) => (
        operation === "setMergePolicyCheck" && parameters.conclusion === "action_required"
      ));
      assert.ok(lastBlock < operationIndex(github, "disableAutoMerge"));
    });
  }
});

test("a staging-check write failure cannot arm native auto-merge", async () => {
  const github = createFakeGitHub(evaluatorState({
    failures: { setMergePolicyCheck: new Error("staging check failed") },
  }));
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  await assert.rejects(() => runMergeEvaluate({
    event: WORKFLOW_EVENT, eventName: "workflow_dispatch", github, config, policyRevision: REVISION,
    dryRun: false, prNumber: "42",
  }), /evaluation failed closed/);
  assert.deepEqual(github.calls.enableAutoMerge, []);
  assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
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

function reviewObserverRun(overrides = {}) {
  return {
    id: 906,
    name: "Review observer",
    workflowPath: ".github/workflows/review-observer.yml",
    workflowSourceRef: "refs/heads/main",
    event: "pull_request_review",
    status: "completed",
    repository: REPOSITORY,
    pullRequestNumbers: [],
    ...overrides,
  };
}

const REVIEW_COMPLETION_EVENT = {
  ...WORKFLOW_EVENT,
  action: "completed",
  workflow_run: { id: 906, status: "completed" },
};

async function evaluateReviewCompletion(github, event = REVIEW_COMPLETION_EVENT) {
  const { runMergeEvaluate } = require("../src/modes/merge-evaluate.js");
  return runMergeEvaluate({
    event,
    eventName: "workflow_run",
    github,
    config,
    policyRevision: REVISION,
    dryRun: false,
    prNumber: "",
  });
}

test("empty trusted review mapping scans open PRs and applies current OWNERS approval", async () => {
  const { github, result } = await run(evaluatorState({
    evaluationWorkflowRuns: [reviewObserverRun()],
    openPullRequestNumbers: [42],
    labels: ["lgtm", "do-not-merge/needs-approval", "maintainer/custom"],
    comments: [{ id: 77, body: policyBody({ approvals: [] }) }],
    issueComments: [liveCommand(8000, "alice", "/lgtm")],
    reviews: [submittedReview(9000, "bob")],
  }), {
    event: REVIEW_COMPLETION_EVENT,
    eventName: "workflow_run",
    prNumber: "",
  });

  assert.deepEqual(github.calls.listOpenPullRequestNumbers, [{}]);
  assert.deepEqual(result.candidates, [42]);
  assert.deepEqual(result.pullRequests[0].merge.blockers, []);
  assert.deepEqual(github.calls.addPolicyLabel, [{ prNumber: 42, label: "approved" }]);
  assert.deepEqual(github.calls.removePolicyLabel, [
    { prNumber: 42, label: "do-not-merge/needs-approval" },
  ]);
  assert.deepEqual(github.calls.setMergePolicyCheck, [{
    prNumber: 42,
    headOid: HEAD,
    conclusion: "action_required",
    summary: "Repository merge policy is preparing native SQUASH auto-merge.",
  }, {
    prNumber: 42,
    headOid: HEAD,
    conclusion: "success",
    summary: "Repository merge policy passed.",
  }]);
  assert.ok(github.calls.getPullRequestReview.length > 0);
  assert.deepEqual(github.calls.enableAutoMerge, [{
    nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
  }]);
});

test("empty trusted review mapping keeps stale and dismissed approvals restrictive", async (t) => {
  for (const [name, overrides] of [
    ["stale reviewed head", { commitOid: NEXT_HEAD }],
    ["dismissed approval", { state: "DISMISSED" }],
  ]) await t.test(name, async () => {
    const { github, result } = await run(evaluatorState({
      evaluationWorkflowRuns: [reviewObserverRun()],
      openPullRequestNumbers: [42],
      comments: [{ id: 77, body: policyBody({ approvals: [] }) }],
      issueComments: [liveCommand(8000, "alice", "/lgtm")],
      reviews: [submittedReview(9000, "bob", overrides)],
    }), {
      event: REVIEW_COMPLETION_EVENT,
      eventName: "workflow_run",
      prNumber: "",
    });

    assert.deepEqual(result.candidates, [42]);
    assert.ok(result.pullRequests[0].merge.blockers.includes("approval-coverage-incomplete"));
    assert.ok(github.calls.addPolicyLabel.some(({ label }) => label === "do-not-merge/needs-approval"));
    assert.ok(github.calls.removePolicyLabel.some(({ label }) => label === "approved"));
    assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "action_required");
    assert.equal(github.calls.setMergePolicyCheck.some(({ conclusion }) => conclusion === "success"), false);
    assert.deepEqual(github.calls.enableAutoMerge, []);
  });
});

test("nonempty trusted review mapping stays scoped to its mapped PRs", async () => {
  const { github, result } = await run(evaluatorState({
    evaluationWorkflowRuns: [reviewObserverRun({ pullRequestNumbers: [42] })],
    openPullRequestNumbers: [99],
  }), {
    event: REVIEW_COMPLETION_EVENT,
    eventName: "workflow_run",
    prNumber: "",
  });

  assert.deepEqual(result.candidates, [42]);
  assert.deepEqual(github.calls.listOpenPullRequestNumbers, []);
  assert.ok(github.calls.getPullRequest.every(({ prNumber }) => prNumber === 42));
  assert.equal(github.calls.setMergePolicyCheck.at(-1).conclusion, "success");
  assert.deepEqual(github.calls.enableAutoMerge, [{
    nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
  }]);
});

test("empty review completion rejects untrusted run identities without PR reads or writes", async (t) => {
  for (const [name, overrides] of [
    ["unknown workflow", { name: "CI Pipeline" }],
    ["different trusted workflow", { name: "Commands", workflowPath: ".github/workflows/commands.yml" }],
    ["wrong event", { event: "workflow_dispatch" }],
    ["wrong path", { workflowPath: ".github/workflows/spoof.yml" }],
    ["wrong repository", { repository: "nvidia/other" }],
    ["incomplete run", { status: "in_progress" }],
    ["empty metadata event mapping", {
      name: "PR metadata", workflowPath: ".github/workflows/pr-metadata.yml", event: "pull_request_target",
    }],
  ]) await t.test(name, async () => {
    const github = createFakeGitHub(evaluatorState({
      evaluationWorkflowRuns: [reviewObserverRun(overrides)],
      openPullRequestNumbers: [42],
    }));
    const result = await evaluateReviewCompletion(github);

    assert.deepEqual(result, { status: "complete", candidates: [], pullRequests: [] });
    assert.deepEqual(github.callOrder, [
      { operation: "getEvaluationWorkflowRun", parameters: { runId: 906 } },
    ]);
  });
});

test("empty review completion rejects wrong run IDs before PR reads or writes", async (t) => {
  await t.test("mismatched refetched ID", async () => {
    const github = createFakeGitHub(evaluatorState({
      evaluationWorkflowRuns: [reviewObserverRun()],
      openPullRequestNumbers: [42],
    }));
    const getRun = github.getEvaluationWorkflowRun.bind(github);
    github.getEvaluationWorkflowRun = async (runId) => ({ ...await getRun(runId), id: 907 });

    assert.deepEqual(await evaluateReviewCompletion(github), {
      status: "complete", candidates: [], pullRequests: [],
    });
    assert.deepEqual(github.callOrder, [
      { operation: "getEvaluationWorkflowRun", parameters: { runId: 906 } },
    ]);
  });

  await t.test("non-positive event ID", async () => {
    const github = createFakeGitHub(evaluatorState({
      evaluationWorkflowRuns: [reviewObserverRun()],
      openPullRequestNumbers: [42],
    }));

    await assert.rejects(() => evaluateReviewCompletion(github, {
      ...REVIEW_COMPLETION_EVENT,
      workflow_run: { id: 0, status: "completed" },
    }), /workflow completion event is invalid/);
    assert.deepEqual(github.callOrder, []);
  });
});

test("review completion rejects missing and malformed mappings without an open scan", async (t) => {
  for (const [name, numbers, error] of [
    ["missing mapping", undefined, /scan exceeds limit/],
    ["null mapping", null, /scan exceeds limit/],
    ["non-array mapping", {}, /scan exceeds limit/],
    ["more than 100 mapped candidates", Array.from({ length: 101 }, (_, index) => index + 1), /scan exceeds limit/],
    ["duplicate mapped candidates", [42, 42], /candidate mapping is invalid/],
    ["non-positive mapped candidate", [42, 0], /candidate mapping is invalid/],
  ]) await t.test(name, async () => {
    const github = createFakeGitHub(evaluatorState({
      evaluationWorkflowRuns: [reviewObserverRun({ pullRequestNumbers: numbers })],
      openPullRequestNumbers: [42],
    }));

    await assert.rejects(() => evaluateReviewCompletion(github), error);
    assert.deepEqual(github.callOrder, [
      { operation: "getEvaluationWorkflowRun", parameters: { runId: 906 } },
    ]);
  });
});

test("empty trusted review mapping enforces bounded open scan candidates before PR reads or writes", async (t) => {
  for (const [name, numbers, error] of [
    ["more than 100 open candidates", Array.from({ length: 101 }, (_, index) => index + 1), /scan exceeds limit/],
    ["duplicate open candidates", [42, 42], /candidate mapping is invalid/],
    ["non-positive open candidate", [42, 0], /candidate mapping is invalid/],
  ]) await t.test(name, async () => {
    const github = createFakeGitHub(evaluatorState({
      evaluationWorkflowRuns: [reviewObserverRun()],
      openPullRequestNumbers: numbers,
    }));

    await assert.rejects(() => evaluateReviewCompletion(github), error);
    assert.deepEqual(github.callOrder, [
      { operation: "getEvaluationWorkflowRun", parameters: { runId: 906 } },
      { operation: "listOpenPullRequestNumbers", parameters: {} },
    ]);
  });
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
  assert.deepEqual(github.calls.enableAutoMerge, [{
    nodeId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: HEAD,
  }]);
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
    policyRevision: REVISION,
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
