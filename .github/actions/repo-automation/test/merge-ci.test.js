"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const { minimatch } = require("minimatch");
const YAML = require("yaml");
const { loadConfig } = require("../src/config.js");
const { evaluateCI } = require("../src/merge-ci.js");
const { MAX_API_COLLECTION_ITEMS, MAX_CHANGED_FILES } = require("../src/limits.js");
const { goldenCases, resultCode } = require("./helpers/merge-ci-golden.js");
const golden = require("./fixtures/merge-ci/golden-809294d.json");

const HEAD = "a".repeat(40);
const OLD_HEAD = "b".repeat(40);
const REPOSITORY = "nvidia/k8s-test-infra";
const WORKFLOWS = ["basic-checks.yaml", "validate-changelog.yaml", "automation-ci.yml", "helm.yaml", "dependency-integrity.yaml", "deploy-pages.yaml"];
const requiredCI = loadConfig(path.resolve(__dirname, "../../../..")).policy.merge.requiredCI;

function run(workflow, overrides = {}) {
  return {
    id: WORKFLOWS.indexOf(workflow) + 1,
    headOid: HEAD, repository: REPOSITORY, prNumber: 42, event: "pull_request",
    workflowPath: `.github/workflows/${workflow}`, workflowSourceRef: "refs/pull/42/merge",
    runNumber: 10, runAttempt: 1, status: "completed", conclusion: "success",
    ...overrides,
  };
}

function dco(overrides = {}) {
  return { id: 100, name: "DCO", appId: 1861, headOid: HEAD, status: "completed", conclusion: "success", ...overrides };
}

function evidence(overrides = {}) {
  return {
    repository: REPOSITORY, prNumber: 42, headOid: HEAD, baseBranch: "main",
    files: [{ path: "pkg/code.go" }],
    runs: [run("basic-checks.yaml"), run("validate-changelog.yaml")], checks: [dco()], requiredCI,
    ...overrides,
  };
}

function unmappedEvidence(overrides = {}) {
  const source = { headRepository: "contributor/k8s-test-infra", headBranch: "topic/readiness" };
  return evidence({
    ...source,
    runs: ["basic-checks.yaml", "validate-changelog.yaml"].map((workflow) => run(workflow, {
      ...source, prNumber: null, workflowSourceRef: undefined,
    })),
    ...overrides,
  });
}

test("policy requiredCI reproduces the 809294d hard-coded requirements for every golden case", () => {
  const cases = goldenCases();
  assert.deepEqual(cases.map(({ key }) => key), Object.keys(golden));
  for (const { key, inputs } of cases) {
    const actual = inputs.map((input) => resultCode(evaluateCI, { ...input, requiredCI })).join("");
    assert.equal(actual, golden[key], key);
  }
});

test("required workflows and checks come from the policy, not a built-in list", () => {
  const basic = { path: ".github/workflows/basic-checks.yaml" };
  const changelog = { path: ".github/workflows/validate-changelog.yaml" };
  const dcoCheck = { name: "DCO", appId: 1861 };
  assert.equal(evaluateCI(evidence({
    runs: [run("basic-checks.yaml")], requiredCI: { workflows: [basic], checks: [dcoCheck] },
  })), "SUCCESS");
  assert.equal(evaluateCI(evidence({
    requiredCI: { workflows: [basic, changelog, { path: ".github/workflows/ci.yaml" }], checks: [dcoCheck] },
  })), "PENDING");
  assert.equal(evaluateCI(evidence({
    runs: [run("validate-changelog.yaml")],
    requiredCI: { workflows: [{ ...basic, files: ["docs/**"] }, changelog], checks: [dcoCheck] },
  })), "SUCCESS");
  const withCla = { workflows: [basic, changelog], checks: [dcoCheck, { name: "license/cla", appId: 9 }] };
  assert.equal(evaluateCI(evidence({ requiredCI: withCla })), "PENDING");
  assert.equal(evaluateCI(evidence({
    requiredCI: withCla, checks: [dco(), dco({ id: 101, name: "license/cla", appId: 9 })],
  })), "SUCCESS");
  assert.equal(evaluateCI(evidence({
    requiredCI: withCla, checks: [dco(), dco({ id: 101, name: "license/cla", appId: 9, conclusion: "failure" })],
  })), "FAILED");
});

test("evaluation fails closed without a well-formed required CI definition", () => {
  for (const override of [
    { requiredCI: undefined }, { requiredCI: null }, { requiredCI: [] },
    { requiredCI: { workflows: requiredCI?.workflows } },
    { requiredCI: { workflows: [{ files: ["docs/**"] }], checks: [{ name: "DCO", appId: 1861 }] } },
    { requiredCI: { workflows: [{ path: ".github/workflows/basic-checks.yaml", files: "docs/**" }], checks: [] } },
    { requiredCI: { workflows: [], checks: [{ name: "DCO", appId: "1861" }] } },
  ]) assert.throws(() => evaluateCI(evidence(override)), TypeError, JSON.stringify(override));
});

test("evaluation rejects an empty workflow or check requirement list", () => {
  for (const requirements of [
    { workflows: [], checks: [{ name: "DCO", appId: 1861 }] },
    { workflows: [{ path: ".github/workflows/basic-checks.yaml" }], checks: [] },
  ]) {
    assert.throws(() => evaluateCI(evidence({ requiredCI: requirements })), {
      name: "TypeError",
      message: "CI requirements must name at least one workflow and one check",
    }, JSON.stringify(requirements));
  }
});

test("portable requiredCI path patterns match as documented in policy.yml", () => {
  const vectors = [
    ["docs/**", "docs/a.md", true], ["docs/**", "docs/a/b.md", true], ["docs/**", "docs", false],
    ["docs/**", "docsx/a.md", false], ["**/OWNERS", "OWNERS", true], ["**/OWNERS", "x/y/OWNERS", true],
    ["a/**/b", "a/b", true], ["a/**/b", "a/x/y/b", true], ["a/**/b", "a/x/y/c", false],
    ["*.yml", "mkdocs.yml", true], ["*.yml", "docs/mkdocs.yml", false], ["*", ".hidden", true],
    ["*.test.*", "x.test.js", true], ["deployments/*/helm/**", "deployments/nvml-mock/helm/Chart.yaml", true],
    ["deployments/*/helm/**", "deployments/a/b/helm/x", false], ["Makefile", "makefile", false],
  ];
  for (const [pattern, changedPath, matches] of vectors) {
    const requirements = {
      workflows: [{ path: ".github/workflows/basic-checks.yaml" }, { path: ".github/workflows/helm.yaml", files: [pattern] }],
      checks: [{ name: "DCO", appId: 1861 }],
    };
    assert.equal(evaluateCI(evidence({ files: [{ path: changedPath }], requiredCI: requirements })),
      matches ? "PENDING" : "SUCCESS", `${pattern} ${changedPath}`);
  }
});

test("requires both broad workflows and DCO instead of passing empty evidence", () => {
  assert.equal(evaluateCI(evidence()), "SUCCESS");
  for (const override of [
    { runs: [], checks: [] }, { runs: [run("basic-checks.yaml")] },
    { runs: [run("validate-changelog.yaml")] }, { checks: [] },
  ]) assert.equal(evaluateCI(evidence(override)), "PENDING");
});

test("does not authorize unsupported base branches", () => {
  assert.equal(evaluateCI(evidence({ baseBranch: "feature/demo" })), "PENDING");
  assert.equal(evaluateCI(evidence({ baseBranch: "release-0.16" })), "SUCCESS");
  assert.equal(evaluateCI(evidence({ baseBranch: "release/subbranch" })), "PENDING");
});

test("requires exact workflow identity instead of names or unrelated successful runs", () => {
  for (const override of [
    { workflowPath: ".github/workflows/ci.yaml", name: "basic checks" },
    { workflowPath: ".github/workflows/basic-checks.yaml@refs/pull/42/merge" },
    { workflowSourceRef: "refs/heads/main" }, { workflowSourceRef: "refs/pull/43/merge" },
    { headOid: OLD_HEAD }, { event: "pull_request_target" }, { event: "push" },
    { prNumber: 43 }, { repository: "attacker/k8s-test-infra" },
  ]) {
    assert.equal(evaluateCI(evidence({ runs: [run("basic-checks.yaml", override), run("validate-changelog.yaml")] })), "PENDING", JSON.stringify(override));
  }
  for (const workflowSourceRef of [null, undefined]) {
    assert.equal(evaluateCI(evidence({ runs: [run("basic-checks.yaml", { workflowSourceRef }), run("validate-changelog.yaml")] })), "SUCCESS");
  }
});

test("requires the exact DCO app and name on the current head", () => {
  for (const override of [{ appId: 15368 }, { name: "dco" }, { name: "DCO / verify" }, { headOid: OLD_HEAD }]) {
    assert.equal(evaluateCI(evidence({ checks: [dco(override)] })), "PENDING");
  }
});

test("accepts plain-path fork runs without PR mappings only for the exact source tuple", () => {
  for (const baseBranch of ["main", "release-0.16"]) {
    for (const prNumber of [null, undefined]) {
      for (const workflowSourceRef of [null, undefined]) {
        const input = unmappedEvidence({ baseBranch });
        input.runs = input.runs.map((entry) => ({ ...entry, prNumber, workflowSourceRef }));
        assert.equal(evaluateCI(input), "SUCCESS");
      }
    }
  }
  assert.equal(evaluateCI(evidence({ headRepository: undefined, headBranch: undefined })), "SUCCESS");
});

test("does not infer an absent PR mapping from missing or different source identities", () => {
  for (const override of [
    { headRepository: undefined }, { headRepository: null }, { headRepository: "other/k8s-test-infra" },
    { headBranch: undefined }, { headBranch: null }, { headBranch: "topic/other" },
    { headOid: OLD_HEAD }, { event: "push" }, { event: "pull_request_target" },
    { repository: "other/k8s-test-infra" }, { prNumber: 43 },
    { workflowSourceRef: "refs/pull/43/merge" }, { workflowSourceRef: "refs/heads/main" },
    { workflowPath: ".github/workflows/ci.yaml" },
  ]) {
    const input = unmappedEvidence();
    input.runs[0] = { ...input.runs[0], ...override };
    assert.equal(evaluateCI(input), "PENDING", JSON.stringify(override));
  }
  for (const override of [
    { headRepository: undefined }, { headRepository: null }, { headRepository: "other/k8s-test-infra" },
    { headBranch: undefined }, { headBranch: null }, { headBranch: "topic/other" },
  ]) assert.equal(evaluateCI(unmappedEvidence(override)), "PENDING", JSON.stringify(override));
});

test("latest matching fork run supersedes an older mapped success", () => {
  for (const [status, conclusion, want] of [
    ["completed", "success", "SUCCESS"], ["queued", null, "PENDING"], ["completed", "failure", "FAILED"],
  ]) {
    const input = unmappedEvidence();
    input.runs = [
      ...evidence().runs,
      { ...input.runs[0], id: 20, runNumber: 11, runAttempt: 2, status, conclusion },
    ];
    assert.equal(evaluateCI(input), want);
    assert.equal(evaluateCI({ ...input, runs: [...input.runs].reverse() }), want);
  }
});

test("rejects malformed fork source identities and applicable run state", () => {
  for (const override of [
    { headRepository: "Contributor/k8s-test-infra" }, { headRepository: "" }, { headRepository: "not-a-repository" },
    { headBranch: "" }, { headBranch: "topic\nbranch" }, { headBranch: "topic branch" },
    { headBranch: "topic/../branch" }, { headBranch: "/topic" }, { headBranch: "topic.lock" }, { headBranch: "@" },
  ]) {
    assert.throws(() => evaluateCI(unmappedEvidence(override)), TypeError, JSON.stringify(override));
    const input = unmappedEvidence();
    input.runs[0] = { ...input.runs[0], ...override };
    assert.throws(() => evaluateCI(input), TypeError, JSON.stringify(override));
  }
  for (const override of [{ runNumber: 0 }, { runAttempt: 0 }, { status: "unknown" }, { conclusion: null }]) {
    const input = unmappedEvidence();
    input.runs[0] = { ...input.runs[0], ...override };
    assert.throws(() => evaluateCI(input), TypeError, JSON.stringify(override));
  }
});

test("ignores policy, metadata, review observer and non-applicable source failures", () => {
  const observerRuns = [
    run("basic-checks.yaml", { id: 90, workflowPath: ".github/workflows/merge-evaluate.yml", conclusion: "failure" }),
    run("basic-checks.yaml", { id: 91, workflowPath: ".github/workflows/pr-metadata.yml", conclusion: "action_required" }),
    run("basic-checks.yaml", { id: 92, workflowPath: ".github/workflows/review-observer.yml", conclusion: "failure" }),
    run("helm.yaml", { conclusion: "failure" }),
  ];
  const observerChecks = [
    dco({ id: 200, name: "repository-automation/merge-policy", appId: 15368, conclusion: "action_required" }),
    dco({ id: 201, name: "repository-automation/metadata", appId: 15368, conclusion: "failure" }),
    dco({ id: 202, name: "deploy", appId: 15368, conclusion: "skipped" }),
  ];
  assert.equal(evaluateCI(evidence({ runs: [...evidence().runs, ...observerRuns], checks: [dco(), ...observerChecks] })), "SUCCESS");
  assert.equal(evaluateCI(evidence({ runs: observerRuns, checks: observerChecks })), "PENDING");
});

test("only completed success passes a required workflow or DCO", () => {
  for (const status of ["queued", "in_progress", "waiting", "requested", "pending"]) {
    assert.equal(evaluateCI(evidence({ runs: [run("basic-checks.yaml", { status, conclusion: null }), run("validate-changelog.yaml")] })), "PENDING");
  }
  for (const conclusion of ["failure", "cancelled", "timed_out", "neutral", "skipped", "action_required", "stale", "startup_failure"]) {
    assert.equal(evaluateCI(evidence({ runs: [run("basic-checks.yaml", { conclusion }), run("validate-changelog.yaml")] })), "FAILED", conclusion);
    assert.equal(evaluateCI(evidence({ checks: [dco({ conclusion })] })), "FAILED", conclusion);
  }
  assert.equal(evaluateCI(evidence({ runs: [run("basic-checks.yaml", { conclusion: "failure" })], checks: [] })), "FAILED");
  assert.equal(evaluateCI(evidence({ checks: [dco({ status: "in_progress", conclusion: null })] })), "PENDING");
});

test("selects the newest matching workflow by run number then ID regardless of order", () => {
  for (const previousConclusion of ["failure", "success"]) {
    for (const [status, conclusion, want] of [
      ["completed", "success", "SUCCESS"], ["queued", null, "PENDING"], ["completed", "failure", "FAILED"],
    ]) {
      const runs = [
        run("basic-checks.yaml", { id: 900, runNumber: 9, conclusion: previousConclusion }),
        run("basic-checks.yaml", { id: 50, runNumber: 10, status, conclusion }),
        run("validate-changelog.yaml"),
      ];
      assert.equal(evaluateCI(evidence({ runs })), want);
      assert.equal(evaluateCI(evidence({ runs: [...runs].reverse() })), want);
    }
  }
  assert.equal(evaluateCI(evidence({ runs: [
    run("basic-checks.yaml", { id: 20, runNumber: 10 }),
    run("basic-checks.yaml", { id: 21, runNumber: 10, conclusion: "failure" }), run("validate-changelog.yaml"),
  ] })), "FAILED");
});

test("uses the current rerun attempt without allowing newer unrelated runs to hide it", () => {
  assert.equal(evaluateCI(evidence({ runs: [
    run("basic-checks.yaml", { runAttempt: 2, status: "in_progress", conclusion: null }),
    run("basic-checks.yaml", { id: 30, runNumber: 30, headOid: OLD_HEAD }),
    run("basic-checks.yaml", { id: 31, runNumber: 31, event: "push" }),
    run("basic-checks.yaml", { id: 32, runNumber: 32, prNumber: 43 }), run("validate-changelog.yaml"),
  ] })), "PENDING");
  assert.equal(evaluateCI(evidence({ runs: [run("basic-checks.yaml", { runAttempt: 2, conclusion: "failure" }), run("validate-changelog.yaml")] })), "FAILED");
});

test("uses the latest exact-head DCO check ID and ignores old failures", () => {
  for (const [status, conclusion, want] of [
    ["completed", "success", "SUCCESS"], ["in_progress", null, "PENDING"], ["completed", "failure", "FAILED"],
  ]) {
    const checks = [
      dco({ id: 101, status, conclusion }), dco({ id: 100, conclusion: "failure" }),
      dco({ id: 102, appId: 15368 }), dco({ id: 103, headOid: OLD_HEAD }),
    ];
    assert.equal(evaluateCI(evidence({ checks })), want);
    assert.equal(evaluateCI(evidence({ checks: [...checks].reverse() })), want);
  }
});

test("requires conditional workflows for changed paths and both sides of renames", () => {
  const cases = [
    [".github/actions/repo-automation/src/index.js", ["automation-ci.yml"]],
    [".github/scripts/cherrypick/backport.js", ["automation-ci.yml"]],
    [".github/repo-automation/policy.yml", ["automation-ci.yml"]], [".github/workflows/new.yml", ["automation-ci.yml"]],
    ["hack/actionlint.sh", ["automation-ci.yml"]], ["OWNERS", ["automation-ci.yml"]], ["OWNERS_ALIASES", ["automation-ci.yml"]],
    ["deployments/nvml-mock/helm/Chart.yaml", ["helm.yaml"]], ["deployments/mokka-crds/helm/templates/crd.yaml", ["helm.yaml"]],
    ["go.mod", ["dependency-integrity.yaml"]], ["go.sum", ["dependency-integrity.yaml"]],
    ["docs/general/overview.md", ["deploy-pages.yaml"]], ["docs/.internal/file.md", ["deploy-pages.yaml"]],
    ["mkdocs.yml", ["deploy-pages.yaml"]], ["requirements-docs.txt", ["deploy-pages.yaml"]],
    ["Makefile", ["automation-ci.yml", "dependency-integrity.yaml", "deploy-pages.yaml"]],
    [".github/workflows/dependency-integrity.yaml", ["automation-ci.yml", "dependency-integrity.yaml"]],
    [".github/workflows/deploy-pages.yaml", ["automation-ci.yml", "deploy-pages.yaml"]],
    ["README.md", []], ["deployments/nvml-mock/manifests/deployment.yaml", []], ["hack/other.sh", []],
  ];
  for (const [changedPath, required] of cases) {
    const runs = [...evidence().runs, ...required.map((workflow) => run(workflow))];
    assert.equal(evaluateCI(evidence({ files: [{ path: changedPath }], runs })), "SUCCESS", changedPath);
    for (const workflow of required) {
      const missing = runs.filter((entry) => entry.workflowPath !== `.github/workflows/${workflow}`);
      assert.equal(evaluateCI(evidence({ files: [{ path: changedPath }], runs: missing })), "PENDING", `${changedPath}: ${workflow}`);
      assert.equal(evaluateCI(evidence({ files: [{ path: "archive/moved.txt", previousPath: changedPath }], runs: missing })), "PENDING", `renamed ${changedPath}: ${workflow}`);
    }
  }
  assert.equal(evaluateCI(evidence({ files: [] })), "SUCCESS");
});

test("documentation PRs use aggregate workflow success despite a skipped deployment job", () => {
  assert.equal(evaluateCI(evidence({
    files: [{ path: "docs/guide.md" }], runs: [...evidence().runs, run("deploy-pages.yaml")],
    checks: [dco(), dco({ id: 101, appId: 15368, name: "deploy", conclusion: "skipped" })],
  })), "SUCCESS");
});

test("required workflow behavior follows tracked pull_request triggers", () => {
  const workflowRoot = path.resolve(__dirname, "../../../workflows");
  const probes = ["pkg/code.go", "README.md", "docs/.hidden/change.md", "Makefile"];
  const definitions = WORKFLOWS.map((workflow) => ({
    workflow, trigger: YAML.parse(fs.readFileSync(path.join(workflowRoot, workflow), "utf8")).on.pull_request,
  }));
  // Probe both sides, so a path added to only the trigger or only the policy fails.
  const patterns = [
    ...definitions.flatMap(({ trigger }) => trigger.paths || []),
    ...requiredCI.workflows.flatMap(({ files }) => files || []),
  ];
  for (const pattern of patterns) probes.push(pattern.replaceAll("**", "nested/changed.txt").replaceAll("*", "change"));
  const options = { dot: true, nocomment: true, nonegate: true };
  for (const baseBranch of ["main", "release-0.16"]) {
    for (const changedPath of new Set(probes)) {
      for (const { workflow, trigger } of definitions) {
        const branchesMatch = !trigger.branches || trigger.branches.some((pattern) => minimatch(baseBranch, pattern, options));
        const pathsMatch = !trigger.paths || trigger.paths.some((pattern) => minimatch(changedPath, pattern, options));
        assert.equal(evaluateCI(evidence({
          baseBranch, files: [{ path: changedPath }], runs: WORKFLOWS.filter((name) => name !== workflow).map((name) => run(name)),
        })), branchesMatch && pathsMatch ? "PENDING" : "SUCCESS", `${baseBranch}: ${changedPath}: ${workflow}`);
      }
    }
  }
});

test("rejects invalid identities, unsafe paths, oversized and sparse collections", () => {
  for (const override of [
    { repository: "NVIDIA/k8s-test-infra" }, { repository: "attacker/k8s-test-infra" }, { prNumber: 0 },
    { prNumber: Number.MAX_SAFE_INTEGER + 1 }, { headOid: "invalid" }, { baseBranch: "main\n" },
    { files: null }, { runs: null }, { checks: null }, { files: Array(1) }, { runs: Array(1) }, { checks: Array(1) },
    { files: Array(MAX_CHANGED_FILES + 1).fill({ path: "pkg/code.go" }) },
    { runs: Array(MAX_API_COLLECTION_ITEMS + 1).fill(run("basic-checks.yaml")) },
    { checks: Array(MAX_API_COLLECTION_ITEMS + 1).fill(dco()) },
  ]) assert.throws(() => evaluateCI(evidence(override)), TypeError);
  for (const invalidPath of ["", "/docs/guide.md", "../docs/guide.md", "docs//guide.md", "docs/./guide.md", "docs\\guide.md", "docs/guide\u0000.md"]) {
    assert.throws(() => evaluateCI(evidence({ files: [{ path: invalidPath }] })), TypeError);
    assert.throws(() => evaluateCI(evidence({ files: [{ path: "pkg/code.go", previousPath: invalidPath }] })), TypeError);
  }
});

test("rejects duplicate IDs even when older evidence would pass", () => {
  assert.throws(() => evaluateCI(evidence({ runs: [...evidence().runs, run("basic-checks.yaml", { runAttempt: 2, status: "queued", conclusion: null })] })), TypeError);
  assert.throws(() => evaluateCI(evidence({ checks: [dco(), dco({ conclusion: "failure" })] })), TypeError);
});

test("rejects malformed applicable evidence instead of falling back to old success", () => {
  for (const override of [
    { id: 0 }, { runNumber: 0 }, { runNumber: "11" }, { runAttempt: 0 }, { status: "unknown" },
    { conclusion: "unknown" }, { conclusion: null }, { status: "queued", conclusion: "success" },
    { workflowSourceRef: "" }, { workflowPath: null }, { headOid: "unknown" },
  ]) {
    assert.throws(() => evaluateCI(evidence({ runs: [...evidence().runs, run("basic-checks.yaml", { id: 10, runNumber: 11, ...override })] })), TypeError, JSON.stringify(override));
  }
  for (const override of [{ id: 0 }, { appId: 0 }, { name: "" }, { headOid: "invalid" }, { status: "unknown" }, { conclusion: null }, { conclusion: "unknown" }]) {
    assert.throws(() => evaluateCI(evidence({ checks: [dco(override)] })), TypeError);
  }
});

test("rejects accessors without running untrusted code", () => {
  let calls = 0;
  for (const field of ["headOid", "status", "runNumber"]) {
    const malformed = run("basic-checks.yaml");
    Object.defineProperty(malformed, field, { enumerable: true, get() { calls += 1; return field === "headOid" ? HEAD : "success"; } });
    assert.throws(() => evaluateCI(evidence({ runs: [malformed, run("validate-changelog.yaml")] })), TypeError);
  }
  const malformedInput = evidence();
  Object.defineProperty(malformedInput, "checks", { enumerable: true, get() { calls += 1; return [dco()]; } });
  assert.throws(() => evaluateCI(malformedInput), TypeError);
  assert.equal(calls, 0);
});
