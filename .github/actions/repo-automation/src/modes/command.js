"use strict";

const { parseCommands } = require("../commands/parser.js");
const { verifyApproverAuthor } = require("../author-approval.js");
const {
  createEmptyState,
  parsePolicyState,
  serializePolicyState,
} = require("../commands/state.js");
const { authorizeCommand } = require("../commands/authorization.js");
const { planCommandExecution } = require("../commands/executor.js");
const { validateConfig } = require("../config.js");
const { parseAliases, parseOwnersFile, resolveOwners } = require("../owners.js");
const { policyDigest } = require("../policy-digest.js");
const { validAuthorContext } = require("../pull-request-author.js");
const { loadReviewEvidence } = require("../review-evidence.js");
const {
  POLICY_COMMENT_MARKER,
  renderCommandPolicyComment,
} = require("../policy-comment.js");

const LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const REPOSITORY = /^[A-Za-z0-9_.-]{1,100}$/;
const OID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const STATE_MARKER = "<!-- repo-automation-state:";
const EVIDENCE_COMMANDS = new Set(["lgtm", "approve"]);
const CATCH_UP_COMMANDS = new Set(["hold", "unhold", "retest"]);
const MAX_BACKLOG_AGE_MS = 24 * 60 * 60 * 1000;

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
  return {
    owner: owner.toLowerCase(),
    repo: repo.toLowerCase(),
    repository: `${owner}/${repo}`.toLowerCase(),
    prNumber,
    commentId,
    pullRequestHint: event.issue?.pull_request !== undefined,
  };
}

function liveComment(value, identity) {
  if (
    value === null
    || typeof value !== "object"
    || Array.isArray(value)
    || value.id !== identity.commentId
    || value.issueNumber !== identity.prNumber
    || typeof value.body !== "string"
    || typeof value.author !== "string"
    || !LOGIN.test(value.author)
    || value.authorType !== "User"
    || value.edited !== false
  ) throw new Error("live command comment is invalid");
  return value;
}

// The same checks as liveComment, but a failing listed comment is skipped instead of fatal.
function pendingComment(value, identity) {
  return value !== null
    && typeof value === "object"
    && !Array.isArray(value)
    && Number.isSafeInteger(value.id)
    && value.id > 0
    && value.issueNumber === identity.prNumber
    && typeof value.body === "string"
    && typeof value.author === "string"
    && LOGIN.test(value.author)
    && value.authorType === "User"
    && value.edited === false;
}

function openPullRequest(value, identity) {
  if (value === null || value === undefined || value.state !== "open") return null;
  if (
    typeof value !== "object"
    || Array.isArray(value)
    || value.number !== identity.prNumber
    || typeof value.nodeId !== "string"
    || value.nodeId === ""
    || typeof value.draft !== "boolean"
    || typeof value.title !== "string"
    || !validAuthorContext(value.author)
    || typeof value.headOid !== "string"
    || !OID.test(value.headOid)
    || typeof value.baseBranch !== "string"
    || value.baseBranch === ""
    || value.baseRepository?.owner?.toLowerCase() !== identity.owner
    || value.baseRepository?.repo?.toLowerCase() !== identity.repo
  ) throw new Error("live pull request state or base repository is invalid");
  return value;
}

function sameComment(left, right) {
  return left.id === right.id
    && left.issueNumber === right.issueNumber
    && left.body === right.body
    && left.author.toLowerCase() === right.author.toLowerCase()
    && left.authorType === right.authorType
    && left.edited === right.edited;
}

function samePullRequest(left, right) {
  return right !== null
    && left.number === right.number
    && left.nodeId === right.nodeId
    && left.state === right.state
    && left.draft === right.draft
    && left.title === right.title
    && left.author.toLowerCase() === right.author.toLowerCase()
    && left.headOid === right.headOid
    && left.baseBranch === right.baseBranch
    && left.baseRepository.owner.toLowerCase() === right.baseRepository.owner.toLowerCase()
    && left.baseRepository.repo.toLowerCase() === right.baseRepository.repo.toLowerCase();
}

function actorIdentity(identity, access, expectedLogin) {
  if (
    identity?.resolved !== true
    || identity?.deleted !== false
    || typeof identity.login !== "string"
    || identity.login.toLowerCase() !== expectedLogin.toLowerCase()
  ) return { ...identity, resolved: false };
  return { ...identity, ...access };
}

function ownerPaths(config) {
  return [...new Set(config.policy.activeOwnerFiles)].sort();
}

async function loadAuthority(github, config, identity, pullRequest, files) {
  const revision = await github.getDefaultBranchRevision();
  if (typeof revision !== "string" || !OID.test(revision)) {
    throw new Error("default branch revision is invalid");
  }
  const sources = [];
  const declarations = [];
  for (const ownerPath of ownerPaths(config)) {
    const source = await github.getContentAtRevision(ownerPath, revision);
    if (typeof source !== "string") throw new Error("OWNERS source is invalid");
    sources.push({ path: ownerPath, source });
    declarations.push(parseOwnersFile(source, ownerPath));
  }
  const aliasesSource = await github.getContentAtRevision("/OWNERS_ALIASES", revision);
  const aliases = parseAliases(aliasesSource);
  const ownership = resolveOwners(
    files.map((file) => file.path),
    declarations,
    aliases,
    { activeOwnerFiles: ownerPaths(config), pullRequestAuthor: pullRequest.author },
  );
  const authorIsHuman = await verifyApproverAuthor(github, ownership, pullRequest.author);
  if (ownership.uncoveredPaths.some((path) => (
    !authorIsHuman || !ownership.authorApprovalPaths.includes(path)
  ))) {
    throw new Error("command authority is unavailable for unowned paths");
  }
  return {
    digest: policyDigest({
      repository: identity.repository,
      policy: config.policy,
      ownerSources: sources,
      aliasesSource,
    }),
    ownership,
    authorIsHuman,
  };
}

function loadState(policyComment, context) {
  if (policyComment?.body === null) {
    return { state: createEmptyState(context), renderBody: null };
  }
  if (
    typeof policyComment?.body !== "string"
    || policyComment.body.split(POLICY_COMMENT_MARKER).length - 1 !== 1
  ) throw new Error("bot-owned policy comment is invalid");
  const markerCount = policyComment.body.split(STATE_MARKER).length - 1;
  if (markerCount === 0) {
    return { state: createEmptyState(context), renderBody: policyComment.body };
  }
  if (markerCount !== 1) throw new Error("bot-owned command state is invalid");
  const state = parsePolicyState(policyComment.body);
  if (
    state === null
    || state.repository !== context.repository
    || state.pullRequest !== context.pullRequest
  ) throw new Error("bot-owned command state is invalid");
  return { state, renderBody: policyComment.body };
}

function boundedParsed(parsed) {
  if (parsed.commands.length + parsed.diagnostics.length <= 100) return parsed;
  return {
    commands: [],
    diagnostics: [{ line: 0, code: "too-many-commands", message: "command result limit exceeded" }],
  };
}

function hasCommands(parsed) {
  return parsed.commands.length > 0 || parsed.diagnostics.length > 0;
}

function storedProcessedIds(policyComment) {
  if (typeof policyComment?.body !== "string") return [];
  return parsePolicyState(policyComment.body)?.processedCommandIds ?? [];
}

// Comment creation time is GitHub server time. The window keeps a first run from
// replaying commands left over from before catch-up existed. Date.parse gives NaN for
// a missing or malformed time, and a NaN age never passes the comparison.
function recentComment(value, nowMilliseconds) {
  return nowMilliseconds - Date.parse(value.createdAt) <= MAX_BACKLOG_AGE_MS;
}

// A later run in the same concurrency group cancels a pending one, so each run first
// applies the unprocessed command comments older than its own, in id order. Comments
// older than the newest processed one were skipped earlier and are never replayed out
// of order; newer comments are left to their own run.
function backlogComments(listed, identity, processedIds, nowMilliseconds) {
  const newestProcessed = Math.max(0, ...processedIds);
  return listed
    .filter((value) => (
      pendingComment(value, identity)
      && value.id < identity.commentId
      && value.id > newestProcessed
      && recentComment(value, nowMilliseconds)
    ))
    .map((value) => ({ comment: value, parsed: boundedParsed(parseCommands(value.body)) }))
    .filter((item) => hasCommands(item.parsed))
    .sort((left, right) => left.comment.id - right.comment.id);
}

// Catch-up replays only /hold, /unhold and /retest. A caught-up /lgtm or /approve can
// predate a push to the head, so it grants no evidence; when its author may give it, the
// policy comment asks them to repeat it. A caught-up /backport or /cherry-pick is not
// replayed.
function caughtUpCommands(parsed, authorization) {
  const rejected = [...new Set(parsed.commands
    .filter((command) => (
      EVIDENCE_COMMANDS.has(command.name) && authorizeCommand(command, authorization).allowed
    ))
    .map((command) => command.name))].sort();
  return {
    parsed: { ...parsed, commands: parsed.commands.filter((command) => CATCH_UP_COMMANDS.has(command.name)) },
    rejected,
  };
}

function sameBacklog(items, latest, identity) {
  return Array.isArray(latest) && items.every(({ comment }) => {
    const current = latest.find((candidate) => candidate?.id === comment.id);
    return current !== undefined && pendingComment(current, identity) && sameComment(comment, current);
  });
}

function samePolicyComment(left, right) {
  return left?.action === right?.action && left?.id === right?.id && left?.body === right?.body;
}

function sameWorkflowRun(left, right) {
  return right?.id === left?.id
    && right.headOid === left.headOid
    && right.status === left.status
    && right.conclusion === left.conclusion
    && right.workflowPath === left.workflowPath
    && right.workflowSourceRef === left.workflowSourceRef
    && right.event === left.event
    && right.prNumber === left.prNumber
    && right.repository === left.repository;
}

async function runCommand({ event, github, config, dryRun, now = () => new Date().toISOString() }) {
  if (typeof dryRun !== "boolean" || typeof now !== "function") {
    throw new TypeError("command mode inputs are invalid");
  }
  const identity = eventIdentity(event);
  if (!identity.pullRequestHint) return { status: "ignored", reason: "not-pull-request" };

  validateConfig(config);
  const comment = liveComment(await github.getIssueComment(identity.commentId), identity);
  const parsed = boundedParsed(parseCommands(comment.body));
  const timestamp = now();
  const listed = await github.listIssueComments(identity.prNumber);
  if (!Array.isArray(listed)) throw new Error("live comment list is invalid");
  const policyComment = await github.getPolicyComment(identity.prNumber, POLICY_COMMENT_MARKER);
  const backlog = backlogComments(listed, identity, storedProcessedIds(policyComment), Date.parse(timestamp));
  const items = hasCommands(parsed) ? [...backlog, { comment, parsed }] : backlog;
  if (items.length === 0) return { status: "ignored", reason: "no-command" };
  const pullRequest = openPullRequest(await github.getPullRequest(identity.prNumber), identity);
  if (pullRequest === null) return { status: "ignored", reason: "not-open-pull-request" };

  const files = await github.listPullRequestFiles(identity.prNumber);
  if (!Array.isArray(files) || files.length === 0) {
    throw new Error("live pull request file list is invalid");
  }
  const authority = await loadAuthority(github, config, identity, pullRequest, files);
  const context = {
    repository: identity.repository,
    pullRequest: identity.prNumber,
    policyDigest: authority.digest,
    headOid: pullRequest.headOid,
  };
  const stored = loadState(policyComment, context);
  const nativeReviewEvidence = await loadReviewEvidence({
    github,
    reviews: await github.listPullRequestReviews(identity.prNumber),
    ownership: authority.ownership,
    pullRequest,
    context,
  });
  const authors = [...new Set(items.map((item) => item.comment.author.toLowerCase()))];
  const [actors, currentLabels] = await Promise.all([
    Promise.all(authors.map(async (author) => {
      const [user, access] = await Promise.all([
        github.getUserIdentity(author),
        github.getCollaboratorAccess(author),
      ]);
      return [author, actorIdentity(user, access, author)];
    })).then((entries) => new Map(entries)),
    github.listIssueLabels(identity.prNumber),
  ]);
  if (!Array.isArray(currentLabels)) throw new Error("live label state is invalid");
  const needsRuns = items.some((item) => item.parsed.commands.some((command) => command.name === "retest"));
  const runs = needsRuns
    ? await github.listWorkflowRunsForHead(pullRequest.headOid, identity.prNumber)
    : [];
  if (!Array.isArray(runs)) throw new Error("live workflow run state is invalid");

  let state = stored.state;
  let plan;
  const processedCommentIds = [];
  const rejectedBacklogEvidence = [];
  const rerunRunIds = [];
  const owners = [...new Set([
    ...authority.ownership.reviewerCandidates,
    ...authority.ownership.approverCandidates,
  ])];
  for (const item of items) {
    const event = item.comment.id === identity.commentId;
    const authorization = {
      actor: actors.get(item.comment.author.toLowerCase()),
      author: pullRequest.author,
      reviewers: authority.ownership.reviewerCandidates,
      approvers: authority.ownership.approverCandidates,
      owners,
    };
    const checked = event
      ? { parsed: item.parsed, rejected: [] }
      : caughtUpCommands(item.parsed, authorization);
    const candidate = planCommandExecution({
      parsed: checked.parsed,
      state,
      context,
      ...authorization,
      ownership: authority.ownership,
      authorIsHuman: authority.authorIsHuman,
      nativeReviewEvidence,
      commentId: item.comment.id,
      now: timestamp,
      historyLimit: config.policy.commands.historyLimit,
      runs,
      cooldownSeconds: config.policy.commands.retestCooldownSeconds,
      retestWorkflowAllowlist: config.policy.commands.retestWorkflows,
      allowedBackportBranches: config.policy.commands.backportBranches,
      // Every plan diffs against the live labels, so the last plan carries the net change.
      currentLabels,
    });
    // A caught-up comment is recorded only when it changed something or had a /lgtm or
    // /approve rejected that its author may give, so other commenters cannot fill the
    // processed history.
    if (
      !event
      && checked.rejected.length === 0
      && !candidate.commands.some((result) => result.status === "applied")
    ) continue;
    plan = candidate;
    if (plan.duplicate) break;
    state = plan.state;
    processedCommentIds.push(item.comment.id);
    if (checked.rejected.length > 0) {
      rejectedBacklogEvidence.push({
        commentId: item.comment.id,
        commands: checked.rejected,
        status: "rejected",
        code: "stale-backlog-evidence",
      });
    }
    for (const runId of plan.mutations.rerunRunIds) {
      if (!rerunRunIds.includes(runId)) rerunRunIds.push(runId);
    }
  }
  if (plan === undefined) return { status: "ignored", reason: "no-command" };
  const result = {
    status: plan.duplicate ? "duplicate" : (dryRun ? "planned" : "pending"),
    headOid: pullRequest.headOid,
    commentId: identity.commentId,
    processedCommentIds,
    rejectedBacklogEvidence,
    commands: plan.commands,
    diagnostics: plan.diagnostics,
    policy: plan.policy,
    labels: {
      add: plan.mutations.addLabels,
      remove: plan.mutations.removeLabels,
    },
    // Only the event comment can request a backport, and its plan is the last one.
    backportRequests: plan.mutations.backportRequests,
    rerunRunIds,
    apply: { attempted: [], applied: [], failed: null },
  };
  if (plan.duplicate || dryRun) return result;

  const commentBody = renderCommandPolicyComment({
    existingBody: stored.renderBody,
    serializedState: serializePolicyState(state),
    commands: plan.commands,
    diagnostics: plan.diagnostics,
    policy: plan.policy,
    rejectedBacklogEvidence,
  });
  const caughtUp = backlog.filter((item) => processedCommentIds.includes(item.comment.id));
  const latestComment = liveComment(await github.getIssueComment(identity.commentId), identity);
  const latestListed = caughtUp.length === 0 ? [] : await github.listIssueComments(identity.prNumber);
  const latestPullRequest = openPullRequest(await github.getPullRequest(identity.prNumber), identity);
  const latestPolicyComment = await github.getPolicyComment(identity.prNumber, POLICY_COMMENT_MARKER);
  if (
    !sameComment(comment, latestComment)
    || !sameBacklog(caughtUp, latestListed, identity)
    || !samePullRequest(pullRequest, latestPullRequest)
    || !samePolicyComment(policyComment, latestPolicyComment)
  ) throw new Error("live command inputs changed after planning; refusing stale writes");
  const latestAuthority = await loadAuthority(github, config, identity, latestPullRequest, files);
  if (
    latestAuthority.digest !== authority.digest
    || latestAuthority.authorIsHuman !== authority.authorIsHuman
  ) throw new Error("command authority changed after planning; refusing stale writes");
  const latestReviewEvidence = await loadReviewEvidence({
    github,
    reviews: await github.listPullRequestReviews(identity.prNumber),
    ownership: latestAuthority.ownership,
    pullRequest: latestPullRequest,
    context,
  });
  if (JSON.stringify(latestReviewEvidence) !== JSON.stringify(nativeReviewEvidence)) {
    throw new Error("command review evidence changed after planning; refusing stale writes");
  }

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

  for (const label of plan.mutations.addLabels) {
    await apply(`addPolicyLabel:${label}`, () => github.addPolicyLabel(identity.prNumber, label));
  }
  for (const label of plan.mutations.removeLabels) {
    await apply(`removePolicyLabel:${label}`, () => github.removePolicyLabel(identity.prNumber, label));
  }
  for (const runId of rerunRunIds) {
    const plannedRun = runs.find((candidate) => candidate.id === runId);
    if (plannedRun === undefined) throw new Error("planned workflow run is missing");
    const liveRun = await github.getWorkflowRun(runId, pullRequest.headOid, identity.prNumber);
    if (!sameWorkflowRun(plannedRun, liveRun)) {
      throw new Error("workflow run changed after planning; refusing stale rerun");
    }
    await apply(`rerunFailedJobs:${runId}`, () => github.rerunFailedJobs(runId));
  }
  if (policyComment.body !== commentBody) {
    await apply("upsertPolicyComment", () => github.upsertPolicyComment(
      identity.prNumber,
      POLICY_COMMENT_MARKER,
      commentBody,
      policyComment,
    ));
  }
  result.status = "complete";
  return result;
}

module.exports = { runCommand };
