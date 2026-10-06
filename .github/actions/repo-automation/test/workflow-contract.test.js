"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const YAML = require("yaml");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const workflowRoot = path.join(repositoryRoot, ".github", "workflows");
const checkout = "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1";
const githubScript = "actions/github-script@3a2844b7e9c422d3c10d287c895573f7108da1b3";
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
  cherryPick: "${{ vars.REPOSITORY_AUTOMATION_CHERRY_PICK_ENABLED == 'true' && github.ref == 'refs/heads/main' }}",
  issueCommands: "${{ vars.REPOSITORY_AUTOMATION_ISSUE_COMMANDS_ENABLED == 'true' && github.ref == 'refs/heads/main' }}",
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

test("cherry-pick runs only when enabled from main, one run per pull request and branch set", () => {
  const { workflow } = readWorkflow("cherrypick.yml");
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  assert.deepEqual(Object.keys(workflow.on.workflow_dispatch.inputs), ["pr_number", "target_branches"]);
  assert.deepEqual(workflow.permissions, {});
  assert.deepEqual(Object.keys(workflow.jobs), ["backport"]);
  const job = workflow.jobs.backport;
  assert.equal(job.if, activationGates.cherryPick);
  assert.deepEqual(job.permissions, { contents: "write", "pull-requests": "write", issues: "write" });
  // GitHub keeps one pending run per group: a PR-only key would cancel a pending
  // dispatch for a different branch set.
  assert.deepEqual(job.concurrency, {
    group: "cherry-pick-${{ inputs.pr_number }}-${{ inputs.target_branches }}",
    "cancel-in-progress": false,
  });
  const checkoutStep = job.steps.find((step) => step.uses?.startsWith("actions/checkout@"));
  assert.equal(checkoutStep.uses, checkout);
  assert.equal(checkoutStep.with["fetch-depth"], 0);
  const validate = job.steps.findIndex((step) => step.id === "inputs");
  const backport = job.steps.findIndex((step) => step.name === "Backport to release branches");
  assert.ok(validate >= 0 && validate < backport, "inputs are validated before the backport step");
  assert.deepEqual(job.steps[backport].env, {
    PR_NUMBER: "${{ steps.inputs.outputs.pr_number }}",
    BRANCHES_JSON: "${{ steps.inputs.outputs.branches }}",
  });
});

test("issue commands run only when enabled from main, and no dispatch waits behind another", () => {
  const { workflow } = readWorkflow("issue-commands.yml");
  assert.equal(workflow.name, "Issue commands");
  assert.equal(workflow["run-name"], "Issue command ${{ inputs.command }} #${{ inputs.number }} by ${{ inputs.requester }}");
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  // The Mokka agent checks this interface: five string inputs, and it always
  // sends all five, with "" for the ones a command does not use.
  assert.deepEqual(workflow.on.workflow_dispatch.inputs, {
    command: {
      description: "Command to apply: assign, unassign, cc, uncc, close, reopen or retitle",
      type: "string",
      required: true,
    },
    number: { description: "Issue or pull request number", type: "string", required: true },
    users: {
      description: "Comma-separated GitHub logins, for assign, unassign, cc and uncc",
      type: "string",
      required: false,
    },
    title: { description: "New title, for retitle", type: "string", required: false },
    requester: { description: "Login of the user whose comment asked for the command", type: "string", required: true },
  });
  assert.deepEqual(workflow.permissions, {});
  assert.deepEqual(Object.keys(workflow.jobs), ["issue-command"]);
  const job = workflow.jobs["issue-command"];
  assert.equal(job.if, activationGates.issueCommands);
  assert.deepEqual(job.permissions, { contents: "read", issues: "write", "pull-requests": "write" });
  assert.equal(job["timeout-minutes"], 5);
  // GitHub keeps one pending run per group and cancels the older one, so any
  // group two dispatches can share would drop a command: each run is its own group.
  assert.deepEqual(job.concurrency, { group: "issue-commands-${{ github.run_id }}", "cancel-in-progress": false });
  assert.deepEqual(job.steps.map((step) => step.uses), [checkout, githubScript, githubScript]);
  assert.deepEqual(job.steps[0].with, { "persist-credentials": false });
  const [, validate, apply] = job.steps;
  assert.equal(validate.id, "inputs");
  assert.deepEqual(validate.env, {
    INPUT_COMMAND: "${{ inputs.command }}",
    INPUT_NUMBER: "${{ inputs.number }}",
    INPUT_USERS: "${{ inputs.users }}",
    INPUT_TITLE: "${{ inputs.title }}",
    INPUT_REQUESTER: "${{ inputs.requester }}",
  });
  // The glue is pinned whole: the request run.js acts on comes only from
  // validate(), which calls parseInputs and answers an invalid dispatch.
  assert.equal(validate.with.script, [
    "const { validate } = require('./.github/scripts/issue-commands/run.js');",
    "const request = await validate({",
    "  github,",
    "  context,",
    "  core,",
    "  inputs: {",
    "    command: process.env.INPUT_COMMAND,",
    "    number: process.env.INPUT_NUMBER,",
    "    users: process.env.INPUT_USERS,",
    "    title: process.env.INPUT_TITLE,",
    "    requester: process.env.INPUT_REQUESTER,",
    "  },",
    "});",
    "if (request !== null) core.setOutput('request', JSON.stringify(request));",
    "",
  ].join("\n"));
  assert.deepEqual(apply.env, { REQUEST_JSON: "${{ steps.inputs.outputs.request }}" });
  assert.equal(apply.with.script, [
    "const run = require('./.github/scripts/issue-commands/run.js');",
    "return await run({ github, context, core, request: JSON.parse(process.env.REQUEST_JSON) });",
    "",
  ].join("\n"));
  // Inputs reach the scripts only through the environment, never as script text.
  for (const step of [validate, apply]) assert.doesNotMatch(step.with.script, /\$\{\{/);
});
