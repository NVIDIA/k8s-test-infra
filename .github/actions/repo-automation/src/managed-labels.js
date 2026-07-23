"use strict";

const { BRANCH_TOKEN } = require("./commands/parser.js");

const MANAGED_STATE_LABELS = new Set(["do-not-merge/work-in-progress", "needs-rebase"]);
const MANAGED_POLICY_LABELS = new Set([
  "approved",
  "do-not-merge/hold",
  "do-not-merge/needs-approval",
  "lgtm",
]);
const CHERRY_PICK_LABEL_PREFIX = "cherry-pick/";

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

function isManagedCherryPickLabel(label) {
  if (typeof label !== "string") return false;
  // GitHub label names are case-insensitive; fold case like the metadata and
  // policy checks before matching the managed prefix and branch token.
  const normalized = label.toLowerCase();
  if (!normalized.startsWith(CHERRY_PICK_LABEL_PREFIX)) return false;
  return BRANCH_TOKEN.test(normalized.slice(CHERRY_PICK_LABEL_PREFIX.length));
}

module.exports = {
  CHERRY_PICK_LABEL_PREFIX,
  isManagedCherryPickLabel,
  isManagedMetadataLabel,
  isManagedPolicyLabel,
};
