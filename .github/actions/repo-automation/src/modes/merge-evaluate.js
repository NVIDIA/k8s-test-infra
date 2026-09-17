"use strict";

const { evaluateApprovalCoverage } = require("../approval-coverage.js");
const { parseCommands } = require("../commands/parser.js");
const {
  currentEvidence,
  currentHold,
  parsePolicyState,
} = require("../commands/state.js");
const { validateConfig } = require("../config.js");
const { isManagedPolicyLabel } = require("../managed-labels.js");
const { decideMergeAction } = require("../merge-state.js");
const { parseAliases, parseOwnersFile, resolveOwners } = require("../owners.js");
const {
  POLICY_COMMENT_MARKER,
  parseMetadataHeadEvidence,
} = require("../policy-comment.js");
const { policyDigest } = require("../policy-digest.js");

const LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const REPOSITORY = /^[A-Za-z0-9_.-]{1,100}$/;
const OID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const MAX_CANDIDATES = 100;
const MAX_FILES = 1000;
const MAX_REVIEWS = 1000;
const ZERO_OID = "0".repeat(40);
const SUCCESS_SUMMARY = "Repository merge policy passed.";

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
  return {
    owner: owner.toLowerCase(),
    repo: repo.toLowerCase(),
    fullName: fullName.toLowerCase(),
  };
}

function explicitNumber(value) {
  if (value === undefined || value === null || value === "") return null;
  const text = typeof value === "number" ? String(value) : value;
  if (typeof text !== "string" || !/^[1-9][0-9]*$/.test(text)) {
    throw new TypeError("explicit pull request number must be strictly numeric");
  }
  const number = Number(text);
  if (!Number.isSafeInteger(number)) {
    throw new TypeError("explicit pull request number is out of range");
  }
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

async function candidatesFor({ event, eventName, github, prNumber }) {
  const explicit = explicitNumber(prNumber);
  if (eventName === "schedule") {
    if (explicit !== null || typeof event?.schedule !== "string" || event.schedule === "") {
      throw new TypeError("schedule event is invalid");
    }
    return boundedCandidates(await github.listOpenPullRequestNumbers());
  }
  if (eventName === "workflow_dispatch") {
    if (event?.workflow_run !== undefined || event?.schedule !== undefined) {
      throw new TypeError("workflow_dispatch event is ambiguous");
    }
    return explicit === null
      ? boundedCandidates(await github.listOpenPullRequestNumbers())
      : [explicit];
  }
  throw new TypeError("unsupported evaluator event name");
}

function validatePullRequest(pullRequest, number, repository) {
  if (
    pullRequest === null
    || typeof pullRequest !== "object"
    || pullRequest.number !== number
    || typeof pullRequest.draft !== "boolean"
    || typeof pullRequest.author !== "string"
    || !LOGIN.test(pullRequest.author)
    || typeof pullRequest.headOid !== "string"
    || !OID.test(pullRequest.headOid)
    || typeof pullRequest.baseBranch !== "string"
    || pullRequest.baseBranch === ""
    || typeof pullRequest.nodeId !== "string"
    || pullRequest.nodeId === ""
    || typeof pullRequest.state !== "string"
    || pullRequest.baseRepository?.owner?.toLowerCase() !== repository.owner
    || pullRequest.baseRepository?.repo?.toLowerCase() !== repository.repo
  ) throw new Error("live pull request state or base repository is invalid");
  return pullRequest;
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
    || typeof state.headOid !== "string"
    || !OID.test(state.headOid)
  ) throw new Error("live GraphQL pull request state is inconsistent");
  return state;
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
    if (
      typeof pattern !== "string"
      || !pattern.endsWith("*")
      || pattern.slice(0, -1).includes("*")
    ) return false;
    return branch.startsWith(pattern.slice(0, -1));
  });
}

function activeOwnerPaths(config) {
  return [...new Set(config.policy.activeOwnerFiles)].sort();
}

function validHumanIdentity(identity, expectedLogin) {
  return identity?.resolved === true
    && identity?.deleted === false
    && identity?.type === "User"
    && typeof identity.login === "string"
    && identity.login.toLowerCase() === expectedLogin;
}

async function validateCommentEvidence({ github, evidence, command, pullRequest }) {
  if (evidence.sourceType !== "comment") return false;
  try {
    const [comment, identity] = await Promise.all([
      github.getIssueComment(evidence.sourceId),
      github.getUserIdentity(evidence.actor),
    ]);
    if (
      comment?.id !== evidence.sourceId
      || comment.issueNumber !== pullRequest.number
      || comment.author?.toLowerCase() !== evidence.actor
      || comment.authorType !== "User"
      || comment.edited !== false
      || !validHumanIdentity(identity, evidence.actor)
    ) return false;
    const parsed = parseCommands(comment.body);
    return parsed.commands.some((candidate) => candidate.name === command);
  } catch {
    return false;
  }
}

function currentRoleAllows(evidence, kind, ownership) {
  const reviewers = new Set(ownership.reviewerCandidates);
  const approvers = new Set(ownership.approverCandidates);
  if (kind === "approval") {
    return evidence.actorRole === "approver" && approvers.has(evidence.actor);
  }
  return (
    (evidence.actorRole === "reviewer" && reviewers.has(evidence.actor))
    || (evidence.actorRole === "approver" && approvers.has(evidence.actor))
  );
}

async function validStoredEvidence({ github, records, kind, ownership, pullRequest }) {
  const validated = [];
  for (const evidence of records) {
    if (
      evidence.actor === pullRequest.author.toLowerCase()
      || !currentRoleAllows(evidence, kind, ownership)
    ) continue;
    const valid = kind === "approval" && evidence.sourceType === "review"
      ? await validateReviewEvidence({ github, evidence, pullRequest })
      : await validateCommentEvidence({
        github,
        evidence,
        command: kind === "approval" ? "approve" : "lgtm",
        pullRequest,
      });
    if (valid) validated.push(evidence);
  }
  return validated;
}

async function validateReviewEvidence({ github, evidence, pullRequest }) {
  try {
    const [review, identity] = await Promise.all([
      github.getPullRequestReview(pullRequest.number, evidence.sourceId),
      github.getUserIdentity(evidence.actor),
    ]);
    return review?.id === evidence.sourceId
      && review.user === evidence.actor
      && review.state === "APPROVED"
      && review.commitOid === pullRequest.headOid
      && validHumanIdentity(identity, evidence.actor);
  } catch {
    return false;
  }
}

async function validReviewApprovers({ github, effectiveReviews, ownership, pullRequest }) {
  const eligible = new Set(ownership.approverCandidates);
  const result = new Set();
  for (const review of effectiveReviews) {
    if (
      review.state !== "APPROVED"
      || review.commitOid !== pullRequest.headOid
      || review.user === pullRequest.author.toLowerCase()
      || !eligible.has(review.user)
    ) continue;
    try {
      const identity = await github.getUserIdentity(review.user);
      if (validHumanIdentity(identity, review.user)) result.add(review.user);
    } catch {
      // Identity resolution fails closed for this review.
    }
  }
  return result;
}

function approvalCoverage(ownership, approvers) {
  return ownership.uncoveredPaths.length === 0
    && ownership.files.every((file) => file.approvers.some((actor) => approvers.has(actor)));
}

function policyCommentState(comment, context) {
  if (
    typeof comment?.body !== "string"
    || comment.body.split(POLICY_COMMENT_MARKER).length - 1 !== 1
  ) return { state: null, metadataHead: null };
  const state = parsePolicyState(comment.body);
  if (
    state === null
    || state.repository !== context.repository
    || state.pullRequest !== context.pullRequest
  ) return { state: null, metadataHead: null };
  return { state, metadataHead: parseMetadataHeadEvidence(comment.body) };
}

async function loadAuthority({ github, config, repository, pullRequest }) {
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
  if (
    !Array.isArray(reviews)
    || reviews.length > MAX_REVIEWS
    || !Array.isArray(labels)
    || typeof revision !== "string"
    || !OID.test(revision)
  ) throw new TypeError("pull request authority input is invalid");

  const sources = [];
  const declarations = [];
  for (const path of activeOwnerPaths(config)) {
    const source = await github.getContentAtRevision(path, revision);
    if (typeof source !== "string") throw new TypeError("OWNERS source is invalid");
    sources.push({ path, source });
    declarations.push(parseOwnersFile(source, path));
  }
  const aliasesSource = await github.getContentAtRevision("/OWNERS_ALIASES", revision);
  const aliases = parseAliases(aliasesSource);
  const ownership = resolveOwners(
    files.map((file) => file.path),
    declarations,
    aliases,
    {
      activeOwnerFiles: activeOwnerPaths(config),
      pullRequestAuthor: pullRequest.author,
    },
  );
  const digest = policyDigest({
    repository: repository.fullName,
    revision,
    policy: config.policy,
    ownerSources: sources,
    aliasesSource,
  });
  const context = {
    repository: repository.fullName,
    pullRequest: pullRequest.number,
    policyDigest: digest,
    headOid: pullRequest.headOid,
  };
  const parsed = policyCommentState(comment, context);
  const lgtmRecords = parsed.state === null
    ? []
    : currentEvidence(parsed.state, "lgtms", context);
  const approvalRecords = parsed.state === null
    ? []
    : currentEvidence(parsed.state, "approvals", context);
  const [lgtms, approvals] = await Promise.all([
    validStoredEvidence({
      github,
      records: lgtmRecords,
      kind: "lgtm",
      ownership,
      pullRequest,
    }),
    validStoredEvidence({
      github,
      records: approvalRecords,
      kind: "approval",
      ownership,
      pullRequest,
    }),
  ]);
  const reviewResult = evaluateApprovalCoverage({
    files: ownership.files.map((file) => ({ path: file.path, approvers: file.approvers })),
    reviews,
    headOid: pullRequest.headOid,
    author: pullRequest.author,
  });
  const reviewApprovers = await validReviewApprovers({
    github,
    effectiveReviews: reviewResult.effectiveReviews,
    ownership,
    pullRequest,
  });
  const approvers = new Set([...approvals.map((record) => record.actor), ...reviewApprovers]);
  const hold = parsed.state === null ? null : currentHold(parsed.state, context);
  return {
    labels,
    lgtm: lgtms[0] ?? null,
    lgtmOwned: parsed.state !== null,
    approved: approvalCoverage(ownership, approvers),
    holdActive: hold !== null,
    metadataHead: parsed.metadataHead,
  };
}

function desiredPolicyLabels(authority) {
  return new Map([
    ["lgtm", authority.lgtm !== null],
    ["approved", authority.approved],
    ["do-not-merge/hold", authority.holdActive],
    ["do-not-merge/needs-approval", !authority.approved],
  ]);
}

function labelPlan(currentLabels, authority) {
  const current = new Map(currentLabels.map((label) => [label.toLowerCase(), label]));
  const add = [];
  const remove = [];
  for (const [label, wanted] of desiredPolicyLabels(authority)) {
    if (wanted && !current.has(label)) add.push(label);
    if (!wanted && current.has(label)) remove.push(current.get(label));
  }
  return { add: add.sort(), remove: remove.sort() };
}

function effectivePolicyLabels(labels, authority) {
  const unmanaged = labels.filter((label) => !isManagedPolicyLabel(label));
  for (const [label, wanted] of desiredPolicyLabels(authority)) {
    if (wanted) unmanaged.push(label);
  }
  return unmanaged;
}

function toMergeDecision({ config, pullRequest, graph, authority, protectedBranch }) {
  return decideMergeAction({
    pullRequestState: graph.state === "OPEN" ? "OPEN" : "CLOSED",
    draft: graph.draft,
    baseBranch: graph.baseBranch,
    baseBranchAllowed: branchAllowed(graph.baseBranch, config.policy.protectedBranches),
    baseBranchProtected: protectedBranch,
    headOid: pullRequest.headOid,
    holdActive: authority.holdActive,
    finalHeadOid: graph.headOid,
    metadataHeadOid: authority.metadataHead ?? ZERO_OID,
    approvalHeadOid: pullRequest.headOid,
    lgtm: authority.lgtm === null ? null : {
      actor: authority.lgtm.actor,
      commentId: authority.lgtm.sourceId,
      headOid: authority.lgtm.headOid,
      createdAt: authority.lgtm.createdAt,
    },
    lgtmStateOwnedByBot: authority.lgtmOwned,
    approvalCoverageComplete: authority.approved,
    mergeability: graph.mergeability,
    labels: effectivePolicyLabels(authority.labels, authority),
    loadError: false,
    ciState: "PENDING",
    autoMergeMethod: graph.autoMergeMethod,
  });
}

async function loadEvaluation({ github, config, repository, number }) {
  const pullRequest = validatePullRequest(
    await github.getPullRequest(number),
    number,
    repository,
  );
  const graph = validateGraphState(
    await github.getMergeState(number),
    pullRequest,
    repository,
  );
  const [authority, protectedBranch] = await Promise.all([
    loadAuthority({ github, config, repository, pullRequest }),
    github.getBranchProtection(pullRequest.baseBranch),
  ]);
  if (typeof protectedBranch !== "boolean") {
    throw new TypeError("branch protection state is invalid");
  }
  const labels = labelPlan(authority.labels, authority);
  return {
    pullRequest,
    graph,
    authority,
    labels,
    merge: toMergeDecision({ config, pullRequest, graph, authority, protectedBranch }),
  };
}

function resultFor(evaluation, merge = evaluation.merge) {
  return {
    number: evaluation.pullRequest.number,
    headOid: evaluation.pullRequest.headOid,
    lgtm: evaluation.authority.lgtm !== null,
    approved: evaluation.authority.approved,
    labels: evaluation.labels,
    merge,
  };
}

function blockedSummary(blockers) {
  const safe = blockers.slice(0, 20).join(", ");
  return `Repository merge policy blocked: ${safe}.`;
}

async function applyLabels(github, number, plan) {
  for (const label of plan.add) await github.addPolicyLabel(number, label);
  for (const label of plan.remove) await github.removePolicyLabel(number, label);
}

async function disableIfStillArmed({ github, repository, evaluation }) {
  const current = validatePullRequest(
    await github.getPullRequest(evaluation.pullRequest.number),
    evaluation.pullRequest.number,
    repository,
  );
  if (!sameHeadIdentity(evaluation.pullRequest, current)) return false;
  const graph = validateGraphState(
    await github.getMergeState(current.number),
    current,
    repository,
  );
  if (
    graph.headOid !== evaluation.pullRequest.headOid
    || graph.autoMergeMethod === null
    || graph.autoMergeMethod !== evaluation.graph.autoMergeMethod
  ) return false;
  await github.disableAutoMerge(graph.nodeId);
  return true;
}

async function applyRestrictive({ github, repository, evaluation, dryRun }) {
  if (dryRun) return resultFor(evaluation);
  await github.setMergePolicyCheck(
    evaluation.pullRequest.number,
    evaluation.pullRequest.headOid,
    "action_required",
    blockedSummary(evaluation.merge.blockers),
  );
  await applyLabels(github, evaluation.pullRequest.number, evaluation.labels);
  if (evaluation.merge.action === "DISABLE") {
    await disableIfStillArmed({ github, repository, evaluation });
  }
  return resultFor(evaluation);
}

function headChangedResult(evaluation) {
  return resultFor(evaluation, { action: "NOOP", blockers: ["head-changed"] });
}

async function applyPermissive({ github, config, repository, evaluation, dryRun }) {
  const reread = await loadEvaluation({
    github,
    config,
    repository,
    number: evaluation.pullRequest.number,
  });
  if (!sameHeadIdentity(evaluation.pullRequest, reread.pullRequest)) {
    return headChangedResult(evaluation);
  }
  if (reread.merge.blockers.length > 0) {
    return applyRestrictive({ github, repository, evaluation: reread, dryRun });
  }
  if (dryRun) return resultFor(reread);

  await applyLabels(github, reread.pullRequest.number, reread.labels);
  await github.setMergePolicyCheck(
    reread.pullRequest.number,
    reread.pullRequest.headOid,
    "success",
    SUCCESS_SUMMARY,
  );
  const finalPullRequest = validatePullRequest(
    await github.getPullRequest(reread.pullRequest.number),
    reread.pullRequest.number,
    repository,
  );
  const finalGraph = validateGraphState(
    await github.getMergeState(reread.pullRequest.number),
    finalPullRequest,
    repository,
  );
  if (
    !sameHeadIdentity(reread.pullRequest, finalPullRequest)
    || finalGraph.headOid !== reread.pullRequest.headOid
  ) return headChangedResult(reread);
  if (finalGraph.autoMergeMethod === null) {
    await github.enableAutoMerge(finalGraph.nodeId, config.policy.merge.method);
  }
  return resultFor(reread);
}

async function reconcile({ github, config, repository, number, dryRun }) {
  const evaluation = await loadEvaluation({ github, config, repository, number });
  if (evaluation.merge.blockers.length > 0) {
    return applyRestrictive({ github, repository, evaluation, dryRun });
  }
  return applyPermissive({ github, config, repository, evaluation, dryRun });
}

async function runMergeEvaluate({ event, eventName, github, config, dryRun, prNumber = "" }) {
  if (typeof dryRun !== "boolean") throw new TypeError("dry-run must be a boolean");
  validateConfig(config);
  const repository = eventRepository(event);
  const candidates = await candidatesFor({ event, eventName, github, prNumber });
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
