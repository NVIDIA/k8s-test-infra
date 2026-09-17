"use strict";

const { Buffer } = require("node:buffer");
const { setTimeout: delay } = require("node:timers/promises");
const { TextDecoder } = require("node:util");
const {
  isManagedMetadataLabel,
  isManagedPolicyLabel,
} = require("./managed-labels.js");
const { MAX_API_COLLECTION_ITEMS } = require("./limits.js");

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
const MERGE_POLICY_CHECK = "repository-automation/merge-policy";
const REVIEW_STATES = new Set([
  "APPROVED", "CHANGES_REQUESTED", "COMMENTED", "DISMISSED", "PENDING",
]);

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

function normalizedLogin(value, name) {
  return nonEmptyString(value, name).toLowerCase();
}

function mappedReview(review) {
  const state = nonEmptyString(review?.state, "review state").toUpperCase();
  if (!REVIEW_STATES.has(state)) throw new TypeError("review state is unsupported");
  const mapped = {
    id: positiveInteger(review?.id, "review id"),
    user: normalizedLogin(review?.user?.login, "review user"),
    state,
    commitOid: review?.commit_id === null
      ? null
      : nonEmptyString(review?.commit_id, "review commit OID").toLowerCase(),
  };
  if (state !== "PENDING") {
    mapped.submittedAt = nonEmptyString(review?.submitted_at, "review submission time");
  }
  return mapped;
}

function issueNumberFromUrl(value) {
  if (typeof value !== "string") throw new TypeError("issue comment URL is invalid");
  const match = /\/issues\/([1-9][0-9]*)$/.exec(value);
  if (match === null) throw new TypeError("issue comment URL is invalid");
  return positiveInteger(Number(match[1]), "issue comment pull request number");
}

function mappedWorkflowRun(run) {
  const rawPath = nonEmptyString(run?.path, "workflow path");
  const separator = rawPath.indexOf("@");
  const workflowPath = separator === -1 ? rawPath : rawPath.slice(0, separator);
  const workflowSourceRef = separator === -1 ? null : rawPath.slice(separator + 1);
  if (workflowPath === "" || (separator !== -1 && workflowSourceRef === "")) {
    throw new TypeError("workflow identity is invalid");
  }
  if (!Array.isArray(run?.pull_requests) || run.pull_requests.length !== 1) {
    throw new TypeError("workflow run must bind exactly one pull request");
  }
  return {
    id: positiveInteger(run.id, "workflow run id"),
    headOid: nonEmptyString(run.head_sha, "workflow run head OID").toLowerCase(),
    status: nonEmptyString(run.status, "workflow run status"),
    conclusion: run.conclusion === null ? null : nonEmptyString(run.conclusion, "workflow conclusion"),
    workflowPath,
    workflowSourceRef,
    event: nonEmptyString(run.event, "workflow event"),
    prNumber: positiveInteger(run.pull_requests[0]?.number, "workflow pull request number"),
    repository: nonEmptyString(run.repository?.full_name, "workflow repository").toLowerCase(),
  };
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
    let overflow = false;
    let collected = 0;
    const values = await call(
      operation,
      () => octokit.paginate(
        endpoint,
        { ...parameters, per_page: 100 },
        (response, done) => {
          const page = map === undefined ? response.data : map(response);
          if (!Array.isArray(page)) {
            throw new TypeError("paginated GitHub API page must be an array");
          }
          collected += page.length;
          if (collected > MAX_API_COLLECTION_ITEMS) {
            overflow = true;
            if (typeof done === "function") done();
            return [];
          }
          return page;
        },
      ),
      true,
    );
    if (
      overflow
      || !Array.isArray(values)
      || values.length > MAX_API_COLLECTION_ITEMS
    ) {
      throw new TypeError(
        `${operation} result must not exceed ${MAX_API_COLLECTION_ITEMS} items`,
      );
    }
    return values;
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
    if (matches.length === 1) {
      return {
        action: "update",
        id: positiveInteger(matches[0].id, "comment id"),
        body: matches[0].body,
      };
    }
    return { action: "create", id: null, body: null };
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
      const pullRequest = {
        number: positiveInteger(data.number, "live PR number"),
        nodeId: nonEmptyString(data.node_id, "live PR node ID"),
        title: nonEmptyString(data.title, "live PR title"),
        body: typeof data.body === "string" ? data.body : "",
        draft: data.draft,
        author: nonEmptyString(data.user?.login, "live PR author"),
        headOid: nonEmptyString(data.head?.sha, "live PR head OID"),
        state: nonEmptyString(data.state, "live PR state").toLowerCase(),
        baseBranch: nonEmptyString(data.base?.ref, "live PR base branch"),
        baseRepository: {
          owner: nonEmptyString(data.base?.repo?.owner?.login, "base repository owner").toLowerCase(),
          repo: nonEmptyString(data.base?.repo?.name, "base repository name").toLowerCase(),
        },
      };
      if (typeof data.merged === "boolean") pullRequest.merged = data.merged;
      if (data.merge_commit_sha === null) {
        pullRequest.mergeCommitOid = null;
      } else if (data.merge_commit_sha !== undefined) {
        pullRequest.mergeCommitOid = nonEmptyString(
          data.merge_commit_sha,
          "live PR merge commit OID",
        ).toLowerCase();
      }
      return pullRequest;
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
      return reviews.map(mappedReview);
    },

    async getPullRequestReview(prNumber, reviewId) {
      positiveInteger(prNumber, "PR number");
      positiveInteger(reviewId, "review id");
      const response = await call("getPullRequestReview", () => octokit.rest.pulls.getReview({
        owner, repo, pull_number: prNumber, review_id: reviewId,
      }), true);
      return mappedReview(response.data);
    },

    async getIssueComment(commentId) {
      positiveInteger(commentId, "comment id");
      const response = await call("getIssueComment", () => octokit.rest.issues.getComment({
        owner, repo, comment_id: commentId,
      }), true);
      const data = response.data;
      return {
        id: positiveInteger(data?.id, "live comment id"),
        issueNumber: issueNumberFromUrl(data?.issue_url),
        body: typeof data?.body === "string" ? data.body : "",
        author: normalizedLogin(data?.user?.login, "live comment author"),
        authorType: nonEmptyString(data?.user?.type, "live comment author type"),
        edited: data?.updated_at !== data?.created_at,
      };
    },

    async getUserIdentity(login) {
      const normalized = normalizedLogin(login, "user login");
      try {
        const response = await call("getUserIdentity", () => octokit.rest.users.getByUsername({
          username: normalized,
        }), true);
        return {
          login: normalizedLogin(response.data?.login, "resolved user login"),
          type: nonEmptyString(response.data?.type, "resolved user type"),
          resolved: true,
          deleted: false,
        };
      } catch (error) {
        if (error.status !== 404) throw error;
        return { login: normalized, type: null, resolved: false, deleted: true };
      }
    },

    async getCollaboratorAccess(login) {
      const normalized = normalizedLogin(login, "collaborator login");
      try {
        const response = await call("getCollaboratorAccess", () => (
          octokit.rest.repos.getCollaboratorPermissionLevel({
            owner, repo, username: normalized,
          })
        ), true);
        const permission = nonEmptyString(response.data?.permission, "collaborator permission").toLowerCase();
        return { liveCollaborator: permission !== "none", permission };
      } catch (error) {
        if (error.status !== 404) throw error;
        return { liveCollaborator: false, permission: "none" };
      }
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

    async addIssueLabel(prNumber, label) {
      positiveInteger(prNumber, "PR number");
      if (!isManagedMetadataLabel(label)) throw new TypeError("label is not metadata-managed");
      await call("addIssueLabel", () => octokit.rest.issues.addLabels({
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

    async addPolicyLabel(prNumber, label) {
      positiveInteger(prNumber, "PR number");
      if (!isManagedPolicyLabel(label)) throw new TypeError("label is not policy-managed");
      await call("addPolicyLabel", () => octokit.rest.issues.addLabels({
        owner, repo, issue_number: prNumber, labels: [label.toLowerCase()],
      }), true);
    },

    async removePolicyLabel(prNumber, label) {
      positiveInteger(prNumber, "PR number");
      if (!isManagedPolicyLabel(label)) throw new TypeError("label is not policy-managed");
      try {
        await call("removePolicyLabel", () => octokit.rest.issues.removeLabel({
          owner, repo, issue_number: prNumber, name: label.toLowerCase(),
        }), true);
      } catch (error) {
        if (error.status !== 404) throw error;
      }
    },

    async listWorkflowRunsForHead(headOid, prNumber) {
      nonEmptyString(headOid, "workflow head OID");
      positiveInteger(prNumber, "PR number");
      const runs = await paginate(
        "listWorkflowRunsForHead",
        octokit.rest.actions.listWorkflowRunsForRepo,
        { owner, repo, head_sha: headOid },
        (response) => response.data.workflow_runs,
      );
      return runs.map(mappedWorkflowRun).filter((run) => (
        run.headOid === headOid.toLowerCase() && run.prNumber === prNumber
      ));
    },

    async getWorkflowRun(runId, headOid, prNumber) {
      positiveInteger(runId, "workflow run id");
      nonEmptyString(headOid, "workflow head OID");
      positiveInteger(prNumber, "PR number");
      const response = await call("getWorkflowRun", () => octokit.rest.actions.getWorkflowRun({
        owner, repo, run_id: runId,
      }), true);
      const run = mappedWorkflowRun(response.data);
      if (run.id !== runId || run.headOid !== headOid.toLowerCase() || run.prNumber !== prNumber) {
        throw new Error("workflow run identity changed");
      }
      return run;
    },

    async rerunFailedJobs(runId) {
      positiveInteger(runId, "workflow run id");
      await call("rerunFailedJobs", () => octokit.rest.actions.reRunWorkflowFailedJobs({
        owner, repo, run_id: runId,
      }), false);
    },

    async listOpenPullRequestNumbers() {
      const pullRequests = await paginate("listOpenPullRequests", octokit.rest.pulls.list, {
        owner, repo, state: "open",
      });
      return pullRequests.map((pullRequest) => positiveInteger(pullRequest.number, "open PR number"));
    },

    async getMergeState(prNumber) {
      positiveInteger(prNumber, "PR number");
      const response = await call("getMergeState", () => octokit.graphql(`
        query RepositoryAutomationMergeState($owner: String!, $repo: String!, $number: Int!) {
          repository(owner: $owner, name: $repo) {
            pullRequest(number: $number) {
              number id state isDraft mergeable headRefOid baseRefName
              autoMergeRequest { mergeMethod }
            }
          }
        }
      `, { owner, repo, number: prNumber }), true);
      const pullRequest = response?.repository?.pullRequest;
      return {
        number: positiveInteger(pullRequest?.number, "GraphQL PR number"),
        nodeId: nonEmptyString(pullRequest?.id, "GraphQL PR node ID"),
        repository: `${owner}/${repo}`.toLowerCase(),
        state: nonEmptyString(pullRequest?.state, "GraphQL PR state").toUpperCase(),
        draft: pullRequest?.isDraft,
        mergeability: nonEmptyString(pullRequest?.mergeable, "GraphQL mergeability").toUpperCase(),
        headOid: nonEmptyString(pullRequest?.headRefOid, "GraphQL head OID").toLowerCase(),
        baseBranch: nonEmptyString(pullRequest?.baseRefName, "GraphQL base branch"),
        autoMergeMethod: pullRequest?.autoMergeRequest === null
          ? null
          : nonEmptyString(pullRequest?.autoMergeRequest?.mergeMethod, "auto-merge method").toUpperCase(),
      };
    },

    async getBranchProtection(branch) {
      nonEmptyString(branch, "branch");
      try {
        await call("getBranchProtection", () => octokit.rest.repos.getBranchProtection({
          owner, repo, branch,
        }), true);
        return true;
      } catch (error) {
        if (error.status === 404) return false;
        throw error;
      }
    },

    async getBranch(branch) {
      nonEmptyString(branch, "branch");
      try {
        const response = await call("getBranch", () => octokit.rest.repos.getBranch({
          owner, repo, branch,
        }), true);
        return {
          name: nonEmptyString(response.data?.name, "branch name"),
          oid: nonEmptyString(response.data?.commit?.sha, "branch commit OID").toLowerCase(),
        };
      } catch (error) {
        if (error.status === 404) return null;
        throw error;
      }
    },

    async findOpenBackportPullRequest(head, base) {
      nonEmptyString(head, "backport head branch");
      nonEmptyString(base, "backport base branch");
      const pullRequests = await paginate("findOpenBackportPullRequest", octokit.rest.pulls.list, {
        owner,
        repo,
        state: "open",
        head: `${owner}:${head}`,
        base,
      });
      if (pullRequests.length > 1) throw new Error("duplicate open backport pull requests");
      if (pullRequests.length === 0) return null;
      const pullRequest = pullRequests[0];
      return {
        number: positiveInteger(pullRequest.number, "backport PR number"),
        url: nonEmptyString(pullRequest.html_url, "backport PR URL"),
        state: nonEmptyString(pullRequest.state, "backport PR state").toLowerCase(),
        base: nonEmptyString(pullRequest.base?.ref, "backport PR base branch"),
        head: nonEmptyString(pullRequest.head?.ref, "backport PR head branch"),
        title: nonEmptyString(pullRequest.title, "backport PR title"),
        body: typeof pullRequest.body === "string" ? pullRequest.body : "",
      };
    },

    async createBackportPullRequest(pullRequest) {
      if (pullRequest === null || typeof pullRequest !== "object" || Array.isArray(pullRequest)) {
        throw new TypeError("backport pull request must be an object");
      }
      const base = nonEmptyString(pullRequest.base, "backport base branch");
      const head = nonEmptyString(pullRequest.head, "backport head branch");
      const title = nonEmptyString(pullRequest.title, "backport PR title");
      const body = nonEmptyString(pullRequest.body, "backport PR body");
      const response = await call("createBackportPullRequest", () => octokit.rest.pulls.create({
        owner, repo, base, head, title, body,
      }), false);
      return {
        number: positiveInteger(response.data?.number, "created backport PR number"),
        url: nonEmptyString(response.data?.html_url, "created backport PR URL"),
      };
    },

    async setMergePolicyCheck(prNumber, headOid, conclusion, summary) {
      positiveInteger(prNumber, "PR number");
      nonEmptyString(headOid, "merge policy head OID");
      if (conclusion !== "success" && conclusion !== "action_required") {
        throw new TypeError("merge policy conclusion is invalid");
      }
      nonEmptyString(summary, "merge policy summary");
      await call("setMergePolicyCheck", () => octokit.rest.checks.create({
        owner,
        repo,
        name: MERGE_POLICY_CHECK,
        head_sha: headOid,
        status: "completed",
        conclusion,
        output: { title: MERGE_POLICY_CHECK, summary },
      }), false);
    },

    async enableAutoMerge(nodeId, mergeMethod) {
      nonEmptyString(nodeId, "pull request node ID");
      if (mergeMethod !== "SQUASH") throw new TypeError("auto-merge method must be SQUASH");
      await call("enableAutoMerge", () => octokit.graphql(`
        mutation EnableAutoMerge($pullRequestId: ID!, $mergeMethod: PullRequestMergeMethod!) {
          enablePullRequestAutoMerge(input: {
            pullRequestId: $pullRequestId,
            mergeMethod: $mergeMethod
          }) { clientMutationId }
        }
      `, { pullRequestId: nodeId, mergeMethod }), false);
    },

    async disableAutoMerge(nodeId) {
      nonEmptyString(nodeId, "pull request node ID");
      await call("disableAutoMerge", () => octokit.graphql(`
        mutation DisableAutoMerge($pullRequestId: ID!) {
          disablePullRequestAutoMerge(input: { pullRequestId: $pullRequestId }) {
            clientMutationId
          }
        }
      `, { pullRequestId: nodeId }), false);
    },

    async getPolicyComment(prNumber, marker) {
      return readPolicyComment(prNumber, marker);
    },

    async planPolicyComment(prNumber, marker) {
      const comment = await readPolicyComment(prNumber, marker);
      return { action: comment.action, id: comment.id };
    },

    async upsertPolicyComment(prNumber, marker, body, existingPlan) {
      const plan = existingPlan ?? await readPolicyComment(prNumber, marker);
      return writePolicyComment(prNumber, marker, body, plan);
    },
  };
}

module.exports = { createGitHubClient };
