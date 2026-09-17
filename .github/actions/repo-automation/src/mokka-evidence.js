"use strict";

const { Buffer } = require("node:buffer");
const { createHash, timingSafeEqual } = require("node:crypto");

const MOKKA_EVIDENCE_MARKER = "mokka-cherry-pick-evidence/v1";
const FIELD_SPECS = Object.freeze([
  ["actionId", "action_id"],
  ["sourcePullRequest", "source_pull_request"],
  ["sourceSha", "source_sha"],
  ["targetBranch", "target_branch"],
  ["targetBaseSha", "target_base_sha"],
  ["producedHeadSha", "produced_head_sha"],
  ["workflowCommitSha", "workflow_commit_sha"],
  ["headBranch", "head_branch"],
  ["pullRequestUrl", "pull_request_url"],
]);

function evidencePayload(input) {
  if (input === null || typeof input !== "object" || Array.isArray(input)) {
    throw new TypeError("Mokka evidence must be an object");
  }
  return FIELD_SPECS.map(([property, wireName]) => {
    const value = input[property];
    if ((typeof value !== "string" && typeof value !== "number") || String(value).includes("\n")) {
      throw new TypeError(`Mokka evidence field is invalid: ${wireName}`);
    }
    return `${wireName}: ${value}`;
  }).join("\n");
}

function evidenceDigest(payload) {
  return createHash("sha256").update(payload, "utf8").digest("hex");
}

function createMokkaEvidence(input) {
  const payload = evidencePayload(input);
  return `<!-- ${MOKKA_EVIDENCE_MARKER}\n${payload}\nsha256: ${evidenceDigest(payload)}\n-->\n`;
}

function parseMokkaEvidence(body) {
  if (typeof body !== "string") return null;
  const lines = body.split("\n");
  if (
    lines.length !== FIELD_SPECS.length + 4
    || lines[0] !== `<!-- ${MOKKA_EVIDENCE_MARKER}`
    || lines.at(-2) !== "-->"
    || lines.at(-1) !== ""
  ) return null;

  const result = {};
  const payloadLines = [];
  for (let index = 0; index < FIELD_SPECS.length; index += 1) {
    const [property, wireName] = FIELD_SPECS[index];
    const prefix = `${wireName}: `;
    const line = lines[index + 1];
    if (!line.startsWith(prefix) || line.length === prefix.length) return null;
    const value = line.slice(prefix.length);
    result[property] = property === "sourcePullRequest" ? Number(value) : value;
    payloadLines.push(line);
  }

  const digestLine = lines[FIELD_SPECS.length + 1];
  if (!/^sha256: [0-9a-f]{64}$/.test(digestLine)) return null;
  const actual = Buffer.from(digestLine.slice("sha256: ".length), "hex");
  const expected = Buffer.from(evidenceDigest(payloadLines.join("\n")), "hex");
  if (actual.length !== expected.length || !timingSafeEqual(actual, expected)) return null;
  return result;
}

module.exports = {
  MOKKA_EVIDENCE_MARKER,
  createMokkaEvidence,
  parseMokkaEvidence,
};
