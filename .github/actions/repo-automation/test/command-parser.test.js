"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { parseCommands } = require("../src/commands/parser.js");

test("parses only the approved command grammar", () => {
  const parsed = parseCommands([
    "/lgtm",
    "/approve",
    "/hold",
    "/unhold",
    "/retest",
    "/backport release-1.2",
    "/cherry-pick release-1.3",
  ].join("\n"));

  assert.deepEqual(parsed.diagnostics, []);
  assert.deepEqual(
    parsed.commands.map(({ name, targetBranch }) => ({ name, targetBranch })),
    [
      { name: "lgtm", targetBranch: null },
      { name: "approve", targetBranch: null },
      { name: "hold", targetBranch: null },
      { name: "unhold", targetBranch: null },
      { name: "retest", targetBranch: null },
      { name: "backport", targetBranch: "release-1.2" },
      { name: "cherry-pick", targetBranch: "release-1.3" },
    ],
  );
});

test("rejects commands outside the approved set and non-exact command names", () => {
  const parsed = parseCommands([
    "/assign @octocat",
    "/unassign @octocat",
    "/help",
    "/LGTM",
    "/appro\u0432e",
  ].join("\n"));

  assert.deepEqual(parsed.commands, []);
  assert.deepEqual(
    parsed.diagnostics.map(({ code }) => code),
    Array(5).fill("unsupported-command"),
  );
});

test("requires exact argument counts and a safe canonical target branch", () => {
  const parsed = parseCommands([
    "/approve now",
    "/hold cancel",
    "/retest ci.yaml",
    "/backport",
    "/backport release-1.2 extra",
    "/backport --upload-pack=evil",
    "/cherry-pick release branch",
  ].join("\n"));

  assert.deepEqual(parsed.commands, []);
  assert.deepEqual(
    parsed.diagnostics.map(({ code }) => code),
    Array(7).fill("invalid-command"),
  );
});

test("ignores commands in CommonMark fences and block quotes", () => {
  const parsed = parseCommands([
    "```text",
    "/approve",
    "```",
    "> /lgtm",
    "~~~",
    "/hold",
    "~~~",
    "/retest",
  ].join("\n"));

  assert.deepEqual(parsed.diagnostics, []);
  assert.deepEqual(parsed.commands.map(({ name }) => name), ["retest"]);
});

test("bounds comment and command line sizes before parsing", () => {
  assert.deepEqual(parseCommands("x".repeat(65_537)).diagnostics[0].code, "body-too-large");
  assert.deepEqual(
    parseCommands(`/backport ${"a".repeat(4_096)}`).diagnostics[0].code,
    "line-too-large",
  );
});
