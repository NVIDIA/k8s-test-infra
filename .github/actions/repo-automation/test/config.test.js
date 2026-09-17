"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const { loadConfig, validateConfig } = require("../src/config.js");
const YAML = require("yaml");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const fixtureRoot = path.join(__dirname, "fixtures", "config");

function readFixture(name) {
  return YAML.parse(fs.readFileSync(path.join(fixtureRoot, name), "utf8"));
}

function assertConfigError(callback, expectedPaths) {
  assert.throws(callback, (error) => {
    assert.equal(error.name, "ConfigError");
    for (const expectedPath of expectedPaths) {
      const escapedPath = expectedPath.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      assert.match(error.message, new RegExp(escapedPath));
    }
    return true;
  });
}

function createConfigRoot(overrides) {
  const rootDir = fs.mkdtempSync(path.join(os.tmpdir(), "repo-automation-config-"));
  const configDir = path.join(rootDir, ".github", "repo-automation");
  fs.mkdirSync(configDir, { recursive: true });

  for (const name of ["policy", "labels", "areas"]) {
    const source = overrides[name]
      ?? fs.readFileSync(path.join(repositoryRoot, ".github", "repo-automation", `${name}.yml`), "utf8");
    fs.writeFileSync(path.join(configDir, `${name}.yml`), source);
  }

  return rootDir;
}

test("loads all repository configuration files at schema version 1", () => {
  const config = loadConfig(repositoryRoot);

  assert.deepEqual(
    {
      policy: config.policy.schemaVersion,
      labels: config.labels.schemaVersion,
      areas: config.areas.schemaVersion,
    },
    { policy: 1, labels: 1, areas: 1 },
  );
});

test("declares the complete approved label taxonomy with valid metadata", () => {
  const { labels } = loadConfig(repositoryRoot);
  const names = labels.labels.map((label) => label.name);

  assert.equal(new Set(names).size, names.length);
  for (const label of labels.labels) {
    assert.match(label.color, /^[0-9a-f]{6}$/);
    assert.equal(label.description.trim().length > 0, true);
  }

  assert.deepEqual(names, [
    "kind/feature",
    "kind/bug",
    "kind/documentation",
    "kind/test",
    "kind/refactor",
    "kind/performance",
    "kind/ci",
    "kind/cleanup",
    "kind/dependencies",
    "kind/revert",
    "size/S",
    "size/M",
    "size/L",
    "size/XL",
    "area/nvml-mock",
    "area/mockcuda",
    "area/helm",
    "area/kubernetes",
    "area/ci",
    "area/docs",
    "lgtm",
    "approved",
    "needs-rebase",
    "do-not-merge/hold",
    "do-not-merge/work-in-progress",
    "do-not-merge/needs-approval",
    "good first issue",
    "help wanted",
    "priority/critical-urgent",
    "priority/important-soon",
    "priority/important-longterm",
    "priority/backlog",
    "needs-triage",
  ]);
  assert.equal(names.includes("priority/unprioritized"), false);
});

test("maps ordered repository paths only to declared area labels", () => {
  const { labels, areas } = loadConfig(repositoryRoot);
  const declaredLabels = new Set(labels.labels.map((label) => label.name));

  for (const mapping of areas.areas) {
    for (const label of mapping.labels) {
      assert.equal(declaredLabels.has(label), true, `${label} must exist in labels.yml`);
    }
  }

  assert.deepEqual(areas.areas, [
    {
      paths: ["deployments/nvml-mock/**", "cmd/nvml-mock/**"],
      labels: ["area/nvml-mock"],
    },
    { paths: ["pkg/gpu/mocknvml/**"], labels: ["area/nvml-mock"] },
    { paths: ["pkg/gpu/mockcuda/**"], labels: ["area/mockcuda"] },
    { paths: ["deployments/nvml-mock/helm/**"], labels: ["area/helm"] },
    { paths: ["tests/e2e/**"], labels: ["area/kubernetes"] },
    { paths: [".github/**", "hack/**", "Makefile"], labels: ["area/ci"] },
    { paths: ["docs/**", "*.md"], labels: ["area/docs"] },
  ]);
});

test("loads exact authority, branch, review, command, bot, and size policy", () => {
  const { policy } = loadConfig(repositoryRoot);

  assert.deepEqual(policy.activeOwnerFiles, ["/OWNERS"]);
  assert.equal(policy.activeOwnerFiles.includes("vendor/**/OWNERS"), false);
  assert.deepEqual(policy.protectedBranches, ["main", "release-*"]);
  assert.equal(policy.review.reviewerTarget, 2);
  assert.equal(policy.commands.retestCooldownSeconds, 600);
  assert.equal(policy.commands.historyLimit, 256);
  assert.deepEqual(policy.commands.retestWorkflows, [
    ".github/workflows/automation-ci.yml",
  ]);
  assert.deepEqual(policy.commands.backportBranches, ["release-*"]);
  assert.equal(policy.merge.method, "SQUASH");
  assert.deepEqual(policy.bots, [
    {
      login: "dependabot[bot]",
      emails: ["49699333+dependabot[bot]@users.noreply.github.com"],
    },
    {
      login: "github-actions[bot]",
      emails: ["41898282+github-actions[bot]@users.noreply.github.com"],
    },
  ]);
  assert.deepEqual(policy.sizeThresholds, { S: 0, M: 50, L: 250, XL: 1000 });
});

test("rejects invalid label metadata with path-specific errors instead of defaulting", () => {
  const config = {
    ...loadConfig(repositoryRoot),
    labels: readFixture("invalid-labels.yml"),
  };

  assertConfigError(() => validateConfig(config), [
    "labels.labels[0].color",
    "labels.labels[0].description",
    "labels.labels[1].name",
  ]);
});

test("rejects invalid policy values with path-specific errors instead of defaulting", () => {
  const config = {
    ...loadConfig(repositoryRoot),
    policy: readFixture("invalid-policy.yml"),
  };

  assertConfigError(() => validateConfig(config), [
    "policy.unexpected",
    "policy.protectedBranches",
    "policy.activeOwnerFiles",
    "policy.review.reviewerTarget",
    "policy.commands.retestCooldownSeconds",
    "policy.merge.method",
    "policy.bots[0].emails",
    "policy.sizeThresholds.S",
  ]);
});

test("rejects unsafe command bounds, workflow paths, and backport patterns", () => {
  const valid = loadConfig(repositoryRoot);
  const policy = {
    ...valid.policy,
    commands: {
      retestCooldownSeconds: 599,
      historyLimit: 257,
      retestWorkflows: ["../workflows/hostile.yml"],
      backportBranches: ["refs/heads/*/nested*"],
    },
  };

  assertConfigError(() => validateConfig({ ...valid, policy }), [
    "policy.commands.retestCooldownSeconds",
    "policy.commands.historyLimit",
    "policy.commands.retestWorkflows[0]",
    "policy.commands.backportBranches[0]",
  ]);
});

test("rejects aliases and merge keys with path-specific errors", () => {
  const aliasRoot = createConfigRoot({
    labels: "schemaVersion: 1\nlabels: &shared []\ncopy: *shared\n",
  });
  const mergeRoot = createConfigRoot({
    areas: "schemaVersion: 1\nbase: &base\n  areas: []\n<<: *base\nareas: []\n",
  });
  assertConfigError(() => loadConfig(aliasRoot), ["labels.alias"]);
  assertConfigError(() => loadConfig(mergeRoot), ["areas.merge"]);
});

test("rejects unknown top-level keys in every configuration file", () => {
  const valid = loadConfig(repositoryRoot);

  assertConfigError(
    () => validateConfig({ ...valid, labels: { ...valid.labels, unexpected: true } }),
    ["labels.unexpected"],
  );
  assertConfigError(
    () => validateConfig({ ...valid, areas: { ...valid.areas, unexpected: true } }),
    ["areas.unexpected"],
  );
});

test("bounds configuration collections and user-visible label text", async (t) => {
  const valid = loadConfig(repositoryRoot);
  const label = valid.labels.labels[0];
  const area = valid.areas.areas[0];
  const cases = [
    [
      "label count",
      { ...valid, labels: { ...valid.labels, labels: Array.from({ length: 1001 }, (_, index) => ({
        ...label,
        name: `kind/generated-${index}`,
      })) } },
      "labels.labels",
    ],
    [
      "label name length",
      { ...valid, labels: { ...valid.labels, labels: valid.labels.labels.map((entry, index) => (
        index === 0 ? { ...entry, name: `kind/${"a".repeat(46)}` } : entry
      )) } },
      "labels.labels[0].name",
    ],
    [
      "label description length",
      { ...valid, labels: { ...valid.labels, labels: valid.labels.labels.map((entry, index) => (
        index === 0 ? { ...entry, description: "a".repeat(101) } : entry
      )) } },
      "labels.labels[0].description",
    ],
    [
      "area count",
      { ...valid, areas: { ...valid.areas, areas: Array.from({ length: 501 }, () => area) } },
      "areas.areas",
    ],
    [
      "patterns per area",
      { ...valid, areas: { ...valid.areas, areas: [{
        ...area,
        paths: Array.from({ length: 101 }, (_, index) => `path-${index}/**`),
      }] } },
      "areas.areas[0].paths",
    ],
    [
      "labels per area",
      { ...valid, areas: { ...valid.areas, areas: [{
        ...area,
        labels: Array.from({ length: 21 }, () => area.labels[0]),
      }] } },
      "areas.areas[0].labels",
    ],
  ];

  for (const [name, config, expectedPath] of cases) {
    await t.test(name, () => assertConfigError(
      () => validateConfig(config),
      [expectedPath],
    ));
  }
});

test("does not include release automation dependencies or scripts", () => {
  const packageJson = JSON.parse(fs.readFileSync(
    path.join(repositoryRoot, ".github", "actions", "repo-automation", "package.json"),
    "utf8",
  ));

  assert.equal(packageJson.dependencies?.["release-please"], undefined);
  assert.equal(packageJson.devDependencies?.["release-please"], undefined);
  assert.equal(
    Object.keys(packageJson.scripts).some((name) => name.includes("release")),
    false,
  );
});

test("repository automation CI contains every Task 1 gate", () => {
  const workflow = fs.readFileSync(
    path.join(repositoryRoot, ".github", "workflows", "automation-ci.yml"),
    "utf8",
  );

  for (const command of [
    "npm ci",
    "npm test",
    "npm run lint",
    "npm run package",
    "git diff --exit-code -- dist",
    "go test ./tests/hack -run TestMokkaCherryPick -count=1",
    "make actionlint",
  ]) {
    assert.equal(workflow.includes(command), true, `workflow must run ${command}`);
  }
  assert.doesNotMatch(workflow, /SPDX-License-Identifier/);
  assert.doesNotMatch(workflow, /uses:\s+[^\s]+@(?![0-9a-f]{40}(?:\s|$))/);
});
