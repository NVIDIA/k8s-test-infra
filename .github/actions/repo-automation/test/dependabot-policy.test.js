"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const YAML = require("yaml");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const dependabotPath = path.join(repositoryRoot, ".github", "dependabot.yml");

function loadDependabot() {
  return YAML.parse(fs.readFileSync(dependabotPath, "utf8"));
}

test("Dependabot monitors every approved ecosystem with the dependency label", () => {
  const config = loadDependabot();
  const updates = Object.fromEntries(
    config.updates.map((entry) => [entry["package-ecosystem"], entry]),
  );

  assert.equal(config.version, 2);
  assert.deepEqual(Object.keys(updates).sort(), ["docker", "github-actions", "gomod", "npm"]);

  for (const entry of Object.values(updates)) {
    assert.equal(entry["target-branch"], "main");
    assert.deepEqual(entry.labels, ["kind/dependencies"]);
  }

  assert.deepEqual(updates.gomod.schedule, { interval: "weekly", day: "sunday" });
  assert.equal(updates.gomod.directory, "/");
  assert.deepEqual(updates.gomod.groups, {
    k8sio: {
      patterns: ["k8s.io/*"],
      "exclude-patterns": ["k8s.io/klog/*"],
    },
  });

  assert.deepEqual(updates["github-actions"].schedule, { interval: "daily" });
  assert.equal(updates["github-actions"].directory, "/");

  assert.deepEqual(updates.docker.schedule, { interval: "daily" });
  assert.deepEqual(updates.docker.directories, ["/deployments/devel"]);

  assert.deepEqual(updates.npm.schedule, { interval: "weekly", day: "sunday" });
  assert.equal(updates.npm.directory, "/.github/actions/repo-automation");
});
