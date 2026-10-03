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
// GitHub keeps one pending run per concurrency group, so each concern gets its own group.
const concurrency = {
  commands: {
    group: "repository-automation-pr-${{ github.event.issue.number }}",
    "cancel-in-progress": false,
  },
  metadata: {
    group: "${{ github.event_name == 'pull_request_target' && format('repository-automation-pr-{0}', github.event.pull_request.number) || github.event_name == 'workflow_dispatch' && format('repository-automation-label-scan-{0}', inputs.request_id) || 'repository-automation-scan' }}",
    "cancel-in-progress": false,
  },
  merge: { group: "repository-automation-merge-evaluate", "cancel-in-progress": false },
  policyLabels: { group: "repository-automation-policy-labels", "cancel-in-progress": false },
  labelSync: { group: "repository-automation-label-sync", "cancel-in-progress": false },
};
const activationGates = {
  metadata:
    "${{ vars.REPOSITORY_AUTOMATION_METADATA_ENABLED == 'true' && (github.event_name != 'workflow_dispatch' || (github.repository == 'NVIDIA/k8s-test-infra' && github.repository_id == '733665780' && github.ref == 'refs/heads/main' && github.sha == inputs.workflow_commit_sha && github.workflow_sha == inputs.workflow_commit_sha)) }}",
  commands:
    "${{ vars.REPOSITORY_AUTOMATION_COMMANDS_ENABLED == 'true' && github.event.issue.pull_request != null && github.event.action == 'created' && github.event.comment.user.type == 'User' && contains(github.event.comment.body, '/') }}",
  reviews: "${{ vars.REPOSITORY_AUTOMATION_REVIEWS_ENABLED == 'true' }}",
  merge:
    "${{ vars.REPOSITORY_AUTOMATION_MERGE_ENABLED == 'true' && (github.event_name != 'workflow_dispatch' || github.ref_name == github.event.repository.default_branch) }}",
  policyLabels:
    "${{ vars.REPOSITORY_AUTOMATION_POLICY_LABELS_ENABLED == 'true' && (github.event_name != 'workflow_dispatch' || github.ref_name == github.event.repository.default_branch) }}",
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
  assert.deepEqual(job.concurrency, concurrency.labelSync);
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
  assert.deepEqual(metadata.jobs.metadata.concurrency, concurrency.metadata);
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
  assert.deepEqual(commands.jobs.command.concurrency, concurrency.commands);
  assertTrustedCheckout(commands.jobs.command);
  assert.equal(actionStep(commands.jobs.command, "command").with["control-directory"], "control");
  // The command job is the only job: nothing in this workflow writes repository contents.
  assert.deepEqual(Object.keys(commands.jobs), ["command"]);
  assert.equal(commands.jobs.command.outputs, undefined);
});

test("review observation and native merge evaluation use bounded events and trusted code", () => {
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
    actions: "read", checks: "write", contents: "write", issues: "write", "pull-requests": "write",
  });
  assert.deepEqual(evaluator.jobs.evaluate.concurrency, concurrency.merge);
  assertTrustedCheckout(evaluator.jobs.evaluate);
  assert.deepEqual(evaluator.jobs.evaluate.steps.map((step) => step.uses), [
    githubScript, checkout, "./control/.github/actions/repo-automation",
  ]);
  const evaluation = actionStep(evaluator.jobs.evaluate, "merge-evaluate");
  assert.equal(evaluation.with["control-directory"], "control");
  assert.equal(evaluation.with["policy-revision"], "${{ steps.trusted.outputs.result }}");
  assert.deepEqual(evaluation.env, { GITHUB_TOKEN: "${{ github.token }}" });
});

test("labels-only policy evaluation is a separate job with label permissions and its own gate", () => {
  const evaluator = readWorkflow("merge-evaluate.yml").workflow;
  assert.deepEqual(Object.keys(evaluator.jobs), ["evaluate", "policy-labels"]);
  const job = evaluator.jobs["policy-labels"];
  assert.equal(job.if, activationGates.policyLabels);
  assert.deepEqual(job.permissions, {
    actions: "read", contents: "read", issues: "write", "pull-requests": "write",
  });
  assert.deepEqual(job.concurrency, concurrency.policyLabels);
  assert.equal(job["timeout-minutes"], 15);
  assertTrustedCheckout(job);
  assert.deepEqual(job.steps.map((step) => step.uses), [
    githubScript, checkout, "./control/.github/actions/repo-automation",
  ]);
  const labels = actionStep(job, "policy-labels");
  assert.deepEqual(labels.with, {
    mode: "policy-labels",
    "pr-number": "${{ inputs.pr_number || '' }}",
    "control-directory": "control",
    "policy-revision": "${{ steps.trusted.outputs.result }}",
    "dry-run": "${{ github.event_name == 'workflow_dispatch' && inputs.dry_run || false }}",
  });
  assert.deepEqual(labels.env, { GITHUB_TOKEN: "${{ github.token }}" });
  assert.equal(evaluator.jobs.evaluate.if, activationGates.merge);
});

test("no workflow queues behind the old repository-wide automation group", () => {
  const sharing = fs.readdirSync(workflowRoot)
    .filter((name) => readWorkflow(name).source.includes("repository-automation-state"));
  assert.deepEqual(sharing, []);
});
