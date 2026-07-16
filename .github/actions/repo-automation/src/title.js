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

const inputError = "title must be a single-line string";
const formatError = "title must match <type>[optional scope][optional !]: <description>";
const typeError = "title type must be one of: feat, fix, docs, test, refactor, perf, build, ci, chore, revert";
const dependencyScopeError = "chore dependency scopes must use exact scope deps";
const titlePattern = /^([a-z]+)(?:\(([^()\r\n]+)\))?(!)?: (.+)$/;

function invalidTitle(error) {
  return {
    valid: false,
    type: null,
    scope: null,
    breaking: false,
    description: null,
    label: null,
    error,
  };
}

function classifyTitle(title) {
  if (typeof title !== "string" || /[\r\n]/.test(title)) {
    return invalidTitle(inputError);
  }

  const match = titlePattern.exec(title);
  if (match === null) {
    return invalidTitle(formatError);
  }

  const [, type, matchedScope, breakingMarker, description] = match;
  const scope = matchedScope ?? null;
  if (description.trim() === "") {
    return invalidTitle(formatError);
  }
  if (!Object.hasOwn(labelsByType, type)) {
    return invalidTitle(typeError);
  }
  if (type === "chore" && scope !== "deps" && scope?.startsWith("deps")) {
    return invalidTitle(dependencyScopeError);
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
