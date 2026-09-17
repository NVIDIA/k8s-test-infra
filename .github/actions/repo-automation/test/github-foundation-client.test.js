"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { createGitHubClient } = require("../src/github-client.js");

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
      getBranch: response("getBranch", { name: "release-1.2", commit: { sha: "b".repeat(40) } }),
    },
    actions: {
      listWorkflowRunsForRepo: response("listWorkflowRuns", { workflow_runs: [{
        id: 701,
        head_sha: "a".repeat(40),
        status: "completed",
        conclusion: "failure",
        path: ".github/workflows/automation-ci.yml@refs/heads/main",
        event: "pull_request",
        pull_requests: [{ number: 42 }],
        repository: { full_name: "NVIDIA/k8s-test-infra" },
      }] }),
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
  });
});

test("exposes only managed policy labels and native auto-merge mutations", async () => {
  const { octokit, calls } = mockOctokit();
  const client = createGitHubClient(octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  await client.addPolicyLabel(42, "lgtm");
  await client.removePolicyLabel(42, "do-not-merge/hold");
  await assert.rejects(() => client.addPolicyLabel(42, "kind/feature"), /policy-managed/);
  await client.setMergePolicyCheck(42, "a".repeat(40), "success", "all gates passed");
  await client.enableAutoMerge("PR_node_42", "SQUASH");
  await client.disableAutoMerge("PR_node_42");

  assert.equal(typeof client.mergePullRequest, "undefined");
  assert.equal(calls.some(({ name }) => name === "createCheck"), true);
  assert.equal(calls.filter(({ name }) => name === "graphql").length, 2);
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

test("ignores unrelated workflow runs before mapping their pull request identity", async () => {
  const base = mockOctokit({
    rest: {
      actions: {
        listWorkflowRunsForRepo: async (parameters) => {
          base.calls.push({ name: "listWorkflowRuns", parameters });
          return { data: { workflow_runs: [
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
          ] } };
        },
      },
    },
  });
  const client = createGitHubClient(base.octokit, "NVIDIA", "k8s-test-infra", { maxAttempts: 1 });

  assert.equal((await client.listWorkflowRunsForHead("a".repeat(40), 42)).length, 1);
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
  const base = mockOctokit({
    rest: {
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
