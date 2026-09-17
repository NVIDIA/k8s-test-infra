"use strict";

const sizeNames = ["S", "M", "L", "XL"];

function requireNonNegativeInteger(value, name) {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new TypeError(`${name} must be a non-negative safe integer`);
  }
}

function validateThresholds(thresholds) {
  if (thresholds === null || typeof thresholds !== "object" || Array.isArray(thresholds)) {
    throw new TypeError("thresholds must be an object");
  }

  const keys = Object.keys(thresholds).sort();
  if (keys.length !== sizeNames.length || !sizeNames.every((name) => keys.includes(name))) {
    throw new TypeError("thresholds must contain exactly S, M, L, and XL");
  }

  for (const name of sizeNames) {
    requireNonNegativeInteger(thresholds[name], `thresholds.${name}`);
  }
  if (thresholds.S !== 0) {
    throw new TypeError("thresholds.S must be zero");
  }
  for (let index = 1; index < sizeNames.length; index += 1) {
    if (thresholds[sizeNames[index]] <= thresholds[sizeNames[index - 1]]) {
      throw new TypeError("thresholds must be strictly increasing");
    }
  }
}

function classifySize(additions, deletions, thresholds) {
  requireNonNegativeInteger(additions, "additions");
  requireNonNegativeInteger(deletions, "deletions");
  validateThresholds(thresholds);

  const changedLines = additions + deletions;
  requireNonNegativeInteger(changedLines, "changed lines");

  let size = sizeNames[0];
  for (const name of sizeNames.slice(1)) {
    if (changedLines < thresholds[name]) {
      break;
    }
    size = name;
  }

  return { changedLines, label: `size/${size}` };
}

module.exports = { classifySize };
