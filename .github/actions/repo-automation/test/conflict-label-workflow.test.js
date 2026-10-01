"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const YAML = require("yaml");

function metadataWorkflow() {
  return YAML.parse(fs.readFileSync(path.resolve(__dirname, "../../../workflows/pr-metadata.yml"), "utf8"));
}

test("PR metadata repairs conflicts after PR changes, supported base pushes, and scheduled scans", () => {
  const workflow = metadataWorkflow();
  assert.deepEqual(workflow.on.pull_request_target.branches, ["main", "release-*"]);
  for (const action of ["opened", "reopened", "synchronize", "edited", "ready_for_review", "converted_to_draft"]) {
    assert.ok(workflow.on.pull_request_target.types.includes(action));
  }
  assert.ok(workflow.on.push, "supported base pushes must trigger conflict repair");
  assert.deepEqual(workflow.on.push.branches, ["main", "release-*"]);
  assert.deepEqual(workflow.on.schedule, [{ cron: "17 * * * *" }],
    "full scans must fit the hourly API budget and avoid the busy start of the hour");
  assert.equal(workflow.on.pull_request, undefined);
});

test("conflict scans use the trusted checkout, metadata flag, and shared write queue", () => {
  const workflow = metadataWorkflow();
  assert.deepEqual(workflow.permissions, {});
  const job = workflow.jobs.metadata;
  assert.equal(job.if, "${{ vars.REPOSITORY_AUTOMATION_METADATA_ENABLED == 'true' }}");
  assert.deepEqual(job.concurrency, { group: "repository-automation-state", "cancel-in-progress": false });
  const resolver = job.steps.find((step) => step.id === "trusted");
  assert.ok(resolver);
  assert.match(resolver.uses, /^actions\/github-script@[0-9a-f]{40}$/);
  assert.match(resolver.with.script, /context\.payload\.repository\.default_branch/);
  const checkout = job.steps.find((step) => /^actions\/checkout@[0-9a-f]{40}$/.test(step.uses ?? ""));
  assert.ok(checkout);
  assert.equal(checkout.with.ref, "${{ steps.trusted.outputs.result }}");
  assert.equal(checkout.with.path, "control");
  assert.equal(checkout.with["persist-credentials"], false);
  assert.equal(checkout.with.submodules, false);
  assert.equal(checkout.with.lfs, false);
  const conflict = job.steps.find((step) => step.with?.mode === "conflict-labels");
  assert.ok(conflict, "the workflow must run conflict label reconciliation");
  assert.equal(conflict.uses, "./control/.github/actions/repo-automation");
  assert.equal(conflict.with["control-directory"], "control");
  assert.equal(conflict.with["dry-run"], "false");
  const metadata = job.steps.find((step) => step.with?.mode === "metadata");
  assert.ok(metadata);
  assert.ok(job.steps.indexOf(conflict) < job.steps.indexOf(metadata));
  assert.equal(metadata.if, "${{ github.event_name == 'pull_request_target' }}",
    "base pushes and scheduled scans must not run ordinary metadata writes");
});

test("metadata label scan runs only for base pushes and scheduled events", () => {
  const job = metadataWorkflow().jobs.metadata;
  const scan = job.steps.find((step) => step.with?.mode === "metadata-labels");
  assert.ok(scan, "the workflow must reconcile metadata labels for older open PRs");
  assert.equal(scan.if, "${{ github.event_name != 'pull_request_target' }}");
  assert.equal(scan.uses, "./control/.github/actions/repo-automation");
  assert.equal(scan.with["control-directory"], "control");
  assert.equal(scan.with["dry-run"], "false");
  assert.equal(scan.with["policy-revision"], "${{ steps.trusted.outputs.result }}");
  const metadata = job.steps.find((step) => step.with?.mode === "metadata");
  assert.equal(metadata.if, "${{ github.event_name == 'pull_request_target' }}");
});
