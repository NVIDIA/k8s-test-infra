"use strict";

const MAX_BODY_LENGTH = 65_536;
const MAX_LINE_LENGTH = 4_096;
const CONTROL_CHARACTERS = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;
const NO_ARGUMENT_COMMANDS = new Set(["lgtm", "approve", "hold", "unhold", "retest"]);
const TARGET_COMMANDS = new Set(["backport", "cherry-pick"]);
const SUPPORTED_COMMANDS = new Set([...NO_ARGUMENT_COMMANDS, ...TARGET_COMMANDS]);
const SAFE_BRANCH = /^(?!-)(?!.*(?:\.\.|@\{|\/\/|\\|[\x00-\x20\x7f~^:?*\[]))[A-Za-z0-9][A-Za-z0-9._\/-]{0,254}$/;

function diagnostic(line, code, message) {
  return { line, code, message };
}

function commandLike(line) {
  let index = 0;
  while (line[index] === " " || line[index] === "\t") index += 1;
  return line[index] === "/" && index + 1 < line.length;
}

function openingFence(line) {
  const match = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
  if (match === null || (match[1][0] === "`" && match[2].includes("`"))) return null;
  return { character: match[1][0], length: match[1].length };
}

function closesFence(line, fence) {
  const content = line.replace(/^ {0,3}/, "");
  let markerLength = 0;
  while (content[markerLength] === fence.character) markerLength += 1;
  return markerLength >= fence.length && /^[ \t]*$/.test(content.slice(markerLength));
}

function parseCommandLine(raw, line) {
  if (raw.length > MAX_LINE_LENGTH) {
    return { command: null, diagnostic: diagnostic(line, "line-too-large", "command line exceeds parser limit") };
  }
  if (CONTROL_CHARACTERS.test(raw.replaceAll("\t", ""))) {
    return { command: null, diagnostic: diagnostic(line, "unsafe-command", "command line contains unsupported control characters") };
  }

  const text = raw.replace(/^[ \t]+|[ \t]+$/g, "");
  const separator = text.search(/[ \t]/);
  const name = separator === -1 ? text.slice(1) : text.slice(1, separator);
  if (!SUPPORTED_COMMANDS.has(name)) {
    return { command: null, diagnostic: diagnostic(line, "unsupported-command", "command is not supported") };
  }

  const argumentsText = separator === -1
    ? ""
    : text.slice(separator).replace(/^[ \t]+|[ \t]+$/g, "");
  let targetBranch = null;
  if (NO_ARGUMENT_COMMANDS.has(name)) {
    if (argumentsText !== "") {
      return { command: null, diagnostic: diagnostic(line, "invalid-command", "command arguments do not match the supported syntax") };
    }
  } else if (!SAFE_BRANCH.test(argumentsText)) {
    return { command: null, diagnostic: diagnostic(line, "invalid-command", "command arguments do not match the supported syntax") };
  } else {
    targetBranch = argumentsText;
  }

  return {
    command: { name, targetBranch, line, raw },
    diagnostic: null,
  };
}

function parseCommands(body) {
  if (typeof body !== "string") {
    return { commands: [], diagnostics: [diagnostic(0, "invalid-body", "comment body must be a string")] };
  }
  if (body.length > MAX_BODY_LENGTH) {
    return { commands: [], diagnostics: [diagnostic(0, "body-too-large", "comment body exceeds parser limit")] };
  }

  const commands = [];
  const diagnostics = [];
  let fence = null;
  const lines = body.split(/\r\n|\n|\r/);
  for (let index = 0; index < lines.length; index += 1) {
    const raw = lines[index];
    if (fence !== null) {
      if (closesFence(raw, fence)) fence = null;
      continue;
    }
    fence = openingFence(raw);
    if (fence !== null || /^[ \t]*>/.test(raw) || !commandLike(raw)) continue;
    const parsed = parseCommandLine(raw, index + 1);
    if (parsed.command === null) diagnostics.push(parsed.diagnostic);
    else commands.push(parsed.command);
  }
  return { commands, diagnostics };
}

module.exports = { parseCommands };
