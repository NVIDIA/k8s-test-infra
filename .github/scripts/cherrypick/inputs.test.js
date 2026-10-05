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

test("accepts a merged PR number and one release branch", () => {
  assert.deepEqual(parseInputs("973", "release-0.4"), { prNumber: 973, branches: ["release-0.4"] });
});

test("accepts several branches, trims spaces and drops duplicates in order", () => {
  assert.deepEqual(
    parseInputs(" 12 ", "release-0.4, release-0.3,release-0.4,,release-1.2.3"),
    { prNumber: 12, branches: ["release-0.4", "release-0.3", "release-1.2.3"] },
  );
});

test("rejects pull request numbers that are not positive integers", () => {
  for (const value of ["0", "-1", "1.5", "12a", "", "01", "12345678901", undefined]) {
    assert.throws(() => parseInputs(value, "release-0.4"), { message: "pr_number must be a positive integer" });
  }
});

test("rejects branches outside release-X.Y and branch-name tricks", () => {
  const cases = {
    main: 'target branch "main" is not a release-X.Y branch',
    "release-0.4/hotfix": 'target branch "release-0.4/hotfix" is not a release-X.Y branch',
    "release-x": 'target branch "release-x" is not a release-X.Y branch',
    "release-0.4;rm -rf /": 'target branch "release-0.4;rm -rf /" is not a release-X.Y branch',
    "release-0.4 release-0.3": 'target branch "release-0.4 release-0.3" is not a release-X.Y branch',
    "-release-0.4": 'target branch "-release-0.4" is not a release-X.Y branch',
  };
  for (const [branch, message] of Object.entries(cases)) {
    assert.throws(() => parseInputs("1", branch), { message });
  }
});

test("rejects an empty branch list and more than five branches", () => {
  assert.throws(() => parseInputs("1", " , "), { message: "target_branches must name at least one release branch" });
  assert.throws(
    () => parseInputs("1", "release-0.1,release-0.2,release-0.3,release-0.4,release-0.5,release-0.6"),
    { message: "target_branches names more than 5 branches" },
  );
  assert.throws(() => parseInputs("1", undefined), {
    message: "target_branches must be a comma-separated list of release branches",
  });
});
