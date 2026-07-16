"use strict";

const OID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const UTC_TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;
const INPUT_KEYS = [
  "runs", "headOid", "now", "lastRetest", "cooldownSeconds", "commentId", "prNumber", "repository",
];
const RUN_KEYS = [
  "id", "headOid", "status", "conclusion", "workflowPath", "event", "prNumber", "repository",
];
const RETEST_KEYS = ["commentId", "headOid", "createdAt"];
const STATUSES = new Set(["requested", "waiting", "pending", "queued", "in_progress", "completed"]);
const CONCLUSIONS = new Set([
  "action_required",
  "cancelled",
  "failure",
  "neutral",
  "skipped",
  "stale",
  "startup_failure",
  "success",
  "timed_out",
]);
const TRUSTED_WORKFLOW_PATHS = new Set([
  ".github/workflows/automation-ci.yml",
  ".github/workflows/basic-checks.yaml",
  ".github/workflows/helm.yaml",
]);
const REPOSITORY = /^[a-z0-9](?:[a-z0-9.-]{0,99})\/[a-z0-9](?:[a-z0-9._-]{0,99})$/;

function noRerun(reason, nextAllowedAt = null) {
  return { rerunRunIds: [], nextAllowedAt, reason };
}

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

function trustedWorkflowPath(value) {
  if (typeof value !== "string" || value === "" || value.length > 512 || /[\0\r\n\\]/.test(value)) {
    return null;
  }
  const separator = value.indexOf("@");
  const path = separator === -1 ? value : value.slice(0, separator);
  if (!TRUSTED_WORKFLOW_PATHS.has(path)) return null;
  if (separator === -1) return path;
  const ref = value.slice(separator + 1);
  if (
    ref === ""
    || ref.includes("@")
    || ref.includes("//")
    || ref.includes("..")
    || ref.includes("@{")
    || /[\x00-\x20~^:?*\[\]]/.test(ref)
  ) return null;
  if (/^refs\/pull\/[1-9]\d*\/(?:head|merge)$/.test(ref)) return path;
  if (!/^refs\/(?:heads|tags)\/[A-Za-z0-9._/-]+$/.test(ref)) return null;
  const segments = ref.split("/");
  if (segments.some((segment) => (
    segment === ""
    || segment.startsWith(".")
    || segment.endsWith(".")
    || segment.endsWith(".lock")
  ))) {
    return null;
  }
  return path;
}

function validRepository(value) {
  return typeof value === "string" && value.length <= 201 && REPOSITORY.test(value);
}

function timestampMilliseconds(value) {
  if (typeof value !== "string" || !UTC_TIMESTAMP.test(value)) return null;
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds) || new Date(milliseconds).toISOString() !== value) return null;
  return milliseconds;
}

function validLastRetest(value) {
  return hasExactKeys(value, RETEST_KEYS)
    && isPositiveId(value.commentId)
    && isOid(value.headOid)
    && timestampMilliseconds(value.createdAt) !== null;
}

function validRun(value) {
  if (
    !hasExactKeys(value, RUN_KEYS)
    || !isPositiveId(value.id)
    || !isOid(value.headOid)
    || !STATUSES.has(value.status)
    || typeof value.workflowPath !== "string"
    || value.workflowPath === ""
    || value.workflowPath.length > 512
    || /[\0\r\n]/.test(value.workflowPath)
    || typeof value.event !== "string"
    || value.event === ""
    || value.event.length > 64
    || /[^a-z_]/.test(value.event)
    || !isPositiveId(value.prNumber)
    || !validRepository(value.repository)
  ) return false;
  if (value.status === "completed") return CONCLUSIONS.has(value.conclusion);
  return value.conclusion === null;
}

function planRetest(input) {
  if (!hasExactKeys(input, INPUT_KEYS) || !Array.isArray(input.runs)) {
    return noRerun("invalid-input");
  }
  const nowMilliseconds = timestampMilliseconds(input.now);
  if (
    !isOid(input.headOid)
    || nowMilliseconds === null
    || input.cooldownSeconds !== 600
    || !isPositiveId(input.commentId)
    || !isPositiveId(input.prNumber)
    || !validRepository(input.repository)
    || (input.lastRetest !== null && !validLastRetest(input.lastRetest))
  ) return noRerun("invalid-input");

  const runById = new Map();
  for (const run of input.runs) {
    if (!validRun(run)) return noRerun("invalid-runs");
    const previous = runById.get(run.id);
    if (
      previous !== undefined
      && (
        previous.headOid !== run.headOid
        || previous.status !== run.status
        || RUN_KEYS.some((key) => previous[key] !== run[key])
      )
    ) return noRerun("invalid-runs");
    runById.set(run.id, run);
  }

  if (input.lastRetest !== null) {
    const retestMilliseconds = timestampMilliseconds(input.lastRetest.createdAt);
    if (retestMilliseconds > nowMilliseconds) return noRerun("invalid-input");
    if (input.lastRetest.commentId === input.commentId) {
      return noRerun("duplicate-delivery");
    }
    if (input.lastRetest.headOid === input.headOid) {
      const nextAllowedMilliseconds = retestMilliseconds + (input.cooldownSeconds * 1000);
      let nextAllowedAt;
      try {
        nextAllowedAt = new Date(nextAllowedMilliseconds).toISOString();
      } catch {
        return noRerun("invalid-input");
      }
      if (!UTC_TIMESTAMP.test(nextAllowedAt)) return noRerun("invalid-input");
      if (nowMilliseconds < nextAllowedMilliseconds) {
        return noRerun("cooldown", nextAllowedAt);
      }
    }
  }

  const rerunRunIds = [...runById.values()]
    .filter((run) => (
      run.headOid === input.headOid
      && run.status === "completed"
      && run.conclusion === "failure"
      && trustedWorkflowPath(run.workflowPath) === run.workflowPath
      && run.event === "pull_request"
      && run.prNumber === input.prNumber
      && run.repository === input.repository
    ))
    .map((run) => run.id)
    .sort((left, right) => left - right);
  return rerunRunIds.length === 0
    ? noRerun("no-failed-runs")
    : { rerunRunIds, nextAllowedAt: null, reason: "rerun-failed" };
}

module.exports = { planRetest, trustedWorkflowPath };
