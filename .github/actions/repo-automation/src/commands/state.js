"use strict";

const STATE_MARKER_PREFIX = "<!-- repo-automation-state:v2 ";
const STATE_MARKER_INTRODUCTION = "<!-- repo-automation-state:";
const STATE_KEYS = [
  "repository",
  "pullRequest",
  "policyDigest",
  "headOid",
  "lgtms",
  "approvals",
  "hold",
  "lastRetest",
  "processedCommandIds",
];
const EVIDENCE_KEYS = [
  "repository",
  "pullRequest",
  "actor",
  "actorRole",
  "sourceType",
  "sourceId",
  "policyDigest",
  "headOid",
  "createdAt",
];
const HOLD_KEYS = [
  "repository",
  "pullRequest",
  "actor",
  "actorRole",
  "sourceType",
  "sourceId",
  "createdAt",
];
const RETEST_KEYS = ["commentId", "headOid", "createdAt"];
const OID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const DIGEST = /^[0-9a-f]{64}$/;
const LOGIN = /^(?!.*--)[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$/;
const REPOSITORY = /^[a-z0-9](?:[a-z0-9.-]{0,99})\/[a-z0-9](?:[a-z0-9._-]{0,99})$/;
const UTC_TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;
const SOURCE_TYPES = new Set(["comment", "review"]);
const LGTM_ROLES = new Set(["reviewer", "approver"]);
const HOLD_ROLES = new Set(["owner", "collaborator"]);
const MAX_EVIDENCE_RECORDS = 256;
const MAX_PROCESSED_COMMANDS = 256;

function isPlainRecord(value) {
  return value !== null
    && typeof value === "object"
    && !Array.isArray(value)
    && Object.getPrototypeOf(value) === Object.prototype;
}

function requireExactKeys(value, keys, field) {
  if (!isPlainRecord(value)) throw new TypeError(`${field} must be a plain object`);
  const actual = Reflect.ownKeys(value);
  if (
    actual.length !== keys.length
    || actual.some((key) => typeof key !== "string" || !keys.includes(key))
  ) throw new TypeError(`${field} must match the exact v2 schema`);
}

function positiveId(value, field) {
  if (!Number.isSafeInteger(value) || value <= 0) throw new TypeError(`${field} must be a positive safe integer`);
  return value;
}

function normalizeRepository(value) {
  if (typeof value !== "string" || !REPOSITORY.test(value)) {
    throw new TypeError("repository must be a normalized repository identity");
  }
  return value;
}

function normalizeOid(value, field) {
  if (typeof value !== "string" || !OID.test(value)) throw new TypeError(`${field} must be a lowercase Git OID`);
  return value;
}

function normalizeDigest(value) {
  if (typeof value !== "string" || !DIGEST.test(value)) throw new TypeError("policy digest must be lowercase SHA-256");
  return value;
}

function normalizeLogin(value, field) {
  if (typeof value !== "string" || !LOGIN.test(value)) throw new TypeError(`${field} must be a normalized GitHub login`);
  return value;
}

function normalizeTimestamp(value, field) {
  if (typeof value !== "string" || !UTC_TIMESTAMP.test(value)) throw new TypeError(`${field} must be a canonical UTC timestamp`);
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds) || new Date(milliseconds).toISOString() !== value) {
    throw new TypeError(`${field} must be a canonical UTC timestamp`);
  }
  return value;
}

function normalizeEvidence(value, kind) {
  requireExactKeys(value, EVIDENCE_KEYS, `${kind} evidence`);
  const actorRole = value.actorRole;
  if (kind === "approval" && actorRole !== "approver") {
    throw new TypeError("approval actor role must be approver");
  }
  if (kind === "lgtm" && !LGTM_ROLES.has(actorRole)) {
    throw new TypeError("LGTM actor role must be reviewer or approver");
  }
  if (!SOURCE_TYPES.has(value.sourceType)) throw new TypeError("evidence source type must be supported");
  return {
    repository: normalizeRepository(value.repository),
    pullRequest: positiveId(value.pullRequest, "evidence pull request"),
    actor: normalizeLogin(value.actor, "evidence actor"),
    actorRole,
    sourceType: value.sourceType,
    sourceId: positiveId(value.sourceId, "evidence source ID"),
    policyDigest: normalizeDigest(value.policyDigest),
    headOid: normalizeOid(value.headOid, "evidence head OID"),
    createdAt: normalizeTimestamp(value.createdAt, "evidence creation time"),
  };
}

function normalizeEvidenceList(value, kind) {
  if (!Array.isArray(value) || value.length > MAX_EVIDENCE_RECORDS) {
    throw new TypeError(`${kind} evidence must be a bounded array`);
  }
  const sources = new Set();
  const actors = new Set();
  const normalized = value.map((record) => {
    const evidence = normalizeEvidence(record, kind);
    const source = `${evidence.sourceType}:${evidence.sourceId}`;
    if (sources.has(source) || actors.has(evidence.actor)) {
      throw new TypeError(`${kind} evidence must not contain duplicates`);
    }
    sources.add(source);
    actors.add(evidence.actor);
    return evidence;
  });
  return normalized.sort((left, right) => (
    left.sourceId - right.sourceId || left.actor.localeCompare(right.actor)
  ));
}

function normalizeHold(value) {
  if (value === null) return null;
  requireExactKeys(value, HOLD_KEYS, "hold");
  if (!HOLD_ROLES.has(value.actorRole)) throw new TypeError("hold actor role must be owner or collaborator");
  if (value.sourceType !== "comment") throw new TypeError("hold source type must be comment");
  return {
    repository: normalizeRepository(value.repository),
    pullRequest: positiveId(value.pullRequest, "hold pull request"),
    actor: normalizeLogin(value.actor, "hold actor"),
    actorRole: value.actorRole,
    sourceType: value.sourceType,
    sourceId: positiveId(value.sourceId, "hold source ID"),
    createdAt: normalizeTimestamp(value.createdAt, "hold creation time"),
  };
}

function normalizeRetest(value) {
  if (value === null) return null;
  requireExactKeys(value, RETEST_KEYS, "last retest");
  return {
    commentId: positiveId(value.commentId, "retest comment ID"),
    headOid: normalizeOid(value.headOid, "retest head OID"),
    createdAt: normalizeTimestamp(value.createdAt, "retest creation time"),
  };
}

function normalizeProcessedCommandIds(value) {
  if (!Array.isArray(value) || value.length > MAX_PROCESSED_COMMANDS) {
    throw new TypeError("processed command IDs must be a bounded array");
  }
  const seen = new Set();
  const normalized = value.map((id) => {
    const checked = positiveId(id, "processed command ID");
    if (seen.has(checked)) throw new TypeError("processed command IDs must be unique");
    seen.add(checked);
    return checked;
  });
  return normalized.sort((left, right) => left - right);
}

function normalizedState(value) {
  requireExactKeys(value, STATE_KEYS, "policy state");
  return {
    repository: normalizeRepository(value.repository),
    pullRequest: positiveId(value.pullRequest, "state pull request"),
    policyDigest: normalizeDigest(value.policyDigest),
    headOid: normalizeOid(value.headOid, "state head OID"),
    lgtms: normalizeEvidenceList(value.lgtms, "lgtm"),
    approvals: normalizeEvidenceList(value.approvals, "approval"),
    hold: normalizeHold(value.hold),
    lastRetest: normalizeRetest(value.lastRetest),
    processedCommandIds: normalizeProcessedCommandIds(value.processedCommandIds),
  };
}

function normalizeContext(value) {
  if (!isPlainRecord(value)) throw new TypeError("state context must be a plain object");
  return {
    repository: normalizeRepository(value.repository),
    pullRequest: positiveId(value.pullRequest, "context pull request"),
    policyDigest: normalizeDigest(value.policyDigest),
    headOid: normalizeOid(value.headOid, "context head OID"),
  };
}

function createEmptyState(context) {
  const normalized = normalizeContext(context);
  return {
    ...normalized,
    lgtms: [],
    approvals: [],
    hold: null,
    lastRetest: null,
    processedCommandIds: [],
  };
}

function serializePolicyState(state) {
  return `${STATE_MARKER_PREFIX}${JSON.stringify(normalizedState(state))} -->`;
}

function parsePolicyState(commentBody) {
  if (typeof commentBody !== "string") return null;
  if (commentBody.split(STATE_MARKER_INTRODUCTION).length - 1 !== 1) return null;
  const matches = [...commentBody.matchAll(/<!-- repo-automation-state:v2 ([^\r\n]*) -->/g)];
  if (matches.length !== 1) return null;
  try {
    const state = normalizedState(JSON.parse(matches[0][1]));
    return serializePolicyState(state) === matches[0][0] ? state : null;
  } catch {
    return null;
  }
}

function currentEvidence(stateValue, kind, contextValue) {
  if (kind !== "lgtms" && kind !== "approvals") throw new TypeError("evidence kind must be lgtms or approvals");
  const state = normalizedState(stateValue);
  const context = normalizeContext(contextValue);
  if (
    state.repository !== context.repository
    || state.pullRequest !== context.pullRequest
    || state.policyDigest !== context.policyDigest
    || state.headOid !== context.headOid
  ) return [];
  return state[kind].filter((evidence) => (
    evidence.repository === context.repository
    && evidence.pullRequest === context.pullRequest
    && evidence.policyDigest === context.policyDigest
    && evidence.headOid === context.headOid
  ));
}

function currentHold(stateValue, contextValue) {
  const state = normalizedState(stateValue);
  const context = normalizeContext(contextValue);
  if (
    state.hold === null
    || state.hold.repository !== context.repository
    || state.hold.pullRequest !== context.pullRequest
  ) return null;
  return state.hold;
}

function appendProcessedCommand(stateValue, commandId, historyLimit) {
  const state = normalizedState(stateValue);
  const id = positiveId(commandId, "processed command ID");
  if (!Number.isSafeInteger(historyLimit) || historyLimit < 1 || historyLimit > MAX_PROCESSED_COMMANDS) {
    throw new TypeError("command history limit must be bounded");
  }
  if (state.processedCommandIds.includes(id)) return stateValue;
  if (state.processedCommandIds.length >= historyLimit) {
    throw new Error("processed command history limit reached");
  }
  return normalizedState({
    ...state,
    processedCommandIds: [...state.processedCommandIds, id],
  });
}

module.exports = {
  appendProcessedCommand,
  createEmptyState,
  currentEvidence,
  currentHold,
  parsePolicyState,
  serializePolicyState,
};
