"use strict";

const MANAGED_STATE_LABELS = new Set(["do-not-merge/work-in-progress"]);
const MANAGED_POLICY_LABELS = new Set([
  "lgtm",
  "approved",
  "do-not-merge/hold",
  "do-not-merge/needs-approval",
]);

function asciiLower(value) {
  return value.replace(/[A-Z]/g, (letter) => letter.toLowerCase());
}

function isManagedMetadataLabel(label) {
  if (typeof label !== "string") return false;
  const normalized = asciiLower(label);
  return normalized.startsWith("kind/")
    || normalized.startsWith("size/")
    || normalized.startsWith("area/")
    || MANAGED_STATE_LABELS.has(normalized);
}

function isManagedPolicyLabel(label) {
  return typeof label === "string" && MANAGED_POLICY_LABELS.has(label.toLowerCase());
}

function isManagedConflictLabel(label) {
  return label === "needs-rebase";
}

module.exports = { asciiLower, isManagedMetadataLabel, isManagedPolicyLabel, isManagedConflictLabel };
