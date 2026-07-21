import { spawnSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";

import { classifyEdgeAncestry, planDevelopmentPublication, validateDefaultBranch } from "./release-state.mjs";

const SHA_RE = /^[0-9a-f]{40}$/;

function fail(message) {
  throw new TypeError(message);
}

function main(path, releaseSha) {
  const defaultBranch = validateDefaultBranch(process.env.DEFAULT_BRANCH);
  if (path === "--validate-branch") return;
  if (typeof path !== "string" || path.length === 0) fail("development state path is required");
  if (typeof releaseSha !== "string" || !SHA_RE.test(releaseSha)) fail("release SHA is invalid");
  const defaultBranchRef = `refs/remotes/origin/${defaultBranch}`;
  const state = JSON.parse(readFileSync(path, "utf8"));
  const ancestor = (left, right) => {
    const result = spawnSync("git", ["merge-base", "--is-ancestor", left, right], { stdio: "ignore" });
    if (result.status === 0) return true;
    if (result.status === 1) return false;
    fail("git ancestry verification failed");
  };
  if (!ancestor(releaseSha, defaultBranchRef)) fail("publication target is not on the default branch");
  state.edgeRelation = classifyEdgeAncestry({ edgeSha: state.edge?.sourceRevision ?? null, releaseSha }, ancestor);
  planDevelopmentPublication(state);
  writeFileSync(path, JSON.stringify(state));
}

try {
  main(process.argv[2], process.env.RELEASE_SHA);
} catch (error) {
  process.stderr.write(`release-edge-state: ${error instanceof Error ? error.message : "failed"}\n`);
  process.exitCode = 1;
}
