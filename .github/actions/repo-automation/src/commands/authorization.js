"use strict";

const CONTROL_CHARACTERS = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;
const GITHUB_LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const PERMISSION_LEVELS = new Map([
  ["none", 0],
  ["read", 1],
  ["triage", 2],
  ["write", 3],
  ["maintain", 4],
  ["admin", 5],
]);
const NO_ARGUMENT_COMMANDS = new Set(["lgtm", "approve", "hold", "unhold", "retest"]);
const TARGET_COMMANDS = new Set(["backport", "cherry-pick"]);
const COMMAND_KEYS = ["name", "targetBranch", "line", "raw"];

function isPlainRecord(value) {
  return value !== null
    && typeof value === "object"
    && !Array.isArray(value)
    && Object.getPrototypeOf(value) === Object.prototype;
}

function normalizeLogin(value) {
  if (typeof value !== "string" || CONTROL_CHARACTERS.test(value) || !GITHUB_LOGIN.test(value)) {
    return null;
  }
  return value.toLowerCase();
}

function normalizeLoginSet(value) {
  if (!Array.isArray(value)) return null;
  const normalized = new Set();
  for (const member of value) {
    const login = normalizeLogin(member);
    if (login === null) return null;
    normalized.add(login);
  }
  return normalized;
}

function normalizeActor(value) {
  if (
    !isPlainRecord(value)
    || value.resolved !== true
    || (Object.hasOwn(value, "error") && value.error !== false)
    || (Object.hasOwn(value, "deleted") && value.deleted !== false)
  ) return { status: "unavailable" };

  const login = normalizeLogin(value.login);
  const type = typeof value.type === "string" ? value.type.toLowerCase() : null;
  const permission = typeof value.permission === "string"
    ? value.permission.toLowerCase()
    : null;
  if (login === null || type !== "user") return { status: "not-human" };
  if (!PERMISSION_LEVELS.has(permission)) return { status: "unavailable" };
  return {
    status: "human",
    login,
    liveCollaborator: value.liveCollaborator === true,
    permission,
  };
}

function normalizeCommand(value) {
  if (!isPlainRecord(value)) return null;
  const keys = Object.keys(value);
  if (keys.length !== COMMAND_KEYS.length || keys.some((key) => !COMMAND_KEYS.includes(key))) {
    return null;
  }
  if (
    !Number.isSafeInteger(value.line)
    || value.line <= 0
    || typeof value.raw !== "string"
    || value.raw.length === 0
  ) return null;
  if (NO_ARGUMENT_COMMANDS.has(value.name) && value.targetBranch === null) return value;
  if (
    TARGET_COMMANDS.has(value.name)
    && typeof value.targetBranch === "string"
    && value.targetBranch !== ""
  ) return value;
  return null;
}

function denied(reason) {
  return { allowed: false, reason };
}

function allowed(actor, actorRole) {
  return { allowed: true, reason: "authorized", actor, actorRole };
}

function writeCollaborator(actor) {
  return actor.liveCollaborator
    && PERMISSION_LEVELS.get(actor.permission) >= PERMISSION_LEVELS.get("write");
}

function authorizeCommand(commandValue, context) {
  const command = normalizeCommand(commandValue);
  if (command === null) return denied("invalid-command");
  if (!isPlainRecord(context)) return denied("invalid-context");

  const actor = normalizeActor(context.actor);
  if (actor.status === "unavailable") return denied("actor-unavailable");
  if (actor.status !== "human") return denied("actor-not-human");

  const author = normalizeLogin(context.author);
  const reviewers = normalizeLoginSet(context.reviewers);
  const approvers = normalizeLoginSet(context.approvers);
  const owners = normalizeLoginSet(context.owners);
  if (author === null || reviewers === null || approvers === null || owners === null) {
    return denied("invalid-context");
  }

  const isAuthor = actor.login === author;
  const isReviewer = reviewers.has(actor.login);
  const isApprover = approvers.has(actor.login);
  const isOwner = owners.has(actor.login);

  if (command.name === "lgtm" || command.name === "approve") {
    if (isAuthor) return denied("author-cannot-provide-evidence");
    if (command.name === "approve") {
      return isApprover ? allowed(actor.login, "approver") : denied("not-authorized");
    }
    if (isApprover) return allowed(actor.login, "approver");
    return isReviewer ? allowed(actor.login, "reviewer") : denied("not-authorized");
  }

  if (command.name === "hold" || command.name === "unhold") {
    if (isOwner) return allowed(actor.login, "owner");
    return writeCollaborator(actor)
      ? allowed(actor.login, "collaborator")
      : denied("not-authorized");
  }

  if (isAuthor) return allowed(actor.login, "author");
  if (isOwner) return allowed(actor.login, "owner");
  return writeCollaborator(actor)
    ? allowed(actor.login, "collaborator")
    : denied("not-authorized");
}

module.exports = { authorizeCommand };
