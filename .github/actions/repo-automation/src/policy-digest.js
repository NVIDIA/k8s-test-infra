"use strict";

const { createHash } = require("node:crypto");

const REPOSITORY = /^[a-z0-9](?:[a-z0-9.-]{0,99})\/[a-z0-9](?:[a-z0-9._-]{0,99})$/;
const REVISION = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const OWNER_PATH = /^\/(?:[A-Za-z0-9_.-]+\/)*OWNERS$/;

function canonical(value) {
  if (value === null || typeof value === "boolean" || typeof value === "string") {
    return JSON.stringify(value);
  }
  if (typeof value === "number" && Number.isSafeInteger(value)) return String(value);
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (value !== null && typeof value === "object" && Object.getPrototypeOf(value) === Object.prototype) {
    const keys = Object.keys(value).sort();
    if (keys.some((key) => value[key] === undefined)) throw new TypeError("policy contains an unsupported value");
    return `{${keys.map((key) => `${JSON.stringify(key)}:${canonical(value[key])}`).join(",")}}`;
  }
  throw new TypeError("policy contains an unsupported value");
}

function policyDigest(input) {
  if (input === null || typeof input !== "object" || Array.isArray(input)) {
    throw new TypeError("policy digest input must be an object");
  }
  if (typeof input.repository !== "string" || !REPOSITORY.test(input.repository)) {
    throw new TypeError("repository identity is invalid");
  }
  if (typeof input.revision !== "string" || !REVISION.test(input.revision)) {
    throw new TypeError("policy revision is invalid");
  }
  if (!Array.isArray(input.ownerSources) || input.ownerSources.length === 0 || input.ownerSources.length > 32) {
    throw new TypeError("owner sources must be a bounded array");
  }
  const paths = new Set();
  const ownerSources = input.ownerSources.map((entry) => {
    if (
      entry === null
      || typeof entry !== "object"
      || Array.isArray(entry)
      || typeof entry.path !== "string"
      || !OWNER_PATH.test(entry.path)
      || typeof entry.source !== "string"
      || entry.source.length > 1024 * 1024
      || paths.has(entry.path)
    ) throw new TypeError("owner source is invalid");
    paths.add(entry.path);
    return { path: entry.path, source: entry.source };
  }).sort((left, right) => left.path.localeCompare(right.path));
  if (typeof input.aliasesSource !== "string" || input.aliasesSource.length > 1024 * 1024) {
    throw new TypeError("alias source is invalid");
  }
  const document = canonical({
    repository: input.repository,
    revision: input.revision,
    policy: input.policy,
    ownerSources,
    aliasesSource: input.aliasesSource,
  });
  return createHash("sha256").update(document, "utf8").digest("hex");
}

module.exports = { policyDigest };
