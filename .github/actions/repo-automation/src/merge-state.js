"use strict";

const CONTROL_CHARACTERS = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;
const OID = /^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$/;
const LOGIN = /^(?!.*--)[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$/;
const UTC_TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;
const MAX_LABELS = 1000;
const SNAPSHOT_KEYS = [
  "approvalCoverageComplete",
  "approvalHeadOid",
  "autoMergeMethod",
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
const AUTO_MERGE_METHODS = new Set(["MERGE", "REBASE", "SQUASH"]);

function isPlainRecord(value) {
  return value !== null
    && typeof value === "object"
    && !Array.isArray(value)
    && Object.getPrototypeOf(value) === Object.prototype;
}

function captureBoundedDescriptorMap(value, maximumOwnKeys, field) {
  const ownKeys = Reflect.ownKeys(value);
  if (ownKeys.length > maximumOwnKeys) {
    throw new TypeError(`${field} has too many fields`);
  }
  const descriptors = Object.create(null);
  for (const key of ownKeys) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (descriptor === undefined) {
      throw new TypeError(`${field} fields changed during capture`);
    }
    descriptors[key] = descriptor;
  }
  return descriptors;
}

function captureExactDescriptorValues(descriptors, keys, field) {
  const ownKeys = Reflect.ownKeys(descriptors);
  if (
    ownKeys.length !== keys.length
    || keys.some((key) => !Object.hasOwn(descriptors, key))
    || ownKeys.some((key) => typeof key !== "string" || !keys.includes(key))
  ) {
    throw new TypeError(`${field} must match the exact schema`);
  }
  const captured = Object.create(null);
  for (const key of keys) {
    const descriptor = descriptors[key];
    if (descriptor === undefined || !Object.hasOwn(descriptor, "value")) {
      throw new TypeError(`${field} fields must be data properties`);
    }
    captured[key] = descriptor.value;
  }
  return captured;
}

function captureExactDataRecord(value, keys, field) {
  if (!isPlainRecord(value)) {
    throw new TypeError(`${field} must be a plain object`);
  }
  const descriptors = captureBoundedDescriptorMap(value, keys.length, field);
  return captureExactDescriptorValues(descriptors, keys, field);
}

function hasEnabledMethodEvidence(descriptor) {
  return descriptor !== undefined
    && Object.hasOwn(descriptor, "value")
    && descriptor.value !== null
    && descriptor.value !== undefined;
}

function captureSnapshotBoundary(value) {
  const failed = {
    complete: false,
    descriptors: null,
    enabledMethodEvidence: false,
    plain: false,
  };
  if (
    value === null
    || (typeof value !== "object" && typeof value !== "function")
  ) {
    return failed;
  }

  let ownKeys;
  try {
    ownKeys = Reflect.ownKeys(value);
  } catch {
    return failed;
  }

  const descriptors = Object.create(null);
  let enabledMethodEvidence = false;
  if (ownKeys.includes("autoMergeMethod")) {
    try {
      const descriptor = Object.getOwnPropertyDescriptor(value, "autoMergeMethod");
      if (descriptor === undefined) return failed;
      descriptors.autoMergeMethod = descriptor;
      enabledMethodEvidence = hasEnabledMethodEvidence(descriptor);
    } catch {
      return failed;
    }
  }

  const incomplete = () => ({
    complete: false,
    descriptors: null,
    enabledMethodEvidence,
    plain: false,
  });
  if (ownKeys.length > SNAPSHOT_KEYS.length) return incomplete();

  try {
    for (const key of ownKeys) {
      if (key === "autoMergeMethod") continue;
      const descriptor = Object.getOwnPropertyDescriptor(value, key);
      if (descriptor === undefined) return incomplete();
      descriptors[key] = descriptor;
    }
    return {
      complete: true,
      descriptors,
      enabledMethodEvidence,
      plain: typeof value === "object"
        && !Array.isArray(value)
        && Object.getPrototypeOf(value) === Object.prototype,
    };
  } catch {
    return incomplete();
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
  const lgtm = captureExactDataRecord(value, LGTM_KEYS, "LGTM provenance");
  if (typeof lgtm.actor !== "string" || !LOGIN.test(lgtm.actor)) {
    throw new TypeError("LGTM actor must be a normalized GitHub login");
  }
  if (!Number.isSafeInteger(lgtm.commentId) || lgtm.commentId <= 0) {
    throw new TypeError("LGTM comment ID must be a positive safe integer");
  }
  return {
    actor: lgtm.actor,
    commentId: lgtm.commentId,
    headOid: normalizeOid(lgtm.headOid, "LGTM head OID"),
    createdAt: normalizeTimestamp(lgtm.createdAt),
  };
}

function normalizeLabels(value) {
  if (!Array.isArray(value) || Object.getPrototypeOf(value) !== Array.prototype) {
    throw new TypeError("labels must be an array");
  }
  const descriptors = captureBoundedDescriptorMap(value, MAX_LABELS + 1, "labels");
  const ownKeys = Reflect.ownKeys(descriptors);
  const lengthDescriptor = descriptors.length;
  if (
    lengthDescriptor === undefined
    || !Object.hasOwn(lengthDescriptor, "value")
    || lengthDescriptor.enumerable
    || !Number.isSafeInteger(lengthDescriptor.value)
    || lengthDescriptor.value < 0
    || lengthDescriptor.value > MAX_LABELS
  ) {
    throw new TypeError("labels must have a bounded ordinary length");
  }
  const length = lengthDescriptor.value;
  if (ownKeys.length !== length + 1 || ownKeys.some((key) => typeof key === "symbol")) {
    throw new TypeError("labels must be a dense ordinary array");
  }
  const normalized = new Set();
  for (let index = 0; index < length; index += 1) {
    const key = String(index);
    const descriptor = descriptors[key];
    if (
      descriptor === undefined
      || !Object.hasOwn(descriptor, "value")
      || !descriptor.enumerable
    ) {
      throw new TypeError("labels must contain enumerable indexed data properties");
    }
    normalized.add(requireSafeText(descriptor.value, 50, "label").toLowerCase());
  }
  return [...normalized].sort();
}

function normalizeSnapshot(capture) {
  if (!capture.complete || !capture.plain) {
    throw new TypeError("merge snapshot must be a plain object");
  }
  const snapshot = captureExactDescriptorValues(
    capture.descriptors,
    SNAPSHOT_KEYS,
    "merge snapshot",
  );
  return {
    pullRequestState: requireEnum(
      snapshot.pullRequestState,
      PULL_REQUEST_STATES,
      "pull request state",
    ),
    draft: requireBoolean(snapshot.draft, "draft"),
    baseBranch: requireSafeText(snapshot.baseBranch, 255, "base branch"),
    baseBranchAllowed: requireBoolean(snapshot.baseBranchAllowed, "base branch allowed"),
    baseBranchProtected: requireBoolean(snapshot.baseBranchProtected, "base branch protected"),
    headOid: normalizeOid(snapshot.headOid, "head OID"),
    finalHeadOid: normalizeOid(snapshot.finalHeadOid, "final head OID"),
    metadataHeadOid: normalizeOid(snapshot.metadataHeadOid, "metadata head OID"),
    approvalHeadOid: normalizeOid(snapshot.approvalHeadOid, "approval head OID"),
    lgtm: normalizeLgtm(snapshot.lgtm),
    lgtmStateOwnedByBot: requireBoolean(
      snapshot.lgtmStateOwnedByBot,
      "LGTM state ownership",
    ),
    approvalCoverageComplete: requireBoolean(
      snapshot.approvalCoverageComplete,
      "approval coverage",
    ),
    mergeability: requireEnum(snapshot.mergeability, MERGEABILITY_STATES, "mergeability"),
    labels: normalizeLabels(snapshot.labels),
    loadError: requireBoolean(snapshot.loadError, "load error"),
    ciState: requireEnum(snapshot.ciState, CI_STATES, "CI state"),
    autoMergeMethod: snapshot.autoMergeMethod === null
      ? null
      : requireEnum(snapshot.autoMergeMethod, AUTO_MERGE_METHODS, "auto-merge method"),
  };
}

function malformedDecision(enabledMethodEvidence) {
  return {
    action: enabledMethodEvidence ? "DISABLE" : "NOOP",
    blockers: ["invalid-snapshot"],
  };
}

function decideMergeAction(input) {
  const capture = captureSnapshotBoundary(input);
  let state;
  try {
    state = normalizeSnapshot(capture);
  } catch {
    return malformedDecision(capture.enabledMethodEvidence);
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
  if (state.autoMergeMethod === "MERGE" || state.autoMergeMethod === "REBASE") {
    blockers.push("auto-merge-method-mismatch");
  }

  if (blockers.length === 0) {
    return {
      action: state.autoMergeMethod === "SQUASH" ? "NOOP" : "ENABLE",
      blockers,
    };
  }
  return {
    action: state.autoMergeMethod === null ? "NOOP" : "DISABLE",
    blockers,
  };
}

module.exports = { decideMergeAction };
