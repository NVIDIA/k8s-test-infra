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
  assert.deepEqual(parsed.commands, [
    { name: "lgtm", line: 1, raw: "/lgtm" },
    { name: "approve", line: 2, raw: "/approve" },
    { name: "hold", line: 3, raw: "/hold" },
    { name: "unhold", line: 4, raw: "/unhold" },
    { name: "retest", line: 5, raw: "/retest" },
  ]);
});

test("rejects commands outside the approved set and non-exact command names", () => {
  const parsed = parseCommands([
    "/label bug",
    "/meow",
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

test("the parser's line guards run before the agent-owned skip", () => {
  const parsed = parseCommands([
    "/cherry-pick release-1.2\u200b",
    `/cherry-pick ${"a".repeat(4_096)}`,
    "/cherry-pick\u00a0release-1.2",
  ].join("\n"));

  assert.deepEqual(parsed.commands, []);
  assert.deepEqual(parsed.diagnostics.map(({ line, code }) => ({ line, code })), [
    { line: 1, code: "unsafe-command" },
    { line: 2, code: "line-too-large" },
    { line: 3, code: "unsupported-command" },
  ]);
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

test("skips the agent-owned Prow commands without a command or diagnostic", () => {
  for (const body of [
    "/assign",
    "/assign @octocat",
    "/assign octocat hubot",
    "/unassign @octocat",
    "/cc @octocat",
    "/cc nvidia/maintainers",
    "/uncc @octocat",
    "/close",
    "/close not-planned",
    "/reopen",
    "/retitle fix: a new title",
    "/retitle",
    "  /retitle\tfix: tabbed\t",
  ]) {
    assert.deepEqual(parseCommands(body), { commands: [], diagnostics: [] }, body);
  }
});

test("a Prow command line mixed with /hold yields exactly the /hold command", () => {
  const parsed = parseCommands("/assign @octocat\n/cc @hubot\n/hold\n/close\n/retitle fix: x");

  assert.deepEqual(parsed.diagnostics, []);
  assert.deepEqual(
    parsed.commands.map(({ name, line, raw }) => ({ name, line, raw })),
    [{ name: "hold", line: 3, raw: "/hold" }],
  );
});

test("only the exact agent-owned Prow command names are skipped", () => {
  const parsed = parseCommands([
    "/assigns @octocat",
    "/ASSIGN @octocat",
    "/Close",
    "/cc-me",
    "/un-cc @octocat",
    "/re-open",
    "/retitles fix: x",
  ].join("\n"));

  assert.deepEqual(parsed.commands, []);
  assert.deepEqual(
    parsed.diagnostics.map(({ line, code }) => ({ line, code })),
    [1, 2, 3, 4, 5, 6, 7].map((line) => ({ line, code: "unsupported-command" })),
  );
});

test("the parser's line guards run before the Prow command skip", () => {
  const parsed = parseCommands([
    "/assign @octocat\u200b",
    `/retitle ${"a".repeat(4_096)}`,
    "/close\u00a0now",
  ].join("\n"));

  assert.deepEqual(parsed.commands, []);
  assert.deepEqual(parsed.diagnostics.map(({ line, code }) => ({ line, code })), [
    { line: 1, code: "unsafe-command" },
    { line: 2, code: "line-too-large" },
    { line: 3, code: "unsupported-command" },
  ]);
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
