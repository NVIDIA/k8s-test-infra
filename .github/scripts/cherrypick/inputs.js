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

// Same branch shape the nvidia-container-toolkit cherry-pick labels accept.
const BRANCH = /^release-\d+\.\d+(?:\.\d+)?$/;
const PR_NUMBER = /^[1-9][0-9]{0,9}$/;
const MAX_BRANCHES = 5;

function parseInputs(prNumber, targetBranches) {
  if (typeof prNumber !== "string" || !PR_NUMBER.test(prNumber.trim())) {
    throw new Error("pr_number must be a positive integer");
  }
  if (typeof targetBranches !== "string") {
    throw new Error("target_branches must be a comma-separated list of release branches");
  }
  const branches = [];
  for (const raw of targetBranches.split(",")) {
    const branch = raw.trim();
    if (branch === "") continue;
    if (!BRANCH.test(branch)) {
      throw new Error(`target branch ${JSON.stringify(branch)} is not a release-X.Y branch`);
    }
    if (!branches.includes(branch)) branches.push(branch);
  }
  if (branches.length === 0) {
    throw new Error("target_branches must name at least one release branch");
  }
  if (branches.length > MAX_BRANCHES) {
    throw new Error(`target_branches names more than ${MAX_BRANCHES} branches`);
  }
  return { prNumber: Number(prNumber.trim()), branches };
}

module.exports = { parseInputs };
