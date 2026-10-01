"use strict";

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
    if (!PR_ACTIONS.has(event.action) || !Number.isSafeInteger(event.number) || event.number <= 0
      || event.pull_request?.number !== event.number || event.schedule !== undefined || event.ref !== undefined
      || event.after !== undefined) throw new TypeError("conflict label event route is invalid");
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
    || new Set(labels.map((label) => label.toLowerCase())).size !== labels.length) {
    throw new TypeError("live conflict labels are invalid");
  }
  const present = labels.some((label) => label.toLowerCase() === LABEL);
  if (mergeability === "CONFLICTING" && !present) return "add";
  if (mergeability === "MERGEABLE" && present) return "remove";
  return null;
}

async function snapshot(github, identity, prNumber) {
  const pr = await github.getPullRequest(prNumber);
  if (pr?.number !== prNumber || pr.state !== "open" || !validBaseBranch(pr.baseBranch)
    || (identity.branch !== null && pr.baseBranch !== identity.branch)
    || pr.baseRepository?.owner?.toLowerCase() !== identity.owner
    || pr.baseRepository?.repo?.toLowerCase() !== identity.repo) {
    return { reason: "unsupported live PR identity or state" };
  }
  const graph = await github.getConflictState(prNumber);
  const branch = await github.getBranch(pr.baseBranch);
  const labels = await github.listIssueLabels(prNumber);
  if (typeof pr.nodeId !== "string" || pr.nodeId === "" || typeof pr.draft !== "boolean"
    || !OID.test(pr.headOid) || graph?.number !== prNumber || graph.nodeId !== pr.nodeId
    || graph.repository !== `${identity.owner}/${identity.repo}` || graph.state !== "OPEN"
    || graph.draft !== pr.draft || graph.headOid !== pr.headOid || graph.baseBranch !== pr.baseBranch
    || !OID.test(graph.baseOid) || branch?.name !== pr.baseBranch || branch.oid !== graph.baseOid
    || !["CONFLICTING", "MERGEABLE", "UNKNOWN"].includes(graph.mergeability)) {
    return { reason: "inconsistent live conflict identity or base tip" };
  }
  const action = labelAction(labels, graph.mergeability);
  if (graph.mergeability === "UNKNOWN") return { reason: "unknown mergeability; defer" };
  return {
    fence: { number: prNumber, nodeId: pr.nodeId, headOid: pr.headOid, baseBranch: pr.baseBranch,
      baseOid: graph.baseOid, draft: pr.draft, mergeability: graph.mergeability },
    action,
  };
}

function failure(summary, record, phase) {
  record.status = "failed";
  record.reason = `${phase} failed`;
  summary.status = summary.results.some((result) => result.status === "applied") ? "partial" : "failed";
  const error = new Error(`conflict label ${phase} failed for PR ${record.prNumber}`);
  error.summary = summary;
  return error;
}

async function runConflictLabels({ event, eventName, github, dryRun }) {
  if (typeof dryRun !== "boolean") throw new TypeError("dryRun must be a boolean");
  const identity = route(event, eventName);
  const numbers = validateCandidates(identity.numbers ?? await github.listOpenPullRequestNumbers());
  const summary = { mode: "conflict-labels", dryRun, status: "planning", results: [] };
  const plans = [];
  // Finish the full initial read phase before the first mutation.
  for (const prNumber of numbers) {
    const record = { prNumber, label: LABEL, status: "pending" };
    summary.results.push(record);
    let planned;
    try {
      planned = await snapshot(github, identity, prNumber);
    } catch {
      throw failure(summary, record, "initial read");
    }
    if (planned.reason !== undefined) {
      record.status = "deferred";
      record.reason = planned.reason;
    } else {
      record.action = planned.action;
      record.status = planned.action === null ? "unchanged" : "planned";
      plans.push({ record, planned });
    }
  }
  if (!dryRun) {
    for (const { record, planned } of plans) {
      if (planned.action === null) continue;
      let current;
      try {
        current = await snapshot(github, identity, record.prNumber);
      } catch {
        throw failure(summary, record, "fresh read");
      }
      if (current.reason !== undefined || JSON.stringify(current.fence) !== JSON.stringify(planned.fence)) {
        record.status = "deferred";
        record.reason = "live conflict state changed at fresh check";
        continue;
      }
      if (current.action === null) {
        record.status = "unchanged";
        continue;
      }
      try {
        if (current.action === "add") await github.addIssueLabel(record.prNumber, LABEL);
        else await github.removeIssueLabel(record.prNumber, LABEL);
        record.status = "applied";
      } catch {
        throw failure(summary, record, "label mutation");
      }
    }
  }
  summary.status = dryRun ? "planned" : "complete";
  return summary;
}

module.exports = { runConflictLabels, route, validateCandidates, supportedBranch, validBaseBranch };
