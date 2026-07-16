"use strict";

const { parseCommands } = require("../commands/parser.js");
const { parsePolicyState } = require("../commands/state.js");
const { planCommandExecution } = require("../commands/executor.js");
const { validateConfig } = require("../config.js");
const { parseAliases, parseOwnersFile, resolveOwners } = require("../owners.js");
const {
  POLICY_COMMENT_MARKER,
  hasValidPolicyCommentStructure,
  renderCommandPolicyComment,
} = require("../policy-comment.js");

const LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const REPOSITORY = /^[A-Za-z0-9_.-]{1,100}$/;

function eventIdentity(event) {
  const owner = event?.repository?.owner?.login;
  const repo = event?.repository?.name;
  const fullName = event?.repository?.full_name;
  const prNumber = event?.issue?.number;
  const commentId = event?.comment?.id;
  if (
    event?.action !== "created"
    || typeof owner !== "string"
    || !LOGIN.test(owner)
    || typeof repo !== "string"
    || !REPOSITORY.test(repo)
    || repo === "."
    || repo === ".."
    || typeof fullName !== "string"
    || fullName.toLowerCase() !== `${owner}/${repo}`.toLowerCase()
    || !Number.isSafeInteger(prNumber)
    || prNumber <= 0
    || !Number.isSafeInteger(commentId)
    || commentId <= 0
  ) {
    throw new TypeError("event must identify a created repository issue comment");
  }
  if (event.issue.pull_request !== undefined && (event.issue.pull_request === null || typeof event.issue.pull_request !== "object")) {
    throw new TypeError("event pull request mapping is invalid");
  }
  return {
    owner: owner.toLowerCase(),
    repo: repo.toLowerCase(),
    prNumber,
    commentId,
    pullRequestHint: event.issue.pull_request !== undefined,
  };
}

function openPullRequest(value, identity) {
  if (value === null || value === undefined || value.state !== "open") return null;
  if (
    typeof value !== "object"
    || Array.isArray(value)
    || value.number !== identity.prNumber
    || typeof value.draft !== "boolean"
    || typeof value.title !== "string"
    || typeof value.author !== "string"
    || !LOGIN.test(value.author)
    || typeof value.headOid !== "string"
    || !/^(?:[0-9a-f]{40}|[0-9a-f]{64})$/.test(value.headOid)
    || value.baseRepository?.owner?.toLowerCase() !== identity.owner
    || value.baseRepository?.repo?.toLowerCase() !== identity.repo
  ) {
    throw new Error("live pull request state or base repository is invalid");
  }
  return value;
}

function samePullRequest(left, right) {
  return right !== null
    && left.number === right.number
    && left.state === right.state
    && left.draft === right.draft
    && left.title === right.title
    && left.author.toLowerCase() === right.author.toLowerCase()
    && left.headOid === right.headOid
    && left.baseRepository.owner.toLowerCase() === right.baseRepository.owner.toLowerCase()
    && left.baseRepository.repo.toLowerCase() === right.baseRepository.repo.toLowerCase();
}

function activeOwnerPaths(config) {
  if (!Array.isArray(config?.policy?.activeOwnerFiles)) return [];
  return [...new Set(config.policy.activeOwnerFiles.filter((value) => (
    typeof value === "string" && /^\/(?:[A-Za-z0-9_.-]+\/)*OWNERS$/.test(value)
  )))].sort();
}

function configurationValid(config) {
  try {
    validateConfig(config);
    return true;
  } catch {
    return false;
  }
}

function emptyOwnership(files) {
  return {
    files: files.map((file) => ({ path: file.path, reviewers: [], approvers: [] })),
    reviewerCandidates: [],
    approverCandidates: [],
    uncoveredPaths: files.map((file) => file.path).sort(),
  };
}

function commandTargets(parsed) {
  const users = new Set();
  for (const command of parsed.commands) {
    if ((command.name === "assign" || command.name === "unassign") && command.users.length <= 20) {
      for (const login of command.users) users.add(login);
    }
  }
  return users.size > 20
    ? { targets: [], exceeded: true }
    : { targets: [...users].sort(), exceeded: false };
}

function boundedParsed(parsed) {
  if (parsed.commands.length + parsed.diagnostics.length <= 100) return parsed;
  return {
    commands: [],
    diagnostics: [{ line: 0, code: "too-many-commands", message: "command result limit exceeded" }],
  };
}

function actorIdentity(identity, access, expectedLogin) {
  if (
    identity?.resolved !== true
    || identity?.deleted !== false
    || typeof identity.login !== "string"
    || identity.login.toLowerCase() !== expectedLogin.toLowerCase()
  ) {
    return { ...identity, resolved: false };
  }
  return { ...identity, ...access };
}

function currentState(comment, headOid) {
  if (comment.body === null) {
    return {
      state: { headOid, lgtm: null, lastRetest: null },
      trustworthy: true,
      renderBody: null,
    };
  }
  if (!hasValidPolicyCommentStructure(comment.body)) {
    return {
      state: { headOid, lgtm: null, lastRetest: null },
      trustworthy: false,
      renderBody: null,
    };
  }
  const parsed = parsePolicyState(comment.body);
  if (parsed === null) {
    return {
      state: { headOid, lgtm: null, lastRetest: null },
      trustworthy: false,
      renderBody: null,
    };
  }
  return {
    state: parsed.headOid === headOid
      ? parsed
      : { headOid, lgtm: null, lastRetest: null },
    trustworthy: true,
    renderBody: comment.body,
  };
}

function descriptor(operation, details = {}) {
  return { operation, ...details };
}

async function runCommand({ event, github, config, dryRun, now = () => new Date().toISOString() }) {
  if (typeof dryRun !== "boolean" || typeof now !== "function") {
    throw new TypeError("command mode inputs are invalid");
  }
  const identity = eventIdentity(event);
  if (!identity.pullRequestHint) return { status: "ignored", reason: "not-open-pull-request" };
  const comment = await github.getIssueComment(identity.commentId);
  if (
    comment?.id !== identity.commentId
    || comment?.issueNumber !== identity.prNumber
    || typeof comment.body !== "string"
    || typeof comment.author !== "string"
    || !LOGIN.test(comment.author)
  ) {
    throw new Error("live issue comment mapping is invalid");
  }
  const parsed = boundedParsed(parseCommands(comment.body));
  if (parsed.commands.length === 0 && parsed.diagnostics.length === 0) {
    return { status: "ignored", reason: "no-command" };
  }

  const pullRequest = openPullRequest(await github.getPullRequest(identity.prNumber), identity);
  if (pullRequest === null) return { status: "ignored", reason: "not-open-pull-request" };

  const [liveUser, access] = await Promise.all([
    github.getUserIdentity(comment.author.toLowerCase()),
    github.getCollaboratorAccess(comment.author.toLowerCase()),
  ]);
  const actor = actorIdentity(liveUser, access, comment.author);
  const files = await github.listPullRequestFiles(identity.prNumber);
  const reviews = await github.listPullRequestReviews(identity.prNumber);
  const requestedReviewers = await github.listRequestedReviewers(identity.prNumber);
  const currentAssignees = await github.listIssueAssignees(identity.prNumber);
  const currentLabels = await github.listIssueLabels(identity.prNumber);
  const policyComment = await github.getPolicyComment(identity.prNumber, POLICY_COMMENT_MARKER);
  if (
    !Array.isArray(files)
    || files.length === 0
    || !Array.isArray(reviews)
    || !Array.isArray(requestedReviewers)
    || !Array.isArray(currentAssignees)
    || !Array.isArray(currentLabels)
  ) {
    throw new TypeError("live command policy list state is invalid");
  }
  const stored = currentState(policyComment, pullRequest.headOid);

  const validConfiguration = configurationValid(config);
  let ownerAuthorityValid = validConfiguration && stored.trustworthy;
  let ownership = emptyOwnership(files);
  if (validConfiguration) {
    try {
      const revision = await github.getDefaultBranchRevision();
      const declarations = [];
      for (const ownerPath of activeOwnerPaths(config)) {
        declarations.push(parseOwnersFile(
          await github.getContentAtRevision(ownerPath, revision),
          ownerPath,
        ));
      }
      const aliases = parseAliases(await github.getContentAtRevision("/OWNERS_ALIASES", revision));
      ownership = resolveOwners(
        files.map((file) => file.path),
        declarations,
        aliases,
        { activeOwnerFiles: activeOwnerPaths(config), pullRequestAuthor: pullRequest.author },
      );
    } catch {
      ownership = emptyOwnership(files);
      ownerAuthorityValid = false;
    }
  }

  if (ownership.uncoveredPaths.length > 0) ownerAuthorityValid = false;
  const targetPlan = ownerAuthorityValid
    ? commandTargets(parsed)
    : { targets: [], exceeded: false };
  const targetPermissions = new Map();
  for (const login of targetPlan.targets) {
    const [target, targetAccess] = await Promise.all([
      github.getUserIdentity(login),
      github.getCollaboratorAccess(login),
    ]);
    targetPermissions.set(login, actorIdentity(target, targetAccess, login));
  }
  const participants = [...new Set([
    pullRequest.author.toLowerCase(),
    ...currentAssignees.map((login) => login.toLowerCase()),
    ...requestedReviewers.map((login) => login.toLowerCase()),
    ...reviews.map((review) => review.user.toLowerCase()),
  ])].sort();
  const needsRuns = ownerAuthorityValid
    && parsed.commands.some((command) => command.name === "retest");
  const runs = needsRuns
    ? await github.listWorkflowRunsForHead(pullRequest.headOid, identity.prNumber)
    : [];
  const timestamp = now();
  const state = ownerAuthorityValid
    ? stored.state
    : { headOid: pullRequest.headOid, lgtm: null, lastRetest: null };
  const plan = planCommandExecution({
    parsed,
    actor,
    author: pullRequest.author,
    ownedFiles: ownership.files,
    participants,
    targetPermissions,
    currentAssignees,
    currentLabels,
    reviews,
    requestedReviewers,
    runs,
    state,
    headOid: pullRequest.headOid,
    commentId: identity.commentId,
    now: timestamp,
    cooldownSeconds: 600,
    prNumber: identity.prNumber,
    repository: `${identity.owner}/${identity.repo}`,
    authorityValid: ownerAuthorityValid,
    assignmentFanoutExceeded: targetPlan.exceeded,
  });
  const result = {
    status: ownerAuthorityValid
      ? (dryRun ? "planned" : "pending")
      : "partial",
    headOid: pullRequest.headOid,
    commentId: identity.commentId,
    items: plan.items,
    commands: plan.commands,
    diagnostics: plan.diagnostics,
    policy: plan.policy,
    labels: {
      add: plan.mutations.addLabels,
      remove: plan.mutations.removeLabels,
    },
    apply: { attempted: [], applied: [], failed: null },
  };
  const commentBody = renderCommandPolicyComment({
    existingBody: stored.renderBody,
    state: plan.state,
    items: plan.items,
    policy: plan.policy,
  });

  if (dryRun) return result;
  const fence = async () => {
    const current = openPullRequest(await github.getPullRequest(identity.prNumber), identity);
    if (!samePullRequest(pullRequest, current)) {
      throw new Error("pull request state changed after planning; refusing stale writes");
    }
  };
  await fence();

  const apply = async (operation, mutation) => {
    result.apply.attempted.push(operation);
    try {
      await mutation();
      result.apply.applied.push(operation);
    } catch (error) {
      result.status = "partial";
      result.apply.failed = operation;
      const failure = error instanceof Error ? error : new Error("command mutation failed");
      failure.summary = result;
      throw failure;
    }
  };

  if (plan.mutations.addAssignees.length > 0) {
    const operation = descriptor("addAssignees", { assignees: plan.mutations.addAssignees });
    await apply(operation, () => github.addAssignees(identity.prNumber, plan.mutations.addAssignees));
  }
  if (plan.mutations.removeAssignees.length > 0) {
    const operation = descriptor("removeAssignees", { assignees: plan.mutations.removeAssignees });
    await apply(operation, () => github.removeAssignees(identity.prNumber, plan.mutations.removeAssignees));
  }
  for (const label of plan.mutations.addLabels) {
    await apply(descriptor("addPolicyLabel", { label }), () => github.addPolicyLabel(identity.prNumber, label));
  }
  for (const label of plan.mutations.removeLabels) {
    await apply(descriptor("removePolicyLabel", { label }), () => github.removePolicyLabel(identity.prNumber, label));
  }
  if (plan.mutations.requestReviewers.length > 0) {
    const operation = descriptor("requestReviewers", { reviewers: plan.mutations.requestReviewers });
    await apply(operation, () => github.requestReviewers(identity.prNumber, plan.mutations.requestReviewers));
  }

  await fence();
  if (policyComment.body !== commentBody) {
    const operation = descriptor("upsertPolicyComment", { action: policyComment.action });
    await apply(operation, () => github.upsertPolicyComment(
      identity.prNumber,
      POLICY_COMMENT_MARKER,
      commentBody,
      policyComment,
    ));
  }

  for (const runId of plan.mutations.rerunRunIds) {
    await fence();
    const plannedRun = runs.find((candidate) => candidate.id === runId);
    const run = await github.getWorkflowRun(runId, pullRequest.headOid, identity.prNumber);
    if (
      plannedRun === undefined
      || run?.id !== plannedRun.id
      || run.headOid !== plannedRun.headOid
      || run.status !== "completed"
      || run.conclusion !== "failure"
      || run.workflowPath !== plannedRun.workflowPath
      || run.event !== plannedRun.event
      || run.prNumber !== plannedRun.prNumber
      || run.repository !== plannedRun.repository
    ) continue;
    await apply(descriptor("rerunFailedJobs", { runId }), () => github.rerunFailedJobs(runId));
  }

  if (result.status !== "partial") result.status = "complete";
  return result;
}

module.exports = { runCommand };
