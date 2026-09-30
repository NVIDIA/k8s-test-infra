"use strict";

const { deriveAreaLabels } = require("../areas.js");
const { validateConfig } = require("../config.js");
const { MAX_API_COLLECTION_ITEMS, MAX_CHANGED_FILES } = require("../limits.js");
const { classifySize } = require("../size.js");
const { classifyTitle } = require("../title.js");
const { route, validateCandidates, validBaseBranch } = require("./conflict-labels.js");
const { changedLineTotals, desiredMetadataLabels, labelPlan } = require("./metadata.js");

function validOid(value) {
  return typeof value === "string" && /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/.test(value) && !/^0+$/.test(value);
}

async function snapshot(github, identity, prNumber, config, policyRevision) {
  const pr = await github.getPullRequest(prNumber);
  if (pr?.number !== prNumber || pr.state !== "open" || !validBaseBranch(pr.baseBranch)
    || (identity.branch !== null && pr.baseBranch !== identity.branch)
    || typeof pr.baseRepository?.owner !== "string" || pr.baseRepository.owner.toLowerCase() !== identity.owner
    || typeof pr.baseRepository?.repo !== "string" || pr.baseRepository.repo.toLowerCase() !== identity.repo) {
    return { reason: "unsupported live PR identity or state" };
  }
  if (typeof pr.nodeId !== "string" || pr.nodeId === "" || !validOid(pr.headOid)
    || typeof pr.title !== "string" || typeof pr.draft !== "boolean"
    || typeof pr.author !== "string" || pr.author.length === 0 || pr.author.length > 100
    || pr.author.trim() !== pr.author || /[\x00-\x20\x7f]/.test(pr.author)) {
    return { reason: "invalid live PR metadata identity" };
  }
  const branch = await github.getBranch(pr.baseBranch);
  const revision = await github.getDefaultBranchRevision();
  if (branch?.name !== pr.baseBranch || !validOid(branch.oid) || revision !== policyRevision) {
    return { reason: "live base tip or trusted policy revision changed" };
  }
  const files = await github.listPullRequestFiles(prNumber);
  const labels = await github.listIssueLabels(prNumber);
  if (!Array.isArray(files) || files.length > MAX_CHANGED_FILES
    || files.some((file) => file === null || typeof file !== "object")
    || !Array.isArray(labels) || labels.length > MAX_API_COLLECTION_ITEMS) {
    throw new TypeError("metadata label scan collections are invalid or exceed limits");
  }
  const totals = changedLineTotals(files);
  const title = classifyTitle(pr.title);
  const desired = desiredMetadataLabels({
    title,
    size: classifySize(totals.additions, totals.deletions, config.policy.sizeThresholds),
    areas: deriveAreaLabels(files.map((file) => file.path), config.areas),
    draft: pr.draft,
  });
  // Validate current labels before inspecting them. An invalid title cannot select a new kind.
  labelPlan(labels, desired);
  const preservedKinds = title.valid ? [] : labels.filter((label) => label.toLowerCase().startsWith("kind/"));
  return {
    fence: { number: prNumber, nodeId: pr.nodeId, headOid: pr.headOid, title: pr.title,
      draft: pr.draft, author: pr.author.toLowerCase(), baseBranch: pr.baseBranch,
      baseOid: branch.oid, policyRevision: revision, desired: [...new Set(desired)].sort() },
    labels: labelPlan(labels, [...desired, ...preservedKinds]),
    titleValid: title.valid,
  };
}

function hasApplied(record) {
  return record.applied.add + record.applied.remove > 0;
}

function failure(summary, record, phase) {
  record.status = "failed";
  record.reason = `${phase} failed`;
  summary.status = summary.results.some(hasApplied) ? "partial" : "failed";
  const error = new Error(`metadata label ${phase} failed for PR ${record.prNumber}`);
  error.summary = summary;
  return error;
}

async function runMetadataLabels({ event, eventName, github, config, dryRun, policyRevision }) {
  if (typeof dryRun !== "boolean") throw new TypeError("dryRun must be a boolean");
  const identity = route(event, eventName);
  if (identity.numbers !== null) throw new TypeError("metadata label scan event route is unsupported");
  if (!validOid(policyRevision)) throw new TypeError("metadata label scan policy revision is invalid");
  const summary = { mode: "metadata-labels", dryRun, status: "planning", results: [] };
  try {
    validateConfig(config);
  } catch {
    return { ...summary, status: "deferred", reason: "repository automation configuration is invalid" };
  }
  const numbers = validateCandidates(await github.listOpenPullRequestNumbers());
  const plans = [];
  // Read every candidate before the first write. Never publish a truncated scan as complete.
  for (const prNumber of numbers) {
    const record = { prNumber, status: "pending", applied: { add: 0, remove: 0 } };
    summary.results.push(record);
    let planned;
    try {
      planned = await snapshot(github, identity, prNumber, config, policyRevision);
    } catch {
      throw failure(summary, record, "initial read");
    }
    if (planned.reason !== undefined) {
      record.status = "deferred";
      record.reason = planned.reason;
      continue;
    }
    record.planned = { add: planned.labels.add.length, remove: planned.labels.remove.length };
    record.status = record.planned.add + record.planned.remove === 0 ? "unchanged" : "planned";
    if (!planned.titleValid) record.reason = "invalid title; preserve existing kind labels";
    plans.push({ record, planned });
  }
  if (!dryRun) {
    for (const { record, planned } of plans) {
      if (record.planned.add + record.planned.remove === 0) continue;
      const attempted = new Set();
      // Reconcile fresh labels too, including labels added after the initial read.
      for (let writes = 0; ; writes += 1) {
        let current;
        try {
          current = await snapshot(github, identity, record.prNumber, config, policyRevision);
        } catch {
          throw failure(summary, record, "fresh read");
        }
        if (current.reason !== undefined || JSON.stringify(current.fence) !== JSON.stringify(planned.fence)) {
          record.status = "deferred";
          record.reason = "live metadata state changed at fresh check";
          break;
        }
        const action = current.labels.add.length > 0 ? "add" : "remove";
        const label = current.labels[action][0];
        if (label === undefined) {
          record.status = hasApplied(record) ? "applied" : "unchanged";
          break;
        }
        if (writes >= MAX_API_COLLECTION_ITEMS * 2) {
          throw failure(summary, record, "reconciliation limit");
        }
        const operation = `${action}\0${label.toLowerCase()}`;
        if (attempted.has(operation)) throw failure(summary, record, "stalled reconciliation");
        try {
          if (action === "add") await github.addIssueLabel(record.prNumber, label);
          else await github.removeIssueLabel(record.prNumber, label);
          record.applied[action] += 1;
          attempted.add(operation);
        } catch {
          throw failure(summary, record, "label mutation");
        }
      }
    }
  }
  summary.status = dryRun ? "planned"
    : summary.results.some((record) => record.status === "deferred") ? "deferred" : "complete";
  return summary;
}

module.exports = { runMetadataLabels };
