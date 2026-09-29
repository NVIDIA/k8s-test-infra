"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
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
    "${{ vars.REPOSITORY_AUTOMATION_MOKKA_ENABLED == 'true' && github.ref == 'refs/heads/main' && github.sha == inputs.workflow_commit_sha }}",
};

const reviewedAutomationFiles = [
  ".github/workflows/mokka-cherry-pick.yml",
  ".github/actions/repo-automation/action.yml",
  ".github/actions/repo-automation/dist/index.js",
];

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

test("Mokka dispatch checks reviewed automation before it checks out the target", () => {
  const workflow = readWorkflow("mokka-cherry-pick.yml").workflow;
  assert.deepEqual(Object.keys(workflow.on), ["workflow_dispatch"]);
  const inputs = workflow.on.workflow_dispatch.inputs;
  assert.deepEqual(Object.keys(inputs).sort(), [
    "action_id", "pull_request_number", "source_sha", "target_branch", "workflow_commit_sha",
  ]);
  for (const input of Object.values(inputs)) {
    assert.equal(input.required, true);
    assert.equal(input.type, "string");
  }
  const job = workflow.jobs["cherry-pick"];
  assert.equal(job.if, activationGates.mokka);
  assert.deepEqual(job.steps.map((step) => step.name), [
    "Validate reviewed automation ref",
    "Check out trusted automation",
    "Check out reviewed automation",
    "Verify reviewed automation",
    "Check out validated target",
    "Cherry-pick merged source pull request",
  ]);
  const validateRef = job.steps[0];
  assert.equal(validateRef.shell, "bash");
  assert.deepEqual(validateRef.env, {
    REVIEWED_SHA: "${{ vars.REPOSITORY_AUTOMATION_MOKKA_REVIEWED_SHA }}",
  });
  assert.match(validateRef.run, /\^\[0-9a-f\]\{40\}\$/);
  const trustedCheckout = job.steps.find((candidate) => candidate.name === "Check out trusted automation");
  assert.ok(trustedCheckout, "trusted default-branch automation checkout is required");
  assert.equal(trustedCheckout.uses, checkout);
  assert.deepEqual(trustedCheckout.with, {
    ref: "${{ github.sha }}",
    path: "control",
    "persist-credentials": false,
    "fetch-depth": 1,
    submodules: false,
    lfs: false,
  });
  const reviewedCheckout = job.steps[2];
  assert.equal(reviewedCheckout.uses, checkout);
  assert.deepEqual(reviewedCheckout.with, {
    ref: "${{ vars.REPOSITORY_AUTOMATION_MOKKA_REVIEWED_SHA }}",
    path: "reviewed",
    "persist-credentials": false,
    "fetch-depth": 1,
    submodules: false,
    lfs: false,
  });
  assert.equal(job.steps[3].shell, "bash");
  assert.equal(job.steps.some((candidate) => candidate.id === "trusted"), false);
  assert.deepEqual(actionStep(job, "mokka-cherry-pick").with, {
    mode: "mokka-cherry-pick",
    pull_request_number: "${{ inputs.pull_request_number }}",
    source_sha: "${{ inputs.source_sha }}",
    "target-branch": "${{ inputs.target_branch }}",
    action_id: "${{ inputs.action_id }}",
    "working-directory": "target",
    "dry-run": "false",
  });
});

test("Mokka accepts a later main commit only when reviewed automation bytes match", (t) => {
  const workflow = readWorkflow("mokka-cherry-pick.yml").workflow;
  const verify = workflow.jobs["cherry-pick"].steps.find(
    (step) => step.name === "Verify reviewed automation",
  );
  assert.ok(verify);
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "mokka-reviewed-automation-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  for (const file of reviewedAutomationFiles) {
    for (const checkoutPath of ["control", "reviewed"]) {
      const filename = path.join(root, checkoutPath, file);
      fs.mkdirSync(path.dirname(filename), { recursive: true });
      fs.writeFileSync(filename, `approved ${file}\n`);
    }
  }
  fs.writeFileSync(path.join(root, "control", "README.md"), "a later main commit\n");
  const execute = () => spawnSync("bash", ["-c", verify.run], { cwd: root, encoding: "utf8" });
  assert.equal(execute().status, 0, "an unrelated main change must not block dispatch");
  for (const file of reviewedAutomationFiles) {
    const filename = path.join(root, "control", file);
    fs.writeFileSync(filename, `changed ${file}\n`);
    const result = execute();
    assert.notEqual(result.status, 0, `${file}: changed automation must block dispatch`);
    fs.writeFileSync(filename, `approved ${file}\n`);
  }
  fs.rmSync(path.join(root, "reviewed", reviewedAutomationFiles[0]));
  assert.notEqual(execute().status, 0, "missing reviewed automation must block dispatch");
});

test("Mokka rejects malformed reviewed SHAs before checkout", () => {
  const workflow = readWorkflow("mokka-cherry-pick.yml").workflow;
  const validate = workflow.jobs["cherry-pick"].steps.find(
    (step) => step.name === "Validate reviewed automation ref",
  );
  assert.ok(validate);
  const execute = (sha) => spawnSync("bash", ["-c", validate.run], {
    env: { ...process.env, REVIEWED_SHA: sha },
    encoding: "utf8",
  });
  assert.equal(execute("a".repeat(40)).status, 0);
  for (const sha of ["", "a".repeat(39), "A".repeat(40), "a".repeat(39) + "!", "$(echo bad)"]) {
    assert.notEqual(execute(sha).status, 0, `${sha}: malformed SHA must block dispatch`);
  }
});
