"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const YAML = require("yaml");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const workflowRoot = path.join(repositoryRoot, ".github", "workflows");
const checkout = "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1";
const githubScript = "actions/github-script@ed597411d8f924073f98dfc5c65a23a2325f34cd";
const sharedConcurrency = {
  group: "repository-automation-state",
  "cancel-in-progress": false,
};
const activationGates = {
  metadata: "${{ vars.REPOSITORY_AUTOMATION_METADATA_ENABLED == 'true' }}",
  commands:
    "${{ vars.REPOSITORY_AUTOMATION_COMMANDS_ENABLED == 'true' && github.event.issue.pull_request != null && github.event.action == 'created' }}",
  backport:
    "${{ vars.REPOSITORY_AUTOMATION_BACKPORT_ENABLED == 'true' && needs.command.outputs.backport-requests != '[]' }}",
  reviews: "${{ vars.REPOSITORY_AUTOMATION_REVIEWS_ENABLED == 'true' }}",
  merge:
    "${{ vars.REPOSITORY_AUTOMATION_MERGE_ENABLED == 'true' && (github.event_name != 'workflow_dispatch' || github.ref_name == github.event.repository.default_branch) }}",
  reusableBackport: "${{ vars.REPOSITORY_AUTOMATION_BACKPORT_ENABLED == 'true' }}",
  mokka:
    "${{ vars.REPOSITORY_AUTOMATION_MOKKA_ENABLED == 'true' && github.ref_name == github.event.repository.default_branch }}",
};

function readWorkflow(name) {
  const source = fs.readFileSync(path.join(workflowRoot, name), "utf8");
  return { source, workflow: YAML.parse(source) };
}

function actionStep(job, mode) {
  return job.steps.find((step) => step.with?.mode === mode);
}

function assertTrustedCheckout(job, target = "control") {
  const resolver = job.steps.find((candidate) => candidate.id === "trusted");
  assert.ok(resolver, "trusted default-branch resolver is required");
  assert.equal(resolver.uses, githubScript);
  assert.equal(resolver.with["result-encoding"], "string");
  assert.match(resolver.with.script, /context\.payload\.repository\.default_branch/);
  assert.match(resolver.with.script, /github\.rest\.repos\.getBranch/);
  assert.match(resolver.with.script, /\^\[0-9a-f\]\{40\}\$/);
  const step = job.steps.find((candidate) => candidate.uses === checkout);
  assert.ok(step, "trusted checkout is required");
  assert.equal(step.with.ref, "${{ steps.trusted.outputs.result }}");
  assert.equal(step.with.path, target);
  assert.equal(step.with["persist-credentials"], false);
  assert.equal(step.with.submodules, false);
  assert.equal(step.with.lfs, false);
}

test("label synchronization is manual, additive, and trusted", () => {
  const { workflow } = readWorkflow("label-sync.yml");
  assert.equal(workflow.name, "Label synchronization");
  assert.deepEqual(workflow.on.workflow_dispatch.inputs, {
    apply: {
      description: "Create or update the declared labels",
      required: false,
      type: "boolean",
      default: false,
    },
  });
  assert.deepEqual(workflow.permissions, {});
  const job = workflow.jobs["label-sync"];
  assert.equal(job.if,
    "${{ github.ref_name == github.event.repository.default_branch }}");
  assert.deepEqual(job.permissions, { contents: "read", issues: "write" });
  assert.equal(job["timeout-minutes"], 10);
  assertTrustedCheckout(job);
  assert.deepEqual(actionStep(job, "label-sync").with, {
    mode: "label-sync",
    "control-directory": "control",
    "dry-run": "${{ !inputs.apply }}",
  });
});

test("metadata and command workflows use exact trusted code", () => {
  const metadata = readWorkflow("pr-metadata.yml").workflow;
  assert.deepEqual(metadata.on.pull_request_target, {
    types: ["opened", "reopened", "synchronize", "edited", "ready_for_review", "converted_to_draft"],
    branches: ["main", "release-*"],
  });
  assert.deepEqual(metadata.permissions, {});
  assert.equal(metadata.jobs.metadata.if, activationGates.metadata);
  assert.deepEqual(metadata.jobs.metadata.permissions, {
    contents: "read", issues: "write", "pull-requests": "write",
  });
  assert.deepEqual(metadata.jobs.metadata.concurrency, sharedConcurrency);
  assertTrustedCheckout(metadata.jobs.metadata);
  assert.equal(actionStep(metadata.jobs.metadata, "metadata").with["control-directory"], "control");

  const commands = readWorkflow("commands.yml").workflow;
  assert.deepEqual(commands.on, { issue_comment: { types: ["created", "edited", "deleted"] } });
  assert.deepEqual(commands.permissions, {});
  assert.equal(commands.jobs.command.if, activationGates.commands);
  assert.deepEqual(commands.jobs.command.permissions, {
    actions: "write", contents: "read", issues: "write", "pull-requests": "write",
  });
  assert.match(commands.jobs.command.if, /github\.event\.action == 'created'/);
  assert.deepEqual(commands.jobs.command.concurrency, sharedConcurrency);
  assertTrustedCheckout(commands.jobs.command);
  assert.equal(actionStep(commands.jobs.command, "command").with["control-directory"], "control");
  assert.equal(commands.jobs.command.outputs["backport-requests"], "${{ steps.command.outputs.backport-requests }}");
  assert.deepEqual(commands.jobs.backport.permissions, { contents: "write", "pull-requests": "write" });
  assert.equal(commands.jobs.backport.if, activationGates.backport);
  assert.equal(commands.jobs.backport.uses, "./.github/workflows/backport.yml");
  assert.equal(commands.jobs.backport.strategy.matrix.request, "${{ fromJSON(needs.command.outputs.backport-requests) }}");
});

test("review observation and merge evaluation use bounded events", () => {
  const observer = readWorkflow("review-observer.yml").workflow;
  assert.deepEqual(observer.on, {
    pull_request_review: { types: ["submitted", "edited", "dismissed"] },
  });
  assert.deepEqual(observer.permissions, {});
  assert.equal(observer.jobs.observe.if, activationGates.reviews);
  assert.deepEqual(observer.jobs.observe.permissions, {});
  assert.deepEqual(observer.jobs.observe.steps, [{ name: "Signal review change", run: ":" }]);

  const evaluator = readWorkflow("merge-evaluate.yml").workflow;
  assert.deepEqual(evaluator.on.workflow_run, {
    workflows: ["Review observer", "PR metadata", "Commands"],
    types: ["completed"],
  });
  assert.deepEqual(evaluator.on.schedule, [{ cron: "*/15 * * * *" }]);
  assert.deepEqual(evaluator.permissions, {});
  assert.equal(evaluator.jobs.evaluate.if, activationGates.merge);
  assert.deepEqual(evaluator.jobs.evaluate.permissions, {
    actions: "read", checks: "write", contents: "read", issues: "write", "pull-requests": "write",
  });
  assert.deepEqual(evaluator.jobs.evaluate.concurrency, sharedConcurrency);
  assertTrustedCheckout(evaluator.jobs.evaluate);
  assert.equal(actionStep(evaluator.jobs.evaluate, "merge-evaluate").with["control-directory"], "control");
});

test("generic backport is reusable only and isolates its target checkout", () => {
  const workflow = readWorkflow("backport.yml").workflow;
  assert.deepEqual(Object.keys(workflow.on), ["workflow_call"]);
  assert.deepEqual(workflow.permissions, {});
  const job = workflow.jobs.backport;
  assert.equal(job.if, activationGates.reusableBackport);
  assert.deepEqual(job.permissions, { contents: "write", "pull-requests": "write" });
  assertTrustedCheckout(job);
  const target = job.steps.filter((step) => step.uses === checkout)[1];
  assert.equal(target.with.ref, "${{ inputs.target-branch }}");
  assert.equal(target.with.path, "target");
  assert.equal(target.with["persist-credentials"], true);
  assert.equal(target.with.submodules, false);
  assert.equal(target.with.lfs, false);
  assert.equal(actionStep(job, "backport").with["control-directory"], "control");
  assert.equal(actionStep(job, "backport").with["working-directory"], "target");
});

test("Mokka dispatch stays disabled until its repository variable is enabled", () => {
  const workflow = readWorkflow("mokka-cherry-pick.yml").workflow;
  assert.equal(workflow.jobs["cherry-pick"].if, activationGates.mokka);
  assertTrustedCheckout(workflow.jobs["cherry-pick"]);
  assert.deepEqual(actionStep(workflow.jobs["cherry-pick"], "mokka-cherry-pick").with, {
    mode: "mokka-cherry-pick",
    pull_request_number: "${{ inputs.pull_request_number }}",
    source_sha: "${{ inputs.source_sha }}",
    "target-branch": "${{ inputs.target_branch }}",
    action_id: "${{ inputs.action_id }}",
    "working-directory": "target",
    "dry-run": "false",
  });
});
