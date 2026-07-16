"use strict";

const CONTROL_CHARACTERS = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;
const OID = /^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$/;
const LOGIN = /^(?!.*--)[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$/;
const UTC_TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;
const SNAPSHOT_KEYS = [
  "approvalCoverageComplete",
  "approvalHeadOid",
  "autoMergeEnabled",
  "baseBranch",
  "baseBranchAllowed",
  "baseBranchProtected",
  "ciState",
  "draft",
  "finalHeadOid",
  "headOid",
  "labels",
  "lgtm",
  "lgtmStateOwnedByBot",
  "loadError",
  "mergeability",
  "metadataHeadOid",
  "pullRequestState",
];
const LGTM_KEYS = ["actor", "commentId", "createdAt", "headOid"];
const PULL_REQUEST_STATES = new Set(["OPEN", "CLOSED"]);
const MERGEABILITY_STATES = new Set(["MERGEABLE", "CONFLICTING", "UNKNOWN"]);
const CI_STATES = new Set(["SUCCESS", "PENDING", "FAILED"]);

function isPlainRecord(value) {
  return value !== null
    && typeof value === "object"
    && !Array.isArray(value)
    && Object.getPrototypeOf(value) === Object.prototype;
}

function requireExactDataRecord(value, keys, field) {
  if (!isPlainRecord(value)) {
    throw new TypeError(`${field} must be a plain object`);
  }
  const ownKeys = Reflect.ownKeys(value);
  if (
    ownKeys.length !== keys.length
    || keys.some((key) => !Object.hasOwn(value, key))
    || ownKeys.some((key) => typeof key !== "string" || !keys.includes(key))
  ) {
    throw new TypeError(`${field} must match the exact schema`);
  }
  const descriptors = Object.getOwnPropertyDescriptors(value);
  if (keys.some((key) => !("value" in descriptors[key]))) {
    throw new TypeError(`${field} fields must be data properties`);
  }
}

function requireBoolean(value, field) {
  if (typeof value !== "boolean") {
    throw new TypeError(`${field} must be a boolean`);
  }
  return value;
}

function normalizeOid(value, field) {
  if (typeof value !== "string" || !OID.test(value)) {
    throw new TypeError(`${field} must be a 40- or 64-character Git OID`);
  }
  return value.toLowerCase();
}

function requireEnum(value, allowed, field) {
  if (typeof value !== "string" || !allowed.has(value)) {
    throw new TypeError(`${field} must be a supported value`);
  }
  return value;
}

function requireSafeText(value, maximumLength, field) {
  if (
    typeof value !== "string"
    || value.length === 0
    || value.length > maximumLength
    || CONTROL_CHARACTERS.test(value)
  ) {
    throw new TypeError(`${field} must be safe text`);
  }
  return value;
}

function normalizeTimestamp(value) {
  if (typeof value !== "string" || !UTC_TIMESTAMP.test(value)) {
    throw new TypeError("LGTM creation time must be a UTC timestamp");
  }
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds) || new Date(milliseconds).toISOString() !== value) {
    throw new TypeError("LGTM creation time must be a UTC timestamp");
  }
  return value;
}

function normalizeLgtm(value) {
  if (value === null) return null;
  requireExactDataRecord(value, LGTM_KEYS, "LGTM provenance");
  if (typeof value.actor !== "string" || !LOGIN.test(value.actor)) {
    throw new TypeError("LGTM actor must be a normalized GitHub login");
  }
  if (!Number.isSafeInteger(value.commentId) || value.commentId <= 0) {
    throw new TypeError("LGTM comment ID must be a positive safe integer");
  }
  return {
    actor: value.actor,
    commentId: value.commentId,
    headOid: normalizeOid(value.headOid, "LGTM head OID"),
    createdAt: normalizeTimestamp(value.createdAt),
  };
}

function normalizeLabels(value) {
  if (!Array.isArray(value) || Object.getPrototypeOf(value) !== Array.prototype) {
    throw new TypeError("labels must be an array");
  }
  const ownKeys = Reflect.ownKeys(value);
  if (
    ownKeys.some((key) => typeof key === "symbol")
    || Object.keys(value).length !== value.length
  ) {
    throw new TypeError("labels must be a dense ordinary array");
  }
  const normalized = new Set();
  for (const label of value) {
    normalized.add(requireSafeText(label, 50, "label").toLowerCase());
  }
  return [...normalized].sort();
}

function normalizeSnapshot(value) {
  requireExactDataRecord(value, SNAPSHOT_KEYS, "merge snapshot");
  return {
    pullRequestState: requireEnum(
      value.pullRequestState,
      PULL_REQUEST_STATES,
      "pull request state",
    ),
    draft: requireBoolean(value.draft, "draft"),
    baseBranch: requireSafeText(value.baseBranch, 255, "base branch"),
    baseBranchAllowed: requireBoolean(value.baseBranchAllowed, "base branch allowed"),
    baseBranchProtected: requireBoolean(value.baseBranchProtected, "base branch protected"),
    headOid: normalizeOid(value.headOid, "head OID"),
    finalHeadOid: normalizeOid(value.finalHeadOid, "final head OID"),
    metadataHeadOid: normalizeOid(value.metadataHeadOid, "metadata head OID"),
    approvalHeadOid: normalizeOid(value.approvalHeadOid, "approval head OID"),
    lgtm: normalizeLgtm(value.lgtm),
    lgtmStateOwnedByBot: requireBoolean(
      value.lgtmStateOwnedByBot,
      "LGTM state ownership",
    ),
    approvalCoverageComplete: requireBoolean(
      value.approvalCoverageComplete,
      "approval coverage",
    ),
    mergeability: requireEnum(value.mergeability, MERGEABILITY_STATES, "mergeability"),
    labels: normalizeLabels(value.labels),
    loadError: requireBoolean(value.loadError, "load error"),
    ciState: requireEnum(value.ciState, CI_STATES, "CI state"),
    autoMergeEnabled: requireBoolean(value.autoMergeEnabled, "auto-merge state"),
  };
}

function malformedDecision(value) {
  let enabled = false;
  try {
    if (isPlainRecord(value)) {
      const descriptor = Object.getOwnPropertyDescriptor(value, "autoMergeEnabled");
      enabled = descriptor !== undefined
        && "value" in descriptor
        && descriptor.value === true;
    }
  } catch {
    enabled = false;
  }
  return {
    action: enabled ? "DISABLE" : "NOOP",
    blockers: ["invalid-snapshot"],
  };
}

function decideMergeAction(input) {
  let state;
  try {
    state = normalizeSnapshot(input);
  } catch {
    return malformedDecision(input);
  }

  const blockers = [];
  if (state.loadError) blockers.push("load-error");
  if (state.pullRequestState !== "OPEN") blockers.push("pr-not-open");
  if (state.draft) blockers.push("pr-draft");
  if (!state.baseBranchAllowed) blockers.push("target-branch-not-allowed");
  if (!state.baseBranchProtected) blockers.push("target-branch-not-protected");
  if (state.lgtm === null) blockers.push("lgtm-missing");
  if (!state.lgtmStateOwnedByBot) blockers.push("lgtm-untrusted");
  if (state.lgtm !== null && state.lgtm.headOid !== state.headOid) {
    blockers.push("lgtm-stale");
  }
  if (!state.approvalCoverageComplete) blockers.push("approval-coverage-incomplete");
  if (state.approvalHeadOid !== state.headOid) blockers.push("approval-stale");
  if (state.metadataHeadOid !== state.headOid) blockers.push("metadata-stale");
  if (state.mergeability === "CONFLICTING") blockers.push("mergeability-conflicting");
  if (state.mergeability === "UNKNOWN") blockers.push("mergeability-unknown");
  if (state.labels.some((label) => label.startsWith("do-not-merge/"))) {
    blockers.push("do-not-merge-label");
  }
  if (state.finalHeadOid !== state.headOid) blockers.push("head-changed");

  if (blockers.length === 0) {
    return {
      action: state.autoMergeEnabled ? "NOOP" : "ENABLE",
      blockers,
    };
  }
  return {
    action: state.autoMergeEnabled ? "DISABLE" : "NOOP",
    blockers,
  };
}

module.exports = { decideMergeAction };
