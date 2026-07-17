"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const YAML = require("yaml");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const workflowRoot = path.join(repositoryRoot, ".github", "workflows");
const HELM_UNITTEST_COMMIT = "6f82a998e0b5461762ca959f87f5dd344af5e4eb";
const LEGACY_HELM_PUBLISH_IF = "${{ github.event.workflow_run.conclusion == 'success' && github.event.workflow_run.head_repository.full_name == github.repository && github.event.workflow_run.head_branch == 'main' && github.event.workflow_run.event == 'push' }}";

const REUSABLE_CALL_EXEMPTIONS_V1 = new Set([
  "basic-checks.yaml/variables",
  "basic-checks.yaml/golang",
  "basic-checks.yaml/code-scanning",
  "ci.yaml/basic",
  "ci.yaml/nvml-mock-e2e",
]);

// Extend this exact, versioned allowlist when Plan 5 adds release.yml.
const WRITE_PERMISSION_ALLOWLIST_V1 = Object.freeze({
  "basic-checks.yaml/code-scanning": ["security-events"],
  "ci.yaml/basic": ["security-events"],
  "code_scanning.yaml/analyze": ["security-events"],
  "commands.yml/commands": ["actions", "issues", "pull-requests"],
  "helm-publish.yaml/publish": ["id-token", "packages"],
  "label-sync.yml/label-sync": ["issues"],
  "merge-evaluator.yml/evaluate": ["contents", "issues", "pull-requests"],
  "nvml-mock-publish.yaml/publish": ["id-token", "packages"],
  "pr-metadata.yml/metadata": ["issues", "pull-requests"],
  "release.yml/publish-chart": ["artifact-metadata", "attestations", "contents", "id-token", "packages"],
  "release.yml/publish-image": ["artifact-metadata", "attestations", "contents", "id-token", "packages"],
  "release.yml/release-please": ["contents", "issues", "pull-requests"],
  "scorecard.yaml/analysis": ["id-token", "security-events"],
  "stale.yaml/stale": ["issues", "pull-requests"],
  "trigger-pages-deploy.yaml/trigger": ["actions"],
});

function workflowFiles() {
  return fs.readdirSync(workflowRoot)
    .filter((name) => name.endsWith(".yml") || name.endsWith(".yaml"))
    .sort();
}

function readWorkflow(name) {
  const source = fs.readFileSync(path.join(workflowRoot, name), "utf8");
  return { name, source, workflow: YAML.parse(source) };
}

function externalUsesLines(source) {
  return source.split("\n")
    .map((line) => line.match(/^\s*(?:-\s+)?uses:\s+([^\s#]+)(?:\s+(#.*))?$/))
    .filter((match) => match && !match[1].startsWith("./"));
}

function hasPrivilegedTrigger(workflow) {
  return ["pull_request_target", "issue_comment", "workflow_run"]
    .some((event) => Object.hasOwn(workflow.on ?? {}, event));
}

function assertImmutableHelmPluginInstall(run) {
  assert.equal(typeof run, "string");
  const installCommands = run.split("\n").filter((line) => line.includes("helm plugin install"));
  assert.deepEqual(installCommands.map((line) => line.trim()), [
    `helm plugin install https://github.com/helm-unittest/helm-unittest.git --version ${HELM_UNITTEST_COMMIT}`,
  ]);
  assert.doesNotMatch(run, /--verify=false|--version\s+(?:v?\d|main|master)\b/);
}

function assertTrustedLegacyHelmPublisher(workflow) {
  assert.deepEqual(workflow.on.workflow_run, {
    workflows: ["helm"],
    types: ["completed"],
    branches: ["main"],
  });
  const job = workflow.jobs.publish;
  assert.equal(job.if, LEGACY_HELM_PUBLISH_IF);
  const checkout = job.steps.find((step) => step.uses?.startsWith("actions/checkout@"));
  assert.deepEqual(checkout.with, {
    ref: "${{ github.event.workflow_run.head_sha }}",
    "persist-credentials": false,
    submodules: false,
  });
}

test("every workflow has explicit least-privilege job boundaries", () => {
  const seenReusableCalls = new Set();
  const seenWritePermissions = {};

  for (const name of workflowFiles()) {
    const { workflow } = readWorkflow(name);
    assert.deepEqual(workflow.permissions, {}, `${name}: top-level permissions must be {}`);

    for (const [jobName, job] of Object.entries(workflow.jobs ?? {})) {
      const identity = `${name}/${jobName}`;
      if (typeof job.uses === "string") {
        seenReusableCalls.add(identity);
        assert.ok(REUSABLE_CALL_EXEMPTIONS_V1.has(identity), `${identity}: unreviewed reusable-call exemption`);
        assert.match(job.uses, /^\.\/\.github\/workflows\/[A-Za-z0-9._-]+\.ya?ml$/);
        for (const illegal of ["runs-on", "steps", "timeout-minutes"]) {
          assert.equal(Object.hasOwn(job, illegal), false, `${identity}: reusable-call job cannot set ${illegal}`);
        }
        const legalKeys = new Set([
          "name", "uses", "with", "secrets", "strategy", "needs", "if", "concurrency", "permissions",
        ]);
        for (const key of Object.keys(job)) {
          assert.ok(legalKeys.has(key), `${identity}: ${key} is not legal on a reusable-call job`);
        }
      } else {
        assert.ok(Object.hasOwn(job, "runs-on"), `${identity}: runner is required`);
        assert.ok(Number.isInteger(job["timeout-minutes"]) && job["timeout-minutes"] > 0,
          `${identity}: positive timeout-minutes is required`);
      }

      assert.equal(typeof job.permissions, "object", `${identity}: job permissions map is required`);
      const writes = Object.entries(job.permissions ?? {})
        .filter(([, access]) => access === "write")
        .map(([permission]) => permission)
        .sort();
      if (writes.length > 0) seenWritePermissions[identity] = writes;
    }
  }

  assert.deepEqual(seenReusableCalls, REUSABLE_CALL_EXEMPTIONS_V1);
  assert.deepEqual(seenWritePermissions, WRITE_PERMISSION_ALLOWLIST_V1);
});

test("every external action is immutable and carries a version comment", () => {
  for (const name of workflowFiles()) {
    for (const match of externalUsesLines(readWorkflow(name).source)) {
      assert.match(match[1], /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+(?:\/[A-Za-z0-9_.-]+)*@[0-9a-f]{40}$/,
        `${name}: external action must use a full SHA`);
      assert.match(match[2] ?? "", /^# v[0-9][0-9A-Za-z.-]*$/,
        `${name}: pinned action must have a version comment`);
    }
  }
});

test("privileged event workflows consume only trusted repository code", () => {
  for (const name of workflowFiles()) {
    const { source, workflow } = readWorkflow(name);
    if (!hasPrivilegedTrigger(workflow)) continue;

    assert.doesNotMatch(source, /pull_request\.head|head\.ref|refs\/pull\//,
      `${name}: privileged workflow cannot select a PR or workflow head`);
    assert.doesNotMatch(source, /\b(?:cache-from|cache-to):|submodules:\s*true/,
      `${name}: privileged workflow cannot consume untrusted cache or submodules`);
    if (name === "helm-publish.yaml") {
      assertTrustedLegacyHelmPublisher(workflow);
      assert.equal(source.match(/workflow_run\.head_sha/g)?.length, 1);
    } else {
      assert.doesNotMatch(source, /workflow_run\.head_sha/,
        `${name}: only the exactly gated temporary Helm publisher may select a workflow-run SHA`);
    }
    for (const [jobName, job] of Object.entries(workflow.jobs ?? {})) {
      for (const step of job.steps ?? []) {
        assert.doesNotMatch(step.uses ?? "", /(?:cache|download-artifact)/i,
          `${name}: privileged workflow cannot restore caches or artifacts`);
        if (step.uses?.startsWith("actions/checkout@")) {
          const trustedLegacyCheckout = name === "helm-publish.yaml" && jobName === "publish";
          assert.equal(step.with?.ref, trustedLegacyCheckout
            ? "${{ github.event.workflow_run.head_sha }}"
            : "${{ github.event.repository.default_branch }}");
          assert.equal(step.with?.["persist-credentials"], false);
          assert.equal(step.with?.submodules, false);
        }
      }
    }
  }
});

test("helm-unittest executes only the verified immutable release commit", () => {
  const { workflow } = readWorkflow("helm.yaml");
  const install = workflow.jobs.unittest.steps.find((step) => step.name === "Install helm-unittest plugin");
  assertImmutableHelmPluginInstall(install.run);

  for (const unsafe of [
    "helm plugin install https://github.com/helm-unittest/helm-unittest.git --version v1.0.3 # 6f82a998e0b5461762ca959f87f5dd344af5e4eb",
    "helm plugin install https://github.com/helm-unittest/helm-unittest.git --version main",
    `helm plugin install https://github.com/helm-unittest/helm-unittest.git --verify=false --version ${HELM_UNITTEST_COMMIT}`,
  ]) {
    assert.throws(() => assertImmutableHelmPluginInstall(unsafe));
  }
});

test("temporary Helm publisher binds writes to the successful trusted push revision", () => {
  const { workflow } = readWorkflow("helm-publish.yaml");
  assertTrustedLegacyHelmPublisher(workflow);

  for (const unsafeIf of [
    "${{ github.event.workflow_run.conclusion == 'success' && github.event.workflow_run.head_branch == 'main' && github.event.workflow_run.event == 'push' }}",
    "${{ github.event.workflow_run.conclusion == 'success' && github.event.workflow_run.head_repository.full_name == github.repository && github.event.workflow_run.event == 'push' }}",
    "${{ github.event.workflow_run.conclusion == 'success' && github.event.workflow_run.head_repository.full_name == github.repository && github.event.workflow_run.head_branch == 'main' }}",
  ]) {
    const candidate = globalThis.structuredClone(workflow);
    candidate.jobs.publish.if = unsafeIf;
    assert.throws(() => assertTrustedLegacyHelmPublisher(candidate));
  }
  const movingCheckout = globalThis.structuredClone(workflow);
  movingCheckout.jobs.publish.steps.find((step) => step.uses?.startsWith("actions/checkout@")).with.ref = "${{ github.event.repository.default_branch }}";
  assert.throws(() => assertTrustedLegacyHelmPublisher(movingCheckout));
});

test("shell scripts receive event, input, and matrix values through env", () => {
  for (const name of workflowFiles()) {
    const { workflow } = readWorkflow(name);
    for (const [jobName, job] of Object.entries(workflow.jobs ?? {})) {
      for (const step of job.steps ?? []) {
        assert.doesNotMatch(step.run ?? "", /\$\{\{\s*(?:github\.event|inputs\.|matrix\.)/,
          `${name}/${jobName}/${step.name ?? "run"}: direct untrusted interpolation in run`);
      }
    }
  }
});

test("Scorecard preserves analysis and SARIF publication", () => {
  const { workflow } = readWorkflow("scorecard.yaml");
  const job = workflow.jobs.analysis;
  assert.deepEqual(job.permissions, {
    contents: "read",
    "security-events": "write",
    "id-token": "write",
  });
  assert.ok(job.steps.some((step) => step.uses === "ossf/scorecard-action@4eaacf0543bb3f2c246792bd56e8cdeffafb205a"));
  assert.ok(job.steps.some((step) => step.uses === "github/codeql-action/upload-sarif@99df26d4f13ea111d4ec1a7dddef6063f76b97e9"
    && step.with?.sarif_file === "results.sarif"));
});

test("Pages trigger passes repository identity through the environment", () => {
  const { workflow } = readWorkflow("trigger-pages-deploy.yaml");
  const step = workflow.jobs.trigger.steps[0];
  assert.equal(step.env.REPOSITORY, "${{ github.repository }}");
  assert.match(step.run, /--repo "\$REPOSITORY"/);
  assert.doesNotMatch(step.run, /\$\{\{/);
});
