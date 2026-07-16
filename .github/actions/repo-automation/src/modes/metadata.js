"use strict";

const { deriveAreaLabels } = require("../areas.js");
const { validateConfig } = require("../config.js");
const { evaluateDco } = require("../dco.js");
const { isManagedMetadataLabel } = require("../managed-labels.js");
const { parseAliases, parseOwnersFile, resolveOwners } = require("../owners.js");
const {
  POLICY_COMMENT_MARKER,
  hasValidPolicyCommentStructure,
  renderPolicyComment,
} = require("../policy-comment.js");
const { parsePolicyState } = require("../commands/state.js");
const { selectReviewers } = require("../reviewer-selection.js");
const { classifySize } = require("../size.js");
const { classifyTitle } = require("../title.js");

const GITHUB_LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const REPOSITORY_NAME = /^[A-Za-z0-9_.-]{1,100}$/;

class MetadataPolicyError extends Error {
  constructor(failures, summary) {
    super(`PR metadata policy failed: ${failures.join(", ")}`);
    this.name = "MetadataPolicyError";
    this.summary = summary;
  }
}

function eventIdentity(event) {
  const owner = event?.repository?.owner?.login;
  const repo = event?.repository?.name;
  const prNumber = event?.number;
  if (
    typeof owner !== "string"
    || !GITHUB_LOGIN.test(owner)
    || typeof repo !== "string"
    || !REPOSITORY_NAME.test(repo)
    || repo === "."
    || repo === ".."
    || !Number.isSafeInteger(prNumber)
    || prNumber <= 0
    || event?.pull_request === null
    || typeof event?.pull_request !== "object"
    || Array.isArray(event?.pull_request)
    || event.pull_request.number !== prNumber
  ) {
    throw new TypeError("event must identify a valid repository and pull request number");
  }
  if (
    typeof event.repository.full_name !== "string"
    || event.repository.full_name.toLowerCase() !== `${owner}/${repo}`.toLowerCase()
  ) {
    throw new TypeError("event repository identity is inconsistent");
  }
  return { owner: owner.toLowerCase(), repo: repo.toLowerCase(), prNumber };
}

function activeOwnerPaths(config) {
  const values = config?.policy?.activeOwnerFiles;
  if (!Array.isArray(values)) return [];
  const paths = [];
  for (const value of values) {
    if (
      typeof value !== "string"
      || !/^\/(?:[A-Za-z0-9_.-]+\/)*OWNERS$/.test(value)
    ) {
      continue;
    }
    if (!paths.includes(value)) paths.push(value);
  }
  return paths.sort();
}

function labelPlan(current, desired) {
  if (
    !Array.isArray(current)
    || current.some((label) => typeof label !== "string" || label === "" || /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u.test(label))
  ) {
    throw new TypeError("live issue labels must be strings");
  }
  const currentByName = new Map();
  for (const label of current) {
    const normalized = label.toLowerCase();
    if (currentByName.has(normalized)) throw new TypeError("live issue labels must be unique");
    currentByName.set(normalized, label);
  }
  const desiredByName = new Map(desired.map((label) => [label.toLowerCase(), label]));
  return {
    add: [...desiredByName]
      .filter(([name]) => !currentByName.has(name))
      .map(([, label]) => label)
      .sort(),
    remove: [...currentByName]
      .filter(([name, label]) => isManagedMetadataLabel(label) && !desiredByName.has(name))
      .map(([, label]) => label)
      .sort(),
  };
}

function configurationResult(config) {
  try {
    validateConfig(config);
    return { valid: true, error: null };
  } catch {
    return { valid: false, error: "repository automation configuration is invalid" };
  }
}

function changedLineTotals(files) {
  let additions = 0;
  let deletions = 0;
  for (const file of files) {
    if (!Number.isSafeInteger(file.additions) || file.additions < 0) {
      throw new TypeError("file additions must be a non-negative safe integer");
    }
    if (!Number.isSafeInteger(file.deletions) || file.deletions < 0) {
      throw new TypeError("file deletions must be a non-negative safe integer");
    }
    additions += file.additions;
    deletions += file.deletions;
    if (!Number.isSafeInteger(additions) || !Number.isSafeInteger(deletions)) {
      throw new TypeError("pull request line totals must be safe integers");
    }
  }
  return { additions, deletions };
}

function safeConfigurationComputation(configuration, operation, fallback) {
  try {
    return operation();
  } catch (error) {
    if (configuration.valid) throw error;
    return fallback;
  }
}

function policyFailureNames(result) {
  const failures = [];
  if (!result.configuration.valid) failures.push("configuration");
  if (!result.title.valid) failures.push("title");
  if (!result.dco.valid) failures.push("DCO");
  if (!result.ownership.valid) failures.push("ownership");
  return failures;
}

function validateLivePullRequest(pullRequest, identity) {
  if (
    pullRequest === null
    || typeof pullRequest !== "object"
    || Array.isArray(pullRequest)
    || pullRequest.number !== identity.prNumber
    || pullRequest.state !== "open"
    || typeof pullRequest.draft !== "boolean"
    || typeof pullRequest.title !== "string"
    || typeof pullRequest.author !== "string"
    || !GITHUB_LOGIN.test(pullRequest.author)
    || typeof pullRequest.headOid !== "string"
    || pullRequest.headOid === ""
    || pullRequest.baseRepository?.owner?.toLowerCase() !== identity.owner
    || pullRequest.baseRepository?.repo?.toLowerCase() !== identity.repo
  ) {
    throw new Error("live pull request state or base repository is invalid");
  }
}

function samePullRequestFence(planned, current) {
  return planned.number === current.number
    && planned.state === current.state
    && planned.headOid === current.headOid
    && planned.title === current.title
    && planned.draft === current.draft
    && planned.author.toLowerCase() === current.author.toLowerCase()
    && planned.baseRepository.owner.toLowerCase() === current.baseRepository.owner.toLowerCase()
    && planned.baseRepository.repo.toLowerCase() === current.baseRepository.repo.toLowerCase();
}

function operation(name, details = {}) {
  return { operation: name, ...details };
}

function classifyReviewState(commentBody, headOid) {
  const parsed = hasValidPolicyCommentStructure(commentBody)
    ? parsePolicyState(commentBody)
    : null;
  if (parsed !== null && parsed.headOid === headOid) {
    return { reset: false, state: parsed };
  }
  return {
    reset: true,
    state: { headOid, lgtm: null, lastRetest: null },
  };
}

function reviewLabelPlan(current, reset) {
  if (!reset) return { add: [], remove: [] };
  const byName = new Map(current.map((label) => [label.toLowerCase(), label]));
  return {
    add: byName.has("do-not-merge/needs-approval")
      ? []
      : ["do-not-merge/needs-approval"],
    remove: ["lgtm", "approved"]
      .map((name) => byName.get(name))
      .filter((label) => label !== undefined),
  };
}

async function runMetadata({ event, github, config, dryRun }) {
  if (typeof dryRun !== "boolean") throw new TypeError("dryRun must be a boolean");
  const identity = eventIdentity(event);
  const configuration = configurationResult(config);
  const ownerPaths = activeOwnerPaths(config);

  const pullRequest = await github.getPullRequest(identity.prNumber);
  validateLivePullRequest(pullRequest, identity);
  const files = await github.listPullRequestFiles(identity.prNumber);
  const commits = await github.listPullRequestCommits(identity.prNumber);
  const reviews = await github.listPullRequestReviews(identity.prNumber);
  const requested = await github.listRequestedReviewers(identity.prNumber);
  const issueLabels = await github.listIssueLabels(identity.prNumber);
  if (!Array.isArray(files) || !Array.isArray(commits) || !Array.isArray(reviews) || !Array.isArray(requested)) {
    throw new TypeError("GitHub list responses must be arrays");
  }
  if (commits.length === 0 || commits.at(-1)?.sha !== pullRequest.headOid) {
    throw new Error("commit snapshot does not end at the live pull request head");
  }

  const defaultBranchRevision = await github.getDefaultBranchRevision();
  const ownerSources = [];
  for (const path of ownerPaths) {
    ownerSources.push({
      path,
      source: await github.getContentAtRevision(path, defaultBranchRevision),
    });
  }
  const aliasSource = await github.getContentAtRevision(
    "/OWNERS_ALIASES",
    defaultBranchRevision,
  );

  const title = classifyTitle(pullRequest.title);
  const totals = changedLineTotals(files);
  const size = safeConfigurationComputation(
    configuration,
    () => classifySize(totals.additions, totals.deletions, config.policy.sizeThresholds),
    null,
  );
  const areas = safeConfigurationComputation(
    configuration,
    () => deriveAreaLabels(files.map((file) => file.path), config.areas),
    [],
  );
  const evaluatedDco = safeConfigurationComputation(
    configuration,
    () => evaluateDco(commits, config.policy.bots),
    { valid: false, failures: [], exempted: [] },
  );
  const dco = {
    valid: evaluatedDco.valid,
    failures: evaluatedDco.failures.map(({ sha }) => ({
      sha,
      reason: "missing or mismatched Signed-off-by trailer",
    })),
    exempted: [...evaluatedDco.exempted],
  };

  const ownershipResolution = safeConfigurationComputation(configuration, () => {
    const aliases = parseAliases(aliasSource);
    const declarations = ownerSources.map(({ path, source }) => parseOwnersFile(source, path));
    return resolveOwners(files.map((file) => file.path), declarations, aliases, {
      activeOwnerFiles: ownerPaths,
      pullRequestAuthor: pullRequest.author,
    });
  }, {
    files: files.map((file) => ({ path: file.path, reviewers: [], approvers: [] })),
    reviewerCandidates: [],
    approverCandidates: [],
    uncoveredPaths: files.map((file) => file.path).sort(),
  });
  const ownership = {
    valid: ownershipResolution.uncoveredPaths.length === 0,
    uncoveredPaths: ownershipResolution.uncoveredPaths,
  };

  const reviewerSelection = safeConfigurationComputation(configuration, () => selectReviewers({
    candidates: ownershipResolution.reviewerCandidates,
    files: files.map((file) => ({
      path: file.path,
      additions: file.additions,
      deletions: file.deletions,
      reviewers: ownershipResolution.files.find((owned) => owned.path === file.path)?.reviewers ?? [],
    })),
    requested,
    author: pullRequest.author,
    target: config.policy.review.reviewerTarget,
    seed: { owner: identity.owner, repo: identity.repo, pr: identity.prNumber },
  }), { selected: [], preserved: [], uncoveredPaths: files.map((file) => file.path).sort() });
  const reviewers = {
    request: reviewerSelection.selected
      .filter((login) => login.toLowerCase() !== pullRequest.author.toLowerCase())
      .filter((login) => !requested.some((existing) => existing.toLowerCase() === login.toLowerCase()))
      .sort(),
    preserved: reviewerSelection.preserved.sort(),
  };

  const desiredLabels = [
    ...(title.valid ? [title.label] : []),
    ...(size === null ? [] : [size.label]),
    ...areas,
    ...(pullRequest.draft ? ["do-not-merge/work-in-progress"] : []),
  ];
  const labels = labelPlan(issueLabels, desiredLabels);
  const existingComment = await github.getPolicyComment(identity.prNumber, POLICY_COMMENT_MARKER);
  const reviewState = classifyReviewState(existingComment.body, pullRequest.headOid);
  const reviewLabels = reviewLabelPlan(issueLabels, reviewState.reset);
  const showResetReason = reviewState.reset
    || existingComment.body?.includes("Review state reset: pull request head changed.") === true;
  const resultBase = {
    headOid: pullRequest.headOid,
    valid: configuration.valid && title.valid && dco.valid && ownership.valid,
    configuration,
    title: { valid: title.valid, error: title.error },
    dco,
    ownership,
    labels,
    reviewState: {
      reset: reviewState.reset,
      reason: reviewState.reset ? "Review state reset: pull request head changed." : null,
      labels: reviewLabels,
    },
    reviewers,
    apply: { status: dryRun ? "planned" : "pending", attempted: [], applied: [], failed: null },
  };
  const commentBody = renderPolicyComment(resultBase, reviewState.state, {
    reviewStateReset: showResetReason,
  });
  const result = {
    ...resultBase,
    comment: { marker: POLICY_COMMENT_MARKER, ...existingComment, body: commentBody },
  };
  const failures = policyFailureNames(result);

  if (!dryRun) {
    const currentPullRequest = await github.getPullRequest(identity.prNumber);
    validateLivePullRequest(currentPullRequest, identity);
    if (!samePullRequestFence(pullRequest, currentPullRequest)) {
      throw new Error("pull request state changed after planning; refusing stale writes");
    }

    const apply = async (descriptor, mutation) => {
      result.apply.attempted.push(descriptor);
      try {
        await mutation();
        result.apply.applied.push(descriptor);
      } catch (error) {
        result.apply.status = "partial";
        result.apply.failed = descriptor;
        const failure = error instanceof Error
          ? error
          : new Error("metadata mutation failed");
        failure.summary = result;
        throw failure;
      }
    };
    if (reviewLabels.add.length > 0) {
      await apply(operation("addPolicyLabel", { label: reviewLabels.add[0] }), () => (
        github.addPolicyLabel(identity.prNumber, reviewLabels.add[0])
      ));
    }
    for (const label of reviewLabels.remove) {
      await apply(operation("removePolicyLabel", { label }), () => (
        github.removePolicyLabel(identity.prNumber, label)
      ));
    }
    if (failures.length === 0) {
      for (const label of labels.add) {
        await apply(operation("addIssueLabel", { label }), () => (
          github.addIssueLabel(identity.prNumber, label)
        ));
      }
      for (const label of labels.remove) {
        await apply(operation("removeIssueLabel", { label }), () => (
          github.removeIssueLabel(identity.prNumber, label)
        ));
      }
      if (reviewers.request.length > 0) {
        await apply(operation("requestReviewers", { reviewers: [...reviewers.request] }), () => (
          github.requestReviewers(identity.prNumber, reviewers.request)
        ));
      }
    }
    try {
      const finalPullRequest = await github.getPullRequest(identity.prNumber);
      validateLivePullRequest(finalPullRequest, identity);
      if (!samePullRequestFence(pullRequest, finalPullRequest)) {
        throw new Error("pull request state changed before policy comment; refusing stale evidence");
      }
    } catch (error) {
      result.apply.status = "partial";
      result.apply.failed = operation("finalHeadFence");
      const failure = error instanceof Error
        ? error
        : new Error("final pull request identity fence failed");
      failure.summary = result;
      throw failure;
    }
    await apply(operation("upsertPolicyComment", { action: existingComment.action }), () => (
      github.upsertPolicyComment(
        identity.prNumber,
        POLICY_COMMENT_MARKER,
        commentBody,
        existingComment,
      )
    ));
    result.apply.status = "complete";
  }

  if (failures.length > 0) throw new MetadataPolicyError(failures, result);
  return result;
}

module.exports = { runMetadata };
