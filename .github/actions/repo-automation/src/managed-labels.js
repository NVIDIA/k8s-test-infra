"use strict";

const MANAGED_STATE_LABELS = new Set(["do-not-merge/work-in-progress"]);

function isManagedMetadataLabel(label) {
  if (typeof label !== "string") return false;
  const normalized = label.toLowerCase();
  return normalized.startsWith("kind/")
    || normalized.startsWith("size/")
    || normalized.startsWith("area/")
    || MANAGED_STATE_LABELS.has(normalized);
}

module.exports = { isManagedMetadataLabel };
