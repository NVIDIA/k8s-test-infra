"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const { URL } = require("node:url");

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
  assert.equal(typeof client.enableAutoMerge, "undefined");
  assert.equal(calls.some(({ name }) => name === "createCheck"), true);
  assert.equal(calls.filter(({ name }) => name === "graphql").length, 1);
});

test("maps merge, workflow, and pull-request state with exact heads", async () => {
  const { octokit } = mockOctokit();
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  const pullRequest = await client.getPullRequest(42);
  assert.equal(pullRequest.nodeId, "PR_node_42");
  assert.equal(pullRequest.baseBranch, "main");
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

test("exposes bounded branch and backport pull-request operations", async () => {
  const existingPullRequest = {
    number: 900,
    html_url: "https://github.com/NVIDIA/k8s-test-infra/pull/900",
    state: "open",
    base: { ref: "release-1.2" },
    head: { ref: "backport/42" },
    title: "[release-1.2] feat: foundation",
    body: "bound evidence",
  };
  const base = mockOctokit();
  base.octokit.rest.pulls.list = async (parameters) => {
    base.calls.push({ name: "listBackportPullRequests", parameters });
    return { data: [existingPullRequest] };
  };
  const client = createGitHubClient(base.octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  assert.deepEqual(await client.getBranch("release-1.2"), {
    name: "release-1.2",
    oid: "b".repeat(40),
  });
  assert.deepEqual(await client.findOpenBackportPullRequest("backport/42", "release-1.2"), {
    number: 900,
    url: existingPullRequest.html_url,
    state: "open",
    base: "release-1.2",
    head: "backport/42",
    title: existingPullRequest.title,
    body: existingPullRequest.body,
  });
  assert.deepEqual(await client.createBackportPullRequest({
    base: "release-1.2",
    head: "backport/42",
    title: existingPullRequest.title,
    body: existingPullRequest.body,
  }), {
    number: 900,
    url: existingPullRequest.html_url,
  });
  assert.deepEqual(
    base.calls.find(({ name }) => name === "listBackportPullRequests").parameters,
    {
      owner: "NVIDIA",
      repo: "k8s-test-infra",
      state: "open",
      head: "NVIDIA:backport/42",
      base: "release-1.2",
      per_page: 100,
    },
  );
});

test("maps bounded Mokka commit and draft pull-request operations", async () => {
  const branch = "mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000";
  const headOid = "c".repeat(40);
  const treeOid = "d".repeat(40);
  const parentOid = "e".repeat(40);
  const pullRequest = {
    number: 901,
    html_url: "https://github.com/NVIDIA/k8s-test-infra/pull/901",
    state: "open",
    draft: true,
    base: { ref: "main" },
    head: { ref: branch, sha: headOid },
    title: "Mokka: cherry-pick #42 to main",
    body: "bound evidence",
  };
  const mokkaMessage = "cherry pick with evidence\n\nMokka-Source-SHA: " + "a".repeat(40) + "\n";
  const base = mockOctokit({
    rest: {
      git: {
        getCommit: async (parameters) => {
          base.calls.push({ name: "getMokkaCommit", parameters });
          return { data: {
            sha: headOid,
            message: mokkaMessage,
            tree: { sha: treeOid },
            parents: [{ sha: parentOid }],
            verification: { verified: true, signature: "signed-payload" },
          } };
        },
        createCommit: async (parameters) => {
          base.calls.push({ name: "createMokkaCommit", parameters });
          return { data: {
            sha: headOid,
            message: parameters.message,
            tree: { sha: treeOid },
            parents: [{ sha: parentOid }],
            verification: { verified: true, reason: "valid", signature: "signed-payload" },
          } };
        },
        createRef: async (parameters) => {
          base.calls.push({ name: "createMokkaRef", parameters });
          return { data: { ref: parameters.ref, object: { sha: parameters.sha } } };
        },
      },
      repos: {
        getCommit: async (parameters) => {
          base.calls.push({ name: "getCommit", parameters });
          return { data: { sha: "a".repeat(40), parents: [{ sha: "b".repeat(40) }] } };
        },
      },
      pulls: {
        list: async (parameters) => {
          base.calls.push({ name: "findMokkaPullRequests", parameters });
          return { data: [pullRequest] };
        },
        create: async (parameters) => {
          base.calls.push({ name: "createMokkaPullRequest", parameters });
          return { data: pullRequest };
        },
        update: async (parameters) => {
          base.calls.push({ name: "updateMokkaPullRequest", parameters });
          return { data: { ...pullRequest, body: parameters.body } };
        },
      },
    },
  });
  const client = createGitHubClient(base.octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  assert.deepEqual(await client.getCommit("a".repeat(40)), {
    sha: "a".repeat(40),
    parents: ["b".repeat(40)],
  });
  assert.deepEqual(await client.getMokkaCommit(headOid), {
    sha: headOid,
    message: mokkaMessage,
    tree: treeOid,
    parents: [parentOid],
    verification: { verified: true, hasSignature: true },
  });
  assert.deepEqual(await client.createMokkaCommit({
    message: "cherry pick with evidence",
    tree: treeOid,
    parents: [parentOid],
  }), {
    sha: headOid,
    message: "cherry pick with evidence",
    tree: treeOid,
    parents: [parentOid],
    verification: { verified: true, hasSignature: true },
  });
  assert.deepEqual(await client.createMokkaRef(branch, headOid), { name: branch, oid: headOid });
  assert.deepEqual(await client.findMokkaPullRequests(branch, "main"), [{
    number: 901,
    url: pullRequest.html_url,
    state: "open",
    draft: true,
    base: "main",
    head: branch,
    headOid,
    title: pullRequest.title,
    body: pullRequest.body,
  }]);
  assert.equal((await client.findMokkaPullRequests(branch)).length, 1);
  assert.deepEqual(await client.createMokkaPullRequest({
    base: "main",
    head: branch,
    title: pullRequest.title,
    body: "bound evidence",
    draft: true,
  }), {
    number: 901,
    url: pullRequest.html_url,
    state: "open",
    draft: true,
    base: "main",
    head: branch,
    headOid,
    title: pullRequest.title,
    body: pullRequest.body,
  });
  await client.updateMokkaPullRequestBody(901, "new evidence");

  assert.deepEqual(
    base.calls.find(({ name }) => name === "createMokkaCommit").parameters,
    {
      owner: "NVIDIA",
      repo: "k8s-test-infra",
      message: "cherry pick with evidence",
      tree: treeOid,
      parents: [parentOid],
    },
  );
  assert.deepEqual(
    base.calls.find(({ name }) => name === "createMokkaRef").parameters,
    { owner: "NVIDIA", repo: "k8s-test-infra", ref: `refs/heads/${branch}`, sha: headOid },
  );
  assert.deepEqual(
    base.calls.find(({ name }) => name === "findMokkaPullRequests").parameters,
    {
      owner: "NVIDIA",
      repo: "k8s-test-infra",
      state: "all",
      head: `NVIDIA:${branch}`,
      base: "main",
      per_page: 100,
    },
  );
  assert.deepEqual(
    base.calls.filter(({ name }) => name === "findMokkaPullRequests")[1].parameters,
    {
      owner: "NVIDIA",
      repo: "k8s-test-infra",
      state: "all",
      head: `NVIDIA:${branch}`,
      per_page: 100,
    },
  );
  assert.deepEqual(
    base.calls.find(({ name }) => name === "createMokkaPullRequest").parameters,
    {
      owner: "NVIDIA",
      repo: "k8s-test-infra",
      base: "main",
      head: branch,
      title: pullRequest.title,
      body: "bound evidence",
      draft: true,
    },
  );
  assert.deepEqual(
    base.calls.find(({ name }) => name === "updateMokkaPullRequest").parameters,
    { owner: "NVIDIA", repo: "k8s-test-infra", pull_number: 901, body: "new evidence" },
  );
});
