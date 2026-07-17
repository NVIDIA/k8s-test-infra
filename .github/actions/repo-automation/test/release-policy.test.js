"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const YAML = require("yaml");

const repositoryRoot = path.resolve(__dirname, "../../../..");

function readJson(name) {
  return JSON.parse(fs.readFileSync(path.join(repositoryRoot, name), "utf8"));
}

function readText(name) {
  return fs.readFileSync(path.join(repositoryRoot, name), "utf8");
}

test("Release Please has one exact root version authority", () => {
  assert.deepEqual(readJson(".release-please-manifest.json"), { ".": "0.2.1" });
  assert.deepEqual(readJson("release-please-config.json"), {
    $schema: "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
    packages: {
      ".": {
        "release-type": "simple",
        "package-name": "nvml-mock",
        "changelog-path": "CHANGELOG.md",
        "include-v-in-tag": true,
        "include-component-in-tag": false,
        "extra-files": [
          {
            type: "yaml",
            path: "deployments/nvml-mock/helm/nvml-mock/Chart.yaml",
            jsonpath: "$.version",
          },
          {
            type: "yaml",
            path: "deployments/nvml-mock/helm/nvml-mock/Chart.yaml",
            jsonpath: "$.appVersion",
          },
        ],
      },
    },
  });
});

test("chart defaults bind root and NRI images to the release appVersion", () => {
  const chart = YAML.parse(readText("deployments/nvml-mock/helm/nvml-mock/Chart.yaml"));
  const values = YAML.parse(readText("deployments/nvml-mock/helm/nvml-mock/values.yaml"));
  assert.equal(chart.version, "0.2.1");
  assert.equal(chart.appVersion, "0.2.1");
  assert.equal(values.image.tag, "");
  assert.match(readText("deployments/nvml-mock/helm/nvml-mock/templates/daemonset.yaml"),
    /default \.Chart\.AppVersion \.Values\.image\.tag/);
  assert.match(readText("deployments/nvml-mock/helm/nvml-mock/templates/nri-daemonset.yaml"),
    /\$imageTag := default \$\.Chart\.AppVersion \$image\.tag/);
});

test("staged release workflow is read-only until one hard activation guard is removed", () => {
  const source = readText(".github/workflows/release.yml");
  const workflow = YAML.parse(source);
  assert.deepEqual(workflow.on, {
    workflow_dispatch: {
      inputs: {
        publish: {
          description: "Publish an existing canonical release after activation",
          required: true,
          type: "boolean",
          default: false,
        },
        version: {
          description: "Canonical X.Y.Z version for an authorized resume",
          required: false,
          type: "string",
          default: "",
        },
      },
    },
  });
  assert.deepEqual(workflow.permissions, {});
  assert.deepEqual(workflow.concurrency, { group: "artifact-publication", "cancel-in-progress": false });

  const plan = workflow.jobs.plan;
  assert.equal(plan.if, "${{ !inputs.publish }}");
  assert.deepEqual(plan.permissions, { contents: "read" });
  assert.match(plan.steps.map((step) => step.run ?? "").join("\n"), /release-state\.mjs plan/);
  const planSource = YAML.stringify(plan);
  assert.doesNotMatch(planSource, /login-action|build-push-action|cosign|actions\/attest|helm push|docker push|gh release/);

  const guard = workflow.jobs["activation-guard"];
  assert.equal(guard.if, "${{ inputs.publish }}");
  assert.match(guard.steps[0].run, /automation not activated/);
  assert.match(guard.steps[0].run, /exit 1/);
  assert.equal(workflow.jobs["release-context"].needs, "activation-guard");
  assert.deepEqual(workflow.jobs["publish-image"].needs, ["release-context"]);
  assert.deepEqual(workflow.jobs["publish-chart"].needs, ["release-context"]);

  const releasePlease = workflow.jobs["release-please"];
  assert.equal(releasePlease.if, "${{ github.event_name == 'push' }}");
  assert.deepEqual(releasePlease.permissions, { contents: "write", issues: "write", "pull-requests": "write" });
  assert.deepEqual(releasePlease.outputs, {
    release_created: "${{ steps.release.outputs.release_created }}",
    tag_name: "${{ steps.release.outputs.tag_name }}",
    major: "${{ steps.release.outputs.major }}",
    minor: "${{ steps.release.outputs.minor }}",
    patch: "${{ steps.release.outputs.patch }}",
    sha: "${{ steps.release.outputs.sha }}",
  });
  assert.equal(Object.hasOwn(releasePlease.outputs, "version"), false);
  assert.ok(releasePlease.steps.some((step) => step.uses === "googleapis/release-please-action@0dfd8538845b8e92600d271a895a5372865d4062"));

  const publishPermissions = {
    contents: "write",
    packages: "write",
    "id-token": "write",
    attestations: "write",
    "artifact-metadata": "write",
  };
  assert.deepEqual(workflow.jobs["publish-image"].permissions, publishPermissions);
  assert.deepEqual(workflow.jobs["publish-chart"].permissions, publishPermissions);
  assert.match(source, /oci:\/\/ghcr\.io\/nvidia\/charts/);
  assert.match(source, /ghcr\.io\/nvidia\/nvml-mock/);
  assert.match(source, /release-state\.mjs image/);
  assert.match(source, /release-state\.mjs chart/);
  assert.match(source, /release-state\.mjs tree/);
  assert.match(source, /image-sbom\.spdx\.json/);
  assert.match(source, /chart-sbom\.spdx\.json/);
  assert.match(source, /push-to-registry:\s*true/);
  assert.doesNotMatch(source, /\b(?:PAT|RELEASE_PLEASE_TOKEN|private[-_ ]key)\b/i);
  assert.doesNotMatch(source, /gh release upload[^\n]*--clobber/);

  for (const [jobName, job] of Object.entries(workflow.jobs)) {
    for (const step of job.steps ?? []) {
      assert.doesNotMatch(step.run ?? "", /\$\{\{\s*(?:github\.event|inputs\.|matrix\.|needs\.|steps\.)/,
        `${jobName}/${step.name ?? "run"}: shell data must cross through env or files`);
    }
  }
});
