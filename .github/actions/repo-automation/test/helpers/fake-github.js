"use strict";

function copyLabel(label) {
  return {
    name: label.name,
    color: label.color,
    description: label.description,
  };
}

function clone(value) {
  return value === undefined ? undefined : JSON.parse(JSON.stringify(value));
}

function normalizedOptions(initialState) {
  if (Array.isArray(initialState)) {
    return { labels: initialState };
  }
  return initialState ?? {};
}

function createFakeGitHub(initialState = []) {
  const options = normalizedOptions(initialState);
  const labels = (options.labels ?? []).map((label) => (
    Object.hasOwn(label, "color") ? copyLabel(label) : { name: label.name ?? label }
  ));
  const comments = clone(options.comments ?? []);
  const issueComments = new Map(
    (options.issueComments ?? []).map((comment) => [comment.id, clone(comment)]),
  );
  const users = clone(options.users ?? {});
  const collaboratorAccess = clone(options.collaboratorAccess ?? {});
  const workflowRuns = clone(options.workflowRuns ?? []);
  const evaluationWorkflowRuns = clone(options.evaluationWorkflowRuns ?? []);
  const mergeStates = clone(options.mergeStates ?? []);
  const branches = clone(options.branches ?? {});
  const backportPullRequests = clone(options.backportPullRequests ?? []);
  const mokkaPullRequests = clone(options.mokkaPullRequests ?? []);
  const commits = clone(options.commitsBySha ?? {});
  const automationLogin = options.automationLogin ?? "github-actions[bot]";
  for (const comment of comments) {
    if (comment.author === undefined) comment.author = automationLogin;
  }
  const pullRequests = clone(options.pullRequests ?? [options.pullRequest]);
  const requestedReviewers = [...(options.requestedReviewers ?? [])];
  const failureQueues = new Map(
    Object.entries(options.failures ?? {}).map(([operation, failures]) => [
      operation,
      Array.isArray(failures) ? [...failures] : [failures],
    ]),
  );
  const calls = {
    listLabels: [],
    createLabel: [],
    updateLabel: [],
    getPullRequest: [],
    getCommit: [],
    listPullRequestFiles: [],
    listPullRequestCommits: [],
    listPullRequestReviews: [],
    getPullRequestReview: [],
    getIssueComment: [],
    getUserIdentity: [],
    getCollaboratorAccess: [],
    listRequestedReviewers: [],
    listIssueLabels: [],
    getContentAtDefaultBranch: [],
    getDefaultBranchRevision: [],
    getContentAtRevision: [],
    requestReviewers: [],
    addIssueLabel: [],
    removeIssueLabel: [],
    addPolicyLabel: [],
    removePolicyLabel: [],
    listWorkflowRunsForHead: [],
    getWorkflowRun: [],
    getEvaluationWorkflowRun: [],
    rerunFailedJobs: [],
    listOpenPullRequestNumbers: [],
    getMergeState: [],
    getBranchProtection: [],
    getBranch: [],
    findMokkaPullRequests: [],
    createMokkaPullRequest: [],
    updateMokkaPullRequestBody: [],
    findOpenBackportPullRequest: [],
    createBackportPullRequest: [],
    setMergePolicyCheck: [],
    enableAutoMerge: [],
    disableAutoMerge: [],
    getPolicyComment: [],
    planPolicyComment: [],
    upsertPolicyComment: [],
  };
  const callOrder = [];

  function record(operation, parameters) {
    const copied = clone(parameters);
    calls[operation].push(copied);
    callOrder.push({ operation, parameters: copied });
    const failures = failureQueues.get(operation);
    if (failures?.length > 0) {
      const failure = failures.shift();
      if (failure !== null && failure !== undefined) {
        throw failure;
      }
    }
  }

  function commentMarkerCount(body, marker) {
    return body.split(marker).length - 1;
  }

  return {
    calls,
    callOrder,

    async listLabels() {
      record("listLabels", {});
      return clone(labels);
    },

    async createLabel(label) {
      const requested = copyLabel(label);
      record("createLabel", requested);
      labels.push(requested);
      return copyLabel(requested);
    },

    async updateLabel(label) {
      const requested = copyLabel(label);
      record("updateLabel", requested);
      const index = labels.findIndex(
        (existing) => existing.name.toLowerCase() === requested.name.toLowerCase(),
      );
      if (index === -1) {
        throw new Error(`cannot update missing label: ${requested.name}`);
      }
      labels[index] = requested;
      return copyLabel(requested);
    },

    async getPullRequest(prNumber) {
      record("getPullRequest", { prNumber });
      const index = Math.min(calls.getPullRequest.length - 1, pullRequests.length - 1);
      return clone(pullRequests[index]);
    },

    async getCommit(sha) {
      record("getCommit", { sha });
      if (!Object.hasOwn(commits, sha)) throw new Error(`missing commit: ${sha}`);
      return clone(commits[sha]);
    },

    async listPullRequestFiles(prNumber) {
      record("listPullRequestFiles", { prNumber });
      return clone((options.filePages ?? [options.files ?? []]).flat());
    },

    async listPullRequestCommits(prNumber) {
      record("listPullRequestCommits", { prNumber });
      return clone((options.commitPages ?? [options.commits ?? []]).flat());
    },

    async listPullRequestReviews(prNumber) {
      record("listPullRequestReviews", { prNumber });
      return clone((options.reviewPages ?? [options.reviews ?? []]).flat());
    },

    async getPullRequestReview(prNumber, reviewId) {
      record("getPullRequestReview", { prNumber, reviewId });
      const review = (options.reviewPages ?? [options.reviews ?? []])
        .flat()
        .find((candidate) => candidate.id === reviewId);
      if (review === undefined) throw new Error(`missing pull request review: ${reviewId}`);
      return clone(review);
    },

    async getIssueComment(commentId) {
      record("getIssueComment", { commentId });
      const comment = issueComments.get(commentId);
      if (comment === undefined) throw new Error(`missing issue comment: ${commentId}`);
      return clone(comment);
    },

    async getUserIdentity(login) {
      record("getUserIdentity", { login });
      return clone(users[login] ?? {
        login,
        type: "User",
        resolved: true,
        deleted: false,
      });
    },

    async getCollaboratorAccess(login) {
      record("getCollaboratorAccess", { login });
      return clone(collaboratorAccess[login] ?? {
        liveCollaborator: false,
        permission: "none",
      });
    },

    async listRequestedReviewers(prNumber) {
      record("listRequestedReviewers", { prNumber });
      return [...requestedReviewers];
    },

    async listIssueLabels(prNumber) {
      record("listIssueLabels", { prNumber });
      return labels.map(({ name }) => name);
    },

    async getContentAtDefaultBranch(path) {
      record("getContentAtDefaultBranch", { path });
      if (!Object.hasOwn(options.contents ?? {}, path)) {
        throw new Error(`missing default-branch content: ${path}`);
      }
      return options.contents[path];
    },

    async getDefaultBranchRevision() {
      record("getDefaultBranchRevision", {});
      return options.defaultBranchRevision ?? "base-commit-oid-91ab";
    },

    async getContentAtRevision(path, revision) {
      record("getContentAtRevision", { path, revision });
      if (!Object.hasOwn(options.contents ?? {}, path)) {
        throw new Error(`missing revision content: ${path}`);
      }
      return options.contents[path];
    },

    async requestReviewers(prNumber, reviewers) {
      record("requestReviewers", { prNumber, reviewers: [...reviewers] });
      for (const reviewer of reviewers) {
        if (!requestedReviewers.includes(reviewer)) {
          requestedReviewers.push(reviewer);
        }
      }
    },

    async addIssueLabel(prNumber, label) {
      record("addIssueLabel", { prNumber, label });
      if (!labels.some((entry) => entry.name === label)) {
        labels.push({ name: label });
      }
    },

    async removeIssueLabel(prNumber, label) {
      record("removeIssueLabel", { prNumber, label });
      const index = labels.findIndex((entry) => entry.name === label);
      if (index !== -1) {
        labels.splice(index, 1);
      }
    },

    async addPolicyLabel(prNumber, label) {
      record("addPolicyLabel", { prNumber, label });
      if (!labels.some((entry) => entry.name === label)) labels.push({ name: label });
    },

    async removePolicyLabel(prNumber, label) {
      record("removePolicyLabel", { prNumber, label });
      const index = labels.findIndex((entry) => entry.name === label);
      if (index !== -1) labels.splice(index, 1);
    },

    async listWorkflowRunsForHead(headOid, prNumber) {
      record("listWorkflowRunsForHead", { headOid, prNumber });
      return clone(workflowRuns.filter((run) => run.headOid === headOid && run.prNumber === prNumber));
    },

    async getWorkflowRun(runId, headOid, prNumber) {
      record("getWorkflowRun", { runId, headOid, prNumber });
      const run = workflowRuns.find((candidate) => (
        candidate.id === runId && candidate.headOid === headOid && candidate.prNumber === prNumber
      ));
      if (run === undefined) throw new Error(`missing workflow run: ${runId}`);
      return clone(run);
    },

    async getEvaluationWorkflowRun(runId) {
      record("getEvaluationWorkflowRun", { runId });
      return clone(evaluationWorkflowRuns.find((candidate) => candidate.id === runId) ?? null);
    },

    async rerunFailedJobs(runId) {
      record("rerunFailedJobs", { runId });
    },

    async listOpenPullRequestNumbers() {
      record("listOpenPullRequestNumbers", {});
      return clone(options.openPullRequestNumbers ?? pullRequests
        .filter((pullRequest) => pullRequest?.state === "open")
        .map((pullRequest) => pullRequest.number));
    },

    async getMergeState(prNumber) {
      record("getMergeState", { prNumber });
      const index = Math.min(calls.getMergeState.length - 1, mergeStates.length - 1);
      if (index < 0) throw new Error("missing merge state");
      return clone(mergeStates[index]);
    },

    async getBranchProtection(branch) {
      record("getBranchProtection", { branch });
      return options.branchProtection?.[branch] ?? false;
    },

    async getBranch(branch) {
      record("getBranch", { branch });
      if (!Object.hasOwn(branches, branch)) return null;
      return { name: branch, oid: branches[branch] };
    },

    async findMokkaPullRequests(head, base) {
      record("findMokkaPullRequests", { head, base });
      return clone(mokkaPullRequests.filter((pullRequest) => (
        pullRequest.head === head && pullRequest.base === base
      )));
    },

    async createMokkaPullRequest(pullRequest) {
      record("createMokkaPullRequest", pullRequest);
      const number = 1000 + mokkaPullRequests.length;
      const created = clone(options.mokkaCreatedPullRequest ?? {
        ...pullRequest,
        number,
        url: `https://github.com/NVIDIA/k8s-test-infra/pull/${number}`,
        state: "open",
        headOid: branches[pullRequest.head],
      });
      mokkaPullRequests.push(created);
      return clone(created);
    },

    async updateMokkaPullRequestBody(prNumber, body) {
      record("updateMokkaPullRequestBody", { prNumber, body });
      const pullRequest = mokkaPullRequests.find((candidate) => candidate.number === prNumber);
      if (pullRequest === undefined) throw new Error(`missing Mokka pull request: ${prNumber}`);
      pullRequest.body = body;
    },

    async findOpenBackportPullRequest(head, base) {
      record("findOpenBackportPullRequest", { head, base });
      const matches = backportPullRequests.filter((pullRequest) => (
        pullRequest.state === "open"
        && pullRequest.head === head
        && pullRequest.base === base
      ));
      if (matches.length > 1) throw new Error("duplicate open backport pull requests");
      return clone(matches[0] ?? null);
    },

    async createBackportPullRequest(pullRequest) {
      record("createBackportPullRequest", pullRequest);
      const created = {
        ...clone(pullRequest),
        number: 1000 + backportPullRequests.length,
        url: `https://github.com/NVIDIA/k8s-test-infra/pull/${1000 + backportPullRequests.length}`,
        state: "open",
      };
      backportPullRequests.push(created);
      return clone(created);
    },

    async setMergePolicyCheck(prNumber, headOid, conclusion, summary) {
      record("setMergePolicyCheck", { prNumber, headOid, conclusion, summary });
    },

    async enableAutoMerge(nodeId, mergeMethod) {
      record("enableAutoMerge", { nodeId, mergeMethod });
    },

    async disableAutoMerge(nodeId) {
      record("disableAutoMerge", { nodeId });
    },

    async getPolicyComment(prNumber, marker) {
      record("getPolicyComment", { prNumber, marker });
      const matches = comments.filter(
        (comment) => comment.author === automationLogin && comment.body.includes(marker),
      );
      if (matches.length > 1) throw new Error("duplicate policy comments");
      return matches.length === 1
        ? { action: "update", id: matches[0].id, body: matches[0].body }
        : { action: "create", id: null, body: null };
    },

    async planPolicyComment(prNumber, marker) {
      record("planPolicyComment", { prNumber, marker });
      const matches = comments.filter(
        (comment) => comment.author === automationLogin && comment.body.includes(marker),
      );
      if (matches.length > 1) {
        throw new Error("duplicate policy comments");
      }
      return matches.length === 1
        ? { action: "update", id: matches[0].id }
        : { action: "create", id: null };
    },

    async upsertPolicyComment(prNumber, marker, body, plan) {
      record("upsertPolicyComment", { prNumber, marker, body });
      if (commentMarkerCount(body, marker) !== 1) {
        throw new Error("policy comment body must contain exactly one marker");
      }
      const matches = comments.filter(
        (comment) => comment.author === automationLogin && comment.body.includes(marker),
      );
      if (matches.length > 1) {
        throw new Error("duplicate policy comments");
      }
      if (plan?.action === "update" && !matches.some((comment) => comment.id === plan.id)) {
        throw new Error("planned policy comment is missing");
      }
      if (plan?.action === "create" && matches.length !== 0) {
        throw new Error("planned policy comment already exists");
      }
      if (matches.length === 1) {
        matches[0].body = body;
        return { action: "updated", id: matches[0].id };
      }
      const comment = { id: comments.length + 1, body, author: automationLogin };
      comments.push(comment);
      return { action: "created", id: comment.id };
    },

    snapshot() {
      return clone(labels);
    },

    metadataSnapshot() {
      return {
        labels: labels.map(({ name }) => name),
        requestedReviewers: [...requestedReviewers],
        comments: clone(comments),
      };
    },

    setBranch(name, oid) {
      branches[name] = oid;
    },
  };
}

module.exports = { createFakeGitHub };
