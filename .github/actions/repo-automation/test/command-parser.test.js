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
  ].join("\n"));

  assert.deepEqual(parsed.commands, []);
  assert.deepEqual(
    parsed.diagnostics.map(({ code }) => code),
    Array(3).fill("invalid-command"),
  );
});

test("skips the agent-owned /cherry-pick command without a command or diagnostic", () => {
  for (const body of [
    "/cherry-pick release-1.2",
    "  /cherry-pick release-1.2\t",
    "/cherry-pick",
    "/cherry-pick release branch",
    "/cherry-pick --upload-pack=evil",
  ]) {
    assert.deepEqual(parseCommands(body), { commands: [], diagnostics: [] }, body);
  }
});

test("a /cherry-pick line mixed with /lgtm yields exactly the /lgtm command", () => {
  const parsed = parseCommands("/cherry-pick release-1.2\n/lgtm");

  assert.deepEqual(parsed.diagnostics, []);
  assert.deepEqual(
    parsed.commands.map(({ name, line, raw }) => ({ name, line, raw })),
    [{ name: "lgtm", line: 2, raw: "/lgtm" }],
  );
});

test("only the exact agent-owned command name is skipped", () => {
  const parsed = parseCommands([
    "/cherry-picks release-1.2",
    "/CHERRY-PICK release-1.2",
    "/cherry_pick release-1.2",
    "/cherrypick release-1.2",
  ].join("\n"));

  assert.deepEqual(parsed.commands, []);
  assert.deepEqual(
    parsed.diagnostics.map(({ line, code }) => ({ line, code })),
    [1, 2, 3, 4].map((line) => ({ line, code: "unsupported-command" })),
  );
});

test("/backport is an ordinary unsupported command", () => {
  const unsupported = {
    commands: [],
    diagnostics: [{ line: 1, code: "unsupported-command", message: "command is not supported" }],
  };

  assert.deepEqual(parseCommands("/backport release-1.2"), unsupported);
  assert.deepEqual(parseCommands("/backport"), unsupported);
  assert.deepEqual(parseCommands("/foo release-1.2"), unsupported);
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
    parseCommands(`/lgtm ${"a".repeat(4_096)}`).diagnostics[0].code,
    "line-too-large",
  );
});
