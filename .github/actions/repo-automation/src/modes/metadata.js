"use strict";

const { deriveAreaLabels } = require("../areas.js");
const { verifyApproverAuthor } = require("../author-approval.js");
const { configurationResult, safeConfigurationComputation, readMetadataEvidence } = require("../metadata-evidence.js");
const { asciiLower, isManagedMetadataLabel } = require("../managed-labels.js");
const { POLICY_COMMENT_MARKER, renderPolicyComment } = require("../policy-comment.js");
const { selectReviewers } = require("../reviewer-selection.js");
const { classifySize } = require("../size.js");

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

function labelPlan(current, desired) {
  if (
    !Array.isArray(current)
    || current.some((label) => typeof label !== "string" || label === "" || /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u.test(label))
  ) {
    throw new TypeError("live issue labels must be strings");
  }
  const currentByName = new Map();
  for (const label of current) {
    const normalized = asciiLower(label);
    if (currentByName.has(normalized)) throw new TypeError("live issue labels must be unique");
    currentByName.set(normalized, label);
  }
  const desiredByName = new Map(desired.map((label) => [asciiLower(label), label]));
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

function desiredMetadataLabels({ title, size, areas, draft }) {
  return [
    ...(title.valid ? [title.label] : []),
    ...(size === null ? [] : [size.label]),
    ...areas,
    ...(draft ? ["do-not-merge/work-in-progress"] : []),
  ];
}

function policyFailureNames(result) {
  const failures = [];
  if (!result.configuration.valid) failures.push("configuration");
  if (!result.title.valid) failures.push("title");
  if (!result.dco.valid) failures.push("DCO");
  if (!result.ownership.valid) failures.push("ownership");
  return failures;
}

function validAuthorContext(value) {
  if (typeof value !== "string" || /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u.test(value)) return false;
  return GITHUB_LOGIN.test(value.endsWith("[bot]") ? value.slice(0, -5) : value);
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
    || !validAuthorContext(pullRequest.author)
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

async function runMetadata({ event, github, config, dryRun }) {
  if (typeof dryRun !== "boolean") throw new TypeError("dryRun must be a boolean");
  const identity = eventIdentity(event);
  const configuration = configurationResult(config);

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
  const { title, dco, ownership, ownershipResolution, authorIsHuman, authorPaths } = await readMetadataEvidence({
    github, config, pullRequest, files, commits, configuration, policyRevision: defaultBranchRevision,
  });
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

  const desiredLabels = desiredMetadataLabels({ title, size, areas, draft: pullRequest.draft });
  const labels = labelPlan(issueLabels, desiredLabels);
  const resultBase = {
    headOid: pullRequest.headOid,
    valid: configuration.valid && title.valid && dco.valid && ownership.valid,
    configuration,
    title: { valid: title.valid, error: title.error },
    dco,
    ownership,
    labels,
    reviewers,
    apply: { status: dryRun ? "planned" : "pending", attempted: [], applied: [], failed: null },
  };
  const existingComment = await github.getPolicyComment(identity.prNumber, POLICY_COMMENT_MARKER);
  const commentBody = renderPolicyComment(resultBase, existingComment.body);
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
    if (authorIsHuman && ownershipResolution.uncoveredPaths.some((path) => authorPaths.has(path))) {
      const latestRevision = await github.getDefaultBranchRevision();
      const authorStillHuman = await verifyApproverAuthor(github, ownershipResolution, pullRequest.author);
      if (latestRevision !== defaultBranchRevision || !authorStillHuman) {
        throw new Error("author ownership changed after planning; refusing stale writes");
      }
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

module.exports = { runMetadata, changedLineTotals, desiredMetadataLabels, labelPlan };
