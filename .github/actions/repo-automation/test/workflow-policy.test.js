"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const YAML = require("yaml");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const workflowRoot = path.join(repositoryRoot, ".github", "workflows");
const trustedRef = "${{ steps.trusted.outputs.result }}";
const mokkaTrustedRef = "${{ github.sha }}";
const mokkaActivationGate =
  "${{ vars.REPOSITORY_AUTOMATION_MOKKA_ENABLED == 'true' && github.ref == 'refs/tags/mokka-cherry-pick-v0.11.0' }}";
const managed = [
  "automation-ci.yml",
  "backport.yml",
  "commands.yml",
  "label-sync.yml",
  "merge-evaluate.yml",
  "mokka-cherry-pick.yml",
  "pr-metadata.yml",
  "review-observer.yml",
];

function read(name) {
  const source = fs.readFileSync(path.join(workflowRoot, name), "utf8");
  return { source, workflow: YAML.parse(source) };
}

test("foundation workflows have explicit permissions and immutable actions", () => {
  for (const name of managed) {
    const { source, workflow } = read(name);
    assert.deepEqual(workflow.permissions, {}, `${name}: top-level permissions`);
    for (const [jobName, job] of Object.entries(workflow.jobs)) {
      assert.equal(typeof job.permissions, "object", `${name}/${jobName}: permissions`);
      if (job.uses !== undefined) {
        assert.match(job.uses, /^\.\/\.github\/workflows\/[A-Za-z0-9._-]+\.ya?ml$/);
        continue;
      }
      assert.ok(Number.isInteger(job["timeout-minutes"]) && job["timeout-minutes"] > 0,
        `${name}/${jobName}: bounded runtime`);
    }
    for (const match of source.matchAll(/^\s*uses:\s+([^\s#]+)(?:\s+#\s*(v[^\s]+))?$/gm)) {
      if (match[1].startsWith("./")) continue;
      assert.match(match[1], /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+(?:\/[A-Za-z0-9_.-]+)*@[0-9a-f]{40}$/,
        `${name}: external action is not immutable`);
      assert.match(match[2] ?? "", /^v[0-9]/, `${name}: pinned action needs a version comment`);
    }
  }
});

test("privileged foundation workflows never execute pull-request code", () => {
  for (const name of [
    "backport.yml", "commands.yml", "label-sync.yml", "merge-evaluate.yml",
    "mokka-cherry-pick.yml", "pr-metadata.yml",
  ]) {
    const { source, workflow } = read(name);
    assert.doesNotMatch(source, /pull_request\.head|workflow_run\.head_sha|refs\/pull\//);
    assert.doesNotMatch(source, /(?:cache|download-artifact)/i);
    for (const job of Object.values(workflow.jobs)) {
      if (!Array.isArray(job.steps)) continue;
      for (const step of job.steps ?? []) {
        if (step.uses?.startsWith("actions/checkout@") && step.with?.path === "control") {
          assert.equal(step.with.ref, name === "mokka-cherry-pick.yml" ? mokkaTrustedRef : trustedRef);
          assert.equal(step.with["persist-credentials"], false);
          assert.equal(step.with.submodules, false);
        }
      }
      if (name === "mokka-cherry-pick.yml") {
        assert.equal(job.if, mokkaActivationGate);
        assert.equal(job.steps.some((step) => step.id === "trusted"), false);
        continue;
      }
      const resolver = job.steps.find((step) => step.id === "trusted");
      assert.ok(resolver, `${name}: trusted default-branch resolver`);
      assert.match(resolver.with.script, /default_branch/);
      assert.match(resolver.with.script, /getBranch/);
    }
  }
});

test("repository automation documentation is in navigation and linked from contribution guidance", () => {
  const navigation = fs.readFileSync(path.join(repositoryRoot, "mkdocs.yml"), "utf8");
  const contributing = fs.readFileSync(
    path.join(repositoryRoot, "docs", "contributing", "pull-requests.md"),
    "utf8",
  );
  assert.match(navigation, /maintainers\/repository-automation\.md/);
  assert.match(contributing, /maintainers\/repository-automation/);
});

test("foundation has one stable merge check and no direct merge or arbitrary dispatch", () => {
  const sourceRoot = path.join(repositoryRoot, ".github", "actions", "repo-automation", "src");
  const source = [
    fs.readFileSync(path.join(sourceRoot, "github-client.js"), "utf8"),
    fs.readFileSync(path.join(sourceRoot, "modes", "merge-evaluate.js"), "utf8"),
    fs.readFileSync(path.join(sourceRoot, "modes", "command.js"), "utf8"),
  ].join("\n");
  assert.equal(source.match(/repository-automation\/merge-policy/g)?.length, 1);
  assert.doesNotMatch(source, /pulls\.merge|mergePullRequest|createWorkflowDispatch/);
  assert.doesNotMatch(read("commands.yml").source, /workflow_dispatch|gh\s+workflow\s+run/);
});

test("foundation scope does not add excluded workflow families", () => {
  for (const name of managed) {
    assert.doesNotMatch(name, /release|spdx|dependabot|prow|tide/i);
  }
  const runbook = fs.readFileSync(
    path.join(repositoryRoot, "docs", "maintainers", "repository-automation.md"),
    "utf8",
  );
  assert.match(runbook, /GitHub native auto-merge/);
  assert.match(runbook, /does not (?:install|deploy) (?:Prow or )?Tide/i);
});
