"use strict";

const POLICY_COMMENT_MARKER = "<!-- repo-automation-policy:v1 -->";
const STATE_MARKER_INTRODUCTION = "<!-- repo-automation-state:";
const STATE_MARKER_PATTERN = /<!-- repo-automation-state:v2 [^\r\n]* -->/g;
const METADATA_HEAD_MARKER_INTRODUCTION = "<!-- repo-automation-metadata-head:";
const METADATA_HEAD_MARKER_PATTERN = /<!-- repo-automation-metadata-head:v1 ([^\r\n]*) -->/g;
const COMMAND_SECTION_START = "<!-- repo-automation-command-summary:v1:start -->";
const COMMAND_SECTION_END = "<!-- repo-automation-command-summary:v1:end -->";
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
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function code(value) {
  return `<code>${escaped(value)}</code>`;
}

function markerJson(value) {
  return JSON.stringify(value).replace(/[<>&]/g, (character) => ({
    "<": "\\u003c",
    ">": "\\u003e",
    "&": "\\u0026",
  })[character]);
}

function sortedStrings(values, name, validator = (value) => safeText(value, name)) {
  if (!Array.isArray(values)) {
    throw new TypeError(`${name} must be an array`);
  }
  return [...new Set(values.map((value) => validator(value)))].sort();
}

function validateResult(result) {
  if (!isRecord(result)) {
    throw new TypeError("policy result must contain a safe head OID");
  }
  safeText(result.headOid, "head OID", 160);
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
    .map(code)
    .join(", ");
  return values.length <= maximumItems
    ? displayed
    : `${displayed}, and ${values.length - maximumItems} more`;
}

function status(valid) {
  return valid ? "PASS" : "FAIL";
}

function preservedState(existingBody) {
  if (typeof existingBody !== "string") return null;
  if (existingBody.split(STATE_MARKER_INTRODUCTION).length - 1 !== 1) return null;
  const matches = existingBody.match(STATE_MARKER_PATTERN);
  return matches?.length === 1 ? matches[0] : null;
}

function renderPolicyComment(result, existingBody = null) {
  const value = validateResult(result);
  const lines = [
    POLICY_COMMENT_MARKER,
    ...(preservedState(existingBody) === null ? [] : [preservedState(existingBody)]),
    `<!-- repo-automation-metadata-head:v1 ${markerJson({ headOid: value.headOid })} -->`,
    "## PR metadata policy",
    "",
    `Head: ${code(value.headOid)}`,
    "",
    `- Title: **${status(value.title.valid)}**${value.title.error === null ? "" : ` — ${escaped(value.title.error)}`}`,
    `- DCO: **${status(value.dco.valid)}**${value.dco.failureOids.length === 0 ? "" : ` — failing commits: ${list(value.dco.failureOids)}`}`,
    `- Ownership: **${status(value.ownership.valid)}**${value.ownership.uncoveredPaths.length === 0 ? "" : ` — uncovered paths: ${list(value.ownership.uncoveredPaths)}`}`,
    `- Configuration: **${status(value.configuration.valid)}**`,
  ];
  return `${lines.join("\n")}\n`;
}

function parseMetadataHeadEvidence(commentBody) {
  if (typeof commentBody !== "string") return null;
  if (commentBody.split(METADATA_HEAD_MARKER_INTRODUCTION).length - 1 !== 1) return null;
  const matches = [...commentBody.matchAll(METADATA_HEAD_MARKER_PATTERN)];
  if (matches.length !== 1) return null;
  try {
    const value = JSON.parse(matches[0][1]);
    if (
      !isRecord(value)
      || Object.keys(value).length !== 1
      || typeof value.headOid !== "string"
    ) return null;
    const headOid = safeText(value.headOid, "metadata head OID", 160);
    const canonical = `<!-- repo-automation-metadata-head:v1 ${markerJson({ headOid })} -->`;
    return canonical === matches[0][0] ? headOid : null;
  } catch {
    return null;
  }
}

function safeCommandItem(value) {
  if (!isRecord(value)) throw new TypeError("command result must be an object");
  if (!Number.isSafeInteger(value.line) || value.line < 0 || value.line > 100_000) {
    throw new TypeError("command result line must be bounded");
  }
  return {
    line: value.line,
    name: safeText(value.name, "command result name", 64),
    status: safeText(value.status, "command result status", 64),
    code: safeText(value.code, "command result code", 128),
  };
}

function metadataSection(existingBody) {
  if (typeof existingBody !== "string") return "";
  if (existingBody.split(POLICY_COMMENT_MARKER).length - 1 !== 1) {
    throw new TypeError("existing policy comment must contain exactly one marker");
  }
  let content = existingBody.replace(POLICY_COMMENT_MARKER, "");
  content = content.replace(STATE_MARKER_PATTERN, "");
  const start = content.indexOf(COMMAND_SECTION_START);
  const end = content.indexOf(COMMAND_SECTION_END);
  if ((start === -1) !== (end === -1) || (start !== -1 && end < start)) {
    throw new TypeError("existing command summary is malformed");
  }
  if (start !== -1) {
    content = `${content.slice(0, start)}${content.slice(end + COMMAND_SECTION_END.length)}`;
  }
  return content.trim();
}

function renderCommandPolicyComment(input) {
  if (!isRecord(input)) throw new TypeError("command policy input must be an object");
  if (
    typeof input.serializedState !== "string"
    || !/^<!-- repo-automation-state:v2 [^\r\n]* -->$/.test(input.serializedState)
  ) throw new TypeError("serialized command state is invalid");
  if (!Array.isArray(input.commands) || !Array.isArray(input.diagnostics)) {
    throw new TypeError("command results must be arrays");
  }
  const items = [...input.commands, ...input.diagnostics].map(safeCommandItem);
  if (items.length > 100) throw new TypeError("command result count exceeds limit");
  items.sort((left, right) => left.line - right.line || left.name.localeCompare(right.name));
  const policy = input.policy;
  if (
    !isRecord(policy)
    || ["lgtm", "approved", "hold", "needsApproval"].some((key) => typeof policy[key] !== "boolean")
  ) throw new TypeError("command policy flags are invalid");

  const metadata = metadataSection(input.existingBody);
  const lines = [
    POLICY_COMMENT_MARKER,
    input.serializedState,
    ...(metadata === "" ? [] : [metadata]),
    COMMAND_SECTION_START,
    "## Repository command policy",
    "",
    `- LGTM: **${status(policy.lgtm)}**`,
    `- Approval: **${status(policy.approved)}**`,
    `- Hold: **${policy.hold ? "ACTIVE" : "CLEAR"}**`,
    `- Needs approval: **${policy.needsApproval ? "YES" : "NO"}**`,
    ...(items.length === 0
      ? ["- Commands: none"]
      : items.map((item) => `- Line ${item.line}: ${code(item.name)} — ${code(item.code)} (${escaped(item.status)})`)),
    COMMAND_SECTION_END,
  ];
  return `${lines.join("\n")}\n`;
}

module.exports = {
  POLICY_COMMENT_MARKER,
  parseMetadataHeadEvidence,
  renderCommandPolicyComment,
  renderPolicyComment,
};
