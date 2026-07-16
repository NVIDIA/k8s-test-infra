"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const {
  authorizeCommand,
  eligibleAssignmentTargets,
} = require("../src/commands/authorization.js");

function command(name, operation = "apply", users = []) {
  return { name, operation, users };
}

function human(login, overrides = {}) {
  return {
    login,
    type: "User",
    resolved: true,
    liveCollaborator: false,
    permission: "none",
    ...overrides,
  };
}

function context(actor, overrides = {}) {
  return {
    actor,
    author: "author",
    reviewers: ["reviewer"],
    approvers: ["approver"],
    lgtmGiver: "giver",
    currentAssignees: ["assignee"],
    participants: ["participant"],
    targetPermissions: new Map(),
    ...overrides,
  };
}

function decision(name, operation, actor, overrides = {}, users = []) {
  return authorizeCommand(
    command(name, operation, users),
    context(actor, overrides),
  );
}

test("authorizes lgtm only for applicable reviewers or approvers and never the author", () => {
  assert.deepEqual(
    decision("lgtm", "apply", human("ReViEwEr")),
    { allowed: true, reason: "authorized" },
  );
  assert.deepEqual(
    decision("lgtm", "apply", human("APPROVER", { type: "uSeR" })),
    { allowed: true, reason: "authorized" },
  );
  assert.deepEqual(
    decision("lgtm", "apply", human("AUTHOR"), {
      reviewers: ["author"],
      approvers: ["AUTHOR"],
    }),
    { allowed: false, reason: "author-cannot-lgtm" },
  );
  assert.deepEqual(
    decision("lgtm", "apply", human("outsider", {
      liveCollaborator: true,
      permission: "admin",
    })),
    { allowed: false, reason: "not-authorized" },
  );
});

test("authorizes lgtm cancellation for the giver, author, or write-capable collaborator", () => {
  for (const login of ["GIVER", "Author"]) {
    assert.deepEqual(
      decision("lgtm", "cancel", human(login)),
      { allowed: true, reason: "authorized" },
    );
  }

  for (const [permission, allowed] of [
    ["none", false],
    ["read", false],
    ["triage", false],
    ["write", true],
    ["maintain", true],
    ["admin", true],
  ]) {
    assert.equal(
      decision("lgtm", "cancel", human("maintainer", {
        liveCollaborator: true,
        permission,
      })).allowed,
      allowed,
      permission,
    );
  }
});

test("authorizes assign for the author, applicable owners, or triage-capable collaborators", () => {
  for (const login of ["author", "reviewer", "APPROVER"]) {
    assert.deepEqual(
      decision("assign", "apply", human(login), {}, ["target"]),
      { allowed: true, reason: "authorized" },
    );
  }

  for (const [permission, allowed] of [
    ["none", false],
    ["read", false],
    ["TRIAGE", true],
    ["write", true],
    ["maintain", true],
    ["admin", true],
  ]) {
    assert.equal(
      decision("assign", "apply", human("collaborator", {
        liveCollaborator: true,
        permission,
      }), {}, ["target"]).allowed,
      allowed,
      permission,
    );
  }
});

test("authorizes unassign for a named current assignee, the author, or triage-capable collaborator", () => {
  assert.deepEqual(
    decision("unassign", "apply", human("ASSIGNEE"), {}, ["assignee"]),
    { allowed: true, reason: "authorized" },
  );
  assert.deepEqual(
    decision("unassign", "apply", human("assignee"), {}, ["someone-else"]),
    { allowed: false, reason: "not-authorized" },
  );
  assert.deepEqual(
    decision("unassign", "apply", human("author"), {}, ["assignee"]),
    { allowed: true, reason: "authorized" },
  );
  for (const [permission, allowed] of [
    ["none", false],
    ["read", false],
    ["triage", true],
    ["write", true],
    ["maintain", true],
    ["admin", true],
  ]) {
    assert.equal(
      decision("unassign", "apply", human("collaborator", {
        liveCollaborator: true,
        permission,
      }), {}, ["assignee"]).allowed,
      allowed,
      permission,
    );
  }
});

test("requires write capability for collaborator hold and retest operations", () => {
  for (const [name, operation] of [
    ["hold", "apply"],
    ["hold", "cancel"],
    ["retest", "run"],
  ]) {
    assert.equal(decision(name, operation, human("author")).allowed, true);
    for (const [permission, allowed] of [
      ["none", false],
      ["read", false],
      ["triage", false],
      ["WrItE", true],
      ["maintain", true],
      ["admin", true],
    ]) {
      assert.equal(
        decision(name, operation, human("collaborator", {
          liveCollaborator: true,
          permission,
        })).allowed,
        allowed,
        `${name} ${operation} ${permission}`,
      );
    }
  }
});

test("does not mistake public read permission for live collaborator proof", () => {
  const publicReader = human("public-reader", {
    liveCollaborator: false,
    permission: "admin",
  });

  assert.deepEqual(
    decision("assign", "apply", publicReader, {}, ["target"]),
    { allowed: false, reason: "not-authorized" },
  );
  assert.deepEqual(
    decision("hold", "apply", publicReader),
    { allowed: false, reason: "not-authorized" },
  );
});

test("ignores stale author association and fails closed for unknown permissions", () => {
  const stalePayloadContext = {
    authorAssociation: "OWNER",
    actorAuthorAssociation: "MEMBER",
  };
  assert.deepEqual(
    decision("retest", "run", human("outsider"), stalePayloadContext),
    { allowed: false, reason: "not-authorized" },
  );

  for (const permission of [undefined, null, "", "owner", "push", "error"]) {
    assert.deepEqual(
      decision("hold", "apply", human("collaborator", {
        liveCollaborator: true,
        permission,
      })),
      { allowed: false, reason: "not-authorized" },
    );
  }
});

test("help is available only to a live resolved human actor", () => {
  assert.deepEqual(
    decision("help", "show", human("new-contributor")),
    { allowed: true, reason: "authorized" },
  );

  for (const actor of [
    human("automation", { type: "Bot" }),
    human("deleted-user", { deleted: true }),
    human("bad_login"),
    { login: "missing", type: "User", resolved: false },
    { login: "api-error", type: "User", resolved: false, error: true },
    null,
  ]) {
    assert.equal(decision("help", "show", actor).allowed, false);
  }
});

test("explicit actor API errors fail closed even when other fields look resolved", () => {
  assert.deepEqual(
    decision("help", "show", human("api-error", {
      error: true,
      liveCollaborator: true,
      permission: "admin",
    })),
    { allowed: false, reason: "actor-unavailable" },
  );
});

test("rejects malformed command shapes without throwing or echoing hostile fields", () => {
  const hostile = "secret-sentinel-91ef";
  for (const invalidCommand of [
    null,
    { name: "approve", operation: "apply", users: [] },
    { name: "lgtm", operation: "run", users: [], raw: hostile },
    { name: "assign", operation: "apply", users: [] },
    { name: "assign", operation: "apply", users: ["bad_login"] },
  ]) {
    const result = authorizeCommand(invalidCommand, context(human("author")));
    assert.deepEqual(result, { allowed: false, reason: "invalid-command" });
    assert.equal(JSON.stringify(result).includes(hostile), false);
  }
});

test("preserves assignment target order with case-insensitive deduplication", () => {
  const targets = new Map([
    ["owner", human("Owner")],
    ["participant", human("PARTICIPANT")],
    ["reader", human("Reader", { liveCollaborator: true, permission: "read" })],
  ]);
  const result = eligibleAssignmentTargets(
    ["Owner", "PARTICIPANT", "reader", "OWNER", "Reader"],
    context(human("author"), {
      reviewers: ["OWNER"],
      participants: ["participant"],
      targetPermissions: targets,
    }),
  );

  assert.deepEqual(result, {
    eligible: ["owner", "participant", "reader"],
    rejected: [],
  });
});

test("accepts only resolved human owners, participants, or proven read collaborators as targets", () => {
  const targetPermissions = new Map([
    ["owner", human("owner")],
    ["participant", human("participant")],
    ["reader", human("reader", { liveCollaborator: true, permission: "READ" })],
    ["triager", human("triager", { liveCollaborator: true, permission: "triage" })],
    ["public-reader", human("public-reader", { liveCollaborator: false, permission: "admin" })],
    ["bot", human("bot", { type: "Bot", liveCollaborator: true, permission: "write" })],
    ["deleted", human("deleted", { deleted: true })],
    ["api-error", {
      login: "api-error",
      type: "User",
      resolved: true,
      error: true,
      liveCollaborator: true,
      permission: "admin",
    }],
  ]);

  assert.deepEqual(
    eligibleAssignmentTargets(
      [
        "owner",
        "participant",
        "reader",
        "triager",
        "public-reader",
        "bot",
        "deleted",
        "api-error",
        "missing",
        "arbitrary",
        "bad_login",
      ],
      context(human("author"), {
        approvers: ["owner"],
        participants: ["participant"],
        targetPermissions,
      }),
    ),
    {
      eligible: ["owner", "participant", "reader", "triager"],
      rejected: [
        { login: "public-reader", reason: "target-not-eligible" },
        { login: "bot", reason: "target-not-human" },
        { login: "deleted", reason: "target-not-human" },
        { login: "api-error", reason: "target-unavailable" },
        { login: "missing", reason: "target-unavailable" },
        { login: "arbitrary", reason: "target-unavailable" },
        { login: null, reason: "invalid-login" },
      ],
    },
  );
});

test("does not use actor permission as assignment target permission", () => {
  const result = eligibleAssignmentTargets(
    ["arbitrary"],
    context(human("admin", {
      liveCollaborator: true,
      permission: "admin",
    }), {
      targetPermissions: new Map([
        ["arbitrary", human("arbitrary", { permission: "read" })],
      ]),
    }),
  );

  assert.deepEqual(result, {
    eligible: [],
    rejected: [{ login: "arbitrary", reason: "target-not-eligible" }],
  });
});
