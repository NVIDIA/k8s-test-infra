"use strict";

const { Buffer } = require("node:buffer");
const { setTimeout: delay } = require("node:timers/promises");
const { TextDecoder } = require("node:util");
const { isManagedMetadataLabel } = require("./managed-labels.js");

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
    return call(
      operation,
      () => octokit.paginate(endpoint, { ...parameters, per_page: 100 }, map),
      true,
    );
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
        user: nonEmptyString(review.user?.login, "review user"),
        state: nonEmptyString(review.state, "review state"),
        commitOid: review.commit_id === null ? null : nonEmptyString(review.commit_id, "review commit OID"),
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
      const blob = await call("getPolicyBlob", () => octokit.rest.git.getBlob({
        owner, repo, file_sha: metadata.sha,
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

    async planPolicyComment(prNumber, marker) {
      return readCommentPlan(prNumber, marker);
    },

    async upsertPolicyComment(prNumber, marker, body, existingPlan) {
      const plan = existingPlan ?? await readCommentPlan(prNumber, marker);
      return writePolicyComment(prNumber, marker, body, plan);
    },
  };
}

module.exports = { createGitHubClient };
