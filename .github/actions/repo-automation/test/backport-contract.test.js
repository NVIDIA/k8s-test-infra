"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");

const { loadConfig } = require("../src/config.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");
const {
  runBackport,
  renderBackportStatusComment,
  assertBackportRef,
  BACKPORT_STATUS_MARKER,
} = require("../src/modes/backport.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");

// The origin (merged) pull request and its squash commit M with parent P.
const PR_NUMBER = 42;
const MERGE_COMMIT = "2".repeat(40); // M = merge_commit_sha
const MERGE_PARENT = "3".repeat(40); // P = M^1
const TARGET_HEAD = "1".repeat(40); // release-1.2 branch head T
const MERGE_TREE_OF_M = "5".repeat(40); // tree(M) — unused by graft, present for realism
const PARENT_TREE = "6".repeat(40); // tree(P) — unused by the corrected graft, present for realism
const TARGET_TREE = "4".repeat(40); // tree(T) — grafted onto the squash parent P
const PR_HEAD = "7".repeat(40);
const BACKPORT_BRANCH = `backport/${PR_NUMBER}-to-release-1.2`;
const BACKPORT_REF = `heads/${BACKPORT_BRANCH}`;
const ORIGINAL_MESSAGE = "feat: add gpu probe\n\nSigned-off-by: Orig Author <orig@example.com>";
const ORIGINAL_AUTHOR = { name: "Orig Author", email: "orig@example.com", date: "2026-07-20T10:00:00Z" };

// The fake mints deterministic Git OIDs by incrementing a seed and hex-padding.
// A single graft createCommit precedes the server-side merge, so with one graft
// per target the merge yields seed+2 (merge commit) and seed+3 (merge tree);
// the final cherry-pick commit is seed+4.
const OID_SEED = 0x1000;
function syntheticOid(counter) {
  return counter.toString(16).padStart(40, "0");
}
const GRAFT_COMMIT = syntheticOid(OID_SEED + 1);
const MERGE_RESULT_TREE = syntheticOid(OID_SEED + 3);
const FINAL_COMMIT = syntheticOid(OID_SEED + 4);

const WRITE_OPERATIONS = new Set([
  "createRef",
  "updateRef",
  "deleteRef",
  "createCommit",
  "mergeBranches",
  "createPullRequest",
  "upsertPolicyComment",
  "addCherryPickLabel",
  "removeCherryPickLabel",
  "ensureLabel",
]);

function writes(github) {
  return github.callOrder.filter(({ operation }) => WRITE_OPERATIONS.has(operation));
}

function closedEvent(overrides = {}) {
  return {
    action: "closed",
    pull_request: {
      number: PR_NUMBER,
      merged: true,
      merge_commit_sha: MERGE_COMMIT,
    },
    repository: {
      name: "k8s-test-infra",
      full_name: "nvidia/k8s-test-infra",
      owner: { login: "nvidia" },
    },
    ...overrides,
  };
}

function labeledEvent() {
  return {
    action: "labeled",
    label: { name: "cherry-pick/release-1.2" },
    pull_request: {
      number: PR_NUMBER,
      merged: true,
      merge_commit_sha: MERGE_COMMIT,
    },
    repository: {
      name: "k8s-test-infra",
      full_name: "nvidia/k8s-test-infra",
      owner: { login: "nvidia" },
    },
  };
}

function mergedPullRequest(overrides = {}) {
  return {
    number: PR_NUMBER,
    title: "feat: add gpu probe",
    body: "",
    draft: false,
    author: "orig-author",
    headOid: PR_HEAD,
    state: "closed",
    merged: true,
    mergeCommitOid: MERGE_COMMIT,
    baseBranch: "main",
    baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    ...overrides,
  };
}

function backportState(overrides = {}) {
  return {
    oidSeed: OID_SEED,
    pullRequests: overrides.pullRequests ?? [mergedPullRequest()],
    labels: overrides.labels ?? [{ name: "cherry-pick/release-1.2" }],
    branches: overrides.branches ?? { "release-1.2": TARGET_HEAD },
    commits: overrides.commits ?? {
      [MERGE_COMMIT]: {
        oid: MERGE_COMMIT,
        treeOid: MERGE_TREE_OF_M,
        parents: [MERGE_PARENT],
        message: ORIGINAL_MESSAGE,
        author: ORIGINAL_AUTHOR,
      },
      [MERGE_PARENT]: { oid: MERGE_PARENT, treeOid: PARENT_TREE, parents: [], message: "", author: null },
      [TARGET_HEAD]: { oid: TARGET_HEAD, treeOid: TARGET_TREE, parents: [], message: "", author: null },
    },
    comments: overrides.comments ?? [],
    pulls: overrides.pulls ?? [],
    mergeConflicts: overrides.mergeConflicts,
    mergeNoops: overrides.mergeNoops,
    failures: overrides.failures,
  };
}

async function run(state = backportState(), options = {}) {
  const github = createFakeGitHub(state);
  const result = await runBackport({
    event: options.event ?? closedEvent(),
    github,
    config: options.config ?? loadConfig(repositoryRoot),
    dryRun: options.dryRun ?? false,
    now: options.now ?? (() => "2026-07-23T10:00:00.000Z"),
  });
  return { github, result };
}

test("happy path grafts the squash commit and opens a namespaced backport PR", async () => {
  const { github, result } = await run();

  assert.equal(result.status, "complete");
  assert.equal(result.prNumber, PR_NUMBER);
  assert.deepEqual(result.targets, [
    { branch: "release-1.2", outcome: "created", backportPr: { number: 1000, url: "https://github.com/NVIDIA/k8s-test-infra/pull/1000" } },
  ]);

  // Exact graft sequence: create ref at T, graft tree(T) parented on the squash
  // parent P, merge M so the replay is only diff(P->M), re-parent the merge tree
  // onto T with author + trailer, open the PR.
  const ops = github.callOrder.map(({ operation }) => operation);
  assert.deepEqual(ops, [
    "getPullRequest",
    "getCommitInfo",
    "listIssueLabels",
    "getBranch",
    "getBranch",
    "findOpenPullRequest",
    "getCommitInfo",
    "getPolicyComment",
    "getPullRequest",
    "createRef",
    "createCommit",
    "updateRef",
    "mergeBranches",
    "createCommit",
    "updateRef",
    "createPullRequest",
    "upsertPolicyComment",
  ]);

  assert.deepEqual(github.calls.createRef, [{ name: BACKPORT_REF, oid: TARGET_HEAD }]);
  assert.deepEqual(github.calls.mergeBranches, [{ base: BACKPORT_BRANCH, head: MERGE_COMMIT }]);
  assert.deepEqual(github.calls.createCommit[0], {
    message: `Graft base for ${BACKPORT_BRANCH}`,
    treeOid: TARGET_TREE,
    parentOids: [MERGE_PARENT],
  });
  assert.deepEqual(github.calls.createCommit[1], {
    message: `${ORIGINAL_MESSAGE}\n\n(cherry picked from commit ${MERGE_COMMIT})`,
    treeOid: MERGE_RESULT_TREE,
    parentOids: [TARGET_HEAD],
    author: ORIGINAL_AUTHOR,
  });
  assert.deepEqual(github.calls.updateRef, [
    { name: BACKPORT_REF, oid: GRAFT_COMMIT, force: true },
    { name: BACKPORT_REF, oid: FINAL_COMMIT, force: true },
  ]);
  assert.deepEqual(github.calls.createPullRequest, [{
    base: "release-1.2",
    head: BACKPORT_BRANCH,
    title: "[release-1.2] feat: add gpu probe",
    body: github.calls.createPullRequest[0].body,
  }]);
  assert.match(github.calls.createPullRequest[0].body, /#42/);
  assert.match(github.calls.createPullRequest[0].body, /orig-author/);

  // The backport branch survives at the re-parented cherry-pick commit.
  const snapshot = github.backportSnapshot();
  assert.equal(snapshot.refs[BACKPORT_REF], FINAL_COMMIT);
  assert.equal(snapshot.pulls.length, 1);

  // The single status comment carries the marker exactly once and records the outcome.
  assert.equal(snapshot.comments.length, 1);
  assert.equal(snapshot.comments[0].body.split(BACKPORT_STATUS_MARKER).length - 1, 1);
  assert.match(snapshot.comments[0].body, /#1000/);
});

test("closed and labeled deliveries produce the same result for the same label set", async () => {
  const { result: closed } = await run(backportState(), { event: closedEvent() });
  const { result: labeled } = await run(backportState(), { event: labeledEvent() });
  assert.deepEqual(closed, labeled);
});

test("a bare merge conflict deletes the ref, opens no PR, and records manual instructions", async () => {
  const { github, result } = await run(backportState({ mergeConflicts: [[BACKPORT_BRANCH, MERGE_COMMIT]] }));

  assert.equal(result.targets[0].outcome, "conflicts");
  assert.equal(github.calls.createPullRequest.length, 0);
  assert.deepEqual(github.calls.deleteRef, [{ name: BACKPORT_REF }]);

  // Zero residue: the temporary backport ref must be gone.
  const snapshot = github.backportSnapshot();
  assert.equal(Object.hasOwn(snapshot.refs, BACKPORT_REF), false);
  assert.match(snapshot.comments[0].body, /manually/i);
  assert.doesNotMatch(snapshot.comments[0].body, /created backport pull request/);
});

test("a 204 already-merged result is a benign empty pick, not a conflict", async () => {
  const { github, result } = await run(backportState({ mergeNoops: [[BACKPORT_BRANCH, MERGE_COMMIT]] }));

  assert.equal(result.targets[0].outcome, "empty");
  assert.equal(github.calls.createPullRequest.length, 0);
  assert.equal(github.calls.createCommit.length, 1); // only the graft; no re-parent commit
  assert.deepEqual(github.calls.deleteRef, [{ name: BACKPORT_REF }]);

  const snapshot = github.backportSnapshot();
  assert.equal(Object.hasOwn(snapshot.refs, BACKPORT_REF), false);
  assert.match(snapshot.comments[0].body, /already present/i);
  assert.doesNotMatch(snapshot.comments[0].body, /manually/i);
});

test("a content-empty successful merge (result tree equals target tree) opens no PR", async () => {
  // Force tree(T) to equal the merge result tree the fake will mint (seed+3).
  const state = backportState();
  state.commits[TARGET_HEAD].treeOid = MERGE_RESULT_TREE;
  const { github, result } = await run(state);

  assert.equal(result.targets[0].outcome, "empty");
  assert.equal(github.calls.createPullRequest.length, 0);
  assert.equal(github.calls.createCommit.length, 2); // graft + re-parent, then detect empty
  assert.deepEqual(github.calls.deleteRef, [{ name: BACKPORT_REF }]);

  const snapshot = github.backportSnapshot();
  assert.equal(Object.hasOwn(snapshot.refs, BACKPORT_REF), false);
});

test("redelivery with an existing branch and open PR is idempotent with zero writes", async () => {
  const existing = renderBackportStatusComment([
    { branch: "release-1.2", outcome: "already-exists", backportPr: { number: 2001, url: "https://github.com/NVIDIA/k8s-test-infra/pull/2001" } },
  ]);
  const { github, result } = await run(backportState({
    branches: { "release-1.2": TARGET_HEAD, [BACKPORT_BRANCH]: GRAFT_COMMIT },
    pulls: [{ number: 2001, url: "https://github.com/NVIDIA/k8s-test-infra/pull/2001", base: "release-1.2", head: BACKPORT_BRANCH, state: "open" }],
    comments: [{ id: 7, author: "github-actions[bot]", body: existing }],
  }));

  assert.equal(result.targets[0].outcome, "already-exists");
  assert.deepEqual(result.targets[0].backportPr, { number: 2001, url: "https://github.com/NVIDIA/k8s-test-infra/pull/2001" });
  // Unchanged status comment must not be re-upserted; no ref/PR writes at all.
  assert.deepEqual(writes(github), []);
});

test("invalid targets (base branch and unmatched pattern) never reach a branch lookup", async () => {
  const { github, result } = await run(backportState({
    labels: [{ name: "cherry-pick/main" }, { name: "cherry-pick/feature-x" }],
  }));

  const byBranch = Object.fromEntries(result.targets.map((t) => [t.branch, t.outcome]));
  assert.equal(byBranch.main, "invalid-target");
  assert.equal(byBranch["feature-x"], "invalid-target");
  assert.deepEqual(github.calls.getBranch, []); // no live lookup for invalid targets
  // No graft writes; only the per-target status comment is posted.
  assert.deepEqual(github.calls.createRef, []);
  assert.deepEqual(github.calls.createPullRequest, []);
  assert.deepEqual(github.calls.mergeBranches, []);
  assert.equal(github.calls.upsertPolicyComment.length, 1);
  assert.match(github.calls.upsertPolicyComment[0].body, /not an eligible backport target/);
});

test("a pattern-valid target whose branch does not exist reports branch-missing", async () => {
  const { github, result } = await run(backportState({
    labels: [{ name: "cherry-pick/release-9.9" }],
    branches: {}, // release-9.9 absent
  }));

  assert.equal(result.targets[0].outcome, "branch-missing");
  assert.deepEqual(github.calls.getBranch, [{ branch: "release-9.9" }]);
  // No graft writes; only the per-target status comment is posted.
  assert.deepEqual(github.calls.createRef, []);
  assert.deepEqual(github.calls.createPullRequest, []);
  assert.equal(github.calls.upsertPolicyComment.length, 1);
  assert.match(github.calls.upsertPolicyComment[0].body, /target branch does not exist/);
});

test("an unmerged pull request is skipped before any planning read", async () => {
  const { github, result } = await run(backportState({ pullRequests: [mergedPullRequest({ merged: false, state: "open" })] }));

  assert.equal(result.status, "skipped");
  assert.deepEqual(github.callOrder.map(({ operation }) => operation), ["getPullRequest"]);
});

test("the pre-write fence aborts every write when the live PR is no longer merged", async () => {
  const state = backportState({
    pullRequests: [mergedPullRequest(), mergedPullRequest({ merged: false, state: "open" })],
  });
  const github = createFakeGitHub(state);
  await assert.rejects(
    () => runBackport({ event: closedEvent(), github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-23T10:00:00.000Z" }),
    /stale/i,
  );
  assert.deepEqual(writes(github), []);
});

test("the pre-write fence aborts every write when the live merge commit OID no longer matches the event", async () => {
  const state = backportState({
    pullRequests: [mergedPullRequest(), mergedPullRequest({ mergeCommitOid: "d".repeat(40) })],
  });
  const github = createFakeGitHub(state);
  await assert.rejects(
    () => runBackport({ event: closedEvent(), github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-23T10:00:00.000Z" }),
    /stale/i,
  );
  assert.deepEqual(writes(github), []);
});

test("the pre-write fence aborts every write when the live re-read omits the merge commit OID", async () => {
  const omitOid = mergedPullRequest();
  delete omitOid.mergeCommitOid;
  const state = backportState({
    pullRequests: [mergedPullRequest(), omitOid],
  });
  const github = createFakeGitHub(state);
  await assert.rejects(
    () => runBackport({ event: closedEvent(), github, config: loadConfig(repositoryRoot), dryRun: false, now: () => "2026-07-23T10:00:00.000Z" }),
    /stale/i,
  );
  assert.deepEqual(writes(github), []);
});

test("the pre-write fence proceeds when the live re-read carries the event merge commit OID", async () => {
  // Two distinct reads (planning and the fence re-read) both carry the event's
  // merge_commit_sha, so the fence's merge-anchor check passes and the graft runs.
  const { github, result } = await run(backportState({
    pullRequests: [mergedPullRequest(), mergedPullRequest()],
  }));

  assert.equal(result.status, "complete");
  assert.equal(result.targets[0].outcome, "created");
  assert.equal(github.calls.getPullRequest.length, 2); // planning read + fence re-read
  assert.equal(github.calls.createPullRequest.length, 1);
});

test("dry-run plans the backport with zero writes and no fence re-read", async () => {
  const { github, result } = await run(backportState(), { dryRun: true });

  assert.equal(result.status, "planned");
  assert.equal(result.targets[0].branch, "release-1.2");
  assert.equal(result.targets[0].outcome, "created");
  assert.equal(Object.hasOwn(result.targets[0], "backportPr"), false);
  assert.deepEqual(writes(github), []);
  assert.equal(github.calls.getPullRequest.length, 1); // planning read only, no fence
});

test("a createPullRequest failure yields an error outcome and still processes remaining targets", async () => {
  const { github, result } = await run(backportState({
    labels: [{ name: "cherry-pick/release-1.2" }, { name: "cherry-pick/release-2.0" }],
    branches: { "release-1.2": TARGET_HEAD, "release-2.0": TARGET_HEAD },
    failures: { createPullRequest: [new Error("simulated PR failure"), null] },
  }));

  const byBranch = Object.fromEntries(result.targets.map((t) => [t.branch, t.outcome]));
  assert.equal(byBranch["release-1.2"], "error");
  assert.equal(byBranch["release-2.0"], "created");

  // The failed target leaves no dangling ref; both refs were namespaced.
  const snapshot = github.backportSnapshot();
  assert.equal(Object.hasOwn(snapshot.refs, "heads/backport/42-to-release-1.2"), false);
  for (const call of github.calls.createRef) {
    assert.equal(call.name.startsWith("heads/backport/"), true);
  }
  for (const call of github.calls.deleteRef) {
    assert.equal(call.name.startsWith("heads/backport/"), true);
  }
});

test("the backport-status comment never leaks event body or secret sentinels", async () => {
  const { github } = await run(backportState({
    pullRequests: [mergedPullRequest({ body: "pr-body-secret-sentinel-9a1f" })],
  }));
  const serialized = JSON.stringify(github.callOrder);
  assert.equal(serialized.includes("pr-body-secret-sentinel-9a1f"), false);
});

test("assertBackportRef discriminates the backport ref namespace", () => {
  assert.doesNotThrow(() => assertBackportRef("heads/backport/42-to-release-1.2"));
  assert.doesNotThrow(() => assertBackportRef("heads/backport/7-to-release/1.2"));
  for (const escaped of [
    "heads/main",
    "heads/release-1.2",
    "heads/backportx/42-to-release-1.2",
    "refs/heads/backport/42",
    "tags/backport/42",
    "backport/42-to-release-1.2",
  ]) {
    assert.throws(() => assertBackportRef(escaped), /backport namespace/i, `must reject ${escaped}`);
  }
});

test("renderBackportStatusComment carries the marker exactly once and every outcome", () => {
  const body = renderBackportStatusComment([
    { branch: "release-1.2", outcome: "created", backportPr: { number: 10, url: "u" } },
    { branch: "release-2.0", outcome: "conflicts" },
    { branch: "release-3.0", outcome: "empty" },
    { branch: "release-4.0", outcome: "already-exists", backportPr: { number: 11, url: "u" } },
    { branch: "feature-x", outcome: "invalid-target" },
    { branch: "release-9.9", outcome: "branch-missing" },
    { branch: "release-5.0", outcome: "error" },
  ]);
  assert.equal(body.split(BACKPORT_STATUS_MARKER).length - 1, 1);
  assert.match(body, /release-1\.2/);
  assert.match(body, /#10/);
  assert.match(body, /manually/i);
});

test("a merged PR with no cherry-pick labels and no prior status comment gets no comment", async () => {
  const { github, result } = await run(backportState({ labels: [] }));

  assert.equal(result.status, "complete");
  assert.deepEqual(result.targets, []);
  // Nothing to report and nothing to update: no bot comment on an ordinary PR.
  assert.deepEqual(github.calls.upsertPolicyComment, []);
  assert.equal(github.backportSnapshot().comments.length, 0);
});

test("a merged PR with no cherry-pick labels still updates a prior status comment", async () => {
  const stale = renderBackportStatusComment([
    { branch: "release-1.2", outcome: "created", backportPr: { number: 9, url: "https://github.com/NVIDIA/k8s-test-infra/pull/9" } },
  ]);
  const { github } = await run(backportState({
    labels: [],
    comments: [{ id: 3, author: "github-actions[bot]", body: stale }],
  }));

  // An existing status comment is kept current even once every label is gone.
  assert.equal(github.calls.upsertPolicyComment.length, 1);
  const updated = github.backportSnapshot().comments[0].body;
  assert.doesNotMatch(updated, /#9/);
  assert.match(updated, /Backport status/);
});

test("a leftover backport branch with no open PR is reused instead of wedging in error", async () => {
  const staleOid = "9".repeat(40);
  const { github, result } = await run(backportState({
    branches: { "release-1.2": TARGET_HEAD, [BACKPORT_BRANCH]: staleOid },
    pulls: [], // the backport PR was closed without deleting its branch
  }));

  assert.equal(result.targets[0].outcome, "created");
  // The stale ref is force-updated through the guard, never re-created (which
  // would 422 "Reference already exists" and wedge the target forever).
  assert.deepEqual(github.calls.createRef, []);
  assert.equal(github.calls.updateRef[0].name, BACKPORT_REF);
  assert.equal(github.calls.updateRef[0].oid, TARGET_HEAD);
  assert.equal(github.backportSnapshot().pulls.length, 1);
});
