"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { authorizeCommand } = require("../src/commands/authorization.js");

function command(name) {
  return { name, line: 1, raw: `/${name}` };
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

test("authorizes /retest for the author, owners, or write collaborators", () => {
  const value = command("retest");
  assert.equal(authorizeCommand(value, context({ actor: actor("author") })).allowed, true);
  assert.equal(authorizeCommand(value, context()).allowed, true);
  assert.equal(authorizeCommand(value, context({
    actor: actor("maintainer", { liveCollaborator: true, permission: "maintain" }),
  })).allowed, true);
  assert.equal(authorizeCommand(value, context({ actor: actor("reader") })).allowed, false);
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

test("accepts app bot pull request authors and rejects unsafe author context", () => {
  assert.deepEqual(authorizeCommand(command("lgtm"), context({ author: "dependabot[bot]" })), {
    allowed: true,
    reason: "authorized",
    actor: "reviewer",
    actorRole: "reviewer",
  });
  for (const author of ["alice​", "alice\n", "[bot]", "bad--login", "dependabot[bot][bot]", 42, null]) {
    assert.equal(authorizeCommand(command("lgtm"), context({ author })).reason, "invalid-context", String(author));
  }
});

test("rejects unapproved command shapes and invalid authority context", () => {
  const invalidCommands = [
    null,
    {},
    command("help"),
    command("backport"),
    command("cherry-pick"),
    { ...command("lgtm"), targetBranch: null },
    { ...command("lgtm"), extra: true },
  ];
  for (const value of invalidCommands) {
    assert.equal(authorizeCommand(value, context()).reason, "invalid-command");
  }
  assert.equal(authorizeCommand(command("lgtm"), context({ reviewers: "reviewer" })).reason, "invalid-context");
});
