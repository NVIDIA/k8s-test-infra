"use strict";

const { verifyApproverAuthor } = require("../author-approval.js");
const { parseCommands } = require("../commands/parser.js");
const {
  currentEvidence,
  currentHold,
  parsePolicyState,
} = require("../commands/state.js");
const { validateConfig } = require("../config.js");
const { evaluateDco } = require("../dco.js");
const { MAX_API_COLLECTION_ITEMS } = require("../limits.js");
const { isManagedPolicyLabel } = require("../managed-labels.js");
const { decideMergeAction } = require("../merge-state.js");
const { parseAliases, parseOwnersFile, resolveOwners, hasApprovalCoverage } = require("../owners.js");
const {
  POLICY_COMMENT_MARKER,
  parseMetadataHeadEvidence,
} = require("../policy-comment.js");
const { policyDigest } = require("../policy-digest.js");
const { currentRoleAllows, loadReviewEvidence, validHumanIdentity } = require("../review-evidence.js");
const { classifyTitle } = require("../title.js");

const LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const REPOSITORY = /^[A-Za-z0-9_.-]{1,100}$/;
const OID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const MAX_CANDIDATES = 100;
const MAX_FILES = 1000;
const MAX_REVIEWS = 1000;
const ZERO_OID = "0".repeat(40);
const SUCCESS_SUMMARY = "Repository merge policy passed.";
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
  ) return null;
  if (run.event === expected.event) return expected;
  if (run.name === "PR metadata" && run.event === "workflow_dispatch") {
    return { ...expected, allOpen: true };
  }
  return null;
}

async function candidatesFor({ event, eventName, github, repository, prNumber }) {
  const explicit = explicitNumber(prNumber);
  if (eventName === "workflow_run") {
    if (explicit !== null) throw new TypeError("workflow_run rejects an explicit pull request");
    if (
      event?.schedule !== undefined
      || event?.workflow_run === null
      || event?.action !== "completed"
      || event?.workflow_run?.status !== "completed"
      || !Number.isSafeInteger(event.workflow_run?.id)
      || event.workflow_run.id <= 0
    ) throw new TypeError("workflow completion event is invalid");
    const run = await github.getEvaluationWorkflowRun(event.workflow_run.id);
    if (run?.id !== event.workflow_run.id) return [];
    const expected = trustedRun(run, repository);
    if (expected === null) return [];
    if (expected.allOpen) return boundedCandidates(await github.listOpenPullRequestNumbers());
    const mapped = boundedCandidates(run.pullRequestNumbers);
    // GitHub can omit PR links on review runs; live authority checks still apply to each PR.
    if (run.name === "Review observer" && mapped.length === 0) {
      return boundedCandidates(await github.listOpenPullRequestNumbers());
    }
    return mapped;
  }
  if (eventName === "schedule") {
    if (
      explicit !== null
      || event?.workflow_run !== undefined
      || typeof event?.schedule !== "string"
      || event.schedule === ""
    ) {
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
    || !(
      LOGIN.test(pullRequest.author)
      || (pullRequest.author.endsWith("[bot]") && LOGIN.test(pullRequest.author.slice(0, -5)))
    )
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
  sourceRepository(pullRequest);
  if (pullRequest.headBranch != null && (
    typeof pullRequest.headBranch !== "string"
    || pullRequest.headBranch.length === 0
    || pullRequest.headBranch.length > 255
    || /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u.test(pullRequest.headBranch)
    || pullRequest.headBranch === "@"
    || pullRequest.headBranch === "HEAD"
    || pullRequest.headBranch.startsWith("-")
    || pullRequest.headBranch.startsWith("/")
    || pullRequest.headBranch.endsWith(".")
    || pullRequest.headBranch.endsWith("/")
    || pullRequest.headBranch.includes("..")
    || pullRequest.headBranch.includes("@{")
    || pullRequest.headBranch.includes("//")
    || /(?:^|\/)\./.test(pullRequest.headBranch)
    || /[ ~^:?*[\]\\]/.test(pullRequest.headBranch)
    || pullRequest.headBranch.split("/").some((segment) => segment.endsWith(".lock"))
  )) throw new TypeError("live pull request source branch is invalid");
  return pullRequest;
}

function sourceRepository(pullRequest) {
  if (pullRequest.headRepository == null) return null;
  const { owner, repo } = pullRequest.headRepository;
  if (
    typeof owner !== "string"
    || !LOGIN.test(owner)
    || typeof repo !== "string"
    || !REPOSITORY.test(repo)
    || repo === "."
    || repo === ".."
  ) throw new TypeError("live pull request source repository is invalid");
  return `${owner}/${repo}`.toLowerCase();
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

function samePullRequestHead(left, right) {
  return right !== null
    && left.number === right.number
    && left.headOid === right.headOid
    && left.nodeId === right.nodeId
    && left.baseRepository.owner.toLowerCase() === right.baseRepository.owner.toLowerCase()
    && left.baseRepository.repo.toLowerCase() === right.baseRepository.repo.toLowerCase();
}

function sameHeadIdentity(left, right) {
  return samePullRequestHead(left, right)
    && left.state === right.state
    && left.baseBranch === right.baseBranch
    && sourceRepository(left) === sourceRepository(right)
    && (left.headBranch ?? null) === (right.headBranch ?? null);
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

function policyCommentState(comment, context) {
  if (
    typeof comment?.body !== "string"
    || comment.body.split(POLICY_COMMENT_MARKER).length - 1 !== 1
  ) return { state: null, metadataHead: null };
  if (comment.body.split("<!-- repo-automation-state:").length - 1 === 0) {
    return { state: null, metadataHead: parseMetadataHeadEvidence(comment.body) };
  }
  const state = parsePolicyState(comment.body);
  if (
    state === null
    || state.repository !== context.repository
    || state.pullRequest !== context.pullRequest
  ) return { state: null, metadataHead: null };
  return { state, metadataHead: parseMetadataHeadEvidence(comment.body) };
}

async function loadAuthority({ github, config, repository, pullRequest, policyRevision }) {
  const [files, reviews, labels, comment, revision, commits] = await Promise.all([
    github.listPullRequestFiles(pullRequest.number),
    github.listPullRequestReviews(pullRequest.number),
    github.listIssueLabels(pullRequest.number),
    github.getPolicyComment(pullRequest.number, POLICY_COMMENT_MARKER),
    github.getDefaultBranchRevision(),
    github.listPullRequestCommits(pullRequest.number),
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
    || revision !== policyRevision
  ) throw new TypeError("pull request authority input is invalid");
  if (!Array.isArray(commits) || commits.length === 0
    || commits.length > MAX_API_COLLECTION_ITEMS || commits.at(-1)?.sha !== pullRequest.headOid) {
    throw new TypeError("commit snapshot does not end at the live pull request head");
  }
  const dcoValid = evaluateDco(commits, config.policy.bots).valid;

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
  const [reviewEvidence, authorIsHuman] = await Promise.all([
    loadReviewEvidence({ github, reviews, ownership, pullRequest, context }),
    verifyApproverAuthor(github, ownership, pullRequest.author),
  ]);
  const approvers = new Set([
    ...approvals.map((record) => record.actor),
    ...reviewEvidence.approvals.map((review) => review.user),
  ]);
  const hold = parsed.state === null ? null : currentHold(parsed.state, context);
  return {
    files,
    labels,
    lgtm: lgtms[0] ?? reviewEvidence.lgtms[0] ?? null,
    lgtmOwned: parsed.state !== null || reviewEvidence.lgtms.length > 0,
    approved: hasApprovalCoverage(ownership, approvers, authorIsHuman),
    holdActive: hold !== null,
    metadataHead: classifyTitle(pullRequest.title).valid && dcoValid ? parsed.metadataHead : null,
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

function toMergeDecision({ config, pullRequest, graph, authority, protectedBranch, ciState }) {
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
    ciState,
    autoMergeMethod: graph.autoMergeMethod,
  });
}

async function loadEvaluation({ github, config, repository, number, policyRevision, completedEvaluation = null }) {
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
  // A confirmed merge advances the default branch and needs no further writes.
  if (
    completedEvaluation !== null
    && pullRequest.state === "closed"
    && pullRequest.merged === true
    && graph.state === "MERGED"
    && graph.headOid === completedEvaluation.pullRequest.headOid
    && sameHeadIdentity(
      { ...completedEvaluation.pullRequest, state: "closed" },
      pullRequest,
    )
  ) return { ...completedEvaluation, pullRequest, graph };
  const [authority, protectedBranch] = await Promise.all([
    loadAuthority({ github, config, repository, pullRequest, policyRevision }),
    github.getBranchProtection(pullRequest.baseBranch),
  ]);
  if (typeof protectedBranch !== "boolean") {
    throw new TypeError("branch protection state is invalid");
  }
  const ciState = await github.getCIState({
    prNumber: number,
    headOid: pullRequest.headOid,
    baseBranch: pullRequest.baseBranch,
    files: authority.files,
    requiredCI: config.policy.merge.requiredCI,
    ...(pullRequest.headRepository == null ? {} : { headRepository: sourceRepository(pullRequest) }),
    ...(pullRequest.headBranch == null ? {} : { headBranch: pullRequest.headBranch }),
  });
  if (!["SUCCESS", "PENDING", "FAILED"].includes(ciState)) {
    throw new TypeError("source CI state is invalid");
  }
  if (await github.getDefaultBranchRevision() !== policyRevision) {
    throw new TypeError("trusted policy revision changed during evaluation");
  }
  const labels = labelPlan(authority.labels, authority);
  return {
    pullRequest,
    graph,
    authority,
    labels,
    protectedBranch,
    merge: toMergeDecision({ config, pullRequest, graph, authority, protectedBranch, ciState }),
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

function failClosedDecision({ config, pullRequest, graph, authority }) {
  return decideMergeAction({
    pullRequestState: graph.state === "OPEN" ? "OPEN" : "CLOSED",
    draft: graph.draft,
    baseBranch: graph.baseBranch,
    baseBranchAllowed: branchAllowed(graph.baseBranch, config.policy.protectedBranches),
    baseBranchProtected: false,
    headOid: pullRequest.headOid,
    holdActive: false,
    finalHeadOid: graph.headOid,
    metadataHeadOid: ZERO_OID,
    approvalHeadOid: pullRequest.headOid,
    lgtm: null,
    lgtmStateOwnedByBot: false,
    approvalCoverageComplete: false,
    mergeability: graph.mergeability,
    labels: effectivePolicyLabels(authority.labels, authority),
    loadError: true,
    ciState: "FAILED",
    autoMergeMethod: graph.autoMergeMethod,
  });
}

async function loadFailClosedEvaluation({ github, config, repository, number }) {
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
  const labels = await github.listIssueLabels(number);
  const authority = {
    labels,
    lgtm: null,
    lgtmOwned: false,
    approved: false,
    holdActive: false,
    metadataHead: null,
  };
  return {
    pullRequest,
    graph,
    authority,
    labels: labelPlan(labels, authority),
    merge: failClosedDecision({ config, pullRequest, graph, authority }),
  };
}

async function applyFailClosedBestEffort({ github, repository, evaluation }) {
  const failures = [];
  const attempt = async (operation) => {
    try {
      await operation();
    } catch {
      failures.push("mutation-failed");
    }
  };

  await attempt(() => github.setMergePolicyCheck(
    evaluation.pullRequest.number,
    evaluation.pullRequest.headOid,
    "action_required",
    blockedSummary(evaluation.merge.blockers),
  ));
  for (const label of evaluation.labels.add) {
    await attempt(() => github.addPolicyLabel(evaluation.pullRequest.number, label));
  }
  for (const label of evaluation.labels.remove) {
    await attempt(() => github.removePolicyLabel(evaluation.pullRequest.number, label));
  }
  if (evaluation.merge.action === "DISABLE") {
    await attempt(() => disableIfStillArmed({ github, repository, evaluation }));
  }
  return { ...resultFor(evaluation), failClosedMutationFailures: failures.length };
}

async function failClosed({ github, config, repository, number, dryRun }) {
  if (dryRun) return { number, failClosed: false };
  try {
    const evaluation = await loadFailClosedEvaluation({
      github,
      config,
      repository,
      number,
    });
    return {
      ...await applyFailClosedBestEffort({ github, repository, evaluation }),
      failClosed: true,
    };
  } catch {
    return { number, failClosed: false };
  }
}

function headChangedResult(evaluation) {
  return resultFor(evaluation, { action: "NOOP", blockers: ["head-changed"] });
}

async function applyPermissive({ github, config, repository, evaluation, dryRun, policyRevision }) {
  const reread = await loadEvaluation({
    github,
    config,
    repository,
    number: evaluation.pullRequest.number,
    policyRevision,
  });
  if (!sameHeadIdentity(evaluation.pullRequest, reread.pullRequest)) {
    return headChangedResult(evaluation);
  }
  if (reread.merge.blockers.length > 0) {
    return applyRestrictive({ github, repository, evaluation: reread, dryRun });
  }
  if (dryRun) return resultFor(reread);

  // GitHub requires a blocking requirement while native auto-merge is enabled.
  await github.setMergePolicyCheck(
    reread.pullRequest.number,
    reread.pullRequest.headOid,
    "action_required",
    "Repository merge policy is preparing native SQUASH auto-merge.",
  );
  await applyLabels(github, reread.pullRequest.number, reread.labels);
  let beforeSuccess = await loadEvaluation({
    github,
    config,
    repository,
    number: reread.pullRequest.number,
    policyRevision,
  });
  if (
    !sameHeadIdentity(reread.pullRequest, beforeSuccess.pullRequest)
    || beforeSuccess.graph.headOid !== reread.pullRequest.headOid
  ) {
    return headChangedResult(reread);
  }
  if (beforeSuccess.merge.blockers.length > 0) {
    return applyRestrictive({ github, repository, evaluation: beforeSuccess, dryRun });
  }
  const enabled = beforeSuccess.merge.action === "ENABLE";
  if (enabled) {
    await github.enableAutoMerge(
      beforeSuccess.graph.nodeId,
      "SQUASH",
      beforeSuccess.pullRequest.headOid,
    );
    const afterEnable = await loadEvaluation({
      github,
      config,
      repository,
      number: beforeSuccess.pullRequest.number,
      policyRevision,
    });
    if (
      !sameHeadIdentity(beforeSuccess.pullRequest, afterEnable.pullRequest)
      || afterEnable.graph.headOid !== beforeSuccess.pullRequest.headOid
    ) return headChangedResult(beforeSuccess);
    if (afterEnable.merge.blockers.length > 0) {
      return applyRestrictive({ github, repository, evaluation: afterEnable, dryRun });
    }
    if (afterEnable.graph.autoMergeMethod !== "SQUASH") {
      throw new Error("native SQUASH auto-merge was not retained");
    }
    beforeSuccess = afterEnable;
  }
  await github.setMergePolicyCheck(
    beforeSuccess.pullRequest.number,
    beforeSuccess.pullRequest.headOid,
    "success",
    SUCCESS_SUMMARY,
  );
  const finalEvaluation = await loadEvaluation({
    github,
    config,
    repository,
    number: beforeSuccess.pullRequest.number,
    policyRevision,
    completedEvaluation: beforeSuccess,
  });
  const completedDecision = enabled ? { action: "ENABLE", blockers: [] } : finalEvaluation.merge;
  if (
    finalEvaluation.pullRequest.state === "closed"
    && finalEvaluation.pullRequest.merged === true
    && finalEvaluation.graph.state === "MERGED"
    && finalEvaluation.graph.headOid === beforeSuccess.pullRequest.headOid
    && sameHeadIdentity(
      { ...beforeSuccess.pullRequest, state: "closed" },
      finalEvaluation.pullRequest,
    )
  ) return resultFor(finalEvaluation, { action: enabled ? "ENABLE" : "NOOP", blockers: [] });
  if (
    !sameHeadIdentity(beforeSuccess.pullRequest, finalEvaluation.pullRequest)
    || finalEvaluation.graph.headOid !== beforeSuccess.pullRequest.headOid
  ) {
    if (
      samePullRequestHead(beforeSuccess.pullRequest, finalEvaluation.pullRequest)
      && finalEvaluation.graph.headOid === beforeSuccess.pullRequest.headOid
    ) {
      const changedEvaluation = {
        ...finalEvaluation,
        merge: {
          action: finalEvaluation.graph.autoMergeMethod === null ? "NOOP" : "DISABLE",
          blockers: [...new Set([...finalEvaluation.merge.blockers, "head-changed"])],
        },
      };
      return applyRestrictive({ github, repository, evaluation: changedEvaluation, dryRun });
    }
    return headChangedResult(beforeSuccess);
  }
  if (finalEvaluation.merge.blockers.length > 0) {
    return applyRestrictive({ github, repository, evaluation: finalEvaluation, dryRun });
  }
  return resultFor(finalEvaluation, completedDecision);
}

async function reconcile({ github, config, repository, number, dryRun, policyRevision }) {
  const evaluation = await loadEvaluation({ github, config, repository, number, policyRevision });
  if (evaluation.merge.blockers.length > 0) {
    return applyRestrictive({ github, repository, evaluation, dryRun });
  }
  return applyPermissive({ github, config, repository, evaluation, dryRun, policyRevision });
}

async function runMergeEvaluate({ event, eventName, github, config, dryRun, policyRevision, prNumber = "" }) {
  if (typeof dryRun !== "boolean") throw new TypeError("dry-run must be a boolean");
  if (typeof policyRevision !== "string" || !OID.test(policyRevision) || /^0+$/.test(policyRevision)) {
    throw new TypeError("policy revision must be a nonzero canonical commit SHA");
  }
  validateConfig(config);
  const repository = eventRepository(event);
  const candidates = await candidatesFor({ event, eventName, github, repository, prNumber });
  const pullRequests = [];
  let failed = false;
  for (const number of candidates) {
    try {
      pullRequests.push(await reconcile({ github, config, repository, number, dryRun, policyRevision }));
    } catch {
      failed = true;
      pullRequests.push(await failClosed({ github, config, repository, number, dryRun }));
    }
  }
  const summary = {
    status: failed ? "failed" : (dryRun ? "planned" : "complete"),
    candidates,
    pullRequests,
  };
  if (failed) {
    const error = new Error("pull request evaluation failed closed");
    error.summary = summary;
    throw error;
  }
  return summary;
}

module.exports = { runMergeEvaluate };
