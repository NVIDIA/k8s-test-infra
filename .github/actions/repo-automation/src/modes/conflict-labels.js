"use strict";

const { asciiLower } = require("../managed-labels.js");

const LABEL = "needs-rebase";
const MAX_CANDIDATES = 100;
const OID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const PR_ACTIONS = new Set([
  "opened", "reopened", "synchronize", "edited", "ready_for_review", "converted_to_draft",
]);

function supportedBranch(branch) {
  return branch === "main" || (typeof branch === "string" && /^release-[A-Za-z0-9][A-Za-z0-9._-]*$/.test(branch)
    && !branch.includes("..") && !branch.endsWith(".") && !branch.endsWith(".lock"));
}

function validBaseBranch(branch) {
  return typeof branch === "string" && branch !== "" && branch !== "@" && !branch.startsWith("-")
    && !/[\x00-\x20\x7f~^:?*\[\\]/.test(branch) && !branch.includes("..") && !branch.includes("@{")
    && !branch.endsWith(".") && branch.split("/").every((part) => part !== ""
      && !part.startsWith(".") && !part.endsWith(".lock"));
}

function route(event, eventName) {
  const owner = event?.repository?.owner?.login;
  const repo = event?.repository?.name;
  if (typeof owner !== "string" || !/^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/.test(owner)
    || typeof repo !== "string" || !/^[A-Za-z0-9_.-]{1,100}$/.test(repo) || [".", ".."].includes(repo)
    || typeof event.repository.full_name !== "string"
    || event.repository.full_name.toLowerCase() !== `${owner}/${repo}`.toLowerCase()) {
    throw new TypeError("event repository identity is invalid");
  }
  const identity = { owner: owner.toLowerCase(), repo: repo.toLowerCase() };
  if (eventName === "pull_request_target") {
    // Synchronize events include before/after SHAs; reconciliation still reads live state.
    if (!PR_ACTIONS.has(event.action) || !Number.isSafeInteger(event.number) || event.number <= 0
      || event.pull_request?.number !== event.number || event.schedule !== undefined || event.ref !== undefined
      || (event.after !== undefined && event.action !== "synchronize")) {
      throw new TypeError("conflict label event route is invalid");
    }
    return { ...identity, numbers: [event.number], branch: null };
  }
  if (event.pull_request !== undefined || event.number !== undefined || event.action !== undefined) {
    throw new TypeError("conflict label event route is ambiguous");
  }
  if (eventName === "schedule" && typeof event.schedule === "string" && event.schedule !== ""
    && event.ref === undefined && event.after === undefined) {
    return { ...identity, numbers: null, branch: null };
  }
  if (eventName === "push" && event.schedule === undefined && event.deleted === false
    && typeof event.ref === "string" && event.ref.startsWith("refs/heads/")
    && typeof event.after === "string" && OID.test(event.after) && !/^0+$/.test(event.after)) {
    const branch = event.ref.slice("refs/heads/".length);
    if (supportedBranch(branch)) return { ...identity, numbers: null, branch };
  }
  throw new TypeError("conflict label event route is unsupported");
}

function validateCandidates(numbers) {
  if (!Array.isArray(numbers) || numbers.length > MAX_CANDIDATES
    || numbers.some((number) => !Number.isSafeInteger(number) || number <= 0)
    || new Set(numbers).size !== numbers.length) {
    throw new TypeError("conflict label scan candidates are invalid or exceed 100");
  }
  return [...numbers].sort((left, right) => left - right);
}

function labelAction(labels, mergeability) {
  if (!Array.isArray(labels) || labels.some((label) => typeof label !== "string" || label === ""
    || /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u.test(label))
    || new Set(labels.map(asciiLower)).size !== labels.length) {
    throw new TypeError("live conflict labels are invalid");
  }
  const present = labels.some((label) => asciiLower(label) === LABEL);
  if (mergeability === "CONFLICTING" && !present) return "add";
  if (mergeability === "MERGEABLE" && present) return "remove";
  return null;
}

async function snapshot(github, identity, prNumber, reportCollector, finalLabels) {
  const pr = await github.getPullRequest(prNumber);
  if (pr?.number !== prNumber || pr.state !== "open" || !validBaseBranch(pr.baseBranch)
    || (identity.branch !== null && pr.baseBranch !== identity.branch)
    || pr.baseRepository?.owner?.toLowerCase() !== identity.owner
    || pr.baseRepository?.repo?.toLowerCase() !== identity.repo) {
    return { reason: "unsupported live PR identity or state", reportReason: "invalid_state" };
  }
  const graph = await github.getConflictState(prNumber);
  const branch = await github.getBranch(pr.baseBranch);
  const labels = finalLabels ?? await github.listIssueLabels(prNumber);
  reportCollector?.validateLabels(labels);
  if (typeof pr.nodeId !== "string" || pr.nodeId === "" || typeof pr.draft !== "boolean"
    || !OID.test(pr.headOid) || graph?.number !== prNumber || graph.nodeId !== pr.nodeId
    || graph.repository !== `${identity.owner}/${identity.repo}` || graph.state !== "OPEN"
    || graph.draft !== pr.draft || graph.headOid !== pr.headOid || graph.baseBranch !== pr.baseBranch
    || !OID.test(graph.baseOid) || branch?.name !== pr.baseBranch || branch.oid !== graph.baseOid
    || !["CONFLICTING", "MERGEABLE", "UNKNOWN"].includes(graph.mergeability)) {
    return { reason: "inconsistent live conflict identity or base tip", reportReason: "invalid_state" };
  }
  let policyRevision;
  if (reportCollector !== undefined) {
    policyRevision = await github.getDefaultBranchRevision();
    if (policyRevision !== reportCollector.context.workflowCommitSha) {
      return { reason: "trusted policy revision changed", reportReason: "invalid_policy" };
    }
  }
  const action = labelAction(labels, graph.mergeability);
  if (graph.mergeability === "UNKNOWN") return { reason: "unknown mergeability; defer", reportReason: "unknown_mergeability" };
  return {
    fence: { number: prNumber, nodeId: pr.nodeId, headOid: pr.headOid, baseBranch: pr.baseBranch,
      baseOid: graph.baseOid, draft: pr.draft, mergeability: graph.mergeability,
      ...(reportCollector === undefined ? {} : { title: pr.title, policyRevision }) },
    action,
    currentLabels: labels,
  };
}

function failure(summary, record, phase, reportCollector, cause) {
  record.status = "failed";
  record.reason = `${phase} failed`;
  summary.status = summary.results.some((result) => result.status === "applied") ? "partial" : "failed";
  const error = new Error(`conflict label ${phase} failed for PR ${record.prNumber}`);
  error.summary = summary;
  reportCollector?.record(record.prNumber, "failed", cause?.reportReason
    ?? (phase.includes("read") ? "read_failed" : "write_failed"));
  return error;
}

async function runConflictLabels({ event, eventName, github, dryRun, reportCollector }) {
  if (typeof dryRun !== "boolean") throw new TypeError("dryRun must be a boolean");
  const identity = reportCollector?.identity ?? route(event, eventName);
  const numbers = validateCandidates(identity.numbers ?? await github.listOpenPullRequestNumbers());
  reportCollector?.setCandidates(numbers);
  const summary = { mode: "conflict-labels", dryRun, status: "planning", results: [] };
  const plans = [];
  // Finish the full initial read phase before the first mutation.
  for (const prNumber of numbers) {
    const record = { prNumber, label: LABEL, status: "pending" };
    summary.results.push(record);
    let planned;
    try {
      planned = await snapshot(github, identity, prNumber, reportCollector);
      if (planned.reason === undefined) reportCollector?.plan(prNumber, planned.fence);
    } catch (cause) {
      throw failure(summary, record, "initial read", reportCollector, cause);
    }
    if (planned.reason !== undefined) {
      record.status = "deferred";
      record.reason = planned.reason;
      reportCollector?.record(prNumber, "deferred", planned.reportReason);
    } else {
      record.action = planned.action;
      record.status = planned.action === null ? "unchanged" : "planned";
      plans.push({ record, planned });
    }
  }
  if (!dryRun) {
    for (const { record, planned } of plans) {
      if (planned.action === null && reportCollector === undefined) continue;
      let current;
      try {
        current = await snapshot(github, identity, record.prNumber, reportCollector);
      } catch (cause) {
        throw failure(summary, record, "fresh read", reportCollector, cause);
      }
      if (current.reason !== undefined || JSON.stringify(current.fence) !== JSON.stringify(planned.fence)) {
        record.status = "deferred";
        record.reason = "live conflict state changed at fresh check";
        reportCollector?.record(record.prNumber, "deferred", "state_changed");
        continue;
      }
      if (current.action === null) {
        if (reportCollector !== undefined) {
          try {
            // Bind the observed labels to an input fence read after their final fetch.
            current = await snapshot(github, identity, record.prNumber, reportCollector, current.currentLabels);
          } catch (cause) {
            throw failure(summary, record, "final read", reportCollector, cause);
          }
          if (current.reason !== undefined || JSON.stringify(current.fence) !== JSON.stringify(planned.fence)) {
            record.status = "deferred";
            record.reason = "live conflict state changed at final check";
            reportCollector.record(record.prNumber, "deferred", "state_changed");
            continue;
          }
        }
        record.status = "unchanged";
        reportCollector?.record(record.prNumber, "unchanged", "none", current.currentLabels);
        continue;
      }
      try {
        if (current.action === "add") await github.addIssueLabel(record.prNumber, LABEL);
        else await github.removeIssueLabel(record.prNumber, LABEL);
        record.status = "applied";
      } catch (cause) {
        throw failure(summary, record, "label mutation", reportCollector, cause);
      }
      if (reportCollector !== undefined) {
        let final;
        try {
          final = await snapshot(github, identity, record.prNumber, reportCollector);
          if (final.reason === undefined && JSON.stringify(final.fence) === JSON.stringify(planned.fence)) {
            final = await snapshot(github, identity, record.prNumber, reportCollector, final.currentLabels);
          }
        } catch (cause) {
          throw failure(summary, record, "final read", reportCollector, cause);
        }
        if (final.reason !== undefined || JSON.stringify(final.fence) !== JSON.stringify(planned.fence)) {
          record.status = "deferred";
          record.reason = "live conflict state changed at final check";
          reportCollector.record(record.prNumber, "deferred", "state_changed");
        } else if (final.action !== null) {
          throw failure(summary, record, "stalled reconciliation", reportCollector);
        } else {
          reportCollector.record(record.prNumber, "applied", "none", final.currentLabels);
        }
      }
    }
  }
  if (dryRun) {
    for (const { record } of plans) reportCollector?.record(record.prNumber, "deferred", "invalid_state");
  }
  summary.status = dryRun ? "planned" : "complete";
  return summary;
}

module.exports = { runConflictLabels, route, validateCandidates, supportedBranch, validBaseBranch };
