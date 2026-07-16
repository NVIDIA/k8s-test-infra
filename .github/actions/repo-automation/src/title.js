"use strict";

const labelsByType = {
  feat: "kind/feature",
  fix: "kind/bug",
  docs: "kind/documentation",
  test: "kind/test",
  refactor: "kind/refactor",
  perf: "kind/performance",
  build: "kind/ci",
  ci: "kind/ci",
  chore: "kind/cleanup",
  revert: "kind/revert",
};

const titlePattern = /^(feat|fix|docs|test|refactor|perf|build|ci|chore|revert)(?:\(([^()\r\n]+)\))?(!)?: (.+)$/;

function invalidTitle() {
  return {
    valid: false,
    type: null,
    scope: null,
    breaking: false,
    description: null,
    label: null,
    error: "invalid pull request title",
  };
}

function classifyTitle(title) {
  if (typeof title !== "string" || /[\r\n]/.test(title)) {
    return invalidTitle();
  }

  const match = titlePattern.exec(title);
  if (match === null) {
    return invalidTitle();
  }

  const [, type, matchedScope, breakingMarker, description] = match;
  const scope = matchedScope ?? null;
  if (description.trim() === "" || (type === "chore" && scope !== null && scope !== "deps")) {
    return invalidTitle();
  }

  return {
    valid: true,
    type,
    scope,
    breaking: breakingMarker === "!",
    description,
    label: type === "chore" && scope === "deps" ? "kind/dependencies" : labelsByType[type],
    error: null,
  };
}

module.exports = { classifyTitle };
