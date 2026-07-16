"use strict";

const MAX_BODY_LENGTH = 65_536;
const MAX_LINE_LENGTH = 4_096;
const CONTROL_CHARACTERS = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;
const LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const SUPPORTED_COMMANDS = new Set([
  "assign",
  "help",
  "hold",
  "lgtm",
  "retest",
  "unassign",
]);

function diagnostic(line, code, message) {
  return { line, code, message };
}

function hasUnsafeControl(line) {
  return CONTROL_CHARACTERS.test(line.replaceAll("\t", ""));
}

function commandLike(line) {
  let index = 0;
  while (line[index] === " " || line[index] === "\t") {
    index += 1;
  }
  return line[index] === "/" && index + 1 < line.length;
}

function openingFence(line) {
  const match = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
  if (match === null) {
    return null;
  }
  if (match[1][0] === "`" && match[2].includes("`")) {
    return null;
  }
  return { character: match[1][0], length: match[1].length };
}

function closesFence(line, fence) {
  const content = line.replace(/^ {0,3}/, "");
  let markerLength = 0;
  while (content[markerLength] === fence.character) {
    markerLength += 1;
  }
  return markerLength >= fence.length && /^[ \t]*$/.test(content.slice(markerLength));
}

function parseUsers(argumentText) {
  if (argumentText === "") {
    return null;
  }

  const users = [];
  const seen = new Set();
  for (const mention of argumentText.split(/[ \t]+/)) {
    if (!mention.startsWith("@") || !LOGIN.test(mention.slice(1))) {
      return null;
    }
    const login = mention.slice(1).toLowerCase();
    if (!seen.has(login)) {
      users.push(login);
      seen.add(login);
    }
  }
  return users;
}

function parsedCommand(name, argumentText, line, raw) {
  if (name === "lgtm" || name === "hold") {
    if (argumentText === "") {
      return { name, operation: "apply", users: [], line, raw };
    }
    if (argumentText === "cancel") {
      return { name, operation: "cancel", users: [], line, raw };
    }
    return null;
  }

  if (name === "assign" || name === "unassign") {
    const users = parseUsers(argumentText);
    return users === null ? null : { name, operation: "apply", users, line, raw };
  }

  if (name === "retest") {
    return argumentText === ""
      ? { name, operation: "run", users: [], line, raw }
      : null;
  }

  return argumentText === ""
    ? { name, operation: "show", users: [], line, raw }
    : null;
}

function parseCommandLine(raw, line) {
  if (raw.length > MAX_LINE_LENGTH) {
    return {
      command: null,
      diagnostic: diagnostic(line, "line-too-large", "command line exceeds parser limit"),
    };
  }
  if (hasUnsafeControl(raw)) {
    return {
      command: null,
      diagnostic: diagnostic(line, "unsafe-command", "command line contains unsupported control characters"),
    };
  }

  const text = raw.replace(/^[ \t]+|[ \t]+$/g, "");
  const separator = text.search(/[ \t]/);
  const token = separator === -1 ? text.slice(1) : text.slice(1, separator);
  const name = token.toLowerCase();
  if (!SUPPORTED_COMMANDS.has(name)) {
    return {
      command: null,
      diagnostic: diagnostic(line, "unsupported-command", "command is not supported"),
    };
  }

  const argumentText = separator === -1
    ? ""
    : text.slice(separator).replace(/^[ \t]+|[ \t]+$/g, "");
  const command = parsedCommand(name, argumentText, line, raw);
  if (command === null) {
    return {
      command: null,
      diagnostic: diagnostic(
        line,
        "invalid-command",
        "command arguments do not match the supported syntax",
      ),
    };
  }
  return { command, diagnostic: null };
}

// Commands and diagnostics are separate so malformed command-like lines can be
// explained without weakening the type or trust boundary of executable input.
function parseCommands(body) {
  if (typeof body !== "string") {
    return {
      commands: [],
      diagnostics: [diagnostic(0, "invalid-body", "comment body must be a string")],
    };
  }
  if (body.length > MAX_BODY_LENGTH) {
    return {
      commands: [],
      diagnostics: [diagnostic(0, "body-too-large", "comment body exceeds parser limit")],
    };
  }

  const commands = [];
  const diagnostics = [];
  let fence = null;
  const lines = body.split(/\r\n|\n|\r/);
  for (let index = 0; index < lines.length; index += 1) {
    const raw = lines[index];
    if (fence !== null) {
      if (closesFence(raw, fence)) {
        fence = null;
      }
      continue;
    }

    fence = openingFence(raw);
    if (fence !== null || /^[ \t]*>/.test(raw) || !commandLike(raw)) {
      continue;
    }

    const result = parseCommandLine(raw, index + 1);
    if (result.command !== null) {
      commands.push(result.command);
    } else {
      diagnostics.push(result.diagnostic);
    }
  }
  return { commands, diagnostics };
}

module.exports = { parseCommands };
