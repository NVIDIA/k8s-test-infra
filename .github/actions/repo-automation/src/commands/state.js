"use strict";

const STATE_MARKER_PREFIX = "<!-- repo-automation-state:v1 ";
const STATE_MARKER_INTRODUCTION = "<!-- repo-automation-state:";
const OID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const LOGIN = /^(?!.*--)[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$/;
const UTC_TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;

function isPlainRecord(value) {
  return value !== null
    && typeof value === "object"
    && !Array.isArray(value)
    && Object.getPrototypeOf(value) === Object.prototype;
}

function hasExactKeys(value, expected) {
  if (!isPlainRecord(value)) return false;
  const keys = Reflect.ownKeys(value);
  return keys.length === expected.length
    && expected.every((key) => Object.prototype.hasOwnProperty.call(value, key));
}

function isOid(value) {
  return typeof value === "string" && OID.test(value);
}

function isPositiveId(value) {
  return Number.isSafeInteger(value) && value > 0;
}

function isTimestamp(value) {
  if (typeof value !== "string" || !UTC_TIMESTAMP.test(value)) return false;
  const milliseconds = Date.parse(value);
  return Number.isFinite(milliseconds) && new Date(milliseconds).toISOString() === value;
}

function isLgtm(value) {
  return hasExactKeys(value, ["actor", "commentId", "headOid", "createdAt"])
    && typeof value.actor === "string"
    && LOGIN.test(value.actor)
    && isPositiveId(value.commentId)
    && isOid(value.headOid)
    && isTimestamp(value.createdAt);
}

function isLastRetest(value) {
  return hasExactKeys(value, ["commentId", "headOid", "createdAt"])
    && isPositiveId(value.commentId)
    && isOid(value.headOid)
    && isTimestamp(value.createdAt);
}

function isPolicyState(value) {
  return hasExactKeys(value, ["headOid", "lgtm", "lastRetest"])
    && isOid(value.headOid)
    && (value.lgtm === null || isLgtm(value.lgtm))
    && (value.lastRetest === null || isLastRetest(value.lastRetest));
}

function normalizedState(state) {
  if (!isPolicyState(state)) {
    throw new TypeError("policy state must match the exact v1 schema");
  }
  return {
    headOid: state.headOid,
    lgtm: state.lgtm === null ? null : {
      actor: state.lgtm.actor,
      commentId: state.lgtm.commentId,
      headOid: state.lgtm.headOid,
      createdAt: state.lgtm.createdAt,
    },
    lastRetest: state.lastRetest === null ? null : {
      commentId: state.lastRetest.commentId,
      headOid: state.lastRetest.headOid,
      createdAt: state.lastRetest.createdAt,
    },
  };
}

function serializePolicyState(state) {
  return `${STATE_MARKER_PREFIX}${JSON.stringify(normalizedState(state))} -->`;
}

function parsePolicyState(commentBody) {
  if (typeof commentBody !== "string") return null;
  if (commentBody.split(STATE_MARKER_INTRODUCTION).length - 1 !== 1) return null;

  const matches = [...commentBody.matchAll(/<!-- repo-automation-state:v1 ([^\r\n]*) -->/g)];
  if (matches.length !== 1) return null;

  try {
    const state = JSON.parse(matches[0][1]);
    return serializePolicyState(state) === matches[0][0]
      ? normalizedState(state)
      : null;
  } catch {
    return null;
  }
}

function currentLgtm(state, headOid) {
  if (!isOid(headOid) || !isPolicyState(state)) return null;
  if (state.headOid !== headOid || state.lgtm === null || state.lgtm.headOid !== headOid) {
    return null;
  }
  return normalizedState(state).lgtm;
}

module.exports = {
  currentLgtm,
  parsePolicyState,
  serializePolicyState,
};
