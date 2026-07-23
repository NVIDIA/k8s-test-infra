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
    "lifecycle/stale",
    "lifecycle/frozen",
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
  assert.deepEqual(policy.commands.cherryPick.targetBranchPatterns, ["release-*"]);
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
    "policy.commands.cherryPick",
    "policy.merge.method",
    "policy.bots[0].emails",
    "policy.sizeThresholds.S",
  ]);
});

function withCommands(commands) {
  const valid = loadConfig(repositoryRoot);
  return { ...valid, policy: { ...valid.policy, commands } };
}

test("rejects an empty cherry-pick target branch pattern list", () => {
  const config = withCommands({
    retestCooldownSeconds: 600,
    cherryPick: { targetBranchPatterns: [] },
  });

  assertConfigError(() => validateConfig(config), [
    "policy.commands.cherryPick.targetBranchPatterns",
  ]);
});

test("rejects cherry-pick patterns that are non-string, interior-wildcard, or leading-dash", () => {
  const config = withCommands({
    retestCooldownSeconds: 600,
    cherryPick: { targetBranchPatterns: [123, "rel*ease", "-release", "release**"] },
  });

  assertConfigError(() => validateConfig(config), [
    "policy.commands.cherryPick.targetBranchPatterns[0]",
    "policy.commands.cherryPick.targetBranchPatterns[1]",
    "policy.commands.cherryPick.targetBranchPatterns[2]",
    "policy.commands.cherryPick.targetBranchPatterns[3]",
  ]);
});

test("rejects unknown keys under the cherry-pick command block", () => {
  const config = withCommands({
    retestCooldownSeconds: 600,
    cherryPick: { targetBranchPatterns: ["release-*"], unexpected: true },
  });

  assertConfigError(() => validateConfig(config), [
    "policy.commands.cherryPick.unexpected",
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
