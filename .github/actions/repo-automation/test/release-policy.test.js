"use strict";

const assert = require("node:assert/strict");
const { createHash } = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const YAML = require("yaml");
const Ajv = require("ajv");
const addFormats = require("ajv-formats");

const repositoryRoot = path.resolve(__dirname, "../../../..");

function readJson(name) {
  return JSON.parse(fs.readFileSync(path.join(repositoryRoot, name), "utf8"));
}

function readText(name) {
  return fs.readFileSync(path.join(repositoryRoot, name), "utf8");
}

test("Release Please has one exact root version authority", () => {
  assert.equal(readJson(".github/actions/repo-automation/package.json").devDependencies["release-please"], "17.6.0");
  assert.equal(readJson(".github/actions/repo-automation/package.json").devDependencies.ajv, "8.20.0");
  assert.equal(readJson(".github/actions/repo-automation/package.json").devDependencies["ajv-formats"], "3.0.1");
  assert.equal(createHash("sha256").update(readText(".github/schemas/spdx-2.3.schema.json")).digest("hex"), "1e7a377f428c24d4b13dd786afe219d1517df18de76114b67040a0ce6ca18afa");
  assert.match(readText(".github/scripts/spdx-schema-validator.mjs"), /upstream sha256:239208b7ac287b3cf5d9a9af23f9d69863971102a5e1587a27a398b43490b89b/);
  assert.deepEqual(readJson(".release-please-manifest.json"), { ".": "0.2.1" });
  assert.deepEqual(readJson("release-please-config.json"), {
    $schema: "https://raw.githubusercontent.com/googleapis/release-please/712fcf01effd08d7b0e7b1fd3861f2cb388bc8d1/schemas/config.json",
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
  const config = readJson("release-please-config.json");
  const releasePleaseRoot = path.dirname(require.resolve("release-please/package.json"));
  const pinnedV5Schema = JSON.parse(fs.readFileSync(path.join(releasePleaseRoot, "schemas/config.json"), "utf8"));
  const ajv = new Ajv({ strict: false });
  addFormats(ajv);
  const validate = ajv.compile(pinnedV5Schema);
  assert.equal(validate(config), true, JSON.stringify(validate.errors));
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
    /\$imageTag := default \(default \$\.Chart\.AppVersion \$rootImage\.tag\) \$nriImage\.tag/);
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
  assert.equal(guard.if, "${{ inputs.publish || github.event_name == 'push' }}");
  assert.match(guard.steps[0].run, /automation not activated/);
  assert.match(guard.steps[0].run, /exit 1/);
  assert.deepEqual(workflow.jobs["release-context"].needs, ["activation-guard", "release-please"]);
  assert.match(workflow.jobs["release-context"].if, /github\.ref == format\('refs\/heads\/\{0\}', github\.event\.repository\.default_branch\)/);
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
  assert.ok(releasePlease.steps.some((step) => step.uses === "googleapis/release-please-action@45996ed1f6d02564a971a2fa1b5860e934307cf7"));

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
  assert.match(source, /release-state\.mjs development/);
  assert.match(source, /release-state\.mjs chart/);
  assert.match(source, /release-state\.mjs tree/);
  assert.match(source, /release-state\.mjs binding/);
  assert.match(source, /release-state\.mjs asset/);
  assert.match(source, /release-reader\.mjs image/);
  assert.match(source, /release-reader\.mjs chart/);
  assert.match(source, /release-reader\.mjs github-release/);
  assert.match(source, /image-sbom\.spdx\.json/);
  assert.match(source, /chart-sbom\.spdx\.json/);
  assert.match(source, /gh api[^\n]*releases\/assets/);
  assert.match(source, /gh release upload/);
  assert.match(source, /docker buildx imagetools create/);
  assert.match(source, /cosign verify /);
  assert.match(source, /cosign verify-attestation --type spdxjson/);
  assert.match(source, /gh attestation verify/);
  assert.match(source, /release-state\.mjs final-digest/);
  assert.match(source, /spdx\.mjs normalize/);
  assert.match(source, /provenance:\s*false/);
  assert.match(source, /push-by-digest=true/);
  assert.doesNotMatch(source, /certificate-identity-regexp[^\n]*tags\/v/);
  assert.match(source, /release-reader\.mjs chart/);
  assert.match(source, /git rev-parse HEAD/);
  assert.match(source, /push-to-registry:\s*true/);
  assert.doesNotMatch(source, /\b(?:PAT|RELEASE_PLEASE_TOKEN|private[-_ ]key)\b/i);
  assert.doesNotMatch(source, /gh release upload[^\n]*--clobber/);
  assert.doesNotMatch(source, /automation not activated[\s\S]*state is checked with/);
  assert.doesNotMatch(source, /test -s "\$STATE_FILE"/);
  assert.doesNotMatch(source, /helm package[^\n]*(?:--version|--app-version)/);
  assert.doesNotMatch(source, /path:\s*\.cr-release-packages\s*$/m);
  assert.doesNotMatch(source, /chart-evidence-state\.json", JSON\.stringify\(\{ signature: false, sbom: false, provenance: false \}\)/);

  const imageSteps = workflow.jobs["publish-image"].steps;
  const imageNames = imageSteps.map((step) => step.name);
  assert.ok(imageNames.indexOf("Gather and validate release, registry, evidence, and asset state") < imageNames.indexOf("Log in to GHCR"));
  const stagedBuild = imageSteps.find((step) => step.uses?.startsWith("docker/build-push-action@"));
  assert.equal(Object.hasOwn(stagedBuild.with, "tags"), false);
  assert.match(stagedBuild.with.outputs, /push-by-digest=true/);
  const immutablePromotion = imageSteps.find((step) => step.name === "Re-read immutable image immediately before final tag promotion");
  assert.match(immutablePromotion.run, /release-reader\.mjs/);
  assert.match(immutablePromotion.run, /immutable-promotion/);
  assert.match(immutablePromotion.run, /imagetools create/);
  assert.ok(imageSteps.some((step) => step.if === "${{ steps.image-plan.outputs.alias_minor == 'update' }}"));
  assert.ok(imageSteps.some((step) => step.if === "${{ steps.image-plan.outputs.alias_major == 'update' }}"));
  assert.ok(imageSteps.some((step) => step.if === "${{ steps.image-plan.outputs.alias_latest == 'update' }}"));
  assert.ok(imageSteps.some((step) => step.if === "${{ steps.image-plan.outputs.short == 'update' }}"));
  assert.ok(imageSteps.some((step) => step.if === "${{ steps.image-plan.outputs.edge == 'update' }}"));
  assert.ok(imageSteps.some((step) => step.if?.includes("steps.image-plan.outputs.signature == 'true'")));
  assert.ok(imageSteps.some((step) => step.if?.includes("steps.image-plan.outputs.sbom == 'true'")));
  assert.ok(imageSteps.some((step) => step.if?.includes("steps.image-plan.outputs.provenance == 'true'")));
  for (const name of ["Promote collision-checked short SHA tag by digest", "Promote edge tag by digest", "Promote minor alias by digest", "Promote major alias by digest", "Promote latest alias by digest"]) {
    const step = imageSteps.find((candidate) => candidate.name === name);
    assert.match(step.run, /release-reader\.mjs/);
    assert.match(step.run, /image-plan\.outputs\.digest|FINAL_IMAGE_DIGEST/);
  }
  assert.ok(imageSteps.some((step) => step.name === "Converge image SBOM asset after final re-query" && !step.if));

  const chartSteps = workflow.jobs["publish-chart"].steps;
  const chartNames = chartSteps.map((step) => step.name);
  assert.ok(chartNames.indexOf("Gather and validate release, registry, evidence, and asset state") < chartNames.indexOf("Log in to GHCR"));
  assert.ok(chartSteps.some((step) => step.if === "${{ steps.final-chart-plan.outputs.action == 'publish' }}"));
  assert.ok(chartSteps.some((step) => step.id === "chart-digest"));
  const chartSbom = chartSteps.find((step) => step.uses?.startsWith("anchore/sbom-action@"));
  assert.equal(chartSbom.with.path, "${{ steps.chart-package.outputs.archive }}");
  assert.ok(chartSteps.some((step) => step.name === "Inspect evidence for the final chart digest"));
  assert.ok(chartSteps.some((step) => step.name === "Converge chart SBOM asset after final re-query" && !step.if));

  for (const [jobName, job] of Object.entries(workflow.jobs)) {
    for (const step of job.steps ?? []) {
      assert.doesNotMatch(step.run ?? "", /\$\{\{\s*(?:github\.event|inputs\.|matrix\.|needs\.|steps\.)/,
        `${jobName}/${step.name ?? "run"}: shell data must cross through env or files`);
    }
  }
});
