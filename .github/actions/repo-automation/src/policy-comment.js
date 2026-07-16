"use strict";

const POLICY_COMMENT_MARKER = "<!-- repo-automation-policy:v1 -->";
const SAFE_HEAD = /^[A-Za-z0-9._:-]{1,160}$/;
const SAFE_LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const CONTROL_CHARACTERS = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function safeText(value, name, maximum = 512) {
  if (
    typeof value !== "string"
    || value === ""
    || value.length > maximum
    || CONTROL_CHARACTERS.test(value)
  ) {
    throw new TypeError(`${name} must be safe bounded text`);
  }
  return value;
}

function escaped(value) {
  return value.replace(/[\\`*_[\]<>]/g, "\\$&");
}

function sortedStrings(values, name, validator = (value) => safeText(value, name)) {
  if (!Array.isArray(values)) {
    throw new TypeError(`${name} must be an array`);
  }
  return [...new Set(values.map((value) => validator(value)))].sort();
}

function validateResult(result) {
  if (!isRecord(result) || !SAFE_HEAD.test(result.headOid)) {
    throw new TypeError("policy result must contain a safe head OID");
  }
  for (const name of ["title", "dco", "ownership"]) {
    if (!isRecord(result[name]) || typeof result[name].valid !== "boolean") {
      throw new TypeError(`policy result must contain ${name} validation`);
    }
  }
  if (typeof result.valid !== "boolean") {
    throw new TypeError("policy result must contain overall validation");
  }
  if (!isRecord(result.labels) || !isRecord(result.reviewers)) {
    throw new TypeError("policy result must contain label and reviewer plans");
  }

  const titleError = result.title.error === null
    ? null
    : safeText(result.title.error, "title error");
  if (!result.title.valid && titleError === null) {
    throw new TypeError("invalid title result must contain an error");
  }
  if (!Array.isArray(result.dco.failures) || !Array.isArray(result.dco.exempted)) {
    throw new TypeError("DCO validation must contain failure and exemption arrays");
  }
  if (!Array.isArray(result.ownership.uncoveredPaths)) {
    throw new TypeError("ownership validation must contain uncovered paths");
  }

  const failureOids = result.dco.failures.map((failure) => {
    if (!isRecord(failure)) {
      throw new TypeError("DCO failures must be objects");
    }
    return safeText(failure.sha, "DCO failure OID", 160);
  }).sort();
  const uncoveredPaths = sortedStrings(
    result.ownership.uncoveredPaths,
    "uncovered path",
    (value) => safeText(value, "uncovered path", 512),
  );
  const labels = {
    add: sortedStrings(result.labels.add, "label"),
    remove: sortedStrings(result.labels.remove, "label"),
  };
  const reviewers = {
    request: sortedStrings(result.reviewers.request, "reviewer", (value) => {
      if (typeof value !== "string" || !SAFE_LOGIN.test(value)) {
        throw new TypeError("reviewer must be a GitHub login");
      }
      return value.toLowerCase();
    }),
    preserved: sortedStrings(result.reviewers.preserved, "reviewer", (value) => {
      if (typeof value !== "string" || !SAFE_LOGIN.test(value)) {
        throw new TypeError("reviewer must be a GitHub login");
      }
      return value.toLowerCase();
    }),
  };
  const configuration = result.configuration === undefined
    ? { valid: true }
    : result.configuration;
  if (!isRecord(configuration) || typeof configuration.valid !== "boolean") {
    throw new TypeError("configuration validation must contain valid");
  }
  return {
    headOid: result.headOid,
    valid: result.valid,
    title: { valid: result.title.valid, error: titleError },
    dco: { valid: result.dco.valid, failureOids },
    ownership: { valid: result.ownership.valid, uncoveredPaths },
    configuration: { valid: configuration.valid },
    labels,
    reviewers,
  };
}

function list(values) {
  if (values.length === 0) return "none";
  const maximumItems = 20;
  const displayed = values.slice(0, maximumItems)
    .map((value) => `\`${escaped(value)}\``)
    .join(", ");
  return values.length <= maximumItems
    ? displayed
    : `${displayed}, and ${values.length - maximumItems} more`;
}

function status(valid) {
  return valid ? "PASS" : "FAIL";
}

function renderPolicyComment(result) {
  const value = validateResult(result);
  const lines = [
    POLICY_COMMENT_MARKER,
    "## PR metadata policy",
    "",
    `Head: \`${escaped(value.headOid)}\``,
    "",
    `- Title: **${status(value.title.valid)}**${value.title.error === null ? "" : ` — ${escaped(value.title.error)}`}`,
    `- DCO: **${status(value.dco.valid)}**${value.dco.failureOids.length === 0 ? "" : ` — failing commits: ${list(value.dco.failureOids)}`}`,
    `- Ownership: **${status(value.ownership.valid)}**${value.ownership.uncoveredPaths.length === 0 ? "" : ` — uncovered paths: ${list(value.ownership.uncoveredPaths)}`}`,
    `- Configuration: **${status(value.configuration.valid)}**`,
  ];
  return `${lines.join("\n")}\n`;
}

module.exports = { POLICY_COMMENT_MARKER, renderPolicyComment };
