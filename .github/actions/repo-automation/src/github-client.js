"use strict";

const { Buffer } = require("node:buffer");
const { setTimeout: delay } = require("node:timers/promises");
const { TextDecoder } = require("node:util");
const { isManagedMetadataLabel, isManagedPolicyLabel } = require("./managed-labels.js");
const { trustedWorkflowPath } = require("./retest.js");

const MAX_CONTENT_BYTES = 1024 * 1024;
const TRANSIENT_STATUSES = new Set([408, 429, 500, 502, 503, 504]);
const TRANSIENT_CODES = new Set([
  "ECONNRESET", "ECONNREFUSED", "EAI_AGAIN", "ETIMEDOUT",
  "UND_ERR_CONNECT_TIMEOUT", "UND_ERR_HEADERS_TIMEOUT", "UND_ERR_SOCKET",
]);
const MAX_RETRY_DELAY_MS = 30000;
const ACTIONS_COMMENT_AUTHOR = Object.freeze({
  login: "github-actions[bot]",
  type: "Bot",
});

function copyLabel(label) {
  return {
    name: label.name,
    color: label.color,
    description: label.description,
  };
}

function sensitiveValues(error) {
  const values = new Set();
  const candidates = [
    error?.request?.headers,
    error?.request?.request?.headers,
    error?.config?.headers,
    error?.response?.headers,
  ];
  for (const headers of candidates) {
    if (!headers || typeof headers !== "object") continue;
    for (const [name, value] of Object.entries(headers)) {
      if (/authorization|token|secret|api[-_]?key/i.test(name) && typeof value === "string") {
        const trimmed = value.trim();
        if (trimmed !== "") {
          values.add(trimmed);
          const separator = trimmed.indexOf(" ");
          if (separator !== -1 && trimmed.slice(separator + 1).trim() !== "") {
            values.add(trimmed.slice(separator + 1).trim());
          }
        }
      }
    }
  }
  return [...values].sort((left, right) => right.length - left.length);
}

function normalizeError(operation, error) {
  const status = Number.isInteger(error?.status) ? error.status : undefined;
  const code = typeof error?.code === "string" ? error.code : undefined;
  let detail = "GitHub API request failed";
  if (status === 422) detail = "GitHub API validation rejected the request";
  else if (status === 401 || status === 403) detail = "GitHub API authorization or rate limit rejected the request";
  else if (status === 404) detail = "GitHub API resource was not found";
  else if (status !== undefined && status >= 500) detail = "GitHub service unavailable";
  else if (code !== undefined && TRANSIENT_CODES.has(code)) detail = "transient GitHub network failure";
  for (const value of sensitiveValues(error)) detail = detail.replaceAll(value, "[REDACTED]");

  const normalized = new Error(`${operation} failed: ${detail}`);
  normalized.name = "GitHubClientError";
  normalized.operation = operation;
  if (Number.isInteger(error?.status)) {
    normalized.status = error.status;
  }
  return normalized;
}

function positiveInteger(value, name) {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new TypeError(`${name} must be a positive safe integer`);
  }
  return value;
}

function nonEmptyString(value, name) {
  if (typeof value !== "string" || value === "" || /[\0\r\n]/.test(value)) {
    throw new TypeError(`${name} must be a safe non-empty string`);
  }
  return value;
}

function repositoryPath(value) {
  nonEmptyString(value, "content path");
  const withoutSlash = value.startsWith("/") ? value.slice(1) : value;
  if (
    withoutSlash === ""
    || withoutSlash.includes("\\")
    || withoutSlash.split("/").some((segment) => segment === "" || segment === "." || segment === "..")
  ) {
    throw new TypeError("content path must be a safe repository path");
  }
  return withoutSlash;
}

function githubLogin(value, name = "GitHub login") {
  nonEmptyString(value, name);
  if (!/^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/.test(value)) {
    throw new TypeError(`${name} must be a GitHub login`);
  }
  return value.toLowerCase();
}

function gitOid(value, name = "Git OID") {
  nonEmptyString(value, name);
  if (!/^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$/.test(value)) {
    throw new TypeError(`${name} must be a 40- or 64-character Git OID`);
  }
  return value.toLowerCase();
}

function loginList(values, name) {
  if (!Array.isArray(values) || values.length === 0 || values.length > 20) {
    throw new TypeError(`${name} must be a non-empty bounded login array`);
  }
  const normalized = values.map((value) => githubLogin(value, `${name} member`));
  if (new Set(normalized).size !== normalized.length) {
    throw new TypeError(`${name} must not contain duplicate logins`);
  }
  return normalized;
}

function workflowRun(data, expected) {
  if (data === null || typeof data !== "object" || Array.isArray(data)) return null;
  try {
    const workflowPath = trustedWorkflowPath(data.path);
    const runRepository = nonEmptyString(
      data.repository?.full_name,
      "workflow run repository",
    ).toLowerCase();
    if (
      workflowPath === null
      || data.event !== "pull_request"
      || !Array.isArray(data.pull_requests)
      || data.pull_requests.length !== 1
      || positiveInteger(data.pull_requests[0]?.number, "workflow run PR number") !== expected.prNumber
      || runRepository !== expected.repository
    ) return null;
    const run = {
      id: positiveInteger(data.id, "workflow run id"),
      headOid: gitOid(data.head_sha, "workflow run head OID"),
      status: nonEmptyString(data.status, "workflow run status"),
      conclusion: data.conclusion === null ? null : nonEmptyString(data.conclusion, "workflow run conclusion"),
      workflowPath,
      event: data.event,
      prNumber: expected.prNumber,
      repository: runRepository,
    };
    return run.headOid === expected.headOid ? run : null;
  } catch {
    return null;
  }
}

function headersFor(error) {
  const source = error?.response?.headers ?? error?.request?.headers;
  if (!source || typeof source !== "object") return {};
  return Object.fromEntries(Object.entries(source).map(([name, value]) => [name.toLowerCase(), value]));
}

function transientError(error) {
  if (TRANSIENT_STATUSES.has(error?.status)) return true;
  if (typeof error?.code === "string" && TRANSIENT_CODES.has(error.code)) return true;
  if (error?.status !== 403) return false;
  const headers = headersFor(error);
  return headers?.["retry-after"] !== undefined || String(headers?.["x-ratelimit-remaining"]) === "0";
}

function retryDelay(error, attempt, now) {
  const headers = headersFor(error);
  const retryAfter = Number(headers["retry-after"]);
  if (Number.isFinite(retryAfter) && retryAfter >= 0) {
    return Math.min(MAX_RETRY_DELAY_MS, Math.ceil(retryAfter * 1000));
  }
  const retryDate = Date.parse(headers["retry-after"]);
  if (Number.isFinite(retryDate)) {
    return Math.min(MAX_RETRY_DELAY_MS, Math.max(0, Math.ceil(retryDate - now())));
  }
  const reset = Number(headers["x-ratelimit-reset"]);
  if (Number.isFinite(reset) && reset >= 0) {
    return Math.min(MAX_RETRY_DELAY_MS, Math.max(0, Math.ceil((reset * 1000) - now())));
  }
  return Math.min(MAX_RETRY_DELAY_MS, 100 * (2 ** (attempt - 1)));
}

function decodeBlob(data) {
  if (
    data === null
    || typeof data !== "object"
    || Array.isArray(data)
    || data.encoding !== "base64"
    || typeof data.content !== "string"
    || (data.size !== undefined && (!Number.isSafeInteger(data.size) || data.size < 0 || data.size > MAX_CONTENT_BYTES))
  ) {
    throw new TypeError("repository blob must be bounded base64 content");
  }
  const encoded = data.content.replace(/[\r\n]/g, "");
  if (encoded.length % 4 !== 0 || !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(encoded)) {
    throw new TypeError("repository content must use valid base64 encoding");
  }
  const bytes = Buffer.from(encoded, "base64");
  if (bytes.length > MAX_CONTENT_BYTES || (data.size !== undefined && bytes.length !== data.size)) {
    throw new TypeError("repository content size is invalid");
  }
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    throw new TypeError("repository content must be UTF-8 text");
  }
}

function createGitHubClient(octokit, owner, repo, options = {}) {
  nonEmptyString(owner, "repository owner");
  nonEmptyString(repo, "repository name");
  const maxAttempts = options.maxAttempts ?? 3;
  positiveInteger(maxAttempts, "maxAttempts");
  if (maxAttempts > 5) throw new TypeError("maxAttempts must not exceed 5");
  const sleep = options.sleep ?? (async (milliseconds) => delay(milliseconds));
  if (typeof sleep !== "function") throw new TypeError("sleep must be a function");
  const now = options.now ?? Date.now;
  if (typeof now !== "function") throw new TypeError("now must be a function");
  const repositoryIdentity = `${owner}/${repo}`.toLowerCase();

  async function call(operation, request, retrySafe = false) {
    for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
      try {
        return await request();
      } catch (error) {
        if (!retrySafe || attempt === maxAttempts || !transientError(error)) {
          throw normalizeError(operation, error);
        }
        await sleep(retryDelay(error, attempt, now));
      }
    }
    throw new Error("unreachable retry state");
  }

  async function paginate(operation, endpoint, parameters, map) {
    return call(
      operation,
      () => octokit.paginate(endpoint, { ...parameters, per_page: 100 }, map),
      true,
    );
  }

  const rootTreeByRevision = new Map();
  const entriesByTree = new Map();

  async function rootTreeForRevision(revision) {
    if (!rootTreeByRevision.has(revision)) {
      rootTreeByRevision.set(revision, (async () => {
        const response = await call("getPolicyCommit", () => octokit.rest.git.getCommit({
          owner,
          repo,
          commit_sha: revision,
        }), true);
        return nonEmptyString(response.data?.tree?.sha, "policy commit root tree OID");
      })());
    }
    return rootTreeByRevision.get(revision);
  }

  async function entriesForTree(treeOid) {
    if (!entriesByTree.has(treeOid)) {
      entriesByTree.set(treeOid, (async () => {
        const response = await call("getPolicyTree", () => octokit.rest.git.getTree({
          owner,
          repo,
          tree_sha: treeOid,
        }), true);
        if (response.data?.truncated !== false || !Array.isArray(response.data.tree)) {
          throw new TypeError("repository policy Git tree is truncated or malformed");
        }
        return response.data.tree;
      })());
    }
    return entriesByTree.get(treeOid);
  }

  async function regularBlobForPath(path, revision) {
    const segments = repositoryPath(path).split("/");
    let treeOid = await rootTreeForRevision(revision);
    for (let index = 0; index < segments.length; index += 1) {
      const entries = await entriesForTree(treeOid);
      const matches = entries.filter((entry) => entry?.path === segments[index]);
      if (matches.length !== 1) {
        throw new TypeError("repository policy path is missing or ambiguous in Git tree");
      }
      const entry = matches[0];
      const oid = nonEmptyString(entry.sha, "policy Git tree entry OID");
      const final = index === segments.length - 1;
      if (final) {
        if (entry.type !== "blob" || entry.mode !== "100644") {
          throw new TypeError("repository policy path must be a regular Git blob");
        }
        return oid;
      }
      if (entry.type !== "tree" || entry.mode !== "040000") {
        throw new TypeError("repository policy path parent must be a Git tree");
      }
      treeOid = oid;
    }
    throw new Error("unreachable repository policy path traversal");
  }

  async function readCommentPlan(prNumber, marker) {
    positiveInteger(prNumber, "PR number");
    nonEmptyString(marker, "comment marker");
    const comments = await paginate("listIssueComments", octokit.rest.issues.listComments, {
      owner,
      repo,
      issue_number: prNumber,
    });
    const matches = comments.filter((comment) => (
      typeof comment.body === "string"
      && comment.body.includes(marker)
      && typeof comment.user?.login === "string"
      && comment.user.login.toLowerCase() === ACTIONS_COMMENT_AUTHOR.login
      && comment.user.type === ACTIONS_COMMENT_AUTHOR.type
    ));
    if (matches.length > 1) throw new Error("duplicate policy comments");
    if (matches.length === 1) {
      return { action: "update", id: positiveInteger(matches[0].id, "comment id") };
    }
    return { action: "create", id: null };
  }

  async function readPolicyComment(prNumber, marker) {
    positiveInteger(prNumber, "PR number");
    nonEmptyString(marker, "comment marker");
    const comments = await paginate("listIssueComments", octokit.rest.issues.listComments, {
      owner,
      repo,
      issue_number: prNumber,
    });
    const matches = comments.filter((comment) => (
      typeof comment.body === "string"
      && comment.body.includes(marker)
      && typeof comment.user?.login === "string"
      && comment.user.login.toLowerCase() === ACTIONS_COMMENT_AUTHOR.login
      && comment.user.type === ACTIONS_COMMENT_AUTHOR.type
    ));
    if (matches.length > 1) throw new Error("duplicate policy comments");
    if (matches.length === 0) return { action: "create", id: null, body: null };
    if (matches[0].body.length > 65_536) throw new TypeError("policy comment body exceeds limit");
    return {
      action: "update",
      id: positiveInteger(matches[0].id, "comment id"),
      body: matches[0].body,
    };
  }

  async function writePolicyComment(prNumber, marker, body, plan) {
    positiveInteger(prNumber, "PR number");
    nonEmptyString(marker, "comment marker");
    if (typeof body !== "string" || body.split(marker).length - 1 !== 1) {
      throw new TypeError("policy comment body must contain exactly one marker");
    }
    if (plan.action === "update") {
      const response = await call("updatePolicyComment", () => octokit.rest.issues.updateComment({
        owner,
        repo,
        comment_id: positiveInteger(plan.id, "comment id"),
        body,
      }), true);
      return { action: "updated", id: response.data.id };
    }
    if (plan.action !== "create" || plan.id !== null) {
      throw new TypeError("invalid policy comment plan");
    }
    const response = await call("createPolicyComment", () => octokit.rest.issues.createComment({
      owner,
      repo,
      issue_number: prNumber,
      body,
    }), false);
    return { action: "created", id: response.data.id };
  }

  return {
    async listLabels() {
      const labels = await paginate("listLabels", octokit.rest.issues.listLabelsForRepo, { owner, repo });
      return labels.map(copyLabel);
    },

    async createLabel(label) {
      const response = await call("createLabel", () => octokit.rest.issues.createLabel({
        owner, repo, ...copyLabel(label),
      }));
      return copyLabel(response.data);
    },

    async updateLabel(label) {
      const requested = copyLabel(label);
      const response = await call("updateLabel", () => octokit.rest.issues.updateLabel({
        owner, repo, name: requested.name, new_name: requested.name,
        color: requested.color, description: requested.description,
      }), true);
      return copyLabel(response.data);
    },

    async getPullRequest(prNumber) {
      positiveInteger(prNumber, "PR number");
      const { data } = await call("getPullRequest", () => octokit.rest.pulls.get({
        owner, repo, pull_number: prNumber,
      }), true);
      if (typeof data.draft !== "boolean") {
        throw new TypeError("live PR draft state must be a boolean");
      }
      return {
        number: positiveInteger(data.number, "live PR number"),
        title: nonEmptyString(data.title, "live PR title"),
        body: typeof data.body === "string" ? data.body : "",
        draft: data.draft,
        author: nonEmptyString(data.user?.login, "live PR author"),
        headOid: nonEmptyString(data.head?.sha, "live PR head OID"),
        state: nonEmptyString(data.state, "live PR state").toLowerCase(),
        baseRepository: {
          owner: nonEmptyString(data.base?.repo?.owner?.login, "base repository owner").toLowerCase(),
          repo: nonEmptyString(data.base?.repo?.name, "base repository name").toLowerCase(),
        },
      };
    },

    async getIssueComment(commentId) {
      positiveInteger(commentId, "comment id");
      const { data } = await call("getIssueComment", () => octokit.rest.issues.getComment({
        owner, repo, comment_id: commentId,
      }), true);
      const issueUrl = nonEmptyString(data.issue_url, "comment issue URL");
      const escapedOwner = owner.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      const escapedRepo = repo.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      const match = new RegExp(`/repos/${escapedOwner}/${escapedRepo}/issues/(\\d+)$`, "i").exec(issueUrl);
      if (match === null) throw new TypeError("comment issue mapping is invalid");
      const issueNumber = Number.parseInt(match[1], 10);
      positiveInteger(issueNumber, "comment issue number");
      const createdAt = data.created_at === undefined ? null : nonEmptyString(data.created_at, "comment creation time");
      const updatedAt = data.updated_at === undefined ? createdAt : nonEmptyString(data.updated_at, "comment update time");
      const liveId = positiveInteger(data.id, "live comment id");
      if (liveId !== commentId) throw new TypeError("live comment id does not match request");
      const body = typeof data.body === "string" ? data.body : "";
      if (body.length > 65_536) throw new TypeError("live comment body exceeds limit");
      return {
        id: liveId,
        issueNumber,
        body,
        author: githubLogin(data.user?.login, "live comment author"),
        edited: createdAt !== null && updatedAt !== createdAt,
      };
    },

    async getUserIdentity(login) {
      const requested = githubLogin(login);
      try {
        const { data } = await call("getUserIdentity", () => octokit.rest.users.getByUsername({
          username: requested,
        }), true);
        return {
          login: githubLogin(data.login, "live user login"),
          type: nonEmptyString(data.type, "live user type"),
          resolved: true,
          deleted: false,
        };
      } catch (error) {
        if (error.status === 404) {
          return { login: requested, type: "User", resolved: true, deleted: true };
        }
        throw error;
      }
    },

    async getCollaboratorAccess(login) {
      const requested = githubLogin(login);
      try {
        await call("checkCollaborator", () => octokit.rest.repos.checkCollaborator({
          owner, repo, username: requested,
        }), true);
      } catch (error) {
        if (error.status === 404) {
          return { liveCollaborator: false, permission: "none" };
        }
        throw error;
      }
      const { data } = await call("getCollaboratorPermission", () => (
        octokit.rest.repos.getCollaboratorPermissionLevel({ owner, repo, username: requested })
      ), true);
      return {
        liveCollaborator: true,
        permission: nonEmptyString(data.permission, "collaborator permission").toLowerCase(),
      };
    },

    async listIssueAssignees(prNumber) {
      positiveInteger(prNumber, "PR number");
      const { data } = await call("getIssueAssignees", () => octokit.rest.issues.get({
        owner, repo, issue_number: prNumber,
      }), true);
      if (!Array.isArray(data.assignees)) throw new TypeError("issue assignees must be an array");
      return data.assignees.map((assignee) => githubLogin(assignee?.login, "assignee"));
    },

    async listPullRequestFiles(prNumber) {
      positiveInteger(prNumber, "PR number");
      const files = await paginate("listPullRequestFiles", octokit.rest.pulls.listFiles, {
        owner, repo, pull_number: prNumber,
      });
      return files.map((file) => ({
        path: nonEmptyString(file.filename, "changed path"),
        additions: file.additions,
        deletions: file.deletions,
        status: nonEmptyString(file.status, "file status"),
      }));
    },

    async listPullRequestCommits(prNumber) {
      positiveInteger(prNumber, "PR number");
      const commits = await paginate("listPullRequestCommits", octokit.rest.pulls.listCommits, {
        owner, repo, pull_number: prNumber,
      });
      return commits.map((entry) => ({
        sha: entry.sha,
        commit: { message: entry.commit?.message, author: {
          name: entry.commit?.author?.name,
          email: entry.commit?.author?.email,
        } },
        author: entry.author === null ? null : { login: entry.author?.login },
      }));
    },

    async listPullRequestReviews(prNumber) {
      positiveInteger(prNumber, "PR number");
      const reviews = await paginate("listPullRequestReviews", octokit.rest.pulls.listReviews, {
        owner, repo, pull_number: prNumber,
      });
      return reviews.map((review) => ({
        id: positiveInteger(review.id, "review id"),
        user: nonEmptyString(review.user?.login, "review user"),
        state: nonEmptyString(review.state, "review state"),
        commitOid: review.commit_id === null ? null : nonEmptyString(review.commit_id, "review commit OID"),
        ...(review.state === "PENDING"
          ? {}
          : { submittedAt: nonEmptyString(review.submitted_at, "review submission time") }),
      }));
    },

    async listRequestedReviewers(prNumber) {
      positiveInteger(prNumber, "PR number");
      const users = await paginate("listRequestedReviewers", octokit.rest.pulls.listRequestedReviewers, {
        owner, repo, pull_number: prNumber,
      }, (response) => response.data.users);
      return users.map((user) => nonEmptyString(user.login, "requested reviewer"));
    },

    async listIssueLabels(prNumber) {
      positiveInteger(prNumber, "PR number");
      const labels = await paginate("listIssueLabels", octokit.rest.issues.listLabelsOnIssue, {
        owner, repo, issue_number: prNumber,
      });
      return labels.map((label) => nonEmptyString(label.name, "issue label"));
    },

    async listWorkflowRunsForHead(headOid, prNumber) {
      const requestedHead = gitOid(headOid, "head OID");
      const requestedPr = positiveInteger(prNumber, "PR number");
      const runs = await paginate("listWorkflowRunsForHead", octokit.rest.actions.listWorkflowRunsForRepo, {
        owner,
        repo,
        head_sha: requestedHead,
      }, (response) => response.data.workflow_runs);
      return runs
        .map((run) => workflowRun(run, {
          headOid: requestedHead,
          prNumber: requestedPr,
          repository: repositoryIdentity,
        }))
        .filter((run) => run !== null);
    },

    async getWorkflowRun(runId, headOid, prNumber) {
      positiveInteger(runId, "workflow run id");
      const requestedHead = gitOid(headOid, "head OID");
      const requestedPr = positiveInteger(prNumber, "PR number");
      const { data } = await call("getWorkflowRun", () => octokit.rest.actions.getWorkflowRun({
        owner, repo, run_id: runId,
      }), true);
      return workflowRun(data, {
        headOid: requestedHead,
        prNumber: requestedPr,
        repository: repositoryIdentity,
      });
    },

    async getDefaultBranchRevision() {
      const repository = await call("getRepository", () => octokit.rest.repos.get({ owner, repo }), true);
      const defaultBranch = nonEmptyString(repository.data.default_branch, "default branch");
      const branch = await call("getDefaultBranch", () => octokit.rest.repos.getBranch({
        owner, repo, branch: defaultBranch,
      }), true);
      return nonEmptyString(branch.data?.commit?.sha, "default branch commit OID");
    },

    async getContentAtRevision(path, revision) {
      const repositoryContentPath = repositoryPath(path);
      nonEmptyString(revision, "content revision");
      const treeBlobOid = await regularBlobForPath(repositoryContentPath, revision);
      const response = await call("getContentAtDefaultBranch", () => octokit.rest.repos.getContent({
        owner, repo, path: repositoryContentPath, ref: revision,
      }), true);
      const metadata = response.data;
      if (
        metadata === null
        || typeof metadata !== "object"
        || Array.isArray(metadata)
        || metadata.type !== "file"
        || typeof metadata.sha !== "string"
        || metadata.sha === ""
        || Object.hasOwn(metadata, "target")
        || Object.hasOwn(metadata, "submodule_git_url")
      ) {
        throw new TypeError("repository policy content must be a regular blob");
      }
      if (metadata.sha !== treeBlobOid) {
        throw new TypeError("repository policy Contents SHA does not match Git tree");
      }
      const blob = await call("getPolicyBlob", () => octokit.rest.git.getBlob({
        owner, repo, file_sha: treeBlobOid,
      }), true);
      return decodeBlob(blob.data);
    },

    async getContentAtDefaultBranch(path) {
      const revision = await this.getDefaultBranchRevision();
      return this.getContentAtRevision(path, revision);
    },

    async requestReviewers(prNumber, reviewers) {
      positiveInteger(prNumber, "PR number");
      if (!Array.isArray(reviewers) || reviewers.length === 0) throw new TypeError("reviewers must be non-empty");
      await call("requestReviewers", () => octokit.rest.pulls.requestReviewers({
        owner, repo, pull_number: prNumber, reviewers: [...reviewers],
      }), true);
    },

    async addAssignees(prNumber, assignees) {
      positiveInteger(prNumber, "PR number");
      const normalized = loginList(assignees, "assignees");
      await call("addAssignees", () => octokit.rest.issues.addAssignees({
        owner, repo, issue_number: prNumber, assignees: normalized,
      }), true);
    },

    async removeAssignees(prNumber, assignees) {
      positiveInteger(prNumber, "PR number");
      const normalized = loginList(assignees, "assignees");
      await call("removeAssignees", () => octokit.rest.issues.removeAssignees({
        owner, repo, issue_number: prNumber, assignees: normalized,
      }), true);
    },

    async rerunFailedJobs(runId) {
      positiveInteger(runId, "workflow run id");
      await call("rerunFailedJobs", () => octokit.rest.actions.reRunWorkflowFailedJobs({
        owner, repo, run_id: runId,
      }), false);
    },

    async addIssueLabel(prNumber, label) {
      positiveInteger(prNumber, "PR number");
      if (!isManagedMetadataLabel(label)) throw new TypeError("label is not metadata-managed");
      await call("addIssueLabel", () => octokit.rest.issues.addLabels({
        owner, repo, issue_number: prNumber, labels: [label],
      }), true);
    },

    async addPolicyLabel(prNumber, label) {
      positiveInteger(prNumber, "PR number");
      if (!isManagedPolicyLabel(label)) throw new TypeError("label is not policy-managed");
      await call("addPolicyLabel", () => octokit.rest.issues.addLabels({
        owner, repo, issue_number: prNumber, labels: [label],
      }), true);
    },

    async removeIssueLabel(prNumber, label) {
      positiveInteger(prNumber, "PR number");
      if (!isManagedMetadataLabel(label)) throw new TypeError("label is not metadata-managed");
      try {
        await call("removeIssueLabel", () => octokit.rest.issues.removeLabel({
          owner, repo, issue_number: prNumber, name: label,
        }), true);
      } catch (error) {
        if (error.status !== 404) throw error;
      }
    },

    async removePolicyLabel(prNumber, label) {
      positiveInteger(prNumber, "PR number");
      if (!isManagedPolicyLabel(label)) throw new TypeError("label is not policy-managed");
      try {
        await call("removePolicyLabel", () => octokit.rest.issues.removeLabel({
          owner, repo, issue_number: prNumber, name: label,
        }), true);
      } catch (error) {
        if (error.status !== 404) throw error;
      }
    },

    async planPolicyComment(prNumber, marker) {
      return readCommentPlan(prNumber, marker);
    },

    async getPolicyComment(prNumber, marker) {
      return readPolicyComment(prNumber, marker);
    },

    async upsertPolicyComment(prNumber, marker, body, existingPlan) {
      const plan = existingPlan ?? await readCommentPlan(prNumber, marker);
      return writePolicyComment(prNumber, marker, body, plan);
    },
  };
}

module.exports = { createGitHubClient };
