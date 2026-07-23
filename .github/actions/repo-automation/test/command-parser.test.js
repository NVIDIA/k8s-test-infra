"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { parseCommands } = require("../src/commands/parser.js");

function command(name, operation, users, line, raw) {
  return { name, operation, users, line, raw };
}

function cherryPick(operation, branch, line, raw) {
  return { name: "cherry-pick", operation, users: [], branch, line, raw };
}

test("parses only the approved command grammar", () => {
  const body = [
    "/lgtm",
    "/lgtm cancel",
    "/assign @Alice @bob",
    "/unassign @carol",
    "/hold",
    "/hold cancel",
    "/retest",
    "/help",
  ].join("\n");

  assert.deepEqual(parseCommands(body), {
    commands: [
      command("lgtm", "apply", [], 1, "/lgtm"),
      command("lgtm", "cancel", [], 2, "/lgtm cancel"),
      command("assign", "apply", ["alice", "bob"], 3, "/assign @Alice @bob"),
      command("unassign", "apply", ["carol"], 4, "/unassign @carol"),
      command("hold", "apply", [], 5, "/hold"),
      command("hold", "cancel", [], 6, "/hold cancel"),
      command("retest", "run", [], 7, "/retest"),
      command("help", "show", [], 8, "/help"),
    ],
    diagnostics: [],
  });
});

test("anchors commands to logical lines and accepts leading horizontal whitespace", () => {
  const result = parseCommands([
    "prose /lgtm",
    "  /LGTM\t",
    "\t/AsSiGn\t@User-One  @USER2 ",
    "suffix /hold",
  ].join("\n"));

  assert.deepEqual(result, {
    commands: [
      command("lgtm", "apply", [], 2, "  /LGTM\t"),
      command("assign", "apply", ["user-one", "user2"], 3, "\t/AsSiGn\t@User-One  @USER2 "),
    ],
    diagnostics: [],
  });
});

test("parses multiple CRLF-delimited commands with stable one-based line numbers", () => {
  const result = parseCommands("intro\r\n/lgtm\r\n\r\n/help\r\n");

  assert.deepEqual(result.commands, [
    command("lgtm", "apply", [], 2, "/lgtm"),
    command("help", "show", [], 4, "/help"),
  ]);
  assert.deepEqual(result.diagnostics, []);
});

test("ignores commands inside backtick and tilde fenced blocks", () => {
  const result = parseCommands([
    "```text",
    "/lgtm",
    "```",
    " /hold",
    "~~~ markdown",
    "/assign @quoted",
    "~~~~",
    "/retest",
  ].join("\n"));

  assert.deepEqual(result.commands, [
    command("hold", "apply", [], 4, " /hold"),
    command("retest", "run", [], 8, "/retest"),
  ]);
  assert.deepEqual(result.diagnostics, []);
});

test("does not close a fence with a tab-indented delimiter", () => {
  const result = parseCommands([
    "```",
    "\t```",
    "/lgtm",
  ].join("\n"));

  assert.deepEqual(result, { commands: [], diagnostics: [] });
});

test("does not open a fence with a tab-indented delimiter", () => {
  const result = parseCommands([
    "\t```",
    "/lgtm",
  ].join("\n"));

  assert.deepEqual(result, {
    commands: [command("lgtm", "apply", [], 2, "/lgtm")],
    diagnostics: [],
  });
});

test("rejects a backtick fence opener whose info string contains a backtick", () => {
  const result = parseCommands([
    "```lang`bad",
    "/lgtm",
  ].join("\n"));

  assert.deepEqual(result, {
    commands: [command("lgtm", "apply", [], 2, "/lgtm")],
    diagnostics: [],
  });
});

test("accepts backticks in a tilde fence info string", () => {
  const result = parseCommands([
    "   ~~~lang`valid",
    "/lgtm",
    "   ~~~",
    "/help",
  ].join("\n"));

  assert.deepEqual(result, {
    commands: [command("help", "show", [], 4, "/help")],
    diagnostics: [],
  });
});

test("ignores Markdown blockquote command examples", () => {
  const result = parseCommands([
    "> /lgtm",
    "  > /hold",
    "\t>> /assign @quoted",
    "/help",
  ].join("\n"));

  assert.deepEqual(result.commands, [command("help", "show", [], 4, "/help")]);
  assert.deepEqual(result.diagnostics, []);
});

test("rejects a whole malformed supported command with a safe diagnostic", () => {
  const body = [
    "/lgtm now",
    "/lgtm CANCEL",
    "/hold cancel extra",
    "/retest please",
    "/help me",
    "/assign",
    "/assign alice",
    "/assign @valid bad",
    "/unassign @-bad",
    "/unassign @bad-",
    "/assign @bad--name",
    "/assign @abcdefghijklmnopqrstuvwxyzabcdefghijklmn",
  ].join("\n");

  const result = parseCommands(body);

  assert.deepEqual(result.commands, []);
  assert.equal(result.diagnostics.length, 12);
  for (const [index, diagnostic] of result.diagnostics.entries()) {
    assert.deepEqual(diagnostic, {
      line: index + 1,
      code: "invalid-command",
      message: "command arguments do not match the supported syntax",
    });
  }
});

test("reports unsupported and command-like prefix lines separately from commands", () => {
  const result = parseCommands([
    "/approve",
    "/lgtmplease",
    "/unknown argument",
    "explanation /approve",
  ].join("\n"));

  assert.deepEqual(result.commands, []);
  assert.deepEqual(result.diagnostics, [
    { line: 1, code: "unsupported-command", message: "command is not supported" },
    { line: 2, code: "unsupported-command", message: "command is not supported" },
    { line: 3, code: "unsupported-command", message: "command is not supported" },
  ]);
});

test("deduplicates mentions case-insensitively in first-seen canonical order", () => {
  const result = parseCommands("/assign @Bravo @alpha @BRAVO @Alpha @charlie");

  assert.deepEqual(result, {
    commands: [
      command("assign", "apply", ["bravo", "alpha", "charlie"], 1, "/assign @Bravo @alpha @BRAVO @Alpha @charlie"),
    ],
    diagnostics: [],
  });
});

test("does not echo untrusted command text or controls in diagnostics", () => {
  const sentinel = "secret-sentinel-7c9e";
  const result = parseCommands([
    `/approve ${sentinel}`,
    `/assign @valid\u0000${sentinel}`,
    `/hold \u202E${sentinel}`,
  ].join("\n"));

  assert.deepEqual(result.commands, []);
  assert.equal(result.diagnostics.length, 3);
  for (const diagnostic of result.diagnostics) {
    assert.equal(JSON.stringify(diagnostic).includes(sentinel), false);
    assert.match(diagnostic.code, /^(unsupported-command|unsafe-command)$/);
  }
});

test("bounds body and logical-line processing with fixed diagnostics", () => {
  const oversizedBody = `/lgtm\n${"x".repeat(65_537)}`;
  assert.deepEqual(parseCommands(oversizedBody), {
    commands: [],
    diagnostics: [
      { line: 0, code: "body-too-large", message: "comment body exceeds parser limit" },
    ],
  });

  const oversizedCommand = `/assign @valid ${"x".repeat(4_097)}`;
  assert.deepEqual(parseCommands(oversizedCommand), {
    commands: [],
    diagnostics: [
      { line: 1, code: "line-too-large", message: "command line exceeds parser limit" },
    ],
  });
});

test("handles non-string bodies without throwing or exposing values", () => {
  assert.deepEqual(parseCommands({ body: "/lgtm", token: "secret" }), {
    commands: [],
    diagnostics: [
      { line: 0, code: "invalid-body", message: "comment body must be a string" },
    ],
  });
});

test("parses the /cherry-pick apply and cancel grammar with a lowercased name", () => {
  const body = [
    "/cherry-pick release-0.2",
    "/CHERRY-PICK release-0.2",
    "/cherry-pick release-0.2 cancel",
  ].join("\n");

  assert.deepEqual(parseCommands(body), {
    commands: [
      cherryPick("apply", "release-0.2", 1, "/cherry-pick release-0.2"),
      cherryPick("apply", "release-0.2", 2, "/CHERRY-PICK release-0.2"),
      cherryPick("cancel", "release-0.2", 3, "/cherry-pick release-0.2 cancel"),
    ],
    diagnostics: [],
  });
});

test("rejects malformed /cherry-pick arguments and branch tokens with a safe diagnostic", () => {
  const body = [
    "/cherry-pick",
    "/cherry-pick a b c",
    "/cherry-pick release-0.2 nope",
    "/cherry-pick -bad",
    "/cherry-pick a..b",
    "/cherry-pick a//b",
    "/cherry-pick bad/",
    "/cherry-pick x.lock",
    `/cherry-pick ${"a".repeat(121)}`,
    "/cherry-pick @user",
  ].join("\n");

  const result = parseCommands(body);

  assert.deepEqual(result.commands, []);
  assert.equal(result.diagnostics.length, 10);
  for (const [index, diagnostic] of result.diagnostics.entries()) {
    assert.deepEqual(diagnostic, {
      line: index + 1,
      code: "invalid-command",
      message: "command arguments do not match the supported syntax",
    });
  }
});

test("ignores /cherry-pick inside fenced and blockquoted regions", () => {
  const result = parseCommands([
    "```",
    "/cherry-pick release-0.2",
    "```",
    "> /cherry-pick release-0.2",
    "/cherry-pick release-0.3",
  ].join("\n"));

  assert.deepEqual(result.commands, [
    cherryPick("apply", "release-0.3", 5, "/cherry-pick release-0.3"),
  ]);
  assert.deepEqual(result.diagnostics, []);
});
