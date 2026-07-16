"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const YAML = require("yaml");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const workflowRoot = path.join(repositoryRoot, ".github", "workflows");
const actionRoot = path.join(repositoryRoot, ".github", "actions", "repo-automation");
const sharedConcurrency = {
  group: "repository-automation-state",
  "cancel-in-progress": false,
};

function readWorkflow(name) {
  const source = fs.readFileSync(path.join(workflowRoot, name), "utf8");
  return { source, workflow: YAML.parse(source) };
}

function singleJob(workflow) {
  assert.deepEqual(Object.keys(workflow.jobs).length, 1);
  return Object.values(workflow.jobs)[0];
}

function localActionStep(job) {
  return job.steps.find((step) => step.uses === "./.github/actions/repo-automation");
}

function assertOfficialPinnedUses(source, workflow) {
  for (const job of Object.values(workflow.jobs)) {
    for (const step of job.steps ?? []) {
      if (typeof step.uses !== "string" || step.uses.startsWith("./")) continue;
      assert.match(step.uses, /^actions\/[a-z0-9-]+@[0-9a-f]{40}$/);
    }
  }
  assert.doesNotMatch(source, /uses:\s+docker:\/\/|uses:\s+[^\s]+@(main|master|v\d+)\b/);
}

function assertTrustedCheckout(job) {
  const checkout = job.steps.find((step) => step.uses?.startsWith("actions/checkout@"));
  assert.ok(checkout);
  assert.deepEqual(checkout.with, {
    ref: "${{ github.event.repository.default_branch }}",
    "persist-credentials": false,
    submodules: false,
  });
}

function assertNoPrivilegedSupplyChain(source, job) {
  assert.equal(job.steps.some((step) => step.uses?.includes("download-artifact")), false);
  assert.equal(job.steps.some((step) => step.uses?.includes("cache")), false);
  assert.equal(job.steps.some((step) => Object.hasOwn(step, "run")), false);
  assert.doesNotMatch(source, /pull_request\.head|head\.ref|workflow_run\.(head|pull_requests)|refs\/pull\//);
  assert.doesNotMatch(source, /submodules:\s+true|persist-credentials:\s+true/);
  assert.doesNotMatch(source, /\b(outputs?|artifacts?|caches?):/i);
}

test("PR metadata is a serialized trusted default-branch state writer", () => {
  const { source, workflow } = readWorkflow("pr-metadata.yml");
  assert.equal(workflow.name, "PR metadata");
  assert.deepEqual(workflow.on, {
    pull_request_target: {
      types: ["opened", "reopened", "synchronize", "edited", "ready_for_review", "converted_to_draft"],
      branches: ["main", "release-*"],
    },
  });
  assert.deepEqual(workflow.permissions, {});
  const job = singleJob(workflow);
  assert.equal(job["timeout-minutes"], 10);
  assert.deepEqual(job.concurrency, sharedConcurrency);
  assert.deepEqual(job.permissions, { contents: "read", issues: "write", "pull-requests": "write" });
  assertTrustedCheckout(job);
  assert.deepEqual(localActionStep(job).with, {
    mode: "metadata",
    "pr-number": "${{ github.event.pull_request.number }}",
    "dry-run": false,
  });
  assertOfficialPinnedUses(source, workflow);
  assertNoPrivilegedSupplyChain(source, job);
});

test("Commands is created-only, PR-only, serialized, and cannot merge", () => {
  const { source, workflow } = readWorkflow("commands.yml");
  assert.equal(workflow.name, "Commands");
  assert.deepEqual(workflow.on, { issue_comment: { types: ["created"] } });
  assert.deepEqual(workflow.permissions, {});
  const job = singleJob(workflow);
  assert.equal(job.if, "${{ github.event.issue.pull_request != null }}");
  assert.equal(job["timeout-minutes"], 10);
  assert.deepEqual(job.concurrency, sharedConcurrency);
  assert.deepEqual(job.permissions, {
    contents: "read",
    actions: "write",
    issues: "write",
    "pull-requests": "write",
  });
  assertTrustedCheckout(job);
  assert.deepEqual(localActionStep(job).with, { mode: "command", "dry-run": false });
  assert.doesNotMatch(source, /merge-evaluate|enablePullRequestAutoMerge|disablePullRequestAutoMerge/);
  assertOfficialPinnedUses(source, workflow);
  assertNoPrivilegedSupplyChain(source, job);
});

test("Review observer emits completion using only one literal colon", () => {
  const { source, workflow } = readWorkflow("review-observer.yml");
  assert.equal(workflow.name, "Review observer");
  assert.deepEqual(workflow.on, {
    pull_request_review: { types: ["submitted", "edited", "dismissed"] },
  });
  assert.deepEqual(workflow.permissions, {});
  const job = singleJob(workflow);
  assert.equal(job["timeout-minutes"], 5);
  assert.equal(job.permissions === undefined || Object.keys(job.permissions).length === 0, true);
  assert.equal(job.steps.length, 1);
  assert.deepEqual(Object.keys(job.steps[0]).sort(), ["name", "run"]);
  assert.equal(job.steps[0].run, ":");
  assert.doesNotMatch(source, /\$\{\{|\buses:|\benv:|\bwith:|\boutputs?:|checkout|artifact|cache/i);
});

test("Merge evaluator accepts only exact trusted workflow identities and safe manual defaults", () => {
  const { source, workflow } = readWorkflow("merge-evaluator.yml");
  assert.equal(workflow.name, "Merge evaluator");
  assert.deepEqual(workflow.on.workflow_run, {
    workflows: ["Review observer", "PR metadata", "Commands"],
    types: ["completed"],
  });
  assert.deepEqual(workflow.on.schedule, [{ cron: "*/15 * * * *" }]);
  assert.deepEqual(workflow.on.workflow_dispatch.inputs, {
    "pr-number": {
      description: "Pull request number; empty scans all open pull requests",
      required: false,
      type: "string",
      default: "",
    },
    "dry-run": {
      description: "Plan without mutating repository state",
      required: false,
      type: "boolean",
      default: true,
    },
  });
  assert.deepEqual(workflow.permissions, {});
  const job = singleJob(workflow);
  assert.equal(job["timeout-minutes"], 15);
  assert.deepEqual(job.concurrency, sharedConcurrency);
  assert.deepEqual(job.permissions, {
    contents: "write",
    actions: "read",
    issues: "write",
    "pull-requests": "write",
  });
  assertTrustedCheckout(job);
  assert.deepEqual(localActionStep(job).with, {
    mode: "merge-evaluate",
    "pr-number": "${{ github.event_name == 'workflow_dispatch' && inputs['pr-number'] || '' }}",
    "dry-run": "${{ github.event_name == 'workflow_dispatch' && inputs['dry-run'] || false }}",
  });
  assertOfficialPinnedUses(source, workflow);
  assertNoPrivilegedSupplyChain(source, job);

  const evaluatorSource = fs.readFileSync(path.join(actionRoot, "src", "modes", "merge-evaluate.js"), "utf8");
  for (const [name, workflowPath, event] of [
    ["Review observer", ".github/workflows/review-observer.yml", "pull_request_review"],
    ["PR metadata", ".github/workflows/pr-metadata.yml", "pull_request_target"],
    ["Commands", ".github/workflows/commands.yml", "issue_comment"],
  ]) {
    assert.match(evaluatorSource, new RegExp(`\\["${name}"[\\s\\S]*?path: "${workflowPath.replaceAll(".", "\\.")}"[\\s\\S]*?event: "${event}"`));
  }
});

test("automation CI stays unprivileged and validates every runtime mode", () => {
  const { source, workflow } = readWorkflow("automation-ci.yml");
  assert.deepEqual(workflow.permissions, {});
  const job = singleJob(workflow);
  assert.deepEqual(job.permissions, { contents: "read" });
  const checkout = job.steps.find((step) => step.uses?.startsWith("actions/checkout@"));
  assert.deepEqual(checkout.with, { "persist-credentials": false, submodules: false });
  assert.equal(job.steps.some((step) => step.uses?.startsWith("actions/setup-node@") && step.with?.["node-version"] === 24), true);
  assert.deepEqual(job.steps.filter((step) => step.run).map((step) => step.run), [
    "npm ci",
    "npm test",
    "npm run lint",
    "make actionlint",
    "npm run package",
    "git diff --exit-code -- dist",
  ]);
  assertOfficialPinnedUses(source, workflow);

  const action = YAML.parse(fs.readFileSync(path.join(actionRoot, "action.yml"), "utf8"));
  assert.equal(action.runs.using, "node24");
  assert.equal(action.runs.main, "dist/index.js");
  const sourceIndex = fs.readFileSync(path.join(actionRoot, "src", "index.js"), "utf8");
  const bundledIndex = fs.readFileSync(path.join(actionRoot, "dist", "index.js"), "utf8");
  for (const mode of ["metadata", "command", "merge-evaluate"]) {
    assert.match(sourceIndex, new RegExp(`"${mode}"`));
    assert.equal(bundledIndex.includes(mode), true, `bundle must expose ${mode}`);
  }
});
