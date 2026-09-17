"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { authorizeCommand } = require("../src/commands/authorization.js");

function command(name, targetBranch = null) {
  return { name, targetBranch, line: 1, raw: targetBranch === null ? `/${name}` : `/${name} ${targetBranch}` };
}

function actor(login = "reviewer", overrides = {}) {
  return {
    resolved: true,
    deleted: false,
    login,
    type: "User",
    liveCollaborator: false,
    permission: "read",
    ...overrides,
  };
}

function context(overrides = {}) {
  return {
    actor: actor(),
    author: "author",
    reviewers: ["reviewer", "owner"],
    approvers: ["approver", "owner"],
    owners: ["reviewer", "approver", "owner"],
    ...overrides,
  };
}

test("authorizes current human reviewers and approvers for evidence", () => {
  assert.deepEqual(authorizeCommand(command("lgtm"), context()), {
    allowed: true,
    reason: "authorized",
    actor: "reviewer",
    actorRole: "reviewer",
  });
  assert.deepEqual(authorizeCommand(command("approve"), context({
    actor: actor("approver"),
  })), {
    allowed: true,
    reason: "authorized",
    actor: "approver",
    actorRole: "approver",
  });
  assert.equal(authorizeCommand(command("approve"), context()).allowed, false);
});

test("never accepts PR-author or bot evidence", () => {
  assert.equal(authorizeCommand(command("lgtm"), context({
    actor: actor("author"),
    reviewers: ["author"],
  })).reason, "author-cannot-provide-evidence");
  assert.equal(authorizeCommand(command("approve"), context({
    actor: actor("dependabot", { type: "Bot", permission: "write", liveCollaborator: true }),
    approvers: ["dependabot"],
  })).reason, "actor-not-human");
});

test("authorizes holds only for current owners or write collaborators", () => {
  for (const name of ["hold", "unhold"]) {
    assert.equal(authorizeCommand(command(name), context()).allowed, true);
    assert.equal(authorizeCommand(command(name), context({
      actor: actor("maintainer", { liveCollaborator: true, permission: "write" }),
    })).allowed, true);
    assert.equal(authorizeCommand(command(name), context({
      actor: actor("reader"),
    })).allowed, false);
  }
});

test("authorizes operational commands for the author, owners, or write collaborators", () => {
  for (const name of ["retest", "backport", "cherry-pick"]) {
    const value = command(name, name === "retest" ? null : "release-1.2");
    assert.equal(authorizeCommand(value, context({ actor: actor("author") })).allowed, true);
    assert.equal(authorizeCommand(value, context()).allowed, true);
    assert.equal(authorizeCommand(value, context({
      actor: actor("maintainer", { liveCollaborator: true, permission: "maintain" }),
    })).allowed, true);
    assert.equal(authorizeCommand(value, context({ actor: actor("reader") })).allowed, false);
  }
});

test("fails closed for stale, deleted, malformed, and unknown identities", () => {
  const invalidActors = [
    null,
    {},
    actor("reviewer", { resolved: false }),
    actor("reviewer", { error: true }),
    actor("reviewer", { deleted: true }),
    actor("bad_login!"),
    actor("reviewer", { permission: "unknown" }),
  ];
  for (const value of invalidActors) {
    assert.equal(authorizeCommand(command("lgtm"), context({ actor: value })).allowed, false);
  }
});

test("rejects unapproved command shapes and invalid authority context", () => {
  const invalidCommands = [
    null,
    {},
    command("help"),
    { ...command("lgtm"), targetBranch: "release-1.2" },
    { ...command("backport", "release-1.2"), extra: true },
  ];
  for (const value of invalidCommands) {
    assert.equal(authorizeCommand(value, context()).reason, "invalid-command");
  }
  assert.equal(authorizeCommand(command("lgtm"), context({ reviewers: "reviewer" })).reason, "invalid-context");
});
