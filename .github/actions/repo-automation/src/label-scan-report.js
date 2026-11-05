"use strict";

const { Buffer } = require("node:buffer");
const { createHash } = require("node:crypto");
const fs = require("node:fs/promises");
const path = require("node:path");
const { asciiLower } = require("./managed-labels.js");

const REPOSITORY_ID = 733665780;
const MODES = new Set(["conflict-labels", "metadata-labels"]);
const OID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const HASH = /^[0-9a-f]{64}$/;
const REASONS = new Set(["none", "unknown_mergeability", "invalid_state", "state_changed", "read_failed",
  "write_failed", "invalid_policy", "limit_exceeded", "not_processed"]);
const contexts = new WeakSet();

function invalid(message, reason = "invalid_state") {
  const error = new TypeError(message);
  error.reportReason = reason;
  return error;
}

function validText(value) {
  return typeof value === "string" && !/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(value);
}

function validOid(value) { return typeof value === "string" && OID.test(value) && !/^0+$/.test(value); }
function sha(value) { return createHash("sha256").update(value, "utf8").digest("hex"); }

function validateScanDispatch({ event, eventName, ref, repositoryId, workflowSha, requestId, workflowCommitSha }) {
  const repository = event?.repository;
  const inputs = event?.inputs;
  if (eventName !== "workflow_dispatch" || ref !== "refs/heads/main"
    || repositoryId !== String(REPOSITORY_ID) || repository?.id !== REPOSITORY_ID
    || repository.node_id !== "R_kgDOK7rZ9A" || repository.name !== "k8s-test-infra"
    || repository.owner?.login !== "NVIDIA" || repository.full_name !== "NVIDIA/k8s-test-infra"
    || !/^[0-9a-f]{40}$/.test(workflowCommitSha) || !validOid(workflowCommitSha)
    || workflowSha !== workflowCommitSha || typeof requestId !== "string" || !UUID.test(requestId)
    || inputs === null || typeof inputs !== "object" || Array.isArray(inputs)
    || Object.keys(inputs).sort().join(",") !== "request_id,workflow_commit_sha"
    || inputs.request_id !== requestId || inputs.workflow_commit_sha !== workflowCommitSha
    || ["pull_request", "number", "action", "schedule", "before", "after", "deleted"].some((key) => event[key] !== undefined)
    || (event.ref !== undefined && event.ref !== "main" && event.ref !== "refs/heads/main")) {
    throw invalid("label scan dispatch is invalid");
  }
  const context = Object.freeze({ requestId, repositoryId: REPOSITORY_ID, workflowCommitSha });
  contexts.add(context);
  return context;
}

function inputSha256(fence) {
  const { validBaseBranch } = require("./modes/conflict-labels.js");
  if (!MODES.has(fence?.mode) || !Number.isInteger(fence.number) || fence.number <= 0 || fence.number > 2147483647
    || typeof fence.nodeId !== "string" || !/^[\x21-\x7e]{1,200}$/.test(fence.nodeId)
    || !validOid(fence.headOid) || !validOid(fence.baseOid) || !validOid(fence.policyRevision)
    || typeof fence.draft !== "boolean" || !validText(fence.title) || !validText(fence.baseBranch)
    || !validBaseBranch(fence.baseBranch)
    || (fence.mode === "conflict-labels" ? !["CONFLICTING", "MERGEABLE"].includes(fence.mergeability) : fence.mergeability !== null)) {
    throw invalid("scan input fence is invalid");
  }
  if (Buffer.byteLength(fence.baseBranch, "utf8") > 4096) throw invalid("scan base branch limit exceeded", "limit_exceeded");
  return sha(JSON.stringify([1, REPOSITORY_ID, fence.mode, fence.number, fence.nodeId, fence.headOid,
    fence.baseBranch, fence.baseOid, fence.draft, fence.policyRevision, sha(fence.title), fence.mergeability]));
}

function labelsSha256(mode, labels) {
  if (!MODES.has(mode) || !Array.isArray(labels)) throw invalid("scan labels are invalid");
  if (labels.length > 1000) throw invalid("scan label collection limit exceeded", "limit_exceeded");
  const names = new Set();
  for (const label of labels) {
    if (!validText(label) || label === "" || /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u.test(label)) throw invalid("scan label is invalid");
    if ([...label].length > 100 || Buffer.byteLength(label, "utf8") > 400) throw invalid("scan label limit exceeded", "limit_exceeded");
    const normalized = asciiLower(label);
    if (names.has(normalized)) throw invalid("scan label duplicate is invalid");
    names.add(normalized);
  }
  const managed = labels.filter((label) => {
    const name = asciiLower(label);
    return mode === "conflict-labels" ? name === "needs-rebase"
      : /^(?:kind\/|size\/|area\/)/.test(name) || name === "do-not-merge/work-in-progress";
  }).sort((left, right) => Buffer.compare(Buffer.from(left, "utf8"), Buffer.from(right, "utf8")));
  return sha(JSON.stringify(managed));
}

function candidateNumbers(numbers) {
  if (!Array.isArray(numbers) || numbers.length > 100 || new Set(numbers).size !== numbers.length
    || numbers.some((number) => !Number.isInteger(number) || number <= 0 || number > 2147483647)) {
    throw invalid("scan candidates are invalid or exceed limit", "limit_exceeded");
  }
  return [...numbers].sort((left, right) => left - right);
}

function buildScanReport(context, mode, dryRun, candidates, results) {
  if (!contexts.has(context) || !MODES.has(mode) || typeof dryRun !== "boolean" || !Array.isArray(results)) {
    throw invalid("scan report context is invalid");
  }
  const numbers = candidateNumbers(candidates);
  const entries = new Map();
  for (const result of results) {
    const success = ["applied", "unchanged"].includes(result?.status);
    if (!numbers.includes(result?.number) || entries.has(result.number)
      || !["applied", "unchanged", "deferred", "failed"].includes(result.status) || !REASONS.has(result.reason)
      || (success ? result.reason !== "none" || !HASH.test(result.inputSha256) || !HASH.test(result.labelsSha256)
        : result.reason === "none" || (result.inputSha256 !== null && !HASH.test(result.inputSha256)) || result.labelsSha256 !== null)) {
      throw invalid("scan report result is invalid");
    }
    entries.set(result.number, { number: result.number, status: result.status, reason: result.reason,
      inputSha256: result.inputSha256, labelsSha256: result.labelsSha256 });
  }
  const report = { schemaVersion: 1, requestId: context.requestId, repositoryId: REPOSITORY_ID,
    workflowCommitSha: context.workflowCommitSha, mode, dryRun, candidates: numbers,
    results: numbers.map((number) => entries.get(number) ?? { number, status: "failed", reason: "not_processed",
      inputSha256: null, labelsSha256: null }) };
  if (Buffer.byteLength(JSON.stringify(report), "utf8") > 70 * 1024) throw invalid("scan report byte limit exceeded", "limit_exceeded");
  return report;
}

function createScanReportCollector(context, mode, dryRun, workspace) {
  if (!contexts.has(context) || !MODES.has(mode) || typeof workspace !== "string" || !path.isAbsolute(workspace)) {
    throw invalid("scan report collector context is invalid");
  }
  let candidates;
  const inputs = new Map();
  const results = new Map();
  return {
    context,
    identity: Object.freeze({ owner: "nvidia", repo: "k8s-test-infra", numbers: null, branch: null }),
    setCandidates(numbers) { candidates = candidateNumbers(numbers); },
    validateLabels(labels) { labelsSha256(mode, labels); },
    plan(number, fence) { inputs.set(number, inputSha256({ ...fence, mode })); },
    record(number, status, reason, labels) {
      results.set(number, { number, status, reason, inputSha256: inputs.get(number) ?? null,
        labelsSha256: ["applied", "unchanged"].includes(status) ? labelsSha256(mode, labels) : null });
    },
    async write() {
      // An unknown catalog must not become a successful empty report.
      if (candidates === undefined) return;
      const report = buildScanReport(context, mode, dryRun, candidates, [...results.values()]);
      const directory = path.join(workspace, ".mokka-label-scan", context.requestId);
      await fs.mkdir(directory, { recursive: true });
      await fs.writeFile(path.join(directory, `${mode}.json`), JSON.stringify(report), { flag: "wx", mode: 0o600 });
    },
  };
}

module.exports = { validateScanDispatch, inputSha256, labelsSha256, buildScanReport, createScanReportCollector };
