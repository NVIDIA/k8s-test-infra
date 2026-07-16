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

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function normalizeLogin(value) {
  if (
    typeof value !== "string"
    || CONTROL_CHARACTERS.test(value)
    || !GITHUB_LOGIN.test(value)
  ) {
    return null;
  }
  return value.toLowerCase();
}

function normalizePermission(value) {
  if (typeof value !== "string") {
    return null;
  }
  const permission = value.toLowerCase();
  return PERMISSION_LEVELS.has(permission) ? permission : null;
}

function permissionAtLeast(identity, required) {
  if (!identity.liveCollaborator) {
    return false;
  }
  const permission = normalizePermission(identity.permission);
  return permission !== null
    && PERMISSION_LEVELS.get(permission) >= PERMISSION_LEVELS.get(required);
}

function normalizeIdentity(identity) {
  if (
    !isRecord(identity)
    || identity.resolved !== true
    || (Object.hasOwn(identity, "error") && identity.error !== false)
  ) {
    return { status: "unavailable" };
  }

  if (Object.hasOwn(identity, "deleted") && identity.deleted !== false) {
    return identity.deleted === true
      ? { status: "not-human" }
      : { status: "unavailable" };
  }

  const login = normalizeLogin(identity.login);
  const type = typeof identity.type === "string" ? identity.type.toLowerCase() : null;
  if (login === null || type !== "user") {
    return { status: "not-human" };
  }

  return {
    status: "human",
    login,
    liveCollaborator: identity.liveCollaborator === true,
    permission: identity.permission,
  };
}

function normalizeLoginList(value) {
  if (!Array.isArray(value)) {
    return null;
  }
  const result = new Set();
  for (const member of value) {
    const login = normalizeLogin(member);
    if (login === null) {
      return null;
    }
    result.add(login);
  }
  return result;
}

function normalizeCommand(value) {
  if (!isRecord(value) || typeof value.name !== "string") {
    return null;
  }
  const name = value.name.toLowerCase();
  const operations = {
    assign: "apply",
    help: "show",
    retest: "run",
    unassign: "apply",
  };
  if (name === "lgtm" || name === "hold") {
    if (value.operation !== "apply" && value.operation !== "cancel") {
      return null;
    }
  } else if (operations[name] !== value.operation) {
    return null;
  }

  const users = normalizeLoginList(value.users);
  if (users === null) {
    return null;
  }
  const assignment = name === "assign" || name === "unassign";
  if ((assignment && users.size === 0) || (!assignment && users.size !== 0)) {
    return null;
  }
  return { name, operation: value.operation, users };
}

function denied(reason) {
  return { allowed: false, reason };
}

function allowed(value) {
  return value ? { allowed: true, reason: "authorized" } : denied("not-authorized");
}

function actorContext(context) {
  if (!isRecord(context)) {
    return { error: "invalid-context" };
  }
  const actor = normalizeIdentity(context.actor);
  if (actor.status === "unavailable") {
    return { error: "actor-unavailable" };
  }
  if (actor.status !== "human") {
    return { error: "actor-not-human" };
  }
  return { actor };
}

function requiredLogin(value) {
  return normalizeLogin(value);
}

function requiredList(value) {
  return normalizeLoginList(value);
}

function authorizeCommand(commandValue, context) {
  const command = normalizeCommand(commandValue);
  if (command === null) {
    return denied("invalid-command");
  }

  const actorResult = actorContext(context);
  if (actorResult.error !== undefined) {
    return denied(actorResult.error);
  }
  const { actor } = actorResult;
  if (command.name === "help") {
    return allowed(true);
  }

  const author = requiredLogin(context.author);
  if (author === null) {
    return denied("invalid-context");
  }
  const isAuthor = actor.login === author;

  if (command.name === "lgtm" && command.operation === "apply") {
    if (isAuthor) {
      return denied("author-cannot-lgtm");
    }
    const reviewers = requiredList(context.reviewers);
    const approvers = requiredList(context.approvers);
    if (reviewers === null || approvers === null) {
      return denied("invalid-context");
    }
    return allowed(reviewers.has(actor.login) || approvers.has(actor.login));
  }

  if (command.name === "lgtm") {
    const giver = context.lgtmGiver === null
      ? null
      : requiredLogin(context.lgtmGiver);
    if (context.lgtmGiver !== null && giver === null) {
      return denied("invalid-context");
    }
    return allowed(
      isAuthor
      || actor.login === giver
      || permissionAtLeast(actor, "write"),
    );
  }

  if (command.name === "assign") {
    const reviewers = requiredList(context.reviewers);
    const approvers = requiredList(context.approvers);
    if (reviewers === null || approvers === null) {
      return denied("invalid-context");
    }
    return allowed(
      isAuthor
      || reviewers.has(actor.login)
      || approvers.has(actor.login)
      || permissionAtLeast(actor, "triage"),
    );
  }

  if (command.name === "unassign") {
    const assignees = requiredList(context.currentAssignees);
    if (assignees === null) {
      return denied("invalid-context");
    }
    return allowed(
      isAuthor
      || (
        assignees.has(actor.login)
        && command.users.size === 1
        && command.users.has(actor.login)
      )
      || permissionAtLeast(actor, "triage"),
    );
  }

  return allowed(isAuthor || permissionAtLeast(actor, "write"));
}

function normalizeTargetPermissions(value) {
  if (!(value instanceof Map)) {
    return null;
  }

  const normalized = new Map();
  for (const [key, identityValue] of value) {
    const login = normalizeLogin(key);
    if (login === null || normalized.has(login)) {
      return null;
    }
    const identity = normalizeIdentity(identityValue);
    if (identity.status === "human" && identity.login !== login) {
      normalized.set(login, { status: "unavailable" });
    } else {
      normalized.set(login, identity);
    }
  }
  return normalized;
}

function normalizeRequestedUsers(users) {
  if (!Array.isArray(users)) {
    return null;
  }

  const result = [];
  const seen = new Set();
  for (const user of users) {
    const login = normalizeLogin(user);
    const key = login ?? (typeof user === "string" ? `invalid:${user.toLowerCase()}` : null);
    if (key !== null && seen.has(key)) {
      continue;
    }
    if (key !== null) {
      seen.add(key);
    }
    result.push(login);
  }
  return result;
}

function eligibleAssignmentTargets(users, context) {
  const requested = normalizeRequestedUsers(users);
  if (requested === null || !isRecord(context)) {
    return {
      eligible: [],
      rejected: [{ login: null, reason: "invalid-input" }],
    };
  }

  const reviewers = requiredList(context.reviewers);
  const approvers = requiredList(context.approvers);
  const participants = requiredList(context.participants);
  const targets = normalizeTargetPermissions(context.targetPermissions);
  const contextValid = reviewers !== null
    && approvers !== null
    && participants !== null
    && targets !== null;
  const eligible = [];
  const rejected = [];

  for (const login of requested) {
    if (login === null) {
      rejected.push({ login: null, reason: "invalid-login" });
      continue;
    }
    if (!contextValid || !targets.has(login)) {
      rejected.push({ login, reason: "target-unavailable" });
      continue;
    }

    const target = targets.get(login);
    if (target.status === "unavailable") {
      rejected.push({ login, reason: "target-unavailable" });
      continue;
    }
    if (target.status !== "human") {
      rejected.push({ login, reason: "target-not-human" });
      continue;
    }

    const owner = reviewers.has(login) || approvers.has(login);
    const participant = participants.has(login);
    if (owner || participant || permissionAtLeast(target, "read")) {
      eligible.push(login);
    } else {
      rejected.push({ login, reason: "target-not-eligible" });
    }
  }

  return { eligible, rejected };
}

module.exports = { authorizeCommand, eligibleAssignmentTargets };
