"use strict";

const { minimatch } = require("minimatch");
const {
  MAX_AREA_RULES,
  MAX_CHANGED_FILES,
  MAX_LABELS_PER_AREA,
  MAX_PATTERNS_PER_AREA,
} = require("./limits.js");

const minimatchOptions = Object.freeze({
  dot: true,
  nocomment: true,
  nonegate: true,
});

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function hasExactKeys(value, expected) {
  const keys = Object.keys(value).sort();
  return keys.length === expected.length && expected.every((key) => keys.includes(key));
}

function validateRepositoryPath(value, name) {
  if (
    typeof value !== "string"
    || value === ""
    || value.includes("\0")
    || value.includes("\\")
    || value.startsWith("/")
  ) {
    throw new TypeError(`${name} must be a repository-relative POSIX path`);
  }

  const segments = value.split("/");
  if (segments.some((segment) => segment === "" || segment === "." || segment === "..")) {
    throw new TypeError(`${name} must not contain empty or traversal segments`);
  }
}

function validateAreaLabel(label) {
  if (
    typeof label !== "string"
    || label !== label.trim()
    || !label.startsWith("area/")
    || label.length === "area/".length
    || /[\0-\x1f\x7f]/.test(label)
  ) {
    throw new TypeError("area labels must be non-empty area/* strings");
  }
}

function validateAreas(areas) {
  if (!isRecord(areas) || !hasExactKeys(areas, ["schemaVersion", "areas"])) {
    throw new TypeError("areas must contain exactly schemaVersion and areas");
  }
  if (areas.schemaVersion !== 1 || !Array.isArray(areas.areas) || areas.areas.length === 0) {
    throw new TypeError("areas must use schemaVersion 1 and contain rules");
  }
  if (areas.areas.length > MAX_AREA_RULES) {
    throw new TypeError(`area rules must not exceed ${MAX_AREA_RULES}`);
  }

  for (const rule of areas.areas) {
    if (!isRecord(rule) || !hasExactKeys(rule, ["paths", "labels"])) {
      throw new TypeError("area rules must contain exactly paths and labels");
    }
    if (!Array.isArray(rule.paths) || rule.paths.length === 0) {
      throw new TypeError("area rule paths must be a non-empty array");
    }
    if (rule.paths.length > MAX_PATTERNS_PER_AREA) {
      throw new TypeError(`area rule paths must not exceed ${MAX_PATTERNS_PER_AREA}`);
    }
    if (!Array.isArray(rule.labels) || rule.labels.length === 0) {
      throw new TypeError("area rule labels must be a non-empty array");
    }
    if (rule.labels.length > MAX_LABELS_PER_AREA) {
      throw new TypeError(`area rule labels must not exceed ${MAX_LABELS_PER_AREA}`);
    }
    for (const pattern of rule.paths) {
      validateRepositoryPath(pattern, "area glob");
    }
    for (const label of rule.labels) {
      validateAreaLabel(label);
    }
  }
}

function deriveAreaLabels(paths, areas) {
  if (!Array.isArray(paths)) {
    throw new TypeError("paths must be an array");
  }
  if (paths.length > MAX_CHANGED_FILES) {
    throw new TypeError(`paths must not exceed ${MAX_CHANGED_FILES}`);
  }
  for (const path of paths) {
    validateRepositoryPath(path, "changed path");
  }
  validateAreas(areas);

  const labels = new Set();
  for (const rule of areas.areas) {
    if (paths.some((path) => rule.paths.some((pattern) => minimatch(path, pattern, minimatchOptions)))) {
      for (const label of rule.labels) {
        labels.add(label);
      }
    }
  }
  return [...labels].sort();
}

module.exports = { deriveAreaLabels };
