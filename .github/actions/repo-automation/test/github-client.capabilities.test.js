"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { createGitHubClient } = require("../src/github-client.js");
const { createFakeGitHub } = require("./helpers/fake-github.js");
const {
  isManagedCherryPickLabel,
  CHERRY_PICK_LABEL_PREFIX,
} = require("../src/managed-labels.js");

const BASE_OID = "a".repeat(40);
const HEAD_OID = "b".repeat(40);
const TREE_OID = "c".repeat(40);
const MERGE_OID = "d".repeat(40);
const NEW_COMMIT_OID = "e".repeat(40);

const NEW_CAPABILITIES = [
  "addCherryPickLabel",
  "removeCherryPickLabel",
  "ensureLabel",
  "getBranch",
  "getCommitInfo",
  "createCommit",
  "createRef",
  "updateRef",
  "deleteRef",
  "mergeBranches",
  "createPullRequest",
  "findOpenPullRequest",
];

function client(rest) {
  return createGitHubClient({ rest }, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
}

// --- managed-labels grammar ---------------------------------------------

test("isManagedCherryPickLabel accepts only cherry-pick/<branch-token>", () => {
  assert.equal(CHERRY_PICK_LABEL_PREFIX, "cherry-pick/");
  for (const good of [
    "cherry-pick/release-1.2",
    "cherry-pick/release/1.2",
    "cherry-pick/v1.2.3",
    "cherry-pick/a",
  ]) {
    assert.equal(isManagedCherryPickLabel(good), true, `should accept ${good}`);
  }
  for (const bad of [
    "cherry-pick/",
    "cherry-pick/.hidden",
    "cherry-pick/release..1",
    "cherry-pick/release//1",
    "cherry-pick/release/",
    "cherry-pick/release.lock",
    "cherrypick/release",
    "kind/bug",
    "lgtm",
    "",
    42,
    null,
    undefined,
  ]) {
    assert.equal(isManagedCherryPickLabel(bad), false, `should reject ${String(bad)}`);
  }
});

// --- real client: cherry-pick labels ------------------------------------

test("client addCherryPickLabel validates the managed prefix and calls addLabels", async () => {
  const calls = [];
  const c = client({ issues: {
    addLabels: async (p) => { calls.push(p); return { data: {} }; },
  } });
  await c.addCherryPickLabel(42, "cherry-pick/release-1.2");
  assert.deepEqual(calls, [{
    owner: "NVIDIA", repo: "k8s-test-infra", issue_number: 42, labels: ["cherry-pick/release-1.2"],
  }]);
  await assert.rejects(() => c.addCherryPickLabel(42, "kind/bug"), /cherry-pick-managed/);
  await assert.rejects(() => c.addCherryPickLabel(42, "cherry-pick/"), /cherry-pick-managed/);
});

test("client removeCherryPickLabel tolerates 404 and rejects unmanaged labels", async () => {
  let attempts = 0;
  const notFound = Object.assign(new Error("nf"), { status: 404 });
  const c = client({ issues: {
    removeLabel: async () => { attempts += 1; throw notFound; },
  } });
  await c.removeCherryPickLabel(42, "cherry-pick/release-1.2");
  assert.equal(attempts, 1);
  await assert.rejects(() => c.removeCherryPickLabel(42, "lgtm"), /cherry-pick-managed/);

  const boom = Object.assign(new Error("boom"), { status: 500 });
  const c2 = client({ issues: { removeLabel: async () => { throw boom; } } });
  await assert.rejects(
    () => c2.removeCherryPickLabel(42, "cherry-pick/release-1.2"),
    /removeCherryPickLabel failed/,
  );
});

// --- real client: ensureLabel -------------------------------------------

test("client ensureLabel returns the existing label without creating", async () => {
  let creates = 0;
  const c = client({ issues: {
    getLabel: async () => ({ data: { name: "cherry-pick", color: "ededed", description: "d" } }),
    createLabel: async () => { creates += 1; return { data: {} }; },
  } });
  assert.deepEqual(
    await c.ensureLabel({ name: "cherry-pick", color: "111111", description: "new" }),
    { name: "cherry-pick", color: "ededed", description: "d" },
  );
  assert.equal(creates, 0);
});

test("client ensureLabel creates the label when absent", async () => {
  const notFound = Object.assign(new Error("nf"), { status: 404 });
  const calls = [];
  const c = client({ issues: {
    getLabel: async () => { throw notFound; },
    createLabel: async (p) => { calls.push(p); return { data: { name: p.name, color: p.color, description: p.description } }; },
  } });
  assert.deepEqual(
    await c.ensureLabel({ name: "cherry-pick", color: "111111", description: "new" }),
    { name: "cherry-pick", color: "111111", description: "new" },
  );
  assert.deepEqual(calls, [{
    owner: "NVIDIA", repo: "k8s-test-infra", name: "cherry-pick", color: "111111", description: "new",
  }]);
});

// --- real client: git data ----------------------------------------------

test("client getBranch returns name/oid and null on 404", async () => {
  const c = client({ repos: {
    getBranch: async (p) => {
      if (p.branch === "missing") throw Object.assign(new Error("nf"), { status: 404 });
      return { data: { name: "release-1.2", commit: { sha: BASE_OID } } };
    },
  } });
  assert.deepEqual(await c.getBranch("release-1.2"), { name: "release-1.2", oid: BASE_OID });
  assert.equal(await c.getBranch("missing"), null);
});

test("client getCommitInfo maps the commit shape", async () => {
  const c = client({ git: {
    getCommit: async () => ({ data: {
      sha: HEAD_OID,
      tree: { sha: TREE_OID },
      parents: [{ sha: BASE_OID }],
      message: "fix: thing\n\nbody",
      author: { name: "Ada", email: "ada@example.com", date: "2026-01-02T03:04:05Z" },
    } }),
  } });
  assert.deepEqual(await c.getCommitInfo(HEAD_OID), {
    oid: HEAD_OID,
    treeOid: TREE_OID,
    parents: [BASE_OID],
    message: "fix: thing\n\nbody",
    author: { name: "Ada", email: "ada@example.com", date: "2026-01-02T03:04:05Z" },
  });
});

test("client createCommit posts a git commit and returns its oid", async () => {
  const calls = [];
  const c = client({ git: {
    createCommit: async (p) => { calls.push(p); return { data: { sha: NEW_COMMIT_OID } }; },
  } });
  const oid = await c.createCommit({
    message: "chore: backport",
    treeOid: TREE_OID,
    parentOids: [BASE_OID],
    author: { name: "Bot", email: "bot@x.io", date: "2026-01-02T03:04:05Z" },
  });
  assert.equal(oid, NEW_COMMIT_OID);
  assert.deepEqual(calls, [{
    owner: "NVIDIA", repo: "k8s-test-infra",
    message: "chore: backport", tree: TREE_OID, parents: [BASE_OID],
    author: { name: "Bot", email: "bot@x.io", date: "2026-01-02T03:04:05Z" },
  }]);
});

test("client createCommit omits author when not provided", async () => {
  let received;
  const c = client({ git: {
    createCommit: async (p) => { received = p; return { data: { sha: NEW_COMMIT_OID } }; },
  } });
  await c.createCommit({ message: "m", treeOid: TREE_OID, parentOids: [BASE_OID] });
  assert.equal(Object.hasOwn(received, "author"), false);
});

test("client createRef fully-qualifies the ref; update/delete use the bare heads form", async () => {
  const calls = [];
  const c = client({ git: {
    createRef: async (p) => { calls.push(["create", p]); return { data: {} }; },
    updateRef: async (p) => { calls.push(["update", p]); return { data: {} }; },
    deleteRef: async (p) => { calls.push(["delete", p]); return { data: {} }; },
  } });
  await c.createRef("heads/cherry-pick/release-1.2", BASE_OID);
  await c.updateRef("heads/cherry-pick/release-1.2", HEAD_OID);
  await c.deleteRef("heads/cherry-pick/release-1.2");
  assert.deepEqual(calls, [
    ["create", { owner: "NVIDIA", repo: "k8s-test-infra", ref: "refs/heads/cherry-pick/release-1.2", sha: BASE_OID }],
    ["update", { owner: "NVIDIA", repo: "k8s-test-infra", ref: "heads/cherry-pick/release-1.2", sha: HEAD_OID, force: true }],
    ["delete", { owner: "NVIDIA", repo: "k8s-test-infra", ref: "heads/cherry-pick/release-1.2" }],
  ]);
});

test("client ref methods reject malformed ref names before calling octokit", async () => {
  const c = client({ git: {
    createRef: async () => { throw new Error("must not call"); },
  } });
  for (const bad of [
    "refs/heads/x", "tags/x", "heads/", "heads/x/", "heads/x..y",
    "heads/x//y", "heads/x.lock", "heads/x y", "heads/x\u007f", "main",
  ]) {
    await assert.rejects(() => c.createRef(bad, BASE_OID), /ref name/, `createRef should reject ${bad}`);
  }
});

// --- real client: merge --------------------------------------------------

test("client mergeBranches returns the merge commit on success", async () => {
  const c = client({ repos: {
    merge: async () => ({ status: 201, data: { sha: MERGE_OID, commit: { tree: { sha: TREE_OID } } } }),
  } });
  assert.deepEqual(await c.mergeBranches("release-1.2", HEAD_OID), {
    merged: true, oid: MERGE_OID, treeOid: TREE_OID,
  });
});

test("client mergeBranches maps a 409 conflict to merged:false without alreadyMerged", async () => {
  const c = client({ repos: {
    merge: async () => { throw Object.assign(new Error("conflict"), { status: 409 }); },
  } });
  const result = await c.mergeBranches("release-1.2", HEAD_OID);
  assert.deepEqual(result, { merged: false });
  assert.ok(!result.alreadyMerged, "409 conflict must not report alreadyMerged");
});

test("client mergeBranches maps a 204 base-already-contains-head to merged:false, alreadyMerged:true", async () => {
  const c = client({ repos: { merge: async () => ({ status: 204, data: undefined }) } });
  assert.deepEqual(
    await c.mergeBranches("release-1.2", HEAD_OID),
    { merged: false, alreadyMerged: true },
  );
});

test("client mergeBranches maps a malformed 201 (non-string sha) to bare merged:false", async () => {
  const c = client({ repos: {
    merge: async () => ({ status: 201, data: { sha: 12345, commit: { tree: { sha: TREE_OID } } } }),
  } });
  const result = await c.mergeBranches("release-1.2", HEAD_OID);
  assert.deepEqual(result, { merged: false });
  assert.ok(!result.alreadyMerged, "malformed 201 must not report alreadyMerged");
});

test("client mergeBranches throws normalized on other errors", async () => {
  const c = client({ repos: {
    merge: async () => { throw Object.assign(new Error("nf"), { status: 404 }); },
  } });
  await assert.rejects(
    () => c.mergeBranches("release-1.2", HEAD_OID),
    (error) => error.name === "GitHubClientError" && /mergeBranches failed/.test(error.message),
  );
});

// --- real client: pull requests -----------------------------------------

test("client createPullRequest returns the number and url", async () => {
  const calls = [];
  const c = client({ pulls: {
    create: async (p) => { calls.push(p); return { data: { number: 77, html_url: "https://github.com/NVIDIA/k8s-test-infra/pull/77" } }; },
  } });
  assert.deepEqual(
    await c.createPullRequest({ base: "release-1.2", head: "cherry-pick/release-1.2", title: "Backport", body: "b" }),
    { number: 77, url: "https://github.com/NVIDIA/k8s-test-infra/pull/77" },
  );
  assert.deepEqual(calls, [{
    owner: "NVIDIA", repo: "k8s-test-infra",
    base: "release-1.2", head: "cherry-pick/release-1.2", title: "Backport", body: "b",
  }]);
});

test("client findOpenPullRequest returns a matching open PR else null", async () => {
  const listCalls = [];
  const list = [{
    number: 5,
    html_url: "https://github.com/NVIDIA/k8s-test-infra/pull/5",
    base: { ref: "release-1.2" },
    head: { ref: "cherry-pick/release-1.2" },
  }];
  const octokit = {
    paginate: async (endpoint, parameters, map) => map(await endpoint(parameters), () => {}),
    rest: { pulls: { list: async (p) => { listCalls.push(p); return { data: list }; } } },
  };
  const c = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
  assert.deepEqual(
    await c.findOpenPullRequest("cherry-pick/release-1.2", "release-1.2"),
    { number: 5, url: "https://github.com/NVIDIA/k8s-test-infra/pull/5" },
  );
  assert.equal(listCalls[0].state, "open");
  assert.equal(listCalls[0].base, "release-1.2");
  assert.equal(listCalls[0].head, "NVIDIA:cherry-pick/release-1.2");
  assert.equal(await c.findOpenPullRequest("cherry-pick/other", "release-1.2"), null);
});

// --- token scrubbing -----------------------------------------------------

test("client getCommitInfo failure never leaks the auth token", async () => {
  const token = "ghp_LEAKEDSECRET1234567890";
  const raw = Object.assign(new Error(`Bad credentials: ${token}`), {
    status: 401,
    request: { headers: { authorization: `token ${token}` } },
  });
  const c = client({ git: { getCommit: async () => { throw raw; } } });
  await assert.rejects(() => c.getCommitInfo(HEAD_OID), (error) => {
    assert.equal(error.name, "GitHubClientError");
    assert.equal(error.message.includes(token), false, "token must be scrubbed");
    return true;
  });
});

test("client mergeBranches non-conflict failure never leaks the auth token", async () => {
  const token = "ghs_ANOTHERSECRETVALUE99";
  const raw = Object.assign(new Error(`boom ${token}`), {
    status: 500,
    request: { headers: { authorization: `Bearer ${token}` } },
  });
  const c = client({ repos: { merge: async () => { throw raw; } } });
  await assert.rejects(() => c.mergeBranches("release-1.2", HEAD_OID), (error) => {
    assert.equal(error.name, "GitHubClientError");
    assert.equal(error.message.includes(token), false, "token must be scrubbed");
    return true;
  });
});

// --- fake: git-data / ref / PR model ------------------------------------

test("fake models branches, refs, and commits with call recording", async () => {
  const fake = createFakeGitHub({
    branches: { "release-1.2": BASE_OID },
    commits: {
      [HEAD_OID]: {
        treeOid: TREE_OID, parents: [BASE_OID], message: "fix",
        author: { name: "A", email: "a@x", date: "2026-01-01T00:00:00Z" },
      },
    },
  });
  assert.deepEqual(await fake.getBranch("release-1.2"), { name: "release-1.2", oid: BASE_OID });
  assert.equal(await fake.getBranch("nope"), null);
  assert.equal((await fake.getCommitInfo(HEAD_OID)).treeOid, TREE_OID);

  await fake.createRef("heads/cherry-pick/release-1.2", BASE_OID);
  assert.deepEqual(await fake.getBranch("cherry-pick/release-1.2"), { name: "cherry-pick/release-1.2", oid: BASE_OID });
  await fake.updateRef("heads/cherry-pick/release-1.2", HEAD_OID);
  assert.deepEqual(await fake.getBranch("cherry-pick/release-1.2"), { name: "cherry-pick/release-1.2", oid: HEAD_OID });
  await fake.deleteRef("heads/cherry-pick/release-1.2");
  assert.equal(await fake.getBranch("cherry-pick/release-1.2"), null);

  assert.equal(fake.calls.getBranch.length, 5);
  assert.deepEqual(fake.calls.createRef, [{ name: "heads/cherry-pick/release-1.2", oid: BASE_OID }]);
});

test("fake createCommit records inputs and returns a git-shaped oid", async () => {
  const fake = createFakeGitHub();
  const oid = await fake.createCommit({ message: "m", treeOid: TREE_OID, parentOids: [BASE_OID] });
  assert.match(oid, /^[0-9a-f]{40}$/);
  assert.deepEqual((await fake.getCommitInfo(oid)).parents, [BASE_OID]);
  // record() clones via JSON, so an undefined author is dropped from the recording.
  assert.deepEqual(fake.calls.createCommit, [{ message: "m", treeOid: TREE_OID, parentOids: [BASE_OID] }]);
});

test("fake mergeBranches honors the mergeConflicts injection and advances the base", async () => {
  const fake = createFakeGitHub({
    branches: { "release-1.2": BASE_OID },
    mergeConflicts: [["release-1.2", HEAD_OID]],
  });
  assert.deepEqual(await fake.mergeBranches("release-1.2", HEAD_OID), { merged: false });

  const clean = await fake.mergeBranches("release-1.2", "feature");
  assert.equal(clean.merged, true);
  assert.match(clean.oid, /^[0-9a-f]{40}$/);
  assert.match(clean.treeOid, /^[0-9a-f]{40}$/);
  assert.deepEqual(await fake.getBranch("release-1.2"), { name: "release-1.2", oid: clean.oid });
});

test("fake mergeBranches honors the mergeNoops injection with the real 204 shape", async () => {
  const fake = createFakeGitHub({
    branches: { "release-1.2": BASE_OID },
    mergeNoops: [["release-1.2", HEAD_OID]],
  });
  assert.deepEqual(
    await fake.mergeBranches("release-1.2", HEAD_OID),
    { merged: false, alreadyMerged: true },
  );
  // A no-op merge must not advance the base ref.
  assert.deepEqual(await fake.getBranch("release-1.2"), { name: "release-1.2", oid: BASE_OID });
});

test("fake createPullRequest, findOpenPullRequest, and backportSnapshot cohere", async () => {
  const fake = createFakeGitHub({ branches: { "release-1.2": BASE_OID } });
  assert.equal(await fake.findOpenPullRequest("cherry-pick/release-1.2", "release-1.2"), null);
  const pr = await fake.createPullRequest({
    base: "release-1.2", head: "cherry-pick/release-1.2", title: "Backport", body: "b",
  });
  assert.equal(typeof pr.number, "number");
  assert.match(pr.url, /\/pull\/\d+$/);
  assert.deepEqual(
    await fake.findOpenPullRequest("cherry-pick/release-1.2", "release-1.2"),
    { number: pr.number, url: pr.url },
  );
  const snapshot = fake.backportSnapshot();
  assert.equal(snapshot.pulls.length, 1);
  assert.equal(snapshot.pulls[0].head, "cherry-pick/release-1.2");
  assert.ok(Object.hasOwn(snapshot, "refs"));
  assert.ok(Object.hasOwn(snapshot, "comments"));
});

test("fake ensureLabel and cherry-pick label ops mutate the label set", async () => {
  const fake = createFakeGitHub({ labels: [] });
  await fake.ensureLabel({ name: "cherry-pick/release-1.2", color: "ededed", description: "d" });
  assert.equal(fake.snapshot().some((label) => label.name === "cherry-pick/release-1.2"), true);
  await fake.addCherryPickLabel(42, "cherry-pick/release-1.2");
  await fake.removeCherryPickLabel(42, "cherry-pick/release-1.2");
  assert.equal(fake.snapshot().some((label) => label.name === "cherry-pick/release-1.2"), false);
  assert.deepEqual(fake.calls.addCherryPickLabel, [{ prNumber: 42, label: "cherry-pick/release-1.2" }]);
});

test("fake failure injection throws the queued error for a new capability", async () => {
  const boom = new Error("injected");
  const fake = createFakeGitHub({ failures: { mergeBranches: [boom] } });
  await assert.rejects(() => fake.mergeBranches("release-1.2", HEAD_OID), /injected/);
});

// --- parity --------------------------------------------------------------

test("fake exposes exactly the same new capability method names as the real client", () => {
  const realClient = client({});
  const fake = createFakeGitHub();
  const realMethods = new Set(Object.keys(realClient).filter((key) => typeof realClient[key] === "function"));
  const fakeMethods = new Set(Object.keys(fake).filter((key) => typeof fake[key] === "function"));
  for (const name of NEW_CAPABILITIES) {
    assert.equal(realMethods.has(name), true, `real client is missing ${name}`);
    assert.equal(fakeMethods.has(name), true, `fake is missing ${name}`);
  }
  assert.deepEqual(
    NEW_CAPABILITIES.filter((name) => fakeMethods.has(name)),
    NEW_CAPABILITIES.filter((name) => realMethods.has(name)),
  );
});
