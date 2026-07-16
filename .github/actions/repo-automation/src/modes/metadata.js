"use strict";

const { deriveAreaLabels } = require("../areas.js");
const { validateConfig } = require("../config.js");
const { evaluateDco } = require("../dco.js");
const { parseAliases, parseOwnersFile, resolveOwners } = require("../owners.js");
const { POLICY_COMMENT_MARKER, renderPolicyComment } = require("../policy-comment.js");
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

function managedLabel(label) {
  const normalized = label.toLowerCase();
  return normalized.startsWith("kind/")
    || normalized.startsWith("size/")
    || normalized.startsWith("area/")
    || normalized === "do-not-merge/work-in-progress";
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
      .filter(([name, label]) => managedLabel(label) && !desiredByName.has(name))
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

async function runMetadata({ event, github, config, dryRun }) {
  if (typeof dryRun !== "boolean") throw new TypeError("dryRun must be a boolean");
  const identity = eventIdentity(event);
  const configuration = configurationResult(config);
  const ownerPaths = activeOwnerPaths(config);

  const pullRequest = await github.getPullRequest(identity.prNumber);
  if (
    pullRequest.number !== identity.prNumber
    || typeof pullRequest.draft !== "boolean"
    || typeof pullRequest.title !== "string"
    || typeof pullRequest.author !== "string"
    || !GITHUB_LOGIN.test(pullRequest.author)
  ) {
    throw new Error("live pull request metadata is invalid or inconsistent");
  }
  const files = await github.listPullRequestFiles(identity.prNumber);
  const commits = await github.listPullRequestCommits(identity.prNumber);
  const reviews = await github.listPullRequestReviews(identity.prNumber);
  const requested = await github.listRequestedReviewers(identity.prNumber);
  const issueLabels = await github.listIssueLabels(identity.prNumber);
  if (!Array.isArray(files) || !Array.isArray(commits) || !Array.isArray(reviews) || !Array.isArray(requested)) {
    throw new TypeError("GitHub list responses must be arrays");
  }

  const ownerSources = [];
  for (const path of ownerPaths) {
    ownerSources.push({ path, source: await github.getContentAtDefaultBranch(path) });
  }
  const aliasSource = await github.getContentAtDefaultBranch("/OWNERS_ALIASES");

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
  const resultBase = {
    headOid: pullRequest.headOid,
    valid: configuration.valid && title.valid && dco.valid && ownership.valid,
    configuration,
    title: { valid: title.valid, error: title.error },
    dco,
    ownership,
    labels,
    reviewers,
  };
  const commentBody = renderPolicyComment(resultBase);
  const existingComment = await github.planPolicyComment(identity.prNumber, POLICY_COMMENT_MARKER);
  const result = {
    ...resultBase,
    comment: { marker: POLICY_COMMENT_MARKER, body: commentBody, ...existingComment },
  };
  const failures = policyFailureNames(result);

  if (!dryRun) {
    await github.upsertPolicyComment(
      identity.prNumber,
      POLICY_COMMENT_MARKER,
      commentBody,
      existingComment,
    );
    if (failures.length === 0) {
      for (const label of labels.add) await github.addIssueLabel(identity.prNumber, label);
      for (const label of labels.remove) await github.removeIssueLabel(identity.prNumber, label);
      if (reviewers.request.length > 0) {
        await github.requestReviewers(identity.prNumber, reviewers.request);
      }
    }
  }

  if (failures.length > 0) throw new MetadataPolicyError(failures, result);
  return result;
}

module.exports = { runMetadata };
