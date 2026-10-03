// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

"use strict";

const { verifyApproverAuthor } = require("./author-approval.js");
const { validateConfig } = require("./config.js");
const { evaluateDco } = require("./dco.js");
const { MAX_API_COLLECTION_ITEMS } = require("./limits.js");
const { parseAliases, parseOwnersFile, resolveOwners } = require("./owners.js");
const { classifyTitle } = require("./title.js");

function configurationResult(config) {
  try {
    validateConfig(config);
    return { valid: true, error: null };
  } catch {
    return { valid: false, error: "repository automation configuration is invalid" };
  }
}

function safeConfigurationComputation(configuration, operation, fallback) {
  try {
    return operation();
  } catch (error) {
    if (configuration.valid) throw error;
    return fallback;
  }
}

function activeOwnerPaths(config) {
  const values = config?.policy?.activeOwnerFiles;
  if (!Array.isArray(values)) return [];
  return [...new Set(values.filter((value) => typeof value === "string"
    && /^\/(?:[A-Za-z0-9_.-]+\/)*OWNERS$/.test(value)))].sort();
}

// This helper only reads evidence. Its caller owns the input fence and all mutations.
async function readMetadataEvidence({ github, config, pullRequest, files, policyRevision,
  commits, configuration = configurationResult(config) }) {
  const commitSnapshot = commits ?? await github.listPullRequestCommits(pullRequest.number);
  if (!Array.isArray(commitSnapshot) || commitSnapshot.length > MAX_API_COLLECTION_ITEMS
    || commitSnapshot.length === 0 || commitSnapshot.at(-1)?.sha !== pullRequest.headOid) {
    throw new Error("commit snapshot does not end at the live pull request head");
  }
  const ownerPaths = activeOwnerPaths(config);
  const ownerSources = [];
  for (const path of ownerPaths) {
    ownerSources.push({ path, source: await github.getContentAtRevision(path, policyRevision) });
  }
  const aliasSource = await github.getContentAtRevision("/OWNERS_ALIASES", policyRevision);
  const title = classifyTitle(pullRequest.title);
  const evaluatedDco = safeConfigurationComputation(configuration,
    () => evaluateDco(commitSnapshot, config.policy.bots), { valid: false, failures: [], exempted: [] });
  const dco = {
    valid: evaluatedDco.valid,
    failures: evaluatedDco.failures.map(({ sha }) => ({ sha,
      reason: "missing or mismatched Signed-off-by trailer" })),
    exempted: [...evaluatedDco.exempted],
  };
  const ownershipResolution = safeConfigurationComputation(configuration, () => {
    const aliases = parseAliases(aliasSource);
    const declarations = ownerSources.map(({ path, source }) => parseOwnersFile(source, path));
    return resolveOwners(files.map((file) => file.path), declarations, aliases, {
      activeOwnerFiles: ownerPaths, pullRequestAuthor: pullRequest.author,
    });
  }, {
    files: files.map((file) => ({ path: file.path, reviewers: [], approvers: [] })),
    reviewerCandidates: [], approverCandidates: [],
    uncoveredPaths: files.map((file) => file.path).sort(), authorApprovalPaths: [],
  });
  const authorIsHuman = await verifyApproverAuthor(github, ownershipResolution, pullRequest.author);
  const authorPaths = new Set(authorIsHuman ? ownershipResolution.authorApprovalPaths : []);
  const uncoveredPaths = ownershipResolution.uncoveredPaths.filter((path) => !authorPaths.has(path));
  const ownership = { valid: uncoveredPaths.length === 0, uncoveredPaths };
  return { headOid: pullRequest.headOid,
    valid: configuration.valid && title.valid && dco.valid && ownership.valid,
    configuration, title, dco, ownership, ownershipResolution, authorIsHuman, authorPaths };
}

module.exports = { configurationResult, safeConfigurationComputation, readMetadataEvidence };
