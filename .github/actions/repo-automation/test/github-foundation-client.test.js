"use strict";

const assert = require("node:assert/strict");
const path = require("node:path");
const test = require("node:test");
const { URL } = require("node:url");

const { loadConfig } = require("../src/config.js");
const { createGitHubClient } = require("../src/github-client.js");

const actionsGitHub = import("@actions/github");

function workflowRun(id, options = {}) {
  return {
    id,
    head_sha: options.headSha ?? "a".repeat(40),
    status: "completed",
    conclusion: "failure",
    path: ".github/workflows/automation-ci.yml@refs/heads/main",
    event: "pull_request",
    pull_requests: options.pullRequests ?? [{ number: 42 }],
    repository: { full_name: "NVIDIA/k8s-test-infra" },
  };
}

async function octokitWithWorkflowFetch(fetch) {
  const { getOctokit } = await actionsGitHub;
  return getOctokit("test-token", {
    baseUrl: "https://api.github.com",
    request: {
      fetch: async (input, init) => {
        const response = await fetch(input, init);
        Object.defineProperty(response, "url", {
          value: typeof input === "string" ? input : input.url,
        });
        return response;
      },
    },
  });
}

function jsonResponse(data, headers = {}) {
  return new globalThis.Response(JSON.stringify(data), {
    status: 200,
    headers: { "content-type": "application/json", ...headers },
  });
}

function conflictPullRequest(baseRef) {
  return {
    number: 42,
    id: "PR_node_42",
    state: "OPEN",
    isDraft: false,
    mergeable: "CONFLICTING",
    headRefOid: "a".repeat(40),
    baseRefName: "main",
    baseRefOid: "c".repeat(40),
    baseRef,
  };
}

test("conflict client maps the current base branch tip instead of the PR's older base OID", async () => {
  let graphRequest;
  const octokit = await octokitWithWorkflowFetch(async (input, init) => {
    graphRequest = JSON.parse(init.body);
    return jsonResponse({ data: { repository: { pullRequest: conflictPullRequest({
      name: "main", target: { oid: "b".repeat(40) },
    }) } } });
  });
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  assert.deepEqual(await client.getConflictState(42), {
    number: 42,
    nodeId: "PR_node_42",
    repository: "nvidia/k8s-test-infra",
    state: "OPEN",
    draft: false,
    headOid: "a".repeat(40),
    baseBranch: "main",
    baseOid: "b".repeat(40),
    mergeability: "CONFLICTING",
  });
  assert.match(graphRequest.query, /baseRef\s*\{\s*name\s+target\s*\{\s*oid\s*\}\s*\}/);
  assert.doesNotMatch(graphRequest.query, /\bbaseRefOid\b/);
  assert.deepEqual(graphRequest.variables, { owner: "NVIDIA", repo: "k8s-test-infra", number: 42 });
});

for (const [name, baseRef] of [
  ["missing ref", undefined],
  ["null ref", null],
  ["wrong branch", { name: "release-1.0", target: { oid: "b".repeat(40) } }],
  ["missing branch name", { target: { oid: "b".repeat(40) } }],
  ["missing target", { name: "main" }],
  ["missing target OID", { name: "main", target: {} }],
  ["empty target OID", { name: "main", target: { oid: "" } }],
]) {
  test(`conflict client rejects ${name} without falling back to the older PR base OID`, async () => {
    const octokit = await octokitWithWorkflowFetch(async () => jsonResponse({
      data: { repository: { pullRequest: conflictPullRequest(baseRef) } },
    }));
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
    await assert.rejects(() => client.getConflictState(42), /GraphQL live base (?:ref|OID)/);
  });
}

function mockOctokit(overrides = {}) {
  const calls = [];
  const response = (name, data) => async (parameters) => {
    calls.push({ name, parameters });
    return { data: typeof data === "function" ? data(parameters) : data };
  };
  const rest = {
    issues: {
      listComments: response("listComments", []),
      getComment: response("getComment", {
        id: 91,
        body: "/lgtm",
        issue_url: "https://api.github.com/repos/NVIDIA/k8s-test-infra/issues/42",
        user: { login: "Alice", type: "User" },
        updated_at: "2026-09-17T10:00:00Z",
        created_at: "2026-09-17T10:00:00Z",
      }),
      addLabels: response("addLabels", {}),
      removeLabel: response("removeLabel", {}),
    },
    pulls: {
      get: response("getPullRequest", {
        number: 42,
        node_id: "PR_node_42",
        title: "feat: foundation",
        body: "",
        draft: false,
        state: "open",
        user: { login: "Author" },
        head: {
          ref: "feature",
          sha: "a".repeat(40),
          repo: { owner: { login: "NVIDIA" }, name: "k8s-test-infra" },
        },
        base: {
          ref: "main",
          repo: { owner: { login: "NVIDIA" }, name: "k8s-test-infra" },
        },
        merged: false,
        merge_commit_sha: null,
      }),
      listReviews: response("listReviews", [{
        id: 501,
        user: { login: "Alice" },
        state: "APPROVED",
        commit_id: "a".repeat(40),
        submitted_at: "2026-09-17T09:00:00Z",
      }]),
      getReview: response("getReview", {
        id: 501,
        user: { login: "Alice" },
        state: "APPROVED",
        commit_id: "a".repeat(40),
        submitted_at: "2026-09-17T09:00:00Z",
        body: "/lgtm\n\nReviewed the current changes.",
      }),
      list: response("listPullRequests", [{ number: 42 }, { number: 44 }]),
      create: response("createPullRequest", {
        number: 900,
        html_url: "https://github.com/NVIDIA/k8s-test-infra/pull/900",
      }),
    },
    users: {
      getByUsername: response("getUser", { login: "Alice", type: "User" }),
    },
    repos: {
      getCollaboratorPermissionLevel: response("getPermission", { permission: "write" }),
      getBranchProtection: response("getBranchProtection", { required_status_checks: {} }),
      getBranch: response("getBranch", ({ branch }) => ({
        name: branch,
        commit: { sha: "b".repeat(40) },
        protected: true,
      })),
    },
    actions: {
      listWorkflowRunsForRepo: response("listWorkflowRuns", [{
        id: 701,
        head_sha: "a".repeat(40),
        status: "completed",
        conclusion: "failure",
        path: ".github/workflows/automation-ci.yml@refs/heads/main",
        event: "pull_request",
        pull_requests: [{ number: 42 }],
        repository: { full_name: "NVIDIA/k8s-test-infra" },
      }]),
      getWorkflowRun: response("getWorkflowRun", {
        id: 701,
        head_sha: "a".repeat(40),
        status: "completed",
        conclusion: "failure",
        path: ".github/workflows/automation-ci.yml@refs/heads/main",
        event: "pull_request",
        pull_requests: [{ number: 42 }],
        repository: { full_name: "NVIDIA/k8s-test-infra" },
      }),
      reRunWorkflowFailedJobs: response("rerunFailed", {}),
    },
    checks: {
      create: response("createCheck", { id: 801 }),
    },
  };
  for (const [group, methods] of Object.entries(overrides.rest ?? {})) {
    rest[group] = { ...rest[group], ...methods };
  }
  const graphql = async (query, variables) => {
    calls.push({ name: "graphql", query, variables });
    if (query.includes("EnableAutoMerge")) return { enablePullRequestAutoMerge: { clientMutationId: null } };
    if (query.includes("DisableAutoMerge")) return { disablePullRequestAutoMerge: { clientMutationId: null } };
    return {
      repository: {
        pullRequest: {
          number: 42,
          id: "PR_node_42",
          state: "OPEN",
          isDraft: false,
          mergeable: "MERGEABLE",
          headRefOid: "a".repeat(40),
          baseRefName: "main",
          autoMergeRequest: null,
        },
      },
    };
  };
  return {
    calls,
    octokit: {
      rest,
      graphql,
      async paginate(endpoint, parameters, map) {
        const page = await endpoint(parameters);
        return map === undefined ? page.data : map(page, () => {});
      },
    },
  };
}

test("lists every pull request comment with live command provenance", async () => {
  const issueUrl = "https://api.github.com/repos/NVIDIA/k8s-test-infra/issues/42";
  const { octokit, calls } = mockOctokit({ rest: { issues: { listComments: async (parameters) => {
    calls.push({ name: "listComments", parameters });
    return { data: [
      { id: 90, body: "/hold", issue_url: issueUrl, user: { login: "Alice", type: "User" },
        created_at: "2026-09-17T10:00:00Z", updated_at: "2026-09-17T10:00:00Z" },
      { id: 91, body: null, issue_url: issueUrl, user: { login: "github-actions[bot]", type: "Bot" },
        created_at: "2026-09-17T10:00:00Z", updated_at: "2026-09-17T10:05:00Z" },
      { id: 92, body: "/unhold", issue_url: issueUrl, user: null,
        created_at: "2026-09-17T10:00:00Z", updated_at: "2026-09-17T10:00:00Z" },
    ] };
  } } } });
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  assert.deepEqual(await client.listIssueComments(42), [
    { id: 90, issueNumber: 42, body: "/hold", author: "alice", authorType: "User", edited: false,
      createdAt: "2026-09-17T10:00:00Z" },
    { id: 91, issueNumber: 42, body: "", author: "github-actions[bot]", authorType: "Bot", edited: true,
      createdAt: "2026-09-17T10:00:00Z" },
    { id: 92, issueNumber: 42, body: "/unhold", author: null, authorType: null, edited: false,
      createdAt: "2026-09-17T10:00:00Z" },
  ]);
  assert.deepEqual(calls.filter(({ name }) => name === "listComments").map(({ parameters }) => parameters), [
    { owner: "NVIDIA", repo: "k8s-test-infra", issue_number: 42, per_page: 100 },
  ]);
});

test("maps live command and approval provenance", async () => {
  const { octokit } = mockOctokit();
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  assert.deepEqual(await client.getIssueComment(91), {
    id: 91,
    issueNumber: 42,
    body: "/lgtm",
    author: "alice",
    authorType: "User",
    edited: false,
  });
  assert.deepEqual(await client.getUserIdentity("alice"), {
    login: "alice", type: "User", resolved: true, deleted: false,
  });
  assert.deepEqual(await client.getCollaboratorAccess("alice"), {
    liveCollaborator: true, permission: "write",
  });
  assert.deepEqual(await client.listPullRequestReviews(42), [{
    id: 501,
    user: "alice",
    state: "APPROVED",
    commitOid: "a".repeat(40),
    submittedAt: "2026-09-17T09:00:00Z",
  }]);
  assert.deepEqual(await client.getPullRequestReview(42, 501), {
    id: 501,
    user: "alice",
    state: "APPROVED",
    commitOid: "a".repeat(40),
    submittedAt: "2026-09-17T09:00:00Z",
    body: "/lgtm\n\nReviewed the current changes.",
  });
});

test("keeps unavailable live review bodies unknown", async () => {
  for (const body of [undefined, null, 42, {}]) {
    const { octokit } = mockOctokit({ rest: { pulls: {
      getReview: async () => ({ data: {
        id: 501,
        user: { login: "Alice" },
        state: "APPROVED",
        commit_id: "a".repeat(40),
        submitted_at: "2026-09-17T09:00:00Z",
        body,
      } }),
    } } });
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
    assert.equal((await client.getPullRequestReview(42, 501)).body, null);
  }
});

test("exposes only managed policy labels and native auto-merge disarm", async () => {
  const { octokit, calls } = mockOctokit();
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  await client.addPolicyLabel(42, "lgtm");
  await client.removePolicyLabel(42, "do-not-merge/hold");
  await assert.rejects(() => client.addPolicyLabel(42, "kind/feature"), /policy-managed/);
  await client.setMergePolicyCheck(42, "a".repeat(40), "success", "all gates passed");
  await client.disableAutoMerge("PR_node_42");

  assert.equal(typeof client.mergePullRequest, "undefined");
  assert.equal(typeof client.enableAutoMerge, "function");
  assert.equal(calls.some(({ name }) => name === "createCheck"), true);
  assert.equal(calls.filter(({ name }) => name === "graphql").length, 1);
});

for (const headOid of ["a".repeat(40), "b".repeat(64)]) {
  test(`native auto-merge pins the expected ${headOid.length}-digit head and SQUASH method in the GraphQL request`, async () => {
    const requests = [];
    const octokit = await octokitWithWorkflowFetch(async (input, init) => {
      requests.push({ url: new URL(input), method: init.method, body: JSON.parse(init.body) });
      return jsonResponse({ data: { enablePullRequestAutoMerge: { clientMutationId: null } } });
    });
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra");

    await client.enableAutoMerge("PR_node_42", "SQUASH", headOid);

    assert.equal(requests.length, 1);
    assert.equal(requests[0].url.pathname, "/graphql");
    assert.equal(requests[0].method, "POST");
    assert.deepEqual(requests[0].body.variables, {
      pullRequestId: "PR_node_42", mergeMethod: "SQUASH", expectedHeadOid: headOid,
    });
    assert.match(requests[0].body.query, /enablePullRequestAutoMerge\s*\(input:\s*\{/);
    assert.match(requests[0].body.query, /pullRequestId:\s*\$pullRequestId/);
    assert.match(requests[0].body.query, /mergeMethod:\s*\$mergeMethod/);
    assert.match(requests[0].body.query, /expectedHeadOid:\s*\$expectedHeadOid/);
    assert.match(requests[0].body.query, /\$expectedHeadOid:\s*GitObjectID!/);
    assert.doesNotMatch(requests[0].body.query, /\bmergePullRequest\s*\(/);
  });
}

for (const [name, status, data] of [
  ["transient service rejection", 503, { message: "unavailable" }],
  ["GraphQL head mismatch", 200, { errors: [{ type: "UNPROCESSABLE", message: "Head changed" }] }],
]) {
  test(`native auto-merge does not retry a ${name}`, async () => {
    let attempts = 0;
    const waits = [];
    const octokit = await octokitWithWorkflowFetch(async () => {
      attempts += 1;
      return new globalThis.Response(JSON.stringify(data), {
        status, headers: { "content-type": "application/json" },
      });
    });
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", {
      maxAttempts: 3, sleep: async (milliseconds) => waits.push(milliseconds),
    });

    await assert.rejects(() => client.enableAutoMerge("PR_node_42", "SQUASH", "a".repeat(40)), {
      name: "GitHubClientError", operation: "enableAutoMerge",
    });
    assert.equal(attempts, 1);
    assert.deepEqual(waits, []);
  });
}

for (const payload of [{}, { enablePullRequestAutoMerge: null }]) {
  test("native auto-merge rejects an absent mutation result without retry", async () => {
    let attempts = 0;
    const waits = [];
    const octokit = await octokitWithWorkflowFetch(async () => {
      attempts += 1;
      return jsonResponse({ data: payload });
    });
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", {
      maxAttempts: 3, sleep: async (milliseconds) => waits.push(milliseconds),
    });
    await assert.rejects(() => client.enableAutoMerge("PR_node_42", "SQUASH", "a".repeat(40)),
      /native auto-merge mutation result/);
    assert.equal(attempts, 1);
    assert.deepEqual(waits, []);
  });
}

for (const [name, nodeId, method, headOid] of [
  ["missing node ID", "", "SQUASH", "a".repeat(40)],
  ["merge method", "PR_node_42", "MERGE", "a".repeat(40)],
  ["rebase method", "PR_node_42", "REBASE", "a".repeat(40)],
  ["lowercase method", "PR_node_42", "squash", "a".repeat(40)],
  ["missing head", "PR_node_42", "SQUASH", undefined],
  ["short head", "PR_node_42", "SQUASH", "abc"],
  ["non-hex head", "PR_node_42", "SQUASH", "g".repeat(40)],
]) {
  test(`native auto-merge rejects ${name} before an API request`, async () => {
    let requests = 0;
    const octokit = await octokitWithWorkflowFetch(async () => {
      requests += 1;
      return jsonResponse({ data: { enablePullRequestAutoMerge: { clientMutationId: null } } });
    });
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra");

    await assert.rejects(() => client.enableAutoMerge(nodeId, method, headOid), TypeError);
    assert.equal(requests, 0);
  });
}

test("maps merge, workflow, and pull-request state with exact heads", async () => {
  const { octokit } = mockOctokit();
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  const pullRequest = await client.getPullRequest(42);
  assert.equal(pullRequest.nodeId, "PR_node_42");
  assert.equal(pullRequest.baseBranch, "main");
  assert.equal(pullRequest.headBranch, "feature");
  assert.deepEqual(await client.listOpenPullRequestNumbers(), [42, 44]);
  assert.deepEqual(await client.getMergeState(42), {
    number: 42,
    nodeId: "PR_node_42",
    repository: "nvidia/k8s-test-infra",
    state: "OPEN",
    draft: false,
    mergeability: "MERGEABLE",
    headOid: "a".repeat(40),
    baseBranch: "main",
    autoMergeMethod: null,
  });
  assert.equal(await client.getBranchProtection("main"), true);
  assert.equal((await client.listWorkflowRunsForHead("a".repeat(40), 42)).length, 1);
  assert.equal((await client.getWorkflowRun(701, "a".repeat(40), 42)).id, 701);
  await client.rerunFailedJobs(701);
});

for (const branch of ["bad branch", "feature/../other", 42]) {
  test(`live PR reader rejects invalid head branch ${JSON.stringify(branch)}`, async () => {
    const base = mockOctokit();
    const { data } = await base.octokit.rest.pulls.get({});
    data.head.ref = branch;
    const { octokit } = mockOctokit({ rest: { pulls: { get: async () => ({ data }) } } });
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

    await assert.rejects(() => client.getPullRequest(42), /live PR head branch/);
  });
}

test("live PR reader keeps an unavailable head branch absent", async () => {
  const base = mockOctokit();
  const { data } = await base.octokit.rest.pulls.get({});
  delete data.head.ref;
  const { octokit } = mockOctokit({ rest: { pulls: { get: async () => ({ data }) } } });
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  assert.equal(Object.hasOwn(await client.getPullRequest(42), "headBranch"), false);
});

for (const protectedBranch of [true, false]) {
  test(`reads branch protection ${protectedBranch} with a Contents-read token`, async () => {
    const requests = [];
    const octokit = await octokitWithWorkflowFetch(async (url) => {
      const request = new URL(url);
      requests.push(request.pathname);
      if (request.pathname !== "/repos/NVIDIA/k8s-test-infra/branches/main") {
        return new globalThis.Response(JSON.stringify({ message: "Resource not accessible by integration" }), {
          status: 403,
          headers: { "content-type": "application/json" },
        });
      }
      return jsonResponse({ name: "main", commit: { sha: "b".repeat(40) }, protected: protectedBranch });
    });
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

    assert.equal(await client.getBranchProtection("main"), protectedBranch);
    assert.deepEqual(requests, ["/repos/NVIDIA/k8s-test-infra/branches/main"]);
  });
}

for (const [name, data] of [
  ["missing response", undefined],
  ["null response", null],
  ["missing protection flag", { name: "main" }],
  ["null protection flag", { name: "main", protected: null }],
  ["string protection flag", { name: "main", protected: "false" }],
  ["numeric protection flag", { name: "main", protected: 0 }],
  ["missing branch name", { protected: false }],
  ["non-string branch name", { name: 1, protected: false }],
  ["wrong branch name", { name: "release-1.2", protected: false }],
  ["different branch name case", { name: "Main", protected: false }],
]) {
  test(`fails closed for branch protection with ${name}`, async () => {
    const { octokit } = mockOctokit({
      rest: { repos: { getBranch: async () => ({ data }) } },
    });
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

    await assert.rejects(() => client.getBranchProtection("main"), /branch/);
  });
}

for (const status of [401, 403, 404, 503]) {
  test(`fails closed when the branch endpoint returns ${status}`, async () => {
    const requests = [];
    const octokit = await octokitWithWorkflowFetch(async (url) => {
      requests.push(new URL(url).pathname);
      return new globalThis.Response(JSON.stringify({ message: "branch lookup failed" }), {
        status,
        headers: { "content-type": "application/json" },
      });
    });
    const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

    await assert.rejects(() => client.getBranchProtection("main"), {
      name: "GitHubClientError",
      operation: "getBranchProtection",
      status,
    });
    assert.deepEqual(requests, ["/repos/NVIDIA/k8s-test-infra/branches/main"]);
  });
}

test("ignores unrelated workflow runs before mapping their pull request identity", async () => {
  const base = mockOctokit({
    rest: {
      actions: {
        listWorkflowRunsForRepo: async (parameters) => {
          base.calls.push({ name: "listWorkflowRuns", parameters });
          return { data: [
            {
              id: 701,
              head_sha: "a".repeat(40),
              status: "completed",
              conclusion: "failure",
              path: ".github/workflows/automation-ci.yml@refs/heads/main",
              event: "pull_request",
              pull_requests: [{ number: 42 }],
              repository: { full_name: "NVIDIA/k8s-test-infra" },
            },
            {
              id: 702,
              head_sha: "a".repeat(40),
              status: "completed",
              conclusion: "failure",
              path: ".github/workflows/automation-ci.yml@refs/heads/main",
              event: "push",
              pull_requests: [],
              repository: { full_name: "NVIDIA/k8s-test-infra" },
            },
          ] };
        },
      },
    },
  });
  const client = createGitHubClient(base.octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  assert.equal((await client.listWorkflowRunsForHead("a".repeat(40), 42)).length, 1);
});

test("maps Octokit-normalized workflow pages for the exact head and pull request", async () => {
  const headOid = "a".repeat(40);
  const requests = [];
  const octokit = await octokitWithWorkflowFetch(async (url) => {
    const request = new URL(url);
    requests.push(request);
    return jsonResponse({
      total_count: 5,
      workflow_runs: [
        workflowRun(701),
        workflowRun(702, { headSha: "b".repeat(40) }),
        workflowRun(703, { pullRequests: [{ number: 43 }] }),
        workflowRun(704, { pullRequests: [] }),
        workflowRun(705, { pullRequests: [{ number: 42 }, { number: 44 }] }),
      ],
    });
  });
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  assert.deepEqual(await client.listWorkflowRunsForHead(headOid, 42), [{
    id: 701,
    headOid,
    status: "completed",
    conclusion: "failure",
    workflowPath: ".github/workflows/automation-ci.yml",
    workflowSourceRef: "refs/heads/main",
    event: "pull_request",
    prNumber: 42,
    repository: "nvidia/k8s-test-infra",
  }]);
  assert.equal(requests.length, 1);
  assert.equal(requests[0].pathname, "/repos/NVIDIA/k8s-test-infra/actions/runs");
  assert.equal(requests[0].searchParams.get("head_sha"), headOid);
  assert.equal(requests[0].searchParams.get("per_page"), "100");
});

test("returns an empty Octokit-normalized workflow collection", async () => {
  const octokit = await octokitWithWorkflowFetch(async () => jsonResponse({
    total_count: 0,
    workflow_runs: [],
  }));
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  assert.deepEqual(await client.listWorkflowRunsForHead("a".repeat(40), 42), []);
});

test("maps linked Octokit workflow pages", async () => {
  const headOid = "a".repeat(40);
  const requests = [];
  const octokit = await octokitWithWorkflowFetch(async (url) => {
    const request = new URL(url);
    requests.push(request);
    const page = request.searchParams.get("page") ?? "1";
    if (page === "1") {
      return jsonResponse({ total_count: 2, workflow_runs: [workflowRun(711)] }, {
        link: `<https://api.github.com/repos/NVIDIA/k8s-test-infra/actions/runs?head_sha=${headOid}&per_page=100&page=2>; rel="next"`,
      });
    }
    return jsonResponse({ total_count: 2, workflow_runs: [workflowRun(712)] });
  });
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  const runs = await client.listWorkflowRunsForHead(headOid, 42);

  assert.deepEqual(runs.map(({ id }) => id), [711, 712]);
  assert.deepEqual(requests.map((request) => request.searchParams.get("page")), [null, "2"]);
});

test("fails closed for a malformed Octokit workflow collection", async () => {
  const octokit = await octokitWithWorkflowFetch(async () => jsonResponse({
    total_count: 1,
    workflow_runs: {},
  }));
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  await assert.rejects(
    () => client.listWorkflowRunsForHead("a".repeat(40), 42),
    /listWorkflowRunsForHead failed: GitHub API request failed/,
  );
});

test("keeps the default 1000-item workflow collection bound", async () => {
  const requests = [];
  const octokit = await octokitWithWorkflowFetch(async (url) => {
    const request = new URL(url);
    requests.push(request);
    const page = Number(request.searchParams.get("page") ?? "1");
    const firstId = ((page - 1) * 100) + 1;
    const count = page === 11 ? 1 : 100;
    const headers = page < 11 ? {
      link: `<https://api.github.com/repos/NVIDIA/k8s-test-infra/actions/runs?head_sha=${"a".repeat(40)}&per_page=100&page=${page + 1}>; rel="next"`,
    } : {};
    return jsonResponse({
      total_count: 1001,
      workflow_runs: Array.from({ length: count }, (_, offset) => workflowRun(firstId + offset)),
    }, headers);
  });
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  await assert.rejects(
    () => client.listWorkflowRunsForHead("a".repeat(40), 42),
    /listWorkflowRunsForHead result must not exceed 1000 items/,
  );
  assert.equal(requests.length, 11);
  assert.equal(requests.every((request) => request.searchParams.get("per_page") === "100"), true);
});

function ciWorkflowRun(id, path, options = {}) {
  return {
    ...workflowRun(id, options),
    path: `${path}@${options.sourceRef ?? "refs/pull/42/merge"}`,
    status: options.status ?? "completed",
    conclusion: options.conclusion === undefined ? "success" : options.conclusion,
    run_number: options.runNumber ?? id,
    run_attempt: options.runAttempt ?? 1,
  };
}

function ciCheckRun(id, options = {}) {
  return {
    id,
    name: options.name ?? "DCO",
    app: { id: options.appId ?? 1861 },
    head_sha: options.headSha ?? "a".repeat(40),
    status: options.status ?? "completed",
    conclusion: options.conclusion === undefined ? "success" : options.conclusion,
  };
}

const CI_INPUT = {
  headOid: "a".repeat(40), prNumber: 42, baseBranch: "main", files: [{ path: "pkg/code.go" }],
  requiredCI: loadConfig(path.resolve(__dirname, "../../../..")).policy.merge.requiredCI,
};

const FORK_CI_INPUT = {
  ...CI_INPUT, headRepository: "ArangoGutierrez/k8s-test-infra", headBranch: "codex/v016-label-scan-dispatch",
};

function forkCIWorkflowRun(id, path) {
  return {
    id,
    head_sha: "a".repeat(40),
    head_branch: "codex/v016-label-scan-dispatch",
    head_repository: { full_name: "ArangoGutierrez/k8s-test-infra" },
    repository: { full_name: "NVIDIA/k8s-test-infra" },
    event: "pull_request",
    path,
    pull_requests: [],
    status: "completed",
    conclusion: "success",
    run_number: id,
    run_attempt: 1,
  };
}

async function ciTransport(runPages, checkPages = [{ total_count: 1, check_runs: [ciCheckRun(801)] }]) {
  const requests = [];
  const octokit = await octokitWithWorkflowFetch(async (url) => {
    const request = new URL(url);
    requests.push(request);
    const page = Number(request.searchParams.get("page") ?? "1");
    const pages = request.pathname.endsWith("/actions/runs") ? runPages
      : request.pathname === `/repos/NVIDIA/k8s-test-infra/commits/${"a".repeat(40)}/check-runs`
        ? checkPages : null;
    assert.notEqual(pages, null, "CI must use workflow and check-run APIs");
    assert.ok(pages[page - 1], "unexpected extra page request");
    const headers = page < pages.length ? {
      link: `<${request.origin}${request.pathname}?${new globalThis.URLSearchParams({
        ...Object.fromEntries(request.searchParams), page: String(page + 1),
      })}>; rel="next"`,
    } : {};
    return jsonResponse(pages[page - 1], headers);
  });
  return { requests, client: createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 }) };
}

test("CI reader joins complete exact-head workflow and check-run pages through Octokit", async () => {
  const { client, requests } = await ciTransport([
    { total_count: 2, workflow_runs: [ciWorkflowRun(701, ".github/workflows/basic-checks.yaml")] },
    { total_count: 2, workflow_runs: [ciWorkflowRun(702, ".github/workflows/validate-changelog.yaml")] },
  ], [
    { total_count: 2, check_runs: [ciCheckRun(800, {
      name: "repository-automation/merge-policy", appId: 15368, conclusion: "action_required",
    })] },
    { total_count: 2, check_runs: [ciCheckRun(801)] },
  ]);

  assert.equal(await client.getCIState(CI_INPUT), "SUCCESS");
  assert.deepEqual(requests.map(({ pathname }) => pathname), [
    "/repos/NVIDIA/k8s-test-infra/actions/runs",
    "/repos/NVIDIA/k8s-test-infra/actions/runs",
    `/repos/NVIDIA/k8s-test-infra/commits/${"a".repeat(40)}/check-runs`,
    `/repos/NVIDIA/k8s-test-infra/commits/${"a".repeat(40)}/check-runs`,
  ]);
  assert.deepEqual(requests.map((request) => request.searchParams.get("page")), [null, "2", null, "2"]);
  assert.equal(requests.every((request) => request.searchParams.get("per_page") === "100"), true);
  assert.equal(requests.slice(0, 2).every((request) => request.searchParams.get("head_sha") === "a".repeat(40)), true);
  assert.equal(requests.slice(2).every((request) => request.searchParams.get("filter") === "all"), true);
});

test("CI reader evaluates the required CI definition the caller passes", async () => {
  const runs = [{ total_count: 1, workflow_runs: [ciWorkflowRun(701, ".github/workflows/basic-checks.yaml")] }];
  const onlyBasic = {
    workflows: [{ path: ".github/workflows/basic-checks.yaml" }], checks: [{ name: "DCO", appId: 1861 }],
  };
  assert.equal(await (await ciTransport(runs)).client.getCIState(CI_INPUT), "PENDING");
  assert.equal(await (await ciTransport(runs)).client.getCIState({ ...CI_INPUT, requiredCI: onlyBasic }), "SUCCESS");
});

test("CI reader cannot treat its own successful policy check as source CI", async () => {
  const { client } = await ciTransport([{ total_count: 0, workflow_runs: [] }], [{
    total_count: 1, check_runs: [ciCheckRun(800, { name: "repository-automation/merge-policy", appId: 15368 })],
  }]);
  assert.equal(await client.getCIState(CI_INPUT), "PENDING");
});

test("CI reader observes a later-page DCO failure", async () => {
  const { client } = await ciTransport([{ total_count: 2, workflow_runs: [
    ciWorkflowRun(701, ".github/workflows/basic-checks.yaml"),
    ciWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
  ] }], [
    { total_count: 2, check_runs: [ciCheckRun(801)] },
    { total_count: 2, check_runs: [ciCheckRun(802, { conclusion: "failure" })] },
  ]);
  assert.equal(await client.getCIState(CI_INPUT), "FAILED");
});

test("CI reader selects the latest workflow run number before the run ID", async () => {
  const { client } = await ciTransport([{ total_count: 3, workflow_runs: [
    ciWorkflowRun(701, ".github/workflows/basic-checks.yaml", { runNumber: 2, conclusion: "failure" }),
    ciWorkflowRun(999, ".github/workflows/basic-checks.yaml", { runNumber: 1 }),
    ciWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
  ] }]);
  assert.equal(await client.getCIState(CI_INPUT), "FAILED");
});

test("CI reader maps the current rerun attempt as pending", async () => {
  const { client } = await ciTransport([{ total_count: 2, workflow_runs: [
    ciWorkflowRun(701, ".github/workflows/basic-checks.yaml", { runAttempt: 2, status: "in_progress", conclusion: null }),
    ciWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
  ] }]);
  assert.equal(await client.getCIState(CI_INPUT), "PENDING");
});

for (const [name, options] of [
  ["old head", { headSha: "b".repeat(40) }],
  ["other PR", { pullRequests: [{ number: 43 }], sourceRef: "refs/pull/43/merge" }],
  ["wrong PR ref with an empty fork mapping", { pullRequests: [], sourceRef: "refs/pull/43/merge" }],
  ["uncertain empty PR mapping", { pullRequests: [], sourceRef: "refs/heads/feature" }],
]) {
  test(`CI reader keeps source evidence for ${name} pending`, async () => {
    const { client } = await ciTransport([{ total_count: 2, workflow_runs: [
      ciWorkflowRun(701, ".github/workflows/basic-checks.yaml", options),
      ciWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
    ] }]);
    assert.equal(await client.getCIState(CI_INPUT), "PENDING");
  });
}

test("CI reader binds an exact-head fork run by its exact pull-request merge ref", async () => {
  const { client } = await ciTransport([{ total_count: 2, workflow_runs: [
    ciWorkflowRun(701, ".github/workflows/basic-checks.yaml", { pullRequests: [] }),
    ciWorkflowRun(702, ".github/workflows/validate-changelog.yaml", { pullRequests: [] }),
  ] }]);
  assert.equal(await client.getCIState(CI_INPUT), "SUCCESS");
});

test("CI reader accepts plain-path fork source runs from the exact current head repository and branch", async () => {
  const { client, requests } = await ciTransport([{ total_count: 2, workflow_runs: [
    forkCIWorkflowRun(701, ".github/workflows/basic-checks.yaml"),
    forkCIWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
  ] }]);

  assert.equal(await client.getCIState(FORK_CI_INPUT), "SUCCESS");
  assert.equal(requests[0].searchParams.get("head_sha"), FORK_CI_INPUT.headOid);
});

test("CI reader observes failure in current-head plain-path fork source evidence", async () => {
  const basic = forkCIWorkflowRun(701, ".github/workflows/basic-checks.yaml");
  basic.conclusion = "failure";
  const { client } = await ciTransport([{ total_count: 2, workflow_runs: [
    basic, forkCIWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
  ] }]);

  assert.equal(await client.getCIState(FORK_CI_INPUT), "FAILED");
});

for (const [name, changes] of [
  ["wrong head repository", { head_repository: { full_name: "Other/k8s-test-infra" } }],
  ["wrong head branch", { head_branch: "other-feature" }],
  ["missing head repository", { head_repository: undefined }],
  ["missing head branch", { head_branch: undefined }],
  ["wrong event", { event: "push" }],
  ["old head", { head_sha: "b".repeat(40) }],
  ["wrong target repository", { repository: { full_name: "Other/k8s-test-infra" } }],
  ["explicit wrong PR", { pull_requests: [{ number: 43 }] }],
  ["explicit wrong merge ref", { path: ".github/workflows/basic-checks.yaml@refs/pull/43/merge" }],
]) {
  test(`CI reader keeps a plain-path fork with ${name} pending`, async () => {
    const { client } = await ciTransport([{ total_count: 2, workflow_runs: [
      { ...forkCIWorkflowRun(701, ".github/workflows/basic-checks.yaml"), ...changes },
      forkCIWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
    ] }]);

    assert.equal(await client.getCIState(FORK_CI_INPUT), "PENDING");
  });
}

for (const field of ["headRepository", "headBranch"]) {
  test(`CI reader keeps a plain-path fork pending when current ${field} is unavailable`, async () => {
    const { client } = await ciTransport([{ total_count: 2, workflow_runs: [
      forkCIWorkflowRun(701, ".github/workflows/basic-checks.yaml"),
      forkCIWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
    ] }]);

    assert.equal(await client.getCIState({ ...FORK_CI_INPUT, [field]: undefined }), "PENDING");
  });
}

for (const [name, changes] of [
  ["malformed head repository", { head_repository: { full_name: "invalid repository" } }],
  ["malformed head branch", { head_branch: "bad branch" }],
]) {
  test(`CI reader rejects a plain-path fork with ${name}`, async () => {
    const { client } = await ciTransport([{ total_count: 2, workflow_runs: [
      { ...forkCIWorkflowRun(701, ".github/workflows/basic-checks.yaml"), ...changes },
      forkCIWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
    ] }]);

    await assert.rejects(() => client.getCIState(FORK_CI_INPUT), /CI (?:head repository|head branch)/);
  });
}

for (const collection of ["workflows", "checks"]) {
  for (const [name, total, metadata] of [
    ["missing total", undefined, {}],
    ["missing final page", 3, {}],
    ["count above the collection bound", 1001, {}],
    ["incomplete API results", 2, { incomplete_results: true }],
  ]) {
    test(`CI reader rejects ${collection} with ${name} instead of accepting partial success`, async () => {
      const workflowPage = { total_count: 2, workflow_runs: [
        ciWorkflowRun(701, ".github/workflows/basic-checks.yaml"),
        ciWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
      ] };
      const checkPage = { total_count: 2, check_runs: [ciCheckRun(801), ciCheckRun(800, {
        name: "repository-automation/merge-policy", appId: 15368,
      })] };
      Object.assign(collection === "workflows" ? workflowPage : checkPage, { total_count: total, ...metadata });
      const { client } = await ciTransport([workflowPage], [checkPage]);
      await assert.rejects(() => client.getCIState(CI_INPUT), /(?:CI|collection|listCI)/);
    });
  }
}

for (const [name, field, value] of [
  ["missing workflow run number", "run_number", undefined],
  ["invalid workflow run number", "run_number", 0],
  ["missing current attempt", "run_attempt", undefined],
  ["invalid current attempt", "run_attempt", "2"],
]) {
  test(`CI reader rejects ${name} without weakening existing retest mapping`, async () => {
    const basic = ciWorkflowRun(701, ".github/workflows/basic-checks.yaml");
    basic[field] = value;
    const { client } = await ciTransport([{ total_count: 2, workflow_runs: [
      basic, ciWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
    ] }]);
    await assert.rejects(() => client.getCIState(CI_INPUT), /workflow (?:run number|run attempt)/);
  });
}

test("CI reader rejects duplicate workflow identities across complete pages", async () => {
  const { client } = await ciTransport([
    { total_count: 3, workflow_runs: [ciWorkflowRun(701, ".github/workflows/basic-checks.yaml")] },
    { total_count: 3, workflow_runs: [
      ciWorkflowRun(701, ".github/workflows/basic-checks.yaml"),
      ciWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
    ] },
  ]);
  await assert.rejects(() => client.getCIState(CI_INPUT), /(?:CI|duplicate|listCI)/);
});

test("CI reader rejects a changing collection count", async () => {
  const { client } = await ciTransport([
    { total_count: 2, workflow_runs: [ciWorkflowRun(701, ".github/workflows/basic-checks.yaml")] },
    { total_count: 3, workflow_runs: [ciWorkflowRun(702, ".github/workflows/validate-changelog.yaml")] },
  ]);
  await assert.rejects(() => client.getCIState(CI_INPUT), /(?:CI|collection|listCI)/);
});

test("CI reader rejects a duplicate check run instead of counting it twice", async () => {
  const { client } = await ciTransport([{ total_count: 2, workflow_runs: [
    ciWorkflowRun(701, ".github/workflows/basic-checks.yaml"),
    ciWorkflowRun(702, ".github/workflows/validate-changelog.yaml"),
  ] }], [{ total_count: 2, check_runs: [ciCheckRun(801), ciCheckRun(801)] }]);
  await assert.rejects(() => client.getCIState(CI_INPUT), /(?:CI|duplicate|listCI)/);
});

test("CI reader stops a pagination chain at the bounded tenth page", async () => {
  const { client, requests } = await ciTransport(Array.from({ length: 11 }, (_, index) => ({
    total_count: 11,
    workflow_runs: [ciWorkflowRun(701 + index, ".github/workflows/basic-checks.yaml")],
  })));
  await assert.rejects(() => client.getCIState(CI_INPUT), /(?:CI|collection|listCI)/);
  assert.equal(requests.length, 10);
});

test("CI file mapping preserves the old path of a renamed automation file", async () => {
  const { octokit } = mockOctokit({ rest: { pulls: { listFiles: async () => ({ data: [{
    filename: "pkg/new.js", previous_filename: ".github/actions/repo-automation/src/old.js",
    additions: 1, deletions: 1, status: "renamed",
  }, { filename: "pkg/code.go", additions: 2, deletions: 0, status: "modified" }] }) } } });
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
  assert.deepEqual(await client.listPullRequestFiles(42), [{
    path: "pkg/new.js", previousPath: ".github/actions/repo-automation/src/old.js",
    additions: 1, deletions: 1, status: "renamed",
  }, { path: "pkg/code.go", additions: 2, deletions: 0, status: "modified" }]);
});

test("refetches only a bounded evaluator workflow identity", async () => {
  let workflowPath = ".github/workflows/pr-metadata.yml@refs/heads/main";
  const base = mockOctokit({
    rest: {
      actions: {
        getWorkflowRun: async (parameters) => {
          base.calls.push({ name: "getEvaluationWorkflowRun", parameters });
          return { data: {
            id: 702,
            name: "PR metadata",
            path: workflowPath,
            event: "pull_request_target",
            status: "completed",
            pull_requests: [{ number: 42 }],
            repository: { full_name: "NVIDIA/k8s-test-infra" },
          } };
        },
      },
    },
  });
  const client = createGitHubClient(base.octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  assert.deepEqual(await client.getEvaluationWorkflowRun(702), {
    id: 702,
    name: "PR metadata",
    workflowPath: ".github/workflows/pr-metadata.yml",
    workflowSourceRef: "refs/heads/main",
    event: "pull_request_target",
    status: "completed",
    repository: "nvidia/k8s-test-infra",
    pullRequestNumbers: [42],
  });

  workflowPath = ".github/workflows/pr-metadata.yml@refs/heads/main@spoof";
  assert.equal(await client.getEvaluationWorkflowRun(702), null);
});

test("accepts exact evaluator workflow paths returned by the live REST API", async () => {
  let workflowPath = ".github/workflows/review-observer.yml";
  const { octokit } = mockOctokit({ rest: { actions: {
    getWorkflowRun: async () => ({ data: {
      id: 702,
      name: "Review observer",
      path: workflowPath,
      event: "pull_request_review",
      status: "completed",
      pull_requests: [{ number: 42 }],
      repository: { full_name: "NVIDIA/k8s-test-infra" },
    } }),
  } } });
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });
  assert.deepEqual(await client.getEvaluationWorkflowRun(702), {
    id: 702,
    name: "Review observer",
    workflowPath: ".github/workflows/review-observer.yml",
    workflowSourceRef: null,
    event: "pull_request_review",
    status: "completed",
    repository: "nvidia/k8s-test-infra",
    pullRequestNumbers: [42],
  });
  for (const path of [
    ".github/workflows/untrusted.yml",
    "../.github/workflows/review-observer.yml",
    ".github/workflows/review-observer.yml@",
    ".github/workflows/review-observer.yml@refs/heads/main@spoof",
    ".github/workflows/review-observer.yml@refs/heads/../main",
    ".github/workflows/review-observer.yml\n",
  ]) {
    workflowPath = path;
    assert.equal(await client.getEvaluationWorkflowRun(702), null, path);
  }
});

test("exposes the bounded branch operation", async () => {
  const base = mockOctokit();
  const client = createGitHubClient(base.octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  assert.deepEqual(await client.getBranch("release-1.2"), {
    name: "release-1.2",
    oid: "b".repeat(40),
  });
});
