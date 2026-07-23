"use strict";

const { Buffer } = require("node:buffer");
const { setTimeout: delay } = require("node:timers/promises");
const { TextDecoder } = require("node:util");
const {
  isManagedCherryPickLabel,
  isManagedMetadataLabel,
  isManagedPolicyLabel,
} = require("./managed-labels.js");
const { trustedWorkflowIdentity, RERUNNABLE_EVENTS } = require("./retest.js");

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
const MAX_OPEN_PULL_REQUESTS = 100;
const MAX_PULL_REQUEST_FILES = 1000;
const MAX_PULL_REQUEST_REVIEWS = 1000;
const MAX_POLICY_COMMENTS = 1000;
const EVALUATOR_WORKFLOW_PATHS = new Set([
  ".github/workflows/review-observer.yml",
  ".github/workflows/pr-metadata.yml",
  ".github/workflows/commands.yml",
]);
const MERGE_STATE_QUERY = `query RepositoryAutomationMergeState($owner: String!, $repo: String!, $number: Int!) {
  repository(owner: $owner, name: $repo) {
    nameWithOwner
    pullRequest(number: $number) {
      id
      number
      state
      isDraft
      baseRefName
      headRefOid
      mergeable
      autoMergeRequest { mergeMethod }
    }
  }
}`;
const ENABLE_AUTO_MERGE_MUTATION = `mutation RepositoryAutomationEnableAutoMerge($pullRequestId: ID!, $mergeMethod: PullRequestMergeMethod!) {
  enablePullRequestAutoMerge(input: {pullRequestId: $pullRequestId, mergeMethod: $mergeMethod}) {
    pullRequest { id }
  }
}`;
const DISABLE_AUTO_MERGE_MUTATION = `mutation RepositoryAutomationDisableAutoMerge($pullRequestId: ID!) {
  disablePullRequestAutoMerge(input: {pullRequestId: $pullRequestId}) {
    pullRequest { id }
  }
}`;

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

function gitRefName(value, name = "ref name") {
  nonEmptyString(value, name);
  if (
    value.length > 256
    || !value.startsWith("heads/")
    || /[\0-\x20\x7f~^:?*\[\]\\]/.test(value)
    || value.includes("@")
    || value.includes("//")
    || value.includes("..")
    || value.includes("@{")
  ) {
    throw new TypeError(`${name} must be a heads/ ref`);
  }
  const segments = value.split("/");
  if (
    segments.length < 2
    || segments.some((segment) => (
      segment === ""
      || segment.startsWith(".")
      || segment.endsWith(".")
      || segment.endsWith(".lock")
    ))
  ) {
    throw new TypeError(`${name} must be a heads/ ref`);
  }
  return value;
}

function commitMessage(value, name = "commit message") {
  if (typeof value !== "string" || value === "" || value.includes("\0") || value.length > MAX_CONTENT_BYTES) {
    throw new TypeError(`${name} must be bounded non-empty text`);
  }
  return value;
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
  if (separator <= 0 || value.indexOf("@", separator + 1) !== -1) return null;
  const workflowPath = value.slice(0, separator);
  const workflowSourceRef = safeWorkflowSourceRef(value.slice(separator + 1));
  return EVALUATOR_WORKFLOW_PATHS.has(workflowPath) && workflowSourceRef !== null
    ? { workflowPath, workflowSourceRef }
    : null;
}

function workflowRun(data, expected) {
  if (data === null || typeof data !== "object" || Array.isArray(data)) return null;
  try {
    const workflow = trustedWorkflowIdentity(data.path);
    const runRepository = nonEmptyString(
      data.repository?.full_name,
      "workflow run repository",
    ).toLowerCase();
    if (
      workflow === null
      // Coarse fence: any event admitted by some workflow's trusted-event
      // set in retest.js. planRetest remains the precise per-workflow
      // authority (ci.yaml <-> push, automation-ci <-> pull_request, etc.).
      || !RERUNNABLE_EVENTS.has(data.event)
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
      workflowPath: workflow.workflowPath,
      workflowSourceRef: workflow.workflowSourceRef,
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

  async function paginateLimited(operation, endpoint, parameters, limit, map = (response) => response.data) {
    let count = 0;
    let overflow = false;
    const values = await call(operation, () => octokit.paginate(
      endpoint,
      { ...parameters, per_page: 100 },
      (response, done) => {
        const page = map(response);
        if (!Array.isArray(page)) throw new TypeError(`${operation} page must be an array`);
        const remaining = (limit + 1) - count;
        const selected = remaining > 0 ? page.slice(0, remaining) : [];
        count += selected.length;
        if (count > limit) {
          overflow = true;
          if (typeof done === "function") done();
        }
        return selected;
      },
    ), true);
    if (overflow || values.length > limit) throw new TypeError(`${operation} exceeds limit`);
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

  async function readCommentPlan(prNumber, marker) {
    positiveInteger(prNumber, "PR number");
    nonEmptyString(marker, "comment marker");
    const comments = await paginateLimited("listIssueComments", octokit.rest.issues.listComments, {
      owner,
      repo,
      issue_number: prNumber,
    }, MAX_POLICY_COMMENTS);
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
    const comments = await paginateLimited("listIssueComments", octokit.rest.issues.listComments, {
      owner,
      repo,
      issue_number: prNumber,
    }, MAX_POLICY_COMMENTS);
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
        ...(typeof data.merged === "boolean" ? { merged: data.merged } : {}),
        ...(data.node_id === undefined ? {} : { nodeId: nonEmptyString(data.node_id, "live PR node ID") }),
        ...(data.base?.ref === undefined ? {} : { baseBranch: nonEmptyString(data.base.ref, "live PR base branch") }),
        baseRepository: {
          owner: nonEmptyString(data.base?.repo?.owner?.login, "base repository owner").toLowerCase(),
          repo: nonEmptyString(data.base?.repo?.name, "base repository name").toLowerCase(),
        },
      };
    },

    async getPullRequestMergeable(prNumber) {
      positiveInteger(prNumber, "PR number");
      const { data } = await call("getPullRequestMergeable", () => octokit.rest.pulls.get({
        owner, repo, pull_number: prNumber,
      }), true);
      if (data.mergeable === true) return "MERGEABLE";
      if (data.mergeable === false) return "CONFLICTING";
      return "UNKNOWN";
    },

    async listOpenPullRequestNumbers() {
      const pulls = await paginateLimited("listOpenPullRequests", octokit.rest.pulls.list, {
        owner, repo, state: "open",
      }, MAX_OPEN_PULL_REQUESTS);
      const numbers = pulls.map((pull) => positiveInteger(pull.number, "open PR number"));
      if (new Set(numbers).size !== numbers.length) throw new TypeError("open PR scan contains duplicates");
      return numbers.sort((left, right) => left - right);
    },

    async getEvaluationWorkflowRun(runId) {
      positiveInteger(runId, "workflow run id");
      const { data } = await call("getEvaluationWorkflowRun", () => octokit.rest.actions.getWorkflowRun({
        owner, repo, run_id: runId,
      }), true);
      try {
        const runRepository = nonEmptyString(data.repository?.full_name, "workflow run repository").toLowerCase();
        const workflow = evaluatorWorkflowIdentity(nonEmptyString(data.path, "workflow path"));
        if (workflow === null) return null;
        if (!Array.isArray(data.pull_requests) || data.pull_requests.length > 100) return null;
        const pullRequestNumbers = data.pull_requests.map((pull) => positiveInteger(pull?.number, "workflow PR number"));
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
          repository: runRepository,
          pullRequestNumbers: pullRequestNumbers.sort((left, right) => left - right),
        };
      } catch {
        return null;
      }
    },

    async getBranchProtection(branch) {
      nonEmptyString(branch, "branch");
      const { data } = await call("getBranchProtection", () => octokit.rest.repos.getBranch({
        owner, repo, branch,
      }), true);
      return typeof data?.protected === "boolean" ? data.protected : null;
    },

    async getMergeState(prNumber) {
      positiveInteger(prNumber, "PR number");
      const data = await call("getMergeState", () => octokit.graphql(MERGE_STATE_QUERY, {
        owner, repo, number: prNumber,
      }), true);
      const repository = data?.repository;
      const pull = repository?.pullRequest;
      if (repository === null || pull === null || typeof repository !== "object" || typeof pull !== "object") {
        throw new TypeError("GraphQL pull request state is missing");
      }
      const method = pull.autoMergeRequest === null
        ? null
        : nonEmptyString(pull.autoMergeRequest?.mergeMethod, "auto-merge method");
      const result = {
        number: positiveInteger(pull.number, "GraphQL PR number"),
        nodeId: nonEmptyString(pull.id, "GraphQL PR node ID"),
        state: nonEmptyString(pull.state, "GraphQL PR state"),
        draft: pull.isDraft,
        baseBranch: nonEmptyString(pull.baseRefName, "GraphQL base branch"),
        headOid: gitOid(pull.headRefOid, "GraphQL head OID"),
        mergeability: nonEmptyString(pull.mergeable, "GraphQL mergeability"),
        autoMergeMethod: method,
        repository: nonEmptyString(repository.nameWithOwner, "GraphQL repository").toLowerCase(),
      };
      if (typeof result.draft !== "boolean" || !["OPEN", "CLOSED", "MERGED"].includes(result.state)) {
        throw new TypeError("GraphQL pull request state is malformed");
      }
      if (!["MERGEABLE", "CONFLICTING", "UNKNOWN"].includes(result.mergeability)) {
        throw new TypeError("GraphQL mergeability is malformed");
      }
      if (result.autoMergeMethod !== null && !["MERGE", "REBASE", "SQUASH"].includes(result.autoMergeMethod)) {
        throw new TypeError("GraphQL auto-merge method is malformed");
      }
      return result;
    },

    async enableAutoMerge(pullRequestId, mergeMethod) {
      nonEmptyString(pullRequestId, "pull request node ID");
      if (mergeMethod !== "SQUASH") throw new TypeError("native auto-merge method must be SQUASH");
      const data = await call("enableAutoMerge", () => octokit.graphql(ENABLE_AUTO_MERGE_MUTATION, {
        pullRequestId,
        mergeMethod,
      }), false);
      if (data?.enablePullRequestAutoMerge?.pullRequest?.id !== pullRequestId) {
        throw new TypeError("enable auto-merge response is ambiguous");
      }
    },

    async disableAutoMerge(pullRequestId) {
      nonEmptyString(pullRequestId, "pull request node ID");
      const data = await call("disableAutoMerge", () => octokit.graphql(DISABLE_AUTO_MERGE_MUTATION, {
        pullRequestId,
      }), false);
      if (data?.disablePullRequestAutoMerge?.pullRequest?.id !== pullRequestId) {
        throw new TypeError("disable auto-merge response is ambiguous");
      }
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
      const files = await paginateLimited("listPullRequestFiles", octokit.rest.pulls.listFiles, {
        owner, repo, pull_number: prNumber,
      }, MAX_PULL_REQUEST_FILES);
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
      const reviews = await paginateLimited("listPullRequestReviews", octokit.rest.pulls.listReviews, {
        owner, repo, pull_number: prNumber,
      }, MAX_PULL_REQUEST_REVIEWS);
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

    async addCherryPickLabel(prNumber, label) {
      positiveInteger(prNumber, "PR number");
      if (!isManagedCherryPickLabel(label)) throw new TypeError("label is not cherry-pick-managed");
      await call("addCherryPickLabel", () => octokit.rest.issues.addLabels({
        owner, repo, issue_number: prNumber, labels: [label],
      }), true);
    },

    async removeCherryPickLabel(prNumber, label) {
      positiveInteger(prNumber, "PR number");
      if (!isManagedCherryPickLabel(label)) throw new TypeError("label is not cherry-pick-managed");
      try {
        await call("removeCherryPickLabel", () => octokit.rest.issues.removeLabel({
          owner, repo, issue_number: prNumber, name: label,
        }), true);
      } catch (error) {
        if (error.status !== 404) throw error;
      }
    },

    async ensureLabel(label) {
      const requested = copyLabel(label);
      nonEmptyString(requested.name, "label name");
      try {
        const existing = await call("getLabel", () => octokit.rest.issues.getLabel({
          owner, repo, name: requested.name,
        }), true);
        return copyLabel(existing.data);
      } catch (error) {
        if (error.status !== 404) throw error;
      }
      const created = await call("createLabel", () => octokit.rest.issues.createLabel({
        owner, repo, ...requested,
      }), false);
      return copyLabel(created.data);
    },

    async getBranch(branch) {
      nonEmptyString(branch, "branch");
      try {
        const { data } = await call("getBranch", () => octokit.rest.repos.getBranch({
          owner, repo, branch,
        }), true);
        return {
          name: nonEmptyString(data?.name, "branch name"),
          oid: gitOid(data?.commit?.sha, "branch commit OID"),
        };
      } catch (error) {
        if (error.status === 404) return null;
        throw error;
      }
    },

    async getCommitInfo(oid) {
      const commitOid = gitOid(oid, "commit OID");
      const { data } = await call("getCommitInfo", () => octokit.rest.git.getCommit({
        owner, repo, commit_sha: commitOid,
      }), true);
      if (!Array.isArray(data?.parents)) {
        throw new TypeError("commit parents must be an array");
      }
      return {
        oid: gitOid(data.sha, "commit OID"),
        treeOid: gitOid(data.tree?.sha, "commit tree OID"),
        parents: data.parents.map((parent) => gitOid(parent?.sha, "commit parent OID")),
        message: typeof data.message === "string" ? data.message : "",
        author: {
          name: nonEmptyString(data.author?.name, "commit author name"),
          email: nonEmptyString(data.author?.email, "commit author email"),
          date: nonEmptyString(data.author?.date, "commit author date"),
        },
      };
    },

    async createCommit({ message, treeOid, parentOids, author } = {}) {
      commitMessage(message);
      const tree = gitOid(treeOid, "commit tree OID");
      if (!Array.isArray(parentOids) || parentOids.length === 0 || parentOids.length > 16) {
        throw new TypeError("commit parents must be a bounded non-empty array");
      }
      const parents = parentOids.map((parent) => gitOid(parent, "commit parent OID"));
      const request = { owner, repo, message, tree, parents };
      if (author !== undefined) {
        request.author = {
          name: nonEmptyString(author?.name, "commit author name"),
          email: nonEmptyString(author?.email, "commit author email"),
          ...(author?.date === undefined ? {} : { date: nonEmptyString(author.date, "commit author date") }),
        };
      }
      const { data } = await call("createCommit", () => octokit.rest.git.createCommit(request), false);
      return gitOid(data?.sha, "created commit OID");
    },

    async createRef(name, oid) {
      gitRefName(name);
      const sha = gitOid(oid, "ref OID");
      await call("createRef", () => octokit.rest.git.createRef({
        owner, repo, ref: `refs/${name}`, sha,
      }), false);
    },

    async updateRef(name, oid, force = true) {
      gitRefName(name);
      const sha = gitOid(oid, "ref OID");
      if (typeof force !== "boolean") throw new TypeError("force must be a boolean");
      await call("updateRef", () => octokit.rest.git.updateRef({
        owner, repo, ref: name, sha, force,
      }), true);
    },

    async deleteRef(name) {
      gitRefName(name);
      await call("deleteRef", () => octokit.rest.git.deleteRef({
        owner, repo, ref: name,
      }), false);
    },

    async mergeBranches(base, head) {
      nonEmptyString(base, "merge base");
      nonEmptyString(head, "merge head");
      let response;
      try {
        response = await call("mergeBranches", () => octokit.rest.repos.merge({
          owner, repo, base, head,
        }), false);
      } catch (error) {
        if (error.status === 409) return { merged: false };
        throw error;
      }
      if (response?.status === 204) {
        return { merged: false, alreadyMerged: true };
      }
      if (typeof response?.data?.sha !== "string") {
        return { merged: false };
      }
      return {
        merged: true,
        oid: gitOid(response.data.sha, "merge commit OID"),
        treeOid: gitOid(response.data.commit?.tree?.sha, "merge tree OID"),
      };
    },

    async createPullRequest({ base, head, title, body } = {}) {
      nonEmptyString(base, "pull request base");
      nonEmptyString(head, "pull request head");
      nonEmptyString(title, "pull request title");
      if (typeof body !== "string") throw new TypeError("pull request body must be a string");
      const { data } = await call("createPullRequest", () => octokit.rest.pulls.create({
        owner, repo, base, head, title, body,
      }), false);
      return {
        number: positiveInteger(data?.number, "created pull request number"),
        url: nonEmptyString(data?.html_url, "created pull request URL"),
      };
    },

    async findOpenPullRequest(head, base) {
      nonEmptyString(head, "pull request head");
      nonEmptyString(base, "pull request base");
      const pulls = await paginateLimited("findOpenPullRequest", octokit.rest.pulls.list, {
        owner, repo, state: "open", base, head: `${owner}:${head}`,
      }, MAX_OPEN_PULL_REQUESTS);
      const match = pulls.find((pull) => (
        pull?.base?.ref === base && pull?.head?.ref === head
      ));
      return match === undefined ? null : {
        number: positiveInteger(match.number, "open pull request number"),
        url: nonEmptyString(match.html_url, "open pull request URL"),
      };
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
