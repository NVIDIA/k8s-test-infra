"use strict";

// Inputs for the merge CI golden test. The expected results in
// test/fixtures/merge-ci/golden-809294d.json were produced by the hard-coded
// WORKFLOWS list and DCO check in src/merge-ci.js at 809294d.

const HEAD = "a".repeat(40);
const REPOSITORY = "nvidia/k8s-test-infra";
const WORKFLOWS = [
  "basic-checks.yaml",
  "validate-changelog.yaml",
  "automation-ci.yml",
  "helm.yaml",
  "dependency-integrity.yaml",
  "deploy-pages.yaml",
];
const BASE_BRANCHES = ["main", "release-0.16", "feature/demo", "release/subbranch"];
const CHANGED_PATHS = [
  "pkg/code.go",
  "README.md",
  ".github/actions/repo-automation/src/index.js",
  ".github/repo-automation/policy.yml",
  ".github/workflows/new.yml",
  ".github/workflows/dependency-integrity.yaml",
  ".github/workflows/deploy-pages.yaml",
  "hack/actionlint.sh",
  "hack/other.sh",
  "Makefile",
  "OWNERS",
  "OWNERS_ALIASES",
  "sub/OWNERS",
  "deployments/nvml-mock/helm/Chart.yaml",
  "deployments/mokka-crds/helm/templates/crd.yaml",
  "deployments/nvml-mock/manifests/deployment.yaml",
  "go.mod",
  "go.sum",
  "docs/general/overview.md",
  "docs/.internal/file.md",
  "mkdocs.yml",
  "requirements-docs.txt",
];
const FILE_SETS = [
  ["none", []],
  ...CHANGED_PATHS.map((changed) => [changed, [{ path: changed }]]),
  ...["go.mod", "docs/guide.md", "deployments/nvml-mock/helm/values.yaml"].map((changed) => (
    [`renamed:${changed}`, [{ path: "archive/moved.txt", previousPath: changed }]]
  )),
  ["pkg/code.go+go.mod+docs/guide.md", [{ path: "pkg/code.go" }, { path: "go.mod" }, { path: "docs/guide.md" }]],
];
const DCO_STATES = [
  ["success", [{ id: 100, name: "DCO", appId: 1861, headOid: HEAD, status: "completed", conclusion: "success" }]],
  ["missing", [{ id: 100, name: "DCO", appId: 15368, headOid: HEAD, status: "completed", conclusion: "success" }]],
  ["failure", [{ id: 100, name: "DCO", appId: 1861, headOid: HEAD, status: "completed", conclusion: "failure" }]],
];

function run(workflow, status, conclusion) {
  return {
    id: WORKFLOWS.indexOf(workflow) + 1,
    headOid: HEAD,
    repository: REPOSITORY,
    prNumber: 42,
    event: "pull_request",
    workflowPath: `.github/workflows/${workflow}`,
    workflowSourceRef: "refs/pull/42/merge",
    runNumber: 10,
    runAttempt: 1,
    status,
    conclusion,
  };
}

// Every subset of successful workflows, then one failed or pending workflow among successes.
function runSets() {
  const sets = [];
  for (let mask = 0; mask < 2 ** WORKFLOWS.length; mask += 1) {
    sets.push(WORKFLOWS.filter((_, index) => (mask & (2 ** index)) !== 0)
      .map((workflow) => run(workflow, "completed", "success")));
  }
  for (const [status, conclusion] of [["completed", "failure"], ["in_progress", null]]) {
    for (const changed of WORKFLOWS) {
      sets.push(WORKFLOWS.map((workflow) => (
        workflow === changed ? run(workflow, status, conclusion) : run(workflow, "completed", "success")
      )));
    }
  }
  return sets;
}

function goldenCases() {
  const cases = [];
  for (const baseBranch of BASE_BRANCHES) {
    for (const [fileSet, files] of FILE_SETS) {
      const inputs = [];
      for (const [, checks] of DCO_STATES) {
        for (const runs of runSets()) {
          inputs.push({ repository: REPOSITORY, prNumber: 42, headOid: HEAD, baseBranch, files, runs, checks });
        }
      }
      cases.push({ key: `${baseBranch} ${fileSet}`, inputs });
    }
  }
  return cases;
}

function resultCode(evaluate, input) {
  try {
    return { SUCCESS: "S", PENDING: "P", FAILED: "F" }[evaluate(input)];
  } catch (error) {
    return error instanceof TypeError ? "T" : "E";
  }
}

module.exports = { goldenCases, resultCode };
