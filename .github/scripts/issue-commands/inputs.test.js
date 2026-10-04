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

const assert = require("node:assert/strict");
const test = require("node:test");
const { parseInputs } = require("./inputs.js");

// The raw workflow_dispatch inputs: GitHub passes an omitted optional input as "".
function inputs(overrides = {}) {
  return { command: "close", number: "42", users: "", title: "", requester: "alice", ...overrides };
}

test("accepts each people command with its users, trimmed and deduplicated in order", () => {
  for (const command of ["assign", "unassign", "cc", "uncc"]) {
    assert.deepEqual(
      parseInputs(inputs({ command, number: " 7 ", users: " bob, Carol-1 ,,bob,carol-1 " })),
      { command, number: 7, users: ["bob", "Carol-1"], title: null, requester: "alice" },
      command,
    );
  }
});

test("accepts close and reopen without users or a title", () => {
  for (const command of ["close", "reopen"]) {
    assert.deepEqual(parseInputs(inputs({ command })),
      { command, number: 42, users: [], title: null, requester: "alice" });
  }
  assert.deepEqual(parseInputs({ command: "close", number: "42", requester: "alice" }),
    { command: "close", number: 42, users: [], title: null, requester: "alice" });
});

test("accepts a retitle and trims the title", () => {
  assert.deepEqual(
    parseInputs(inputs({ command: "retitle", title: "  fix(ci): pin actionlint to v1.7.7 " })),
    { command: "retitle", number: 42, users: [], title: "fix(ci): pin actionlint to v1.7.7", requester: "alice" },
  );
});

test("rejects commands outside the seven agent-owned commands", () => {
  for (const command of ["", "lgtm", "cherry-pick", "Assign", "assign ", "label", undefined]) {
    assert.throws(() => parseInputs(inputs({ command })), {
      message: "command must be one of assign, unassign, cc, uncc, close, reopen, retitle",
    }, String(command));
  }
});

test("rejects numbers that are not positive integers", () => {
  for (const number of ["0", "-1", "1.5", "12a", "", "01", "12345678901", "#12", undefined]) {
    assert.throws(() => parseInputs(inputs({ number })), { message: "number must be a positive integer" }, String(number));
  }
});

test("rejects a requester that is not a GitHub login", () => {
  for (const requester of ["", "@alice", "github-actions[bot]", "bad--login", "-alice", "alice-", "a".repeat(40), "al ice", undefined]) {
    assert.throws(() => parseInputs(inputs({ requester })), { message: "requester must be a GitHub login" }, String(requester));
  }
  assert.equal(parseInputs(inputs({ requester: "a".repeat(39) })).requester, "a".repeat(39));
});

test("rejects user lists that are missing, malformed or too long", () => {
  for (const users of ["", " , ", undefined]) {
    assert.throws(() => parseInputs(inputs({ command: "assign", users })),
      { message: "users must name at least one GitHub login for assign" }, String(users));
  }
  const cases = {
    "@bob": 'user "@bob" is not a GitHub login',
    "bob carol": 'user "bob carol" is not a GitHub login',
    "bob;carol": 'user "bob;carol" is not a GitHub login',
    "nvidia/maintainers": 'user "nvidia/maintainers" is not a GitHub login',
    "bad--login": 'user "bad--login" is not a GitHub login',
    "dependabot[bot]": 'user "dependabot[bot]" is not a GitHub login',
    "bob​": 'user "bob​" is not a GitHub login',
  };
  for (const [users, message] of Object.entries(cases)) {
    assert.throws(() => parseInputs(inputs({ command: "cc", users })), { message }, users);
  }
  const ten = Array.from({ length: 10 }, (_, index) => `user${index}`);
  assert.deepEqual(parseInputs(inputs({ command: "assign", users: ten.join(",") })).users, ten);
  assert.throws(() => parseInputs(inputs({ command: "assign", users: [...ten, "user10"].join(",") })),
    { message: "users names more than 10 logins" });
  // Duplicates are dropped before the limit is counted, case-insensitively.
  assert.deepEqual(parseInputs(inputs({ command: "assign", users: [...ten, "USER0"].join(",") })).users, ten);
});

test("rejects users or a title on a command that does not take them", () => {
  for (const command of ["close", "reopen", "retitle"]) {
    assert.throws(() => parseInputs(inputs({ command, users: "bob", title: "fix: x" })),
      { message: "users is only used by assign, unassign, cc and uncc" }, command);
  }
  for (const command of ["assign", "unassign", "cc", "uncc"]) {
    assert.throws(() => parseInputs(inputs({ command, users: "bob", title: "fix: x" })),
      { message: "title is only used by retitle" }, command);
  }
  for (const command of ["close", "reopen"]) {
    assert.throws(() => parseInputs(inputs({ command, title: "fix: x" })),
      { message: "title is only used by retitle" }, command);
  }
});

test("rejects an empty, oversized or unsafe title", () => {
  for (const title of ["", "   ", undefined]) {
    assert.throws(() => parseInputs(inputs({ command: "retitle", title })), { message: "title must not be empty" }, String(title));
  }
  assert.equal(parseInputs(inputs({ command: "retitle", title: "x".repeat(256) })).title, "x".repeat(256));
  // 256 characters, not UTF-16 code units: an astral character counts once.
  assert.equal(parseInputs(inputs({ command: "retitle", title: "\u{1d54f}".repeat(256) })).title.length, 512);
  assert.throws(() => parseInputs(inputs({ command: "retitle", title: "x".repeat(257) })),
    { message: "title is longer than 256 characters" });
  for (const title of ["fix:\ttabs", "fix: two\nlines", "fix: x\n", "fix: \u0000", "fix: zero​width", "fix:  ", "fix: ‮"]) {
    assert.throws(() => parseInputs(inputs({ command: "retitle", title })),
      { message: "title contains control or format characters" }, JSON.stringify(title));
  }
});

test("rejects a title with a keyword that would close an issue, as Prow /retitle does", () => {
  for (const title of [
    "fix: thing, fixes #12", "Closes #1", "CLOSED: #3", "close #4", "resolves:#5", "Resolved   #6",
    "fix #7", "fixed other/repo#8", "feat: x (fixes other/repo#9)", "refix #10",
    // Prow's \w+/\w+ misses a repository name with "-" or ".", such as this one.
    "fixed NVIDIA/k8s-test-infra#11", "fixes some.org/some.repo#12",
  ]) {
    assert.throws(() => parseInputs(inputs({ command: "retitle", title })),
      { message: "title must not contain a keyword that closes an issue" }, title);
  }
  for (const title of [
    "fix: handle #12 in the parser", "fix(ci): retry #3", "closes 12", "fixes # 12", "closing #12",
    "fix: close the file", "resolve-#12", "fixes #12",
  ]) {
    assert.equal(parseInputs(inputs({ command: "retitle", title })).title, title, title);
  }
});
