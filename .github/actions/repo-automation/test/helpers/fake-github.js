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
  const automationLogin = options.automationLogin ?? "github-actions[bot]";
  for (const comment of comments) {
    if (comment.author === undefined) comment.author = automationLogin;
    if (comment.type === undefined) comment.type = comment.author === automationLogin ? "Bot" : "User";
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
    getPullRequestMergeable: [],
    getIssueComment: [],
    getUserIdentity: [],
    getCollaboratorAccess: [],
    listIssueAssignees: [],
    listPullRequestFiles: [],
    listPullRequestCommits: [],
    listPullRequestReviews: [],
    listRequestedReviewers: [],
    listIssueLabels: [],
    listWorkflowRunsForHead: [],
    getWorkflowRun: [],
    getContentAtDefaultBranch: [],
    getDefaultBranchRevision: [],
    getContentAtRevision: [],
    requestReviewers: [],
    addAssignees: [],
    removeAssignees: [],
    rerunFailedJobs: [],
    addIssueLabel: [],
    removeIssueLabel: [],
    addPolicyLabel: [],
    removePolicyLabel: [],
    planPolicyComment: [],
    getPolicyComment: [],
    upsertPolicyComment: [],
    listOpenPullRequestNumbers: [],
    getEvaluationWorkflowRun: [],
    getBranchProtection: [],
    getMergeState: [],
    enableAutoMerge: [],
    disableAutoMerge: [],
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

    async getPullRequestMergeable(prNumber) {
      record("getPullRequestMergeable", { prNumber });
      return clone(options.mergeable ?? "UNKNOWN");
    },

    async listOpenPullRequestNumbers() {
      record("listOpenPullRequestNumbers", {});
      return clone(options.openPullRequestNumbers ?? [42]);
    },

    async getEvaluationWorkflowRun(runId) {
      record("getEvaluationWorkflowRun", { runId });
      return clone(options.evaluationWorkflowRuns?.[runId] ?? null);
    },

    async getBranchProtection(branch) {
      record("getBranchProtection", { branch });
      if (!Object.hasOwn(options.branchProtection ?? {}, branch)) return null;
      return options.branchProtection[branch];
    },

    async getMergeState(prNumber) {
      record("getMergeState", { prNumber });
      const states = options.mergeStates ?? [];
      const index = Math.min(calls.getMergeState.length - 1, states.length - 1);
      return clone(states[index]);
    },

    async enableAutoMerge(pullRequestId, mergeMethod) {
      record("enableAutoMerge", { pullRequestId, mergeMethod });
    },

    async disableAutoMerge(pullRequestId) {
      record("disableAutoMerge", { pullRequestId });
    },

    async getIssueComment(commentId) {
      record("getIssueComment", { commentId });
      return clone(options.issueComment);
    },

    async getUserIdentity(login) {
      record("getUserIdentity", { login });
      return clone(options.identities?.[login.toLowerCase()] ?? {
        login,
        type: "User",
        resolved: false,
        deleted: false,
      });
    },

    async getCollaboratorAccess(login) {
      record("getCollaboratorAccess", { login });
      return clone(options.collaboratorAccess?.[login.toLowerCase()] ?? {
        liveCollaborator: false,
        permission: "none",
      });
    },

    async listIssueAssignees(prNumber) {
      record("listIssueAssignees", { prNumber });
      return clone(options.assignees ?? []);
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

    async listRequestedReviewers(prNumber) {
      record("listRequestedReviewers", { prNumber });
      return [...requestedReviewers];
    },

    async listIssueLabels(prNumber) {
      record("listIssueLabels", { prNumber });
      return labels.map(({ name }) => name);
    },

    async listWorkflowRunsForHead(requestedHeadOid, prNumber) {
      record("listWorkflowRunsForHead", { headOid: requestedHeadOid, prNumber });
      return clone(options.workflowRuns ?? []);
    },

    async getWorkflowRun(runId, requestedHeadOid, prNumber) {
      record("getWorkflowRun", { runId, headOid: requestedHeadOid, prNumber });
      const configured = options.workflowRunReads?.[runId];
      if (Array.isArray(configured) && configured.length > 0) {
        return clone(configured[Math.min(calls.getWorkflowRun.length - 1, configured.length - 1)]);
      }
      return clone((options.workflowRuns ?? []).find((run) => run.id === runId));
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

    async addAssignees(prNumber, assignees) {
      record("addAssignees", { prNumber, assignees: [...assignees] });
      const state = options.assignees ?? (options.assignees = []);
      for (const assignee of assignees) {
        if (!state.some((current) => current.toLowerCase() === assignee.toLowerCase())) {
          state.push(assignee);
        }
      }
    },

    async removeAssignees(prNumber, assignees) {
      record("removeAssignees", { prNumber, assignees: [...assignees] });
      const state = options.assignees ?? (options.assignees = []);
      for (const assignee of assignees) {
        const index = state.findIndex((current) => current.toLowerCase() === assignee.toLowerCase());
        if (index !== -1) state.splice(index, 1);
      }
    },

    async rerunFailedJobs(runId) {
      record("rerunFailedJobs", { runId });
      const run = (options.workflowRuns ?? []).find((candidate) => candidate.id === runId);
      if (run !== undefined) {
        run.status = "queued";
        run.conclusion = null;
      }
    },

    async addIssueLabel(prNumber, label) {
      record("addIssueLabel", { prNumber, label });
      if (!labels.some((entry) => entry.name === label)) {
        labels.push({ name: label });
      }
    },

    async addPolicyLabel(prNumber, label) {
      record("addPolicyLabel", { prNumber, label });
      if (!labels.some((entry) => entry.name.toLowerCase() === label.toLowerCase())) {
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

    async removePolicyLabel(prNumber, label) {
      record("removePolicyLabel", { prNumber, label });
      const index = labels.findIndex((entry) => entry.name.toLowerCase() === label.toLowerCase());
      if (index !== -1) labels.splice(index, 1);
    },

    async planPolicyComment(prNumber, marker) {
      record("planPolicyComment", { prNumber, marker });
      const matches = comments.filter(
        (comment) => comment.author === automationLogin
          && comment.type === "Bot"
          && comment.body.includes(marker),
      );
      if (matches.length > 1) {
        throw new Error("duplicate policy comments");
      }
      return matches.length === 1
        ? { action: "update", id: matches[0].id }
        : { action: "create", id: null };
    },

    async getPolicyComment(prNumber, marker) {
      record("getPolicyComment", { prNumber, marker });
      const matches = comments.filter(
        (comment) => comment.author === automationLogin
          && comment.type === "Bot"
          && comment.body.includes(marker),
      );
      if (matches.length > 1) throw new Error("duplicate policy comments");
      return matches.length === 1
        ? { action: "update", id: matches[0].id, body: matches[0].body }
        : { action: "create", id: null, body: null };
    },

    async upsertPolicyComment(prNumber, marker, body, plan) {
      record("upsertPolicyComment", { prNumber, marker, body });
      if (commentMarkerCount(body, marker) !== 1) {
        throw new Error("policy comment body must contain exactly one marker");
      }
      const matches = comments.filter(
        (comment) => comment.author === automationLogin
          && comment.type === "Bot"
          && comment.body.includes(marker),
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
      const comment = { id: comments.length + 1, body, author: automationLogin, type: "Bot" };
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

    commandSnapshot() {
      return {
        labels: labels.map(({ name }) => name).sort(),
        requestedReviewers: [...requestedReviewers].sort(),
        assignees: [...(options.assignees ?? [])].sort(),
        comments: clone(comments),
        workflowRuns: clone(options.workflowRuns ?? []),
      };
    },
  };
}

module.exports = { createFakeGitHub };
