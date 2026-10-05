/**
 * Copyright 2026 NVIDIA CORPORATION
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

"use strict";

// The Mokka agent decides who may use each command before it dispatches the
// workflow; this module only checks that the dispatch inputs are well formed.
const COMMANDS = ["assign", "unassign", "cc", "uncc", "close", "reopen", "retitle"];
const PEOPLE_COMMANDS = new Set(["assign", "unassign", "cc", "uncc"]);
const NUMBER = /^[1-9][0-9]{0,9}$/;
const GITHUB_LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
// GitHub accepts at most 10 assignees on an issue.
const MAX_USERS = 10;
const MAX_TITLE_LENGTH = 256;
const CONTROL_CHARACTERS = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;
// Prow's invalidcommitmsg.CloseIssueRegex, which /retitle refuses. RE2's \s is
// [\t\n\f\r ]; JavaScript's would also match a no-break space. The repository
// part also allows "-" and ".": Prow's \w+/\w+ misses names like k8s-test-infra.
const CLOSES_ISSUE = /(?:clos(?:e[sd]?)|fix(?:es|ed)?|resolv(?:e[sd]?))[\t\n\f\r :]+(?:[\w.-]+\/[\w.-]+)?#\d+/i;
const UNSAFE_IN_CODE_SPAN = /[`\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/gu;

// detail is the reason in a form safe for a comment: it names the rule and
// never carries a title, and a rejected login only inside a code span.
class InputError extends Error {
  constructor(message, detail = message) {
    super(message);
    this.detail = detail;
  }
}

// One line, with a backtick or hidden character shown as U+FFFD, so it can
// neither leave its code span nor look like a valid login.
function codeSpan(value) {
  return `\`${[...value.replace(UNSAFE_IN_CODE_SPAN, "\ufffd")].slice(0, 40).join("")}\``;
}

function parseNumber(number) {
  return typeof number === "string" && NUMBER.test(number.trim()) ? Number(number.trim()) : null;
}

function present(value) {
  return typeof value === "string" && value.trim() !== "";
}

function parseUsers(command, users) {
  const logins = [];
  for (const raw of (users ?? "").split(",")) {
    const login = raw.trim();
    if (login === "") continue;
    if (!GITHUB_LOGIN.test(login)) {
      throw new InputError(`user ${JSON.stringify(login)} is not a GitHub login`,
        `user ${codeSpan(login)} is not a GitHub login`);
    }
    if (!logins.some((known) => known.toLowerCase() === login.toLowerCase())) logins.push(login);
  }
  if (logins.length === 0) {
    throw new InputError(`users must name at least one GitHub login for ${command}`);
  }
  if (logins.length > MAX_USERS) {
    throw new InputError(`users names more than ${MAX_USERS} logins`);
  }
  return logins;
}

function parseTitle(title) {
  if (!present(title)) throw new InputError("title must not be empty");
  if (CONTROL_CHARACTERS.test(title)) throw new InputError("title contains control or format characters");
  const trimmed = title.trim();
  if ([...trimmed].length > MAX_TITLE_LENGTH) {
    throw new InputError(`title is longer than ${MAX_TITLE_LENGTH} characters`);
  }
  if (CLOSES_ISSUE.test(trimmed)) throw new InputError("title must not contain a keyword that closes an issue");
  return trimmed;
}

function parseInputs({ command, number, users, title, requester }) {
  if (!COMMANDS.includes(command)) {
    throw new InputError(`command must be one of ${COMMANDS.join(", ")}`);
  }
  if (parseNumber(number) === null) {
    throw new InputError("number must be a positive integer");
  }
  if (typeof requester !== "string" || !GITHUB_LOGIN.test(requester)) {
    throw new InputError("requester must be a GitHub login");
  }
  if (!PEOPLE_COMMANDS.has(command) && present(users)) {
    throw new InputError("users is only used by assign, unassign, cc and uncc");
  }
  if (command !== "retitle" && present(title)) {
    throw new InputError("title is only used by retitle");
  }
  return {
    command,
    number: parseNumber(number),
    users: PEOPLE_COMMANDS.has(command) ? parseUsers(command, users) : [],
    title: command === "retitle" ? parseTitle(title) : null,
    requester,
  };
}

module.exports = { COMMANDS, InputError, parseInputs, parseNumber };
