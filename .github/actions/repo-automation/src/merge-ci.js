"use strict";

const { minimatch } = require("minimatch");
const { MAX_API_COLLECTION_ITEMS, MAX_CHANGED_FILES } = require("./limits.js");

const REPOSITORY = "nvidia/k8s-test-infra";
const OID = /^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$/;
const UNSAFE_TEXT = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;
const STATUSES = new Set(["queued", "in_progress", "completed", "waiting", "requested", "pending"]);
const CONCLUSIONS = new Set([
  "success", "failure", "cancelled", "timed_out", "neutral", "skipped",
  "action_required", "stale", "startup_failure",
]);
const MATCH_OPTIONS = Object.freeze({ dot: true, nocomment: true, nonegate: true });

// Behavioral contract tests compare these expectations with the tracked PR triggers.
const WORKFLOWS = [
  { path: ".github/workflows/basic-checks.yaml" },
  { path: ".github/workflows/validate-changelog.yaml" },
  {
    path: ".github/workflows/automation-ci.yml",
    files: [
      ".github/actions/repo-automation/**", ".github/repo-automation/**", ".github/workflows/**",
      "hack/actionlint.sh", "Makefile", "OWNERS", "OWNERS_ALIASES",
    ],
  },
  {
    path: ".github/workflows/helm.yaml",
    files: ["deployments/nvml-mock/helm/**", "deployments/mokka-crds/helm/**"],
  },
  {
    path: ".github/workflows/dependency-integrity.yaml",
    files: ["go.mod", "go.sum", "Makefile", ".github/workflows/dependency-integrity.yaml"],
  },
  {
    path: ".github/workflows/deploy-pages.yaml",
    files: ["docs/**", "mkdocs.yml", "requirements-docs.txt", "Makefile", ".github/workflows/deploy-pages.yaml"],
  },
];

function captureRecord(value, field) {
  if (value === null || typeof value !== "object" || Object.getPrototypeOf(value) !== Object.prototype) {
    throw new TypeError(`${field} must be a plain record`);
  }
  const keys = Reflect.ownKeys(value);
  if (keys.length > 32) throw new TypeError(`${field} has too many fields`);
  const captured = Object.create(null);
  for (const key of keys) {
    const descriptor = Object.getOwnPropertyDescriptor(value, key);
    if (typeof key !== "string" || descriptor === undefined || !Object.hasOwn(descriptor, "value")) {
      throw new TypeError(`${field} must contain own data fields`);
    }
    captured[key] = descriptor.value;
  }
  return captured;
}

function captureCollection(value, maximum, field) {
  if (!Array.isArray(value) || Object.getPrototypeOf(value) !== Array.prototype) {
    throw new TypeError(`${field} must be an ordinary array`);
  }
  const length = Object.getOwnPropertyDescriptor(value, "length")?.value;
  if (!Number.isSafeInteger(length) || length < 0 || length > maximum) {
    throw new TypeError(`${field} exceeds the collection bound`);
  }
  if (Reflect.ownKeys(value).length !== length + 1) {
    throw new TypeError(`${field} must be a dense array`);
  }
  const captured = [];
  for (let index = 0; index < length; index += 1) {
    const descriptor = Object.getOwnPropertyDescriptor(value, String(index));
    if (descriptor === undefined || !descriptor.enumerable || !Object.hasOwn(descriptor, "value")) {
      throw new TypeError(`${field} must contain indexed data values`);
    }
    captured.push(captureRecord(descriptor.value, field));
  }
  return captured;
}

function positiveInteger(value, field) {
  if (!Number.isSafeInteger(value) || value <= 0) throw new TypeError(`${field} must be a positive safe integer`);
  return value;
}

function safeText(value, maximum, field) {
  if (typeof value !== "string" || value.length === 0 || value.length > maximum || UNSAFE_TEXT.test(value)) {
    throw new TypeError(`${field} must be bounded safe text`);
  }
  return value;
}

function headOid(value) {
  if (typeof value !== "string" || !OID.test(value)) throw new TypeError("CI head must be a Git OID");
  return value.toLowerCase();
}

function normalizedRepository(value, field) {
  safeText(value, 140, field);
  if (!/^[a-z0-9][a-z0-9-]{0,38}\/[a-z0-9._-]{1,100}$/.test(value)) {
    throw new TypeError(`${field} must be normalized`);
  }
  return value;
}

function sourceBranch(value) {
  safeText(value, 255, "CI source branch");
  if (value === "@" || value === "HEAD" || value.startsWith("-") || value.startsWith("/") || value.endsWith(".") || value.endsWith("/")
    || value.includes("..") || value.includes("@{") || value.includes("//")
    || /(?:^|\/)\./.test(value) || /[ ~^:?*[\]\\]/.test(value)
    || value.split("/").some((segment) => segment.endsWith(".lock"))) {
    throw new TypeError("CI source branch must be a valid branch name");
  }
  return value;
}

function repositoryPath(value) {
  safeText(value, 4096, "CI path");
  if (value.includes("\\") || value.split("/").some((segment) => segment === "" || segment === "." || segment === "..")) {
    throw new TypeError("CI path must be repository-relative without traversal");
  }
  return value;
}

function uniqueId(record, seen) {
  positiveInteger(record.id, "CI evidence ID");
  if (seen.has(record.id)) throw new TypeError("CI evidence IDs must be unique");
  seen.add(record.id);
}

function evidenceState(record) {
  if (!STATUSES.has(record.status)) throw new TypeError("CI status is invalid");
  if (record.status !== "completed") {
    if (record.conclusion !== null) throw new TypeError("Active CI evidence must have no conclusion");
    return "PENDING";
  }
  if (!CONCLUSIONS.has(record.conclusion)) throw new TypeError("Completed CI evidence must have a valid conclusion");
  return record.conclusion === "success" ? "SUCCESS" : "FAILED";
}

function evaluateCI(input) {
  const state = captureRecord(input, "CI input");
  if (state.repository !== REPOSITORY) throw new TypeError("CI repository must be the normalized managed repository");
  positiveInteger(state.prNumber, "CI pull request number");
  const head = headOid(state.headOid);
  const baseBranch = safeText(state.baseBranch, 255, "CI base branch");
  const headRepository = state.headRepository == null ? null : normalizedRepository(state.headRepository, "CI head repository");
  const headBranch = state.headBranch == null ? null : sourceBranch(state.headBranch);
  const files = captureCollection(state.files, MAX_CHANGED_FILES, "CI files");
  const runs = captureCollection(state.runs, MAX_API_COLLECTION_ITEMS, "CI runs");
  const checks = captureCollection(state.checks, MAX_API_COLLECTION_ITEMS, "CI checks");
  const paths = [];
  for (const file of files) {
    paths.push(repositoryPath(file.path));
    if (file.previousPath !== undefined) paths.push(repositoryPath(file.previousPath));
  }
  const supportedBranch = baseBranch === "main" || minimatch(baseBranch, "release-*", MATCH_OPTIONS);
  const required = new Map(WORKFLOWS.filter((workflow) => supportedBranch && (
    workflow.files === undefined
    || paths.some((path) => workflow.files.some((pattern) => minimatch(path, pattern, MATCH_OPTIONS)))
  )).map((workflow) => [workflow.path, null]));

  const runIds = new Set();
  for (const run of runs) {
    uniqueId(run, runIds);
    const runHead = headOid(run.headOid);
    const repository = normalizedRepository(run.repository, "CI run repository");
    const runHeadRepository = run.headRepository == null ? null : normalizedRepository(run.headRepository, "CI run head repository");
    const runHeadBranch = run.headBranch == null ? null : sourceBranch(run.headBranch);
    const workflowPath = repositoryPath(run.workflowPath);
    safeText(run.event, 100, "CI run event");
    const hasPrMapping = run.prNumber !== null && run.prNumber !== undefined;
    if (hasPrMapping) positiveInteger(run.prNumber, "CI run PR number");
    if (run.workflowSourceRef !== null && run.workflowSourceRef !== undefined) {
      safeText(run.workflowSourceRef, 256, "CI workflow source ref");
    }
    // GitHub can omit PR mappings for fork runs; these runs remain scoped to the source head.
    const matchesPr = hasPrMapping ? run.prNumber === state.prNumber
      : headRepository !== null && headBranch !== null && runHeadRepository === headRepository && runHeadBranch === headBranch;
    if (runHead !== head || repository !== state.repository || !matchesPr
      || run.event !== "pull_request" || !required.has(workflowPath)
      || (run.workflowSourceRef !== null && run.workflowSourceRef !== undefined
        && run.workflowSourceRef !== `refs/pull/${state.prNumber}/merge`)) continue;

    positiveInteger(run.runNumber, "CI run number");
    // The API reader supplies the current attempt for each unique run ID.
    positiveInteger(run.runAttempt, "CI run attempt");
    evidenceState(run);
    const latest = required.get(workflowPath);
    if (latest === null || run.runNumber > latest.runNumber
      || (run.runNumber === latest.runNumber && run.id > latest.id)) required.set(workflowPath, run);
  }

  let latestDco = null;
  const checkIds = new Set();
  for (const check of checks) {
    uniqueId(check, checkIds);
    const checkHead = headOid(check.headOid);
    safeText(check.name, 255, "CI check name");
    positiveInteger(check.appId, "CI check app ID");
    if (checkHead !== head || check.name !== "DCO" || check.appId !== 1861) continue;
    evidenceState(check);
    if (latestDco === null || check.id > latestDco.id) latestDco = check;
  }
  if (!supportedBranch) return "PENDING";
  const states = [...required.values(), latestDco].map((record) => record === null ? "PENDING" : evidenceState(record));
  if (states.includes("FAILED")) return "FAILED";
  return states.every((result) => result === "SUCCESS") ? "SUCCESS" : "PENDING";
}

module.exports = { evaluateCI };
