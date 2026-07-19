"use strict";

const MANAGED_STATE_LABELS = new Set(["do-not-merge/work-in-progress", "needs-rebase"]);
const MANAGED_POLICY_LABELS = new Set([
  "approved",
  "do-not-merge/hold",
  "do-not-merge/needs-approval",
  "lgtm",
]);

function isManagedMetadataLabel(label) {
  if (typeof label !== "string") return false;
  const normalized = label.toLowerCase();
  return normalized.startsWith("kind/")
    || normalized.startsWith("size/")
    || normalized.startsWith("area/")
    || MANAGED_STATE_LABELS.has(normalized);
}

function isManagedPolicyLabel(label) {
  return typeof label === "string" && MANAGED_POLICY_LABELS.has(label.toLowerCase());
}

module.exports = { isManagedMetadataLabel, isManagedPolicyLabel };
