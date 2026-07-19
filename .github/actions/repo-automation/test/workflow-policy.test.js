"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const YAML = require("yaml");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const workflowRoot = path.join(repositoryRoot, ".github", "workflows");
const HELM_UNITTEST_COMMIT = "6f82a998e0b5461762ca959f87f5dd344af5e4eb";

const REUSABLE_CALL_EXEMPTIONS_V1 = new Set([
  "basic-checks.yaml/variables",
  "basic-checks.yaml/golang",
  "basic-checks.yaml/code-scanning",
  "ci.yaml/basic",
  "ci.yaml/nvml-mock-e2e",
]);

// Exact, versioned write boundary for every workflow job.
const WRITE_PERMISSION_ALLOWLIST_V1 = Object.freeze({
  "basic-checks.yaml/code-scanning": ["security-events"],
  "ci.yaml/basic": ["security-events"],
  "code_scanning.yaml/analyze": ["security-events"],
  "commands.yml/commands": ["actions", "issues", "pull-requests"],
  "label-sync.yml/label-sync": ["issues"],
  "merge-evaluator.yml/evaluate": ["contents", "issues", "pull-requests"],
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

// Resolve a single `with.tags` entry one level through a bare `${{ env.NAME }}`
// expression (the only indirection build-image uses) so the ttl.sh carve-out
// below inspects the real registry ref rather than the literal expression text.
function resolveEnvExpression(value, workflow, job) {
  const match = typeof value === "string" && value.match(/^\$\{\{\s*env\.([A-Za-z0-9_]+)\s*\}\}$/);
  if (!match) return value;
  const resolved = (job.env ?? {})[match[1]] ?? (workflow.env ?? {})[match[1]];
  return typeof resolved === "string" ? resolved : value;
}

// ttl.sh is an anonymous, auth-free, auto-expiring registry. nvml-mock-e2e-go.yaml's
// build-image job pushes there purely to hand the just-built image to later jobs of
// the SAME workflow run (see its header comment) - that's intra-run transport, not
// artifact publication, so a build-push step whose tags ALL resolve to ttl.sh/ is
// exempt from the sole-publisher gate. Any tag resolving elsewhere still counts.
function isEphemeralTtlShOnlyPush(step, workflow, job) {
  const rawTags = step.with?.tags;
  if (typeof rawTags !== "string") return false;
  const tags = rawTags.split(/[\n,]/).map((tag) => tag.trim()).filter(Boolean);
  return tags.length > 0 &&
    tags.every((tag) => resolveEnvExpression(tag, workflow, job).startsWith("ttl.sh/"));
}

function assertImmutableHelmPluginInstall(run) {
  assert.equal(typeof run, "string");
  const installCommands = run.split("\n").filter((line) => line.includes("helm plugin install"));
  assert.deepEqual(installCommands.map((line) => line.trim()), [
    `helm plugin install https://github.com/helm-unittest/helm-unittest.git --version ${HELM_UNITTEST_COMMIT}`,
  ]);
  assert.doesNotMatch(run, /--verify=false|--version\s+(?:v?\d|main|master)\b/);
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
    assert.doesNotMatch(source, /workflow_run\.head_sha/,
      `${name}: privileged workflow cannot select a workflow-run SHA`);
    for (const job of Object.values(workflow.jobs ?? {})) {
      for (const step of job.steps ?? []) {
        assert.doesNotMatch(step.uses ?? "", /(?:cache|download-artifact)/i,
          `${name}: privileged workflow cannot restore caches or artifacts`);
        if (step.uses?.startsWith("actions/checkout@")) {
          assert.equal(step.with?.ref, "${{ github.event.repository.default_branch }}");
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

test("release workflow is the sole image and chart publisher", () => {
  assert.equal(fs.existsSync(path.join(workflowRoot, "helm-publish.yaml")), false);
  assert.equal(fs.existsSync(path.join(workflowRoot, "nvml-mock-publish.yaml")), false);

  const publishers = workflowFiles().filter((name) => {
    const { workflow } = readWorkflow(name);
    return Object.values(workflow.jobs ?? {}).some((job) => (job.steps ?? []).some((step) => {
      const buildPush = step.uses?.startsWith("docker/build-push-action@") &&
        (step.with?.push === true || /(?:^|,)push=true(?:,|$)/.test(step.with?.outputs ?? ""));
      const durableBuildPush = buildPush && !isEphemeralTtlShOnlyPush(step, workflow, job);
      return durableBuildPush || /docker buildx imagetools create|\bhelm push\b/.test(step.run ?? "");
    }));
  });
  assert.deepEqual(publishers, ["release.yml"]);

  const { source, workflow } = readWorkflow("release.yml");
  assert.deepEqual(workflow.on.push, { branches: ["main"] });
  assert.equal(Object.hasOwn(workflow.jobs, "activation-guard"), false);
  assert.doesNotMatch(source, /oci:\/\/ghcr\.io\/nvidia\/k8s-test-infra\/chart/);
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
