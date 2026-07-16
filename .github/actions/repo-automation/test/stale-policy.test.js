"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const YAML = require("yaml");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const workflowPath = path.join(repositoryRoot, ".github", "workflows", "stale.yaml");

function loadStaleWorkflow() {
  const source = fs.readFileSync(workflowPath, "utf8");
  return { source, workflow: YAML.parse(source) };
}

test("stale lifecycle is pinned, serialized, bounded, and dry-run only", () => {
  const { source, workflow } = loadStaleWorkflow();
  const job = workflow.jobs.stale;
  const step = job.steps.find(({ uses }) => uses?.startsWith("actions/stale@"));

  assert.equal(job["timeout-minutes"], 15);
  assert.deepEqual(job.concurrency, {
    group: "stale-lifecycle",
    "cancel-in-progress": false,
  });
  assert.equal(step.uses, "actions/stale@1e223db275d687790206a7acac4d1a11bd6fe629");
  assert.match(source, /actions\/stale@1e223db275d687790206a7acac4d1a11bd6fe629 # v10/);
  assert.equal(step.with["debug-only"], true);
  assert.equal(step.with["delete-branch"], false);
  assert.match(source, /github\.com\/NVIDIA\/gpu-operator\/blob\/main\/\.github\/workflows\/stale\.yaml/);
});

test("stale lifecycle uses the approved issue and pull request policy", () => {
  const { workflow } = loadStaleWorkflow();
  const policy = workflow.jobs.stale.steps.find(({ uses }) => uses?.startsWith("actions/stale@")).with;

  assert.deepEqual(
    {
      "days-before-issue-stale": policy["days-before-issue-stale"],
      "days-before-issue-close": policy["days-before-issue-close"],
      "days-before-pr-stale": policy["days-before-pr-stale"],
      "days-before-pr-close": policy["days-before-pr-close"],
      "stale-issue-label": policy["stale-issue-label"],
      "stale-pr-label": policy["stale-pr-label"],
      "exempt-issue-labels": policy["exempt-issue-labels"],
      "exempt-pr-labels": policy["exempt-pr-labels"],
      "remove-stale-when-updated": policy["remove-stale-when-updated"],
      "close-issue-reason": policy["close-issue-reason"],
      "operations-per-run": policy["operations-per-run"],
      ascending: policy.ascending,
    },
    {
      "days-before-issue-stale": 90,
      "days-before-issue-close": 30,
      "days-before-pr-stale": 30,
      "days-before-pr-close": 14,
      "stale-issue-label": "lifecycle/stale",
      "stale-pr-label": "lifecycle/stale",
      "exempt-issue-labels": "lifecycle/frozen,kind/feature",
      "exempt-pr-labels": "lifecycle/frozen",
      "remove-stale-when-updated": true,
      "close-issue-reason": "not_planned",
      "operations-per-run": 200,
      ascending: true,
    },
  );
});

test("stale lifecycle messages state the exact inactivity windows", () => {
  const { workflow } = loadStaleWorkflow();
  const policy = workflow.jobs.stale.steps.find(({ uses }) => uses?.startsWith("actions/stale@")).with;

  assert.match(policy["stale-issue-message"], /inactive for 90 days[\s\S]*close in 30 days/);
  assert.match(policy["close-issue-message"], /30 days after it was marked `lifecycle\/stale`/);
  assert.match(policy["stale-pr-message"], /inactive for 30 days[\s\S]*close in 14 days/);
  assert.match(policy["close-pr-message"], /14 days after it was marked `lifecycle\/stale`/);
  for (const messageName of [
    "stale-issue-message",
    "close-issue-message",
    "stale-pr-message",
    "close-pr-message",
  ]) {
    assert.doesNotMatch(policy[messageName], /delete|lock|reopen/i);
  }
});
