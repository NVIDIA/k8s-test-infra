"use strict";

const { deriveAreaLabels } = require("../areas.js");
const { validateConfig } = require("../config.js");
const { parsePolicyState } = require("../commands/state.js");
const { MAX_API_COLLECTION_ITEMS, MAX_CHANGED_FILES } = require("../limits.js");
const { asciiLower } = require("../managed-labels.js");
const { readMetadataEvidence } = require("../metadata-evidence.js");
const { POLICY_COMMENT_MARKER, renderPolicyComment } = require("../policy-comment.js");
const { classifySize } = require("../size.js");
const { classifyTitle } = require("../title.js");
const { route, validateCandidates, validBaseBranch } = require("./conflict-labels.js");
const { changedLineTotals, desiredMetadataLabels, labelPlan } = require("./metadata.js");

function validOid(value) {
  return typeof value === "string" && /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/.test(value) && !/^0+$/.test(value);
}

async function snapshot(github, identity, prNumber, config, policyRevision, reportCollector, finalLabels) {
  const pr = await github.getPullRequest(prNumber);
  if (pr?.number !== prNumber || pr.state !== "open" || !validBaseBranch(pr.baseBranch)
    || (identity.branch !== null && pr.baseBranch !== identity.branch)
    || typeof pr.baseRepository?.owner !== "string" || pr.baseRepository.owner.toLowerCase() !== identity.owner
    || typeof pr.baseRepository?.repo !== "string" || pr.baseRepository.repo.toLowerCase() !== identity.repo) {
    return { reason: "unsupported live PR identity or state", reportReason: "invalid_state" };
  }
  if (typeof pr.nodeId !== "string" || pr.nodeId === "" || !validOid(pr.headOid)
    || typeof pr.title !== "string" || typeof pr.draft !== "boolean"
    || typeof pr.author !== "string" || pr.author.length === 0 || pr.author.length > 100
    || pr.author.trim() !== pr.author || /[\x00-\x20\x7f]/.test(pr.author)) {
    return { reason: "invalid live PR metadata identity", reportReason: "invalid_state" };
  }
  const branch = await github.getBranch(pr.baseBranch);
  const revision = await github.getDefaultBranchRevision();
  if (branch?.name !== pr.baseBranch || !validOid(branch.oid) || revision !== policyRevision) {
    return { reason: "live base tip or trusted policy revision changed", reportReason: "invalid_policy" };
  }
  const files = await github.listPullRequestFiles(prNumber);
  const labels = finalLabels ?? await github.listIssueLabels(prNumber);
  reportCollector?.validateLabels(labels);
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
  const preservedKinds = title.valid ? [] : labels.filter((label) => asciiLower(label).startsWith("kind/"));
  return {
    fence: { number: prNumber, nodeId: pr.nodeId, headOid: pr.headOid, title: pr.title,
      draft: pr.draft, author: pr.author.toLowerCase(), baseBranch: pr.baseBranch,
      baseOid: branch.oid, policyRevision: revision, desired: [...new Set(desired)].sort() },
    labels: labelPlan(labels, [...desired, ...preservedKinds]),
    currentLabels: labels,
    titleValid: title.valid,
    pullRequest: pr,
    files,
  };
}

function hasApplied(record) {
  return record.applied.add + record.applied.remove + (record.applied.comment ?? 0) > 0;
}

function sameSnapshot(planned, current) {
  return current.reason === undefined
    && JSON.stringify(current.fence) === JSON.stringify(planned.fence)
    && JSON.stringify(current.files) === JSON.stringify(planned.files);
}

function validateComment(comment, identity, prNumber) {
  if (comment?.action === "create" && comment.id === null && comment.body === null) return comment;
  if (comment?.action !== "update" || !Number.isSafeInteger(comment.id) || comment.id <= 0
    || typeof comment.body !== "string" || comment.body.split(POLICY_COMMENT_MARKER).length !== 2) {
    throw new Error("trusted policy comment identity is invalid");
  }
  if (comment.body.includes("<!-- repo-automation-state:")) {
    const state = parsePolicyState(comment.body);
    const repository = `${identity.owner}/${identity.repo}`;
    const belongs = (value) => value.repository === repository && value.pullRequest === prNumber;
    if (state === null || !belongs(state) || !state.lgtms.every(belongs) || !state.approvals.every(belongs)
      || (state.hold !== null && !belongs(state.hold))) {
      throw new Error("trusted policy command state is invalid");
    }
  }
  return comment;
}

async function commentPlan(github, identity, prNumber, config, policyRevision, input) {
  const evidence = await readMetadataEvidence({ github, config, policyRevision,
    pullRequest: input.pullRequest, files: input.files });
  const comment = validateComment(await github.getPolicyComment(prNumber, POLICY_COMMENT_MARKER), identity, prNumber);
  const body = renderPolicyComment({ ...evidence, labels: input.labels,
    reviewers: { request: [], preserved: [] } }, comment.body);
  return { comment, body };
}

function deferChanged(record, reportCollector) {
  record.status = "deferred";
  record.reason = "live metadata state changed at fresh check";
  reportCollector?.record(record.prNumber, "deferred", "state_changed");
}

function failure(summary, record, phase, reportCollector, cause) {
  record.status = "failed";
  record.reason = `${phase} failed`;
  summary.status = summary.results.some(hasApplied) ? "partial" : "failed";
  const error = new Error(`metadata label ${phase} failed for PR ${record.prNumber}`);
  error.summary = summary;
  reportCollector?.record(record.prNumber, "failed", cause?.reportReason
    ?? (phase.includes("read") ? "read_failed" : "write_failed"));
  return error;
}

async function runMetadataLabels({ event, eventName, github, config, dryRun, policyRevision, reportCollector }) {
  if (typeof dryRun !== "boolean") throw new TypeError("dryRun must be a boolean");
  const identity = reportCollector?.identity ?? route(event, eventName);
  if (identity.numbers !== null) throw new TypeError("metadata label scan event route is unsupported");
  if (!validOid(policyRevision)) throw new TypeError("metadata label scan policy revision is invalid");
  const summary = { mode: "metadata-labels", dryRun, status: "planning", results: [] };
  let configurationValid = true;
  try {
    validateConfig(config);
  } catch {
    if (reportCollector === undefined) {
      return { ...summary, status: "deferred", reason: "repository automation configuration is invalid" };
    }
    configurationValid = false;
  }
  const numbers = validateCandidates(await github.listOpenPullRequestNumbers());
  reportCollector?.setCandidates(numbers);
  if (!configurationValid || (reportCollector !== undefined && policyRevision !== reportCollector.context.workflowCommitSha)) {
    for (const prNumber of numbers) {
      summary.results.push({ prNumber, status: "deferred", reason: "repository automation configuration is invalid",
        applied: { add: 0, remove: 0 } });
      reportCollector.record(prNumber, "deferred", "invalid_policy");
    }
    return { ...summary, status: "deferred" };
  }
  const plans = [];
  // Read every candidate before the first write. Never publish a truncated scan as complete.
  for (const prNumber of numbers) {
    const record = { prNumber, status: "pending", applied: { add: 0, remove: 0 } };
    summary.results.push(record);
    let planned;
    try {
      planned = await snapshot(github, identity, prNumber, config, policyRevision, reportCollector);
      if (planned.reason === undefined) {
        planned.policy = await commentPlan(github, identity, prNumber, config, policyRevision, planned);
        reportCollector?.plan(prNumber, { ...planned.fence, mergeability: null });
      }
    } catch (cause) {
      throw failure(summary, record, "initial read", reportCollector, cause);
    }
    if (planned.reason !== undefined) {
      record.status = "deferred";
      record.reason = planned.reason;
      reportCollector?.record(prNumber, "deferred", planned.reportReason);
      continue;
    }
    record.planned = { add: planned.labels.add.length, remove: planned.labels.remove.length };
    record.status = record.planned.add + record.planned.remove === 0 ? "unchanged" : "planned";
    if (!planned.titleValid) record.reason = "invalid title; preserve existing kind labels";
    plans.push({ record, planned });
  }
  if (!dryRun) {
    for (const { record, planned } of plans) {
      const attempted = new Set();
      // Reconcile fresh labels too, including labels added after the initial read.
      for (let writes = 0; ; writes += 1) {
        let current;
        try {
          current = await snapshot(github, identity, record.prNumber, config, policyRevision, reportCollector);
        } catch (cause) {
          throw failure(summary, record, "fresh read", reportCollector, cause);
        }
        if (!sameSnapshot(planned, current)) {
          deferChanged(record, reportCollector);
          break;
        }
        const action = current.labels.add.length > 0 ? "add" : "remove";
        const label = current.labels[action][0];
        if (label === undefined) {
          let freshPolicy;
          try {
            freshPolicy = await commentPlan(github, identity, record.prNumber, config, policyRevision, current);
            // Bind the evidence and observed labels to a fence read immediately before the comment write.
            current = await snapshot(github, identity, record.prNumber, config, policyRevision,
              reportCollector, current.currentLabels);
          } catch (cause) {
            throw failure(summary, record, "comment read", reportCollector, cause);
          }
          if (!sameSnapshot(planned, current) || freshPolicy.body !== planned.policy.body
            || JSON.stringify(freshPolicy.comment) !== JSON.stringify(planned.policy.comment)) {
            deferChanged(record, reportCollector);
            break;
          }
          let lastComment;
          try {
            lastComment = validateComment(await github.getPolicyComment(record.prNumber,
              POLICY_COMMENT_MARKER), identity, record.prNumber);
          } catch (cause) {
            throw failure(summary, record, "comment read", reportCollector, cause);
          }
          if (JSON.stringify(lastComment) !== JSON.stringify(freshPolicy.comment)) {
            deferChanged(record, reportCollector);
            break;
          }
          if (freshPolicy.body !== freshPolicy.comment.body) {
            try {
              await github.upsertPolicyComment(record.prNumber, POLICY_COMMENT_MARKER,
                freshPolicy.body, freshPolicy.comment);
              record.applied.comment = 1;
            } catch (cause) {
              throw failure(summary, record, "comment mutation", reportCollector, cause);
            }
          }
          let verifiedComment;
          try {
            const finalLabels = await github.listIssueLabels(record.prNumber);
            verifiedComment = validateComment(await github.getPolicyComment(record.prNumber,
              POLICY_COMMENT_MARKER), identity, record.prNumber);
            current = await snapshot(github, identity, record.prNumber, config, policyRevision,
              reportCollector, finalLabels);
          } catch (cause) {
            throw failure(summary, record, "final read", reportCollector, cause);
          }
          if (!sameSnapshot(planned, current)) {
            deferChanged(record, reportCollector);
            break;
          }
          if (verifiedComment.action !== "update" || verifiedComment.body !== freshPolicy.body
            || (freshPolicy.comment.action === "update" && verifiedComment.id !== freshPolicy.comment.id)
            || current.labels.add.length + current.labels.remove.length !== 0) {
            throw failure(summary, record, "comment verification", reportCollector);
          }
          record.status = hasApplied(record) ? "applied" : "unchanged";
          reportCollector?.record(record.prNumber, record.status, "none", current.currentLabels);
          break;
        }
        if (writes >= MAX_API_COLLECTION_ITEMS * 2) {
          throw failure(summary, record, "reconciliation limit", reportCollector);
        }
        const operation = `${action}\0${asciiLower(label)}`;
        if (attempted.has(operation)) throw failure(summary, record, "stalled reconciliation", reportCollector);
        try {
          if (action === "add") await github.addIssueLabel(record.prNumber, label);
          else await github.removeIssueLabel(record.prNumber, label);
          record.applied[action] += 1;
          attempted.add(operation);
        } catch (cause) {
          throw failure(summary, record, "label mutation", reportCollector, cause);
        }
      }
    }
  }
  if (dryRun) {
    for (const { record } of plans) reportCollector?.record(record.prNumber, "deferred", "invalid_state");
  }
  summary.status = dryRun ? "planned"
    : summary.results.some((record) => record.status === "deferred") ? "deferred" : "complete";
  return summary;
}

module.exports = { runMetadataLabels };
