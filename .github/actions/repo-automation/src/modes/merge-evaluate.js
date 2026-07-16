"use strict";

const { evaluateApprovalCoverage } = require("../approval-coverage.js");
const { currentLgtm, parsePolicyState } = require("../commands/state.js");
const { validateConfig } = require("../config.js");
const { decideMergeAction } = require("../merge-state.js");
const { parseAliases, parseOwnersFile, resolveOwners } = require("../owners.js");
const {
  POLICY_COMMENT_MARKER,
  hasValidPolicyCommentStructure,
  parseMetadataHeadEvidence,
} = require("../policy-comment.js");

const LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const REPOSITORY = /^[A-Za-z0-9_.-]{1,100}$/;
const OID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const MAX_CANDIDATES = 100;
const MAX_FILES = 1000;
const MAX_REVIEWS = 1000;
const TRUSTED_WORKFLOWS = Object.freeze(new Map([
  ["Review observer", Object.freeze({
    path: ".github/workflows/review-observer.yml",
    event: "pull_request_review",
    allOpen: false,
  })],
  ["PR metadata", Object.freeze({
    path: ".github/workflows/pr-metadata.yml",
    event: "pull_request_target",
    allOpen: false,
  })],
  ["Commands", Object.freeze({
    path: ".github/workflows/commands.yml",
    event: "issue_comment",
    allOpen: true,
  })],
]));

function eventRepository(event) {
  const owner = event?.repository?.owner?.login;
  const repo = event?.repository?.name;
  const fullName = event?.repository?.full_name;
  if (
    typeof owner !== "string"
    || !LOGIN.test(owner)
    || typeof repo !== "string"
    || !REPOSITORY.test(repo)
    || repo === "."
    || repo === ".."
    || typeof fullName !== "string"
    || fullName.toLowerCase() !== `${owner}/${repo}`.toLowerCase()
  ) throw new TypeError("event repository identity is invalid");
  return { owner: owner.toLowerCase(), repo: repo.toLowerCase(), fullName: fullName.toLowerCase() };
}

function explicitNumber(value) {
  if (value === undefined || value === null || value === "") return null;
  const text = typeof value === "number" ? String(value) : value;
  if (typeof text !== "string" || !/^[1-9][0-9]*$/.test(text)) {
    throw new TypeError("explicit pull request number must be strictly numeric");
  }
  const number = Number(text);
  if (!Number.isSafeInteger(number)) throw new TypeError("explicit pull request number is out of range");
  return number;
}

function boundedCandidates(numbers) {
  if (!Array.isArray(numbers) || numbers.length > MAX_CANDIDATES) {
    throw new TypeError("open pull request scan exceeds limit");
  }
  const unique = new Set();
  for (const number of numbers) {
    if (!Number.isSafeInteger(number) || number <= 0 || unique.has(number)) {
      throw new TypeError("pull request candidate mapping is invalid");
    }
    unique.add(number);
  }
  return [...unique].sort((left, right) => left - right);
}

function trustedRun(run, repository) {
  if (
    run === null
    || typeof run !== "object"
    || run.status !== "completed"
    || run.repository !== repository.fullName
  ) return null;
  const expected = TRUSTED_WORKFLOWS.get(run.name);
  if (
    expected === undefined
    || run.workflowPath !== expected.path
    || run.event !== expected.event
  ) return null;
  return expected;
}

async function candidatesFor({ event, github, repository, prNumber }) {
  const explicit = explicitNumber(prNumber);
  if (explicit !== null) return [explicit];
  if (event?.workflow_run !== undefined) {
    if (event.action !== "completed" || !Number.isSafeInteger(event.workflow_run?.id) || event.workflow_run.id <= 0) {
      throw new TypeError("workflow completion event is invalid");
    }
    const run = await github.getEvaluationWorkflowRun(event.workflow_run.id);
    if (run?.id !== event.workflow_run.id) return [];
    const expected = trustedRun(run, repository);
    if (expected === null) return [];
    if (expected.allOpen) return boundedCandidates(await github.listOpenPullRequestNumbers());
    return boundedCandidates(run.pullRequestNumbers);
  }
  return boundedCandidates(await github.listOpenPullRequestNumbers());
}

function activeOwnerPaths(config) {
  return [...new Set(config.policy.activeOwnerFiles)].sort();
}

function validatePullRequest(pullRequest, number, repository) {
  if (
    pullRequest === null
    || typeof pullRequest !== "object"
    || pullRequest.number !== number
    || pullRequest.state !== "open"
    || typeof pullRequest.draft !== "boolean"
    || typeof pullRequest.author !== "string"
    || !LOGIN.test(pullRequest.author)
    || typeof pullRequest.headOid !== "string"
    || !OID.test(pullRequest.headOid)
    || typeof pullRequest.baseBranch !== "string"
    || pullRequest.baseBranch === ""
    || typeof pullRequest.nodeId !== "string"
    || pullRequest.nodeId === ""
    || pullRequest.baseRepository?.owner?.toLowerCase() !== repository.owner
    || pullRequest.baseRepository?.repo?.toLowerCase() !== repository.repo
  ) throw new Error("live pull request state or base repository is invalid");
  return pullRequest;
}

function sameHeadIdentity(left, right) {
  return right !== null
    && left.number === right.number
    && left.state === right.state
    && left.headOid === right.headOid
    && left.baseBranch === right.baseBranch
    && left.nodeId === right.nodeId
    && left.baseRepository.owner.toLowerCase() === right.baseRepository.owner.toLowerCase()
    && left.baseRepository.repo.toLowerCase() === right.baseRepository.repo.toLowerCase();
}

function branchAllowed(branch, configured) {
  if (!Array.isArray(configured)) return false;
  return configured.some((pattern) => {
    if (pattern === branch) return true;
    if (typeof pattern !== "string" || !pattern.endsWith("*") || pattern.slice(0, -1).includes("*")) return false;
    return branch.startsWith(pattern.slice(0, -1));
  });
}

function labelPlan(currentLabels, { lgtm, approved }) {
  const current = new Map(currentLabels.map((label) => [label.toLowerCase(), label]));
  const desired = new Map([
    ["lgtm", lgtm],
    ["approved", approved],
    ["do-not-merge/needs-approval", !approved],
  ]);
  const add = [];
  const remove = [];
  for (const [label, wanted] of desired) {
    if (wanted && !current.has(label)) add.push(label);
    if (!wanted && current.has(label)) remove.push(current.get(label));
  }
  return { add: add.sort(), remove: remove.sort() };
}

function applyLabelPlan(labels, plan) {
  const removed = new Set(plan.remove.map((label) => label.toLowerCase()));
  const result = labels.filter((label) => !removed.has(label.toLowerCase()));
  const present = new Set(result.map((label) => label.toLowerCase()));
  for (const label of plan.add) if (!present.has(label)) result.push(label);
  return result;
}

function validateGraphState(state, pullRequest, repository) {
  if (
    state === null
    || typeof state !== "object"
    || state.number !== pullRequest.number
    || state.nodeId !== pullRequest.nodeId
    || state.repository !== repository.fullName
    || state.baseBranch !== pullRequest.baseBranch
    || typeof state.draft !== "boolean"
    || !["OPEN", "CLOSED", "MERGED"].includes(state.state)
    || !["MERGEABLE", "CONFLICTING", "UNKNOWN"].includes(state.mergeability)
    || ![null, "MERGE", "REBASE", "SQUASH"].includes(state.autoMergeMethod)
  ) throw new Error("live GraphQL pull request state is inconsistent");
  return state;
}

async function loadAuthority({ github, config, pullRequest }) {
  const [files, reviews, labels, comment, revision] = await Promise.all([
    github.listPullRequestFiles(pullRequest.number),
    github.listPullRequestReviews(pullRequest.number),
    github.listIssueLabels(pullRequest.number),
    github.getPolicyComment(pullRequest.number, POLICY_COMMENT_MARKER),
    github.getDefaultBranchRevision(),
  ]);
  if (!Array.isArray(files) || files.length === 0 || files.length > MAX_FILES) {
    throw new TypeError("pull request file scan exceeds limit");
  }
  if (!Array.isArray(reviews) || reviews.length > MAX_REVIEWS || !Array.isArray(labels)) {
    throw new TypeError("pull request review or label scan exceeds limit");
  }
  const paths = activeOwnerPaths(config);
  const declarations = [];
  for (const path of paths) {
    declarations.push(parseOwnersFile(await github.getContentAtRevision(path, revision), path));
  }
  const aliases = parseAliases(await github.getContentAtRevision("/OWNERS_ALIASES", revision));
  const ownership = resolveOwners(
    files.map((file) => file.path),
    declarations,
    aliases,
    { activeOwnerFiles: paths, pullRequestAuthor: pullRequest.author },
  );
  const approval = evaluateApprovalCoverage({
    files: ownership.files.map((file) => ({ path: file.path, approvers: file.approvers })),
    reviews,
    headOid: pullRequest.headOid,
    author: pullRequest.author,
  });
  const structurallyOwned = comment?.body !== null && hasValidPolicyCommentStructure(comment.body);
  const state = structurallyOwned ? parsePolicyState(comment.body) : null;
  const liveLgtm = state === null ? null : currentLgtm(state, pullRequest.headOid);
  const metadataHead = structurallyOwned ? parseMetadataHeadEvidence(comment.body) : null;
  return {
    approved: approval.approved && ownership.uncoveredPaths.length === 0,
    labels,
    lgtm: liveLgtm,
    lgtmOwned: state !== null,
    metadataHead,
  };
}

function raceResult(number, headOid, attempts, authority = undefined) {
  return {
    number,
    headOid,
    attempts,
    lgtm: authority?.lgtm !== null && authority?.lgtm !== undefined,
    approved: authority?.approved === true,
    labels: authority?.labelsPlan ?? { add: [], remove: [] },
    merge: { action: "NOOP", blockers: ["head-changed"] },
  };
}

async function reconcileAttempt({ github, config, repository, number, dryRun, attempts }) {
  const pullRequest = validatePullRequest(await github.getPullRequest(number), number, repository);
  const authority = await loadAuthority({ github, config, pullRequest });
  const initialGraph = validateGraphState(
    await github.getMergeState(number),
    pullRequest,
    repository,
  );
  const protectedLive = await github.getBranchProtection(pullRequest.baseBranch);
  const labelsPlan = labelPlan(authority.labels, {
    lgtm: authority.lgtm !== null,
    approved: authority.approved,
  });
  authority.labelsPlan = labelsPlan;
  if (initialGraph.headOid !== pullRequest.headOid) {
    return { race: true, result: raceResult(number, pullRequest.headOid, attempts, authority) };
  }

  const preWrite = validatePullRequest(await github.getPullRequest(number), number, repository);
  if (!sameHeadIdentity(pullRequest, preWrite)) {
    return { race: true, result: raceResult(number, pullRequest.headOid, attempts, authority) };
  }

  if (!dryRun) {
    for (const label of labelsPlan.add) await github.addPolicyLabel(number, label);
    for (const label of labelsPlan.remove) await github.removePolicyLabel(number, label);
  }

  const finalPullRequest = validatePullRequest(await github.getPullRequest(number), number, repository);
  const [liveLabels, finalGraphRaw] = await Promise.all([
    github.listIssueLabels(number),
    github.getMergeState(number),
  ]);
  if (!sameHeadIdentity(pullRequest, finalPullRequest)) {
    return { race: true, result: raceResult(number, pullRequest.headOid, attempts, authority) };
  }
  const finalGraph = validateGraphState(finalGraphRaw, finalPullRequest, repository);
  if (finalGraph.headOid !== pullRequest.headOid) {
    return { race: true, result: raceResult(number, pullRequest.headOid, attempts, authority) };
  }
  const decisionLabels = dryRun ? applyLabelPlan(liveLabels, labelsPlan) : liveLabels;
  const merge = decideMergeAction({
    pullRequestState: finalGraph.state === "OPEN" ? "OPEN" : "CLOSED",
    draft: finalGraph.draft,
    baseBranch: finalGraph.baseBranch,
    baseBranchAllowed: branchAllowed(finalGraph.baseBranch, config.policy.protectedBranches),
    baseBranchProtected: protectedLive === true,
    headOid: pullRequest.headOid,
    finalHeadOid: finalGraph.headOid,
    metadataHeadOid: authority.metadataHead ?? "0".repeat(40),
    approvalHeadOid: pullRequest.headOid,
    lgtm: authority.lgtm,
    lgtmStateOwnedByBot: authority.lgtmOwned,
    approvalCoverageComplete: authority.approved,
    mergeability: finalGraph.mergeability,
    labels: decisionLabels,
    loadError: false,
    ciState: "PENDING",
    autoMergeMethod: finalGraph.autoMergeMethod,
  });
  if (!dryRun && merge.action === "ENABLE") {
    await github.enableAutoMerge(finalGraph.nodeId, "SQUASH");
  } else if (!dryRun && merge.action === "DISABLE") {
    await github.disableAutoMerge(finalGraph.nodeId);
  }
  return {
    race: false,
    result: {
      number,
      headOid: pullRequest.headOid,
      attempts,
      lgtm: authority.lgtm !== null,
      approved: authority.approved,
      labels: labelsPlan,
      merge,
    },
  };
}

async function reconcile({ github, config, repository, number, dryRun }) {
  let last;
  for (let attempts = 1; attempts <= 2; attempts += 1) {
    const attempt = await reconcileAttempt({ github, config, repository, number, dryRun, attempts });
    last = attempt.result;
    if (!attempt.race) return last;
  }
  return last;
}

async function runMergeEvaluate({ event, github, config, dryRun, prNumber = "" }) {
  if (typeof dryRun !== "boolean") throw new TypeError("dry-run must be a boolean");
  validateConfig(config);
  const repository = eventRepository(event);
  const candidates = await candidatesFor({ event, github, repository, prNumber });
  const pullRequests = [];
  let partial = false;
  for (const number of candidates) {
    try {
      pullRequests.push(await reconcile({ github, config, repository, number, dryRun }));
    } catch (error) {
      partial = true;
      pullRequests.push({
        number,
        error: error instanceof Error ? error.message : "pull request evaluation failed",
      });
    }
  }
  return {
    status: partial ? "partial" : (dryRun ? "planned" : "complete"),
    candidates,
    pullRequests,
  };
}

module.exports = { runMergeEvaluate };
