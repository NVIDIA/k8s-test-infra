"use strict";

const { parsePolicyState, serializePolicyState } = require("./commands/state.js");

const POLICY_COMMENT_MARKER = "<!-- repo-automation-policy:v1 -->";
const COMMAND_SUMMARY_MARKER = "<!-- repo-automation-command-summary:v1 -->";
const METADATA_HEAD_INTRODUCTION = "<!-- repo-automation-metadata-head:";
const METADATA_HEAD_LINE = /^<!-- repo-automation-metadata-head:v1 \{"headOid":"([0-9a-f]{40}|[0-9a-f]{64})"\} -->$/;
const SAFE_LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const CONTROL_CHARACTERS = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;
const STATE_MARKER_INTRODUCTION = "<!-- repo-automation-state:";
const STATE_MARKER_LINE = /^<!-- repo-automation-state:v1 [^\r\n]+ -->$/;

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

function policyCommentStructure(body) {
  if (
    typeof body !== "string"
    || body === ""
    || body.length > 65_536
    || body.includes("\r")
    || body.split(POLICY_COMMENT_MARKER).length - 1 !== 1
    || body.split(STATE_MARKER_INTRODUCTION).length - 1 !== 1
    || body.split(METADATA_HEAD_INTRODUCTION).length - 1 > 1
    || body.split(COMMAND_SUMMARY_MARKER).length - 1 > 1
  ) return null;
  const lines = body.split("\n");
  if (lines[0] !== POLICY_COMMENT_MARKER || !STATE_MARKER_LINE.test(lines[1] ?? "")) return null;
  const metadataCount = body.split(METADATA_HEAD_INTRODUCTION).length - 1;
  if (metadataCount === 1 && !METADATA_HEAD_LINE.test(lines[2] ?? "")) return null;
  const commandIndex = lines.indexOf(COMMAND_SUMMARY_MARKER);
  if (
    (body.includes(COMMAND_SUMMARY_MARKER) && commandIndex < 2)
    || lines.slice(2).some((line, index) => (
      line === POLICY_COMMENT_MARKER
      || STATE_MARKER_LINE.test(line)
      || (line === COMMAND_SUMMARY_MARKER && index + 2 !== commandIndex)
    ))
  ) return null;
  return { commandIndex, stateMarker: lines[1] };
}

function serializeMetadataHeadEvidence(headOid) {
  if (typeof headOid !== "string" || !/^(?:[0-9a-f]{40}|[0-9a-f]{64})$/.test(headOid)) {
    throw new TypeError("metadata head evidence must contain a normalized Git OID");
  }
  return `<!-- repo-automation-metadata-head:v1 {"headOid":"${headOid}"} -->`;
}

function parseMetadataHeadEvidence(body) {
  if (typeof body !== "string" || body.split(METADATA_HEAD_INTRODUCTION).length - 1 !== 1) {
    return null;
  }
  const line = body.split("\n")[2];
  const match = METADATA_HEAD_LINE.exec(line ?? "");
  return match === null ? null : match[1];
}

function hasValidPolicyCommentStructure(body) {
  return policyCommentStructure(body) !== null;
}

function renderPolicyComment(result, state = undefined, options = {}) {
  const value = validateResult(result);
  if (
    !isRecord(options)
    || (options.reviewStateReset !== undefined && typeof options.reviewStateReset !== "boolean")
  ) {
    throw new TypeError("policy comment options must be structured");
  }
  const policyState = state === undefined
    ? { headOid: value.headOid, lgtm: null, lastRetest: null }
    : state;
  if (policyState?.headOid !== value.headOid) {
    throw new TypeError("policy state must match the rendered head OID");
  }
  const stateMarker = serializePolicyState(policyState);
  const lines = [
    POLICY_COMMENT_MARKER,
    stateMarker,
    serializeMetadataHeadEvidence(value.headOid),
    "## PR metadata policy",
    "",
    `Head: ${code(value.headOid)}`,
    "",
    ...(options.reviewStateReset === true
      ? ["Review state reset: pull request head changed.", ""]
      : []),
    `- Title: **${status(value.title.valid)}**${value.title.error === null ? "" : ` — ${escaped(value.title.error)}`}`,
    `- DCO: **${status(value.dco.valid)}**${value.dco.failureOids.length === 0 ? "" : ` — failing commits: ${list(value.dco.failureOids)}`}`,
    `- Ownership: **${status(value.ownership.valid)}**${value.ownership.uncoveredPaths.length === 0 ? "" : ` — uncovered paths: ${list(value.ownership.uncoveredPaths)}`}`,
    `- Configuration: **${status(value.configuration.valid)}**`,
  ];
  return `${lines.join("\n")}\n`;
}

function renderCommandPolicyComment({ existingBody = null, state, items, policy }) {
  if (!isRecord(policy) || !Array.isArray(items)) {
    throw new TypeError("command policy summary must be structured");
  }
  const stateMarker = serializePolicyState(state);
  const headOid = safeText(state.headOid, "head OID", 160);
  for (const name of ["lgtm", "approved", "hold", "needsApproval"]) {
    if (typeof policy[name] !== "boolean") {
      throw new TypeError(`command policy ${name} must be a boolean`);
    }
  }
  const displayed = items.slice(0, 50).map((item) => {
    if (!isRecord(item) || !Number.isSafeInteger(item.line) || item.line < 0) {
      throw new TypeError("command summary item line must be safe");
    }
    const itemCode = safeText(item.code, "command summary code", 64);
    if (!/^[a-z0-9-]+$/.test(itemCode)) {
      throw new TypeError("command summary code must be fixed text");
    }
    return `- Line ${item.line}: ${code(itemCode)}`;
  });
  if (items.length > displayed.length) {
    displayed.push(`- ${items.length - displayed.length} additional bounded results omitted`);
  }
  let preserved;
  if (existingBody === null) {
    preserved = `${POLICY_COMMENT_MARKER}\n${stateMarker}`;
  } else {
    const structure = policyCommentStructure(existingBody);
    if (structure === null || parsePolicyState(existingBody) === null) {
      throw new TypeError("existing policy comment structure is invalid");
    }
    const lines = existingBody.split("\n");
    const prefixLines = lines.slice(0, structure.commandIndex === -1 ? lines.length : structure.commandIndex);
    if (prefixLines.at(-1) === "") prefixLines.pop();
    prefixLines[1] = stateMarker;
    preserved = prefixLines.join("\n");
  }
  const lines = [
    preserved,
    COMMAND_SUMMARY_MARKER,
    "## Command policy",
    "",
    `Head: ${code(headOid)}`,
    "",
    `- LGTM: **${status(policy.lgtm)}**`,
    `- Approval coverage: **${status(policy.approved)}**`,
    `- Hold: **${policy.hold ? "ACTIVE" : "CLEAR"}**`,
    `- Needs approval: **${policy.needsApproval ? "ACTIVE" : "CLEAR"}**`,
    "",
    "### Command results",
    "",
    ...(displayed.length === 0 ? ["No command-like lines were found."] : displayed),
  ];
  if (items.some((item) => item.code === "help")) {
    lines.push(
      "",
      "### Supported commands",
      "",
      `- ${code("/lgtm")} or ${code("/lgtm cancel")}: applicable non-author reviewer or approver; cancellation also permits the giver, author, or write collaborator.`,
      `- ${code("/assign @user...")}: author, applicable owner, or triage collaborator.`,
      `- ${code("/unassign @user...")}: author or triage collaborator; an ordinary current assignee may target only themselves.`,
      `- ${code("/hold")} or ${code("/hold cancel")}, and ${code("/retest")}: author or write collaborator.`,
      `- ${code("/help")}: any resolved human.`,
      `- Approval authority comes only from an applicable GitHub ${code("APPROVED")} review for the current head; ${code("/approve")} is unsupported.`,
    );
  }
  const body = `${lines.join("\n")}\n`;
  if (body.length > 65_536) {
    throw new TypeError("rendered command policy comment exceeds limit");
  }
  if (
    policyCommentStructure(body) === null
    || body.split(stateMarker).length - 1 !== 1
    || JSON.stringify(parsePolicyState(body)) !== JSON.stringify(state)
  ) {
    throw new TypeError("rendered command policy comment structure is invalid");
  }
  return body;
}

module.exports = {
  COMMAND_SUMMARY_MARKER,
  POLICY_COMMENT_MARKER,
  hasValidPolicyCommentStructure,
  parseMetadataHeadEvidence,
  renderCommandPolicyComment,
  renderPolicyComment,
  serializeMetadataHeadEvidence,
};
