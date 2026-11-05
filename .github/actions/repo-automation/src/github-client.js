"use strict";

const { Buffer } = require("node:buffer");
const { setTimeout: delay } = require("node:timers/promises");
const { TextDecoder } = require("node:util");
const {
  isManagedConflictLabel,
  isManagedMetadataLabel,
  isManagedPolicyLabel,
} = require("./managed-labels.js");
const { MAX_API_COLLECTION_ITEMS } = require("./limits.js");
const { evaluateCI } = require("./merge-ci.js");

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
const EVALUATOR_WORKFLOW_PATHS = new Set([
  ".github/workflows/review-observer.yml",
  ".github/workflows/pr-metadata.yml",
  ".github/workflows/commands.yml",
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

function commitOid(value, name) {
  if (typeof value !== "string" || !/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/i.test(value)) {
    throw new TypeError(`${name} must be a 40- or 64-digit hexadecimal OID`);
  }
  return value.toLowerCase();
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

function mappedWorkflowRunForPR(run, prNumber) {
  const rawPath = nonEmptyString(run?.path, "workflow path");
  const separator = rawPath.indexOf("@");
  const workflowPath = separator === -1 ? rawPath : rawPath.slice(0, separator);
  const workflowSourceRef = separator === -1 ? null : rawPath.slice(separator + 1);
  if (workflowPath === "" || (separator !== -1 && workflowSourceRef === "")) {
    throw new TypeError("workflow identity is invalid");
  }
  const mapped = {
    id: positiveInteger(run.id, "workflow run id"),
    headOid: nonEmptyString(run.head_sha, "workflow run head OID").toLowerCase(),
    status: nonEmptyString(run.status, "workflow run status"),
    conclusion: run.conclusion === null ? null : nonEmptyString(run.conclusion, "workflow conclusion"),
    workflowPath,
    workflowSourceRef,
    event: nonEmptyString(run.event, "workflow event"),
    prNumber,
    repository: nonEmptyString(run.repository?.full_name, "workflow repository").toLowerCase(),
  };
  if (run.run_number !== undefined) mapped.runNumber = run.run_number;
  if (run.run_attempt !== undefined) mapped.runAttempt = run.run_attempt;
  return mapped;
}

function mappedWorkflowRun(run) {
  if (!Array.isArray(run?.pull_requests) || run.pull_requests.length !== 1) {
    throw new TypeError("workflow run must bind exactly one pull request");
  }
  return mappedWorkflowRunForPR(run, positiveInteger(run.pull_requests[0]?.number, "workflow pull request number"));
}

function optionalHeadRepository(value) {
  if (value === undefined || value === null) return null;
  const repository = nonEmptyString(value, "CI head repository").toLowerCase();
  if (!/^[a-z0-9][a-z0-9-]{0,38}\/[a-z0-9._-]{1,100}$/.test(repository)) {
    throw new TypeError("CI head repository must identify an owner and repository");
  }
  return repository;
}

function optionalHeadBranch(value, label = "CI head branch") {
  if (value === undefined || value === null) return null;
  if (safeWorkflowSourceRef(value) === null || value.length > 255) {
    throw new TypeError(`${label} is invalid`);
  }
  return value;
}

function mappedCIWorkflowRun(run, headOid, prNumber, headRepository, headBranch) {
  const liveHead = commitOid(run?.head_sha, "CI workflow head OID");
  if (liveHead !== headOid) return null;
  if (!Array.isArray(run.pull_requests)) {
    throw new TypeError("CI workflow pull request mapping must be an array");
  }
  let runPrNumber = prNumber;
  let runHeadRepository;
  let runHeadBranch;
  if (run.pull_requests.length === 0) {
    if (run.event !== "pull_request" || typeof run.path !== "string") return null;
    if (!run.path.endsWith(`@refs/pull/${prNumber}/merge`)) {
      if (run.path.includes("@")) return null;
      runHeadRepository = optionalHeadRepository(run.head_repository?.full_name);
      runHeadBranch = optionalHeadBranch(run.head_branch);
      if (headRepository === null || headBranch === null
        || runHeadRepository !== headRepository || runHeadBranch !== headBranch) return null;
      runPrNumber = null;
    }
  } else if (run.pull_requests.length !== 1 || run.pull_requests[0]?.number !== prNumber) {
    return null;
  }
  const mapped = mappedWorkflowRunForPR(run, runPrNumber);
  if (runPrNumber === null) {
    mapped.headRepository = runHeadRepository;
    mapped.headBranch = runHeadBranch;
  }
  mapped.runNumber = positiveInteger(run.run_number, "workflow run number");
  mapped.runAttempt = positiveInteger(run.run_attempt, "workflow run attempt");
  return mapped;
}

function mappedCICheckRun(check) {
  return {
    id: positiveInteger(check?.id, "CI check run id"),
    name: nonEmptyString(check?.name, "CI check run name"),
    appId: positiveInteger(check?.app?.id, "CI check app ID"),
    headOid: commitOid(check?.head_sha, "CI check head OID"),
    status: nonEmptyString(check?.status, "CI check status"),
    conclusion: check?.conclusion === null ? null : nonEmptyString(check?.conclusion, "CI check conclusion"),
  };
}

function safeWorkflowSourceRef(value) {
  if (
    typeof value !== "string"
    || value === ""
    || value.length > 256
    || /[\0-\x20\x7f~^:?*\[\]\\]/.test(value)
    || value.includes("@")
    || value.includes("//")
    || value.includes("..")
    || value.includes("@{")
  ) return null;
  const segments = value.split("/");
  if (segments.some((segment) => (
    segment === ""
    || segment.startsWith(".")
    || segment.endsWith(".")
    || segment.endsWith(".lock")
  ))) return null;
  return value;
}

function evaluatorWorkflowIdentity(value) {
  if (typeof value !== "string" || value.length > 512 || /[\0\r\n\\]/.test(value)) return null;
  const separator = value.indexOf("@");
  if (separator === -1) {
    return EVALUATOR_WORKFLOW_PATHS.has(value)
      ? { workflowPath: value, workflowSourceRef: null }
      : null;
  }
  if (separator <= 0 || value.indexOf("@", separator + 1) !== -1) return null;
  const workflowPath = value.slice(0, separator);
  const workflowSourceRef = safeWorkflowSourceRef(value.slice(separator + 1));
  return EVALUATOR_WORKFLOW_PATHS.has(workflowPath) && workflowSourceRef !== null
    ? { workflowPath, workflowSourceRef }
    : null;
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

  async function paginateCI(operation, endpoint, parameters) {
    return call(operation, async () => {
      let total;
      let collected = 0;
      let pages = 0;
      const ids = new Set();
      const values = await octokit.paginate(endpoint, { ...parameters, per_page: 100 }, (response) => {
        const page = response.data;
        if (
          !Array.isArray(page)
          || !Number.isSafeInteger(page.total_count)
          || page.total_count < 0
          || page.total_count > MAX_API_COLLECTION_ITEMS
          || (page.incomplete_results !== undefined && page.incomplete_results !== false)
          || (total !== undefined && page.total_count !== total)
          || page.length > 100
        ) throw new TypeError("CI collection is incomplete or malformed");
        total = page.total_count;
        collected += page.length;
        pages += 1;
        const hasNext = /<[^<>]+>;\s*rel="next"/.test(response.headers?.link ?? "");
        if (
          collected > total
          || pages > Math.ceil(MAX_API_COLLECTION_ITEMS / 100)
          || (hasNext && (page.length === 0 || collected === total || pages === Math.ceil(MAX_API_COLLECTION_ITEMS / 100)))
        ) throw new TypeError("CI collection exceeds its complete pagination bound");
        for (const item of page) {
          const id = positiveInteger(item?.id, "CI collection item id");
          if (ids.has(id)) throw new TypeError("CI collection contains duplicate identities");
          ids.add(id);
        }
        return page;
      });
      if (!Array.isArray(values) || total === undefined || values.length !== total || collected !== total) {
        throw new TypeError("CI collection does not contain all reported items");
      }
      return values;
    }, true);
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
      if (data.head?.repo !== null && data.head?.repo !== undefined) {
        pullRequest.headRepository = {
          owner: nonEmptyString(
            data.head.repo.owner?.login,
            "head repository owner",
          ).toLowerCase(),
          repo: nonEmptyString(data.head.repo.name, "head repository name").toLowerCase(),
        };
      }
      if (data.head?.ref !== undefined && data.head.ref !== null) {
        pullRequest.headBranch = optionalHeadBranch(data.head.ref, "live PR head branch");
      }
      if (typeof data.merged === "boolean") pullRequest.merged = data.merged;
      return pullRequest;
    },

    async listPullRequestFiles(prNumber) {
      positiveInteger(prNumber, "PR number");
      const files = await paginate("listPullRequestFiles", octokit.rest.pulls.listFiles, {
        owner, repo, pull_number: prNumber,
      });
      return files.map((file) => ({
        path: nonEmptyString(file.filename, "changed path"),
        ...(file.previous_filename === undefined ? {} : {
          previousPath: nonEmptyString(file.previous_filename, "previous changed path"),
        }),
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
      return {
        ...mappedReview(response.data),
        body: typeof response.data?.body === "string" ? response.data.body : null,
      };
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

    async listIssueComments(prNumber) {
      positiveInteger(prNumber, "PR number");
      const comments = await paginate("listIssueComments", octokit.rest.issues.listComments, {
        owner, repo, issue_number: prNumber,
      });
      // Authors stay unvalidated here: command mode skips any comment that is not from a human login.
      return comments.map((data) => ({
        id: positiveInteger(data?.id, "listed comment id"),
        issueNumber: issueNumberFromUrl(data?.issue_url),
        body: typeof data?.body === "string" ? data.body : "",
        author: typeof data?.user?.login === "string" ? data.user.login.toLowerCase() : null,
        authorType: typeof data?.user?.type === "string" ? data.user.type : null,
        edited: data?.updated_at !== data?.created_at,
        createdAt: typeof data?.created_at === "string" ? data.created_at : null,
      }));
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
      if (!isManagedMetadataLabel(label) && !isManagedConflictLabel(label)) throw new TypeError("label is not metadata/conflict-managed");
      await call("addIssueLabel", () => octokit.rest.issues.addLabels({
        owner, repo, issue_number: prNumber, labels: [label],
      }), true);
    },

    async removeIssueLabel(prNumber, label) {
      positiveInteger(prNumber, "PR number");
      if (!isManagedMetadataLabel(label) && !isManagedConflictLabel(label)) throw new TypeError("label is not metadata/conflict-managed");
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
      );
      const expectedHead = headOid.toLowerCase();
      return runs.filter((run) => (
        typeof run?.head_sha === "string"
        && run.head_sha.toLowerCase() === expectedHead
        && Array.isArray(run.pull_requests)
        && run.pull_requests.length === 1
        && run.pull_requests[0]?.number === prNumber
      )).map(mappedWorkflowRun);
    },

    async getCIState({ headOid, prNumber, baseBranch, files, headRepository, headBranch, requiredCI }) {
      const expectedHead = commitOid(headOid, "CI head OID");
      positiveInteger(prNumber, "PR number");
      nonEmptyString(baseBranch, "CI base branch");
      const expectedHeadRepository = optionalHeadRepository(headRepository);
      const expectedHeadBranch = optionalHeadBranch(headBranch);
      const workflowRuns = await paginateCI("listCIWorkflowRuns", octokit.rest.actions.listWorkflowRunsForRepo, {
        owner, repo, head_sha: expectedHead,
      });
      const checkRuns = await paginateCI("listCICheckRuns", octokit.rest.checks.listForRef, {
        owner, repo, ref: expectedHead, filter: "all",
      });
      return evaluateCI({
        repository: `${owner}/${repo}`.toLowerCase(),
        prNumber,
        headOid: expectedHead,
        baseBranch,
        files,
        headRepository: expectedHeadRepository,
        headBranch: expectedHeadBranch,
        runs: workflowRuns.map((run) => mappedCIWorkflowRun(
          run, expectedHead, prNumber, expectedHeadRepository, expectedHeadBranch,
        )).filter((run) => run !== null),
        checks: checkRuns.map(mappedCICheckRun),
        requiredCI,
      });
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

    async getEvaluationWorkflowRun(runId) {
      positiveInteger(runId, "workflow run id");
      const response = await call("getEvaluationWorkflowRun", () => octokit.rest.actions.getWorkflowRun({
        owner, repo, run_id: runId,
      }), true);
      const data = response.data;
      try {
        const workflow = evaluatorWorkflowIdentity(nonEmptyString(data?.path, "workflow path"));
        if (workflow === null) return null;
        if (!Array.isArray(data.pull_requests) || data.pull_requests.length > 100) return null;
        const pullRequestNumbers = data.pull_requests.map((pullRequest) => (
          positiveInteger(pullRequest?.number, "workflow PR number")
        ));
        if (new Set(pullRequestNumbers).size !== pullRequestNumbers.length) return null;
        const liveId = positiveInteger(data.id, "workflow run id");
        if (liveId !== runId) return null;
        return {
          id: liveId,
          name: nonEmptyString(data.name, "workflow name"),
          workflowPath: workflow.workflowPath,
          workflowSourceRef: workflow.workflowSourceRef,
          event: nonEmptyString(data.event, "workflow source event"),
          status: nonEmptyString(data.status, "workflow run status"),
          repository: nonEmptyString(data.repository?.full_name, "workflow run repository").toLowerCase(),
          pullRequestNumbers: pullRequestNumbers.sort((left, right) => left - right),
        };
      } catch {
        return null;
      }
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

    async getConflictState(prNumber) {
      positiveInteger(prNumber, "PR number");
      const response = await call("getConflictState", () => octokit.graphql(`
        query RepositoryAutomationConflictState($owner: String!, $repo: String!, $number: Int!) {
          repository(owner: $owner, name: $repo) {
            pullRequest(number: $number) {
              number id state isDraft mergeable headRefOid baseRefName
              baseRef { name target { oid } }
            }
          }
        }
      `, { owner, repo, number: prNumber }), true);
      const pullRequest = response?.repository?.pullRequest;
      const baseBranch = nonEmptyString(pullRequest?.baseRefName, "GraphQL base branch");
      if (pullRequest?.baseRef?.name !== baseBranch) {
        throw new TypeError("GraphQL live base ref does not match the PR base branch");
      }
      return {
        number: positiveInteger(pullRequest?.number, "GraphQL PR number"),
        nodeId: nonEmptyString(pullRequest?.id, "GraphQL PR node ID"),
        repository: `${owner}/${repo}`.toLowerCase(),
        state: nonEmptyString(pullRequest?.state, "GraphQL PR state").toUpperCase(),
        draft: pullRequest?.isDraft,
        headOid: nonEmptyString(pullRequest?.headRefOid, "GraphQL head OID").toLowerCase(),
        baseBranch,
        baseOid: nonEmptyString(pullRequest?.baseRef?.target?.oid, "GraphQL live base OID").toLowerCase(),
        mergeability: nonEmptyString(pullRequest?.mergeable, "GraphQL mergeability").toUpperCase(),
      };
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
      // The branch endpoint only requires the workflow's Contents-read permission.
      const response = await call("getBranchProtection", () => octokit.rest.repos.getBranch({
        owner, repo, branch,
      }), true);
      if (response.data?.name !== branch) {
        throw new Error("branch identity changed");
      }
      if (typeof response.data.protected !== "boolean") {
        throw new TypeError("branch protection flag must be a boolean");
      }
      return response.data.protected;
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

    async enableAutoMerge(nodeId, mergeMethod, expectedHeadOid) {
      nonEmptyString(nodeId, "pull request node ID");
      if (mergeMethod !== "SQUASH") throw new TypeError("auto-merge method must be SQUASH");
      const headOid = commitOid(expectedHeadOid, "expected head OID");
      const response = await call("enableAutoMerge", () => octokit.graphql(`
        mutation EnableAutoMerge($pullRequestId: ID!, $mergeMethod: PullRequestMergeMethod!, $expectedHeadOid: GitObjectID!) {
          enablePullRequestAutoMerge(input: {
            pullRequestId: $pullRequestId, mergeMethod: $mergeMethod, expectedHeadOid: $expectedHeadOid
          }) {
            clientMutationId
          }
        }
      `, { pullRequestId: nodeId, mergeMethod, expectedHeadOid: headOid }), false);
      const payload = response?.enablePullRequestAutoMerge;
      if (payload === null || typeof payload !== "object" || Array.isArray(payload)) {
        throw new TypeError("native auto-merge mutation result is absent or malformed");
      }
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
