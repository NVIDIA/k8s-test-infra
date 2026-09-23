"use strict";

const { createHash } = require("node:crypto");

const { validateConfig } = require("../config.js");
const { gitIsAncestor } = require("../git.js");

const BACKPORT_EVIDENCE_MARKER = "<!-- repo-automation-backport:v1 -->";
const GIT_OID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;
const SAFE_REPOSITORY = /^[a-z0-9](?:[a-z0-9._-]{0,99})\/[a-z0-9](?:[a-z0-9._-]{0,99})$/;
const SAFE_BRANCH = /^(?!-)(?!.*(?:\.\.|@\{|\/\/|\\|[\x00-\x20\x7f~^:?*\[]))[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$/;
const SAFE_LOGIN = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$/;

function positiveIntegerInput(value) {
  if (typeof value !== "string" || !/^[1-9][0-9]*$/.test(value)) {
    throw new TypeError("pr-number must be a canonical positive integer");
  }
  const number = Number(value);
  if (!Number.isSafeInteger(number)) {
    throw new TypeError("pr-number must be a canonical positive integer");
  }
  return number;
}

function normalizedRepository(value) {
  if (typeof value !== "string" || !SAFE_REPOSITORY.test(value.toLowerCase())) {
    throw new TypeError("repository must be an owner/name identity");
  }
  return value.toLowerCase();
}

function branchAllowed(branch, patterns) {
  if (typeof branch !== "string" || !SAFE_BRANCH.test(branch)) return false;
  return patterns.some((pattern) => (
    pattern === branch
    || (pattern.endsWith("*") && branch.startsWith(pattern.slice(0, -1)))
  ));
}

function backportBranchName(prNumber, targetBranch) {
  const readableTarget = targetBranch
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48) || "target";
  const digest = createHash("sha256")
    .update(`${prNumber}\0${targetBranch}`, "utf8")
    .digest("hex")
    .slice(0, 12);
  return `backport/${prNumber}-to-${readableTarget}-${digest}`;
}

function evidenceFields(input) {
  return {
    schemaVersion: 1,
    repository: input.repository,
    sourcePullRequest: input.sourcePullRequest,
    sourceCommit: input.sourceCommit,
    targetBranch: input.targetBranch,
    backportBranch: input.backportBranch,
  };
}

function backportPullRequestBody(input) {
  return [
    BACKPORT_EVIDENCE_MARKER,
    "```json",
    JSON.stringify(evidenceFields(input)),
    "```",
    "",
    `Automated backport of #${input.sourcePullRequest} to \`${input.targetBranch}\`.`,
    "",
    `Original author: @${input.author}`,
    "",
    "This pull request is subject to the normal review and merge policy.",
    "",
  ].join("\n");
}

function liveMergedPullRequest(value, prNumber, repository) {
  if (
    value === null
    || typeof value !== "object"
    || Array.isArray(value)
    || value.number !== prNumber
    || value.merged !== true
    || value.state !== "closed"
    || typeof value.mergeCommitOid !== "string"
    || !GIT_OID.test(value.mergeCommitOid.toLowerCase())
  ) {
    throw new Error("source pull request must be merged with a valid merge commit");
  }
  const liveRepository = `${value.baseRepository?.owner}/${value.baseRepository?.repo}`.toLowerCase();
  if (liveRepository !== repository) throw new Error("source pull request repository does not match");
  if (
    typeof value.title !== "string"
    || value.title === ""
    || typeof value.author !== "string"
    || !SAFE_LOGIN.test(value.author)
    || typeof value.headOid !== "string"
    || typeof value.baseBranch !== "string"
    || value.baseBranch === ""
  ) {
    throw new Error("source pull request state is invalid");
  }
  return { ...value, mergeCommitOid: value.mergeCommitOid.toLowerCase() };
}

function sameSource(left, right) {
  return left.number === right.number
    && left.title === right.title
    && left.author.toLowerCase() === right.author.toLowerCase()
    && left.headOid.toLowerCase() === right.headOid.toLowerCase()
    && left.baseBranch === right.baseBranch
    && left.mergeCommitOid === right.mergeCommitOid;
}

function result(input, outcome, pullRequest) {
  const summary = {
    status: "complete",
    outcome,
    sourcePullRequest: input.sourcePullRequest,
    sourceCommit: input.sourceCommit,
    targetBranch: input.targetBranch,
    backportBranch: input.backportBranch,
  };
  if (pullRequest !== undefined) {
    summary.backportPullRequest = { number: pullRequest.number, url: pullRequest.url };
  }
  return summary;
}

function exactExistingPullRequest(pullRequest, expected) {
  return pullRequest !== null
    && pullRequest.state === "open"
    && pullRequest.base === expected.base
    && pullRequest.head === expected.head
    && pullRequest.title === expected.title
    && pullRequest.body === expected.body;
}

function cherryPickArguments(revisionLine, sourceCommit) {
  const revisions = revisionLine.trim().split(/\s+/);
  if (revisions[0] !== sourceCommit || revisions.some((revision) => !GIT_OID.test(revision))) {
    throw new Error("source commit ancestry is invalid");
  }
  const parentCount = revisions.length - 1;
  if (parentCount > 1) return ["cherry-pick", "-m", "1", "-x", sourceCommit];
  return ["cherry-pick", "-x", sourceCommit];
}

async function abortCherryPick(git) {
  try {
    await git(["cherry-pick", "--abort"]);
  } catch (error) {
    throw new Error("cherry-pick cleanup failed", { cause: error });
  }
}

async function cleanupFailedCreation({ github, git, backportBranch, targetBranch, backportCommit }) {
  try {
    const [pullRequest, branch] = await Promise.all([
      github.findOpenBackportPullRequest(backportBranch, targetBranch),
      github.getBranch(backportBranch),
    ]);
    if (
      pullRequest !== null
      || branch?.name !== backportBranch
      || branch?.oid !== backportCommit
    ) throw new Error("cleanup lease state is not exact");
    await git([
      "push",
      "--porcelain",
      "--atomic",
      `--force-with-lease=refs/heads/${backportBranch}:${backportCommit}`,
      "origin",
      `:refs/heads/${backportBranch}`,
    ]);
  } catch (error) {
    throw new Error(
      `manual investigation required: BACKPORT_MANUAL_INVESTIGATION branch=${backportBranch}`,
      { cause: error },
    );
  }
}

async function runBackport({
  github,
  git,
  config,
  dryRun,
  prNumber: prNumberInput,
  targetBranch,
  repository: repositoryInput,
}) {
  if (typeof github !== "object" || typeof git !== "function" || typeof dryRun !== "boolean") {
    throw new TypeError("backport mode dependencies are invalid");
  }
  validateConfig(config);
  const prNumber = positiveIntegerInput(prNumberInput);
  const repository = normalizedRepository(repositoryInput);
  if (!branchAllowed(targetBranch, config.policy.commands.backportBranches)) {
    throw new Error(`target branch is not allowed: ${targetBranch}`);
  }

  const plannedSource = liveMergedPullRequest(
    await github.getPullRequest(prNumber),
    prNumber,
    repository,
  );
  if (targetBranch === plannedSource.baseBranch) {
    throw new Error("target branch must differ from the source base branch");
  }
  const target = await github.getBranch(targetBranch);
  if (target === null) throw new Error(`target branch does not exist: ${targetBranch}`);
  if (target.name !== targetBranch || typeof target.oid !== "string" || !GIT_OID.test(target.oid)) {
    throw new Error("target branch state is invalid");
  }

  const backportBranch = backportBranchName(prNumber, targetBranch);
  const expected = {
    base: targetBranch,
    head: backportBranch,
    title: `[${targetBranch}] ${plannedSource.title}`,
    body: backportPullRequestBody({
      repository,
      sourcePullRequest: prNumber,
      sourceCommit: plannedSource.mergeCommitOid,
      targetBranch,
      backportBranch,
      author: plannedSource.author,
    }),
  };
  const [existingBranch, existingPullRequest] = await Promise.all([
    github.getBranch(backportBranch),
    github.findOpenBackportPullRequest(backportBranch, targetBranch),
  ]);
  if (existingBranch !== null || existingPullRequest !== null) {
    if (existingBranch !== null && exactExistingPullRequest(existingPullRequest, expected)) {
      const [duplicateSourceValue, duplicateTarget, duplicateBranch, duplicatePullRequest] = await Promise.all([
        github.getPullRequest(prNumber),
        github.getBranch(targetBranch),
        github.getBranch(backportBranch),
        github.findOpenBackportPullRequest(backportBranch, targetBranch),
      ]);
      const duplicateSource = liveMergedPullRequest(
        duplicateSourceValue,
        prNumber,
        repository,
      );
      if (
        !sameSource(plannedSource, duplicateSource)
        || duplicateTarget?.name !== target.name
        || duplicateTarget?.oid !== target.oid
        || duplicateBranch?.name !== existingBranch.name
        || duplicateBranch?.oid !== existingBranch.oid
        || !exactExistingPullRequest(duplicatePullRequest, expected)
        || duplicatePullRequest.number !== existingPullRequest.number
        || duplicatePullRequest.url !== existingPullRequest.url
      ) {
        throw new Error("source or existing backport state changed during duplicate verification");
      }
      return result({
        sourcePullRequest: prNumber,
        sourceCommit: plannedSource.mergeCommitOid,
        targetBranch,
        backportBranch,
      }, "already-exists", existingPullRequest);
    }
    throw new Error("backport branch or pull request collision");
  }

  if (dryRun) {
    return {
      status: "planned",
      outcome: "create",
      sourcePullRequest: prNumber,
      sourceCommit: plannedSource.mergeCommitOid,
      targetBranch,
      backportBranch,
    };
  }

  const fencedSource = liveMergedPullRequest(
    await github.getPullRequest(prNumber),
    prNumber,
    repository,
  );
  const fencedTarget = await github.getBranch(targetBranch);
  const fencedBackport = await github.getBranch(backportBranch);
  if (
    !sameSource(plannedSource, fencedSource)
    || fencedTarget === null
    || fencedTarget.oid !== target.oid
    || fencedBackport !== null
  ) {
    throw new Error("source or target state changed before backport execution");
  }

  await git(["fetch", "--no-tags", "origin", target.oid]);
  await git(["fetch", "--no-tags", "origin", plannedSource.mergeCommitOid]);
  const ancestry = await git([
    "rev-list", "--parents", "--max-count=1", plannedSource.mergeCommitOid,
  ]);
  const cherryPick = cherryPickArguments(ancestry.stdout, plannedSource.mergeCommitOid);
  await git(["switch", "--create", backportBranch, target.oid]);
  await git(["config", "--local", "user.name", "github-actions[bot]"]);
  await git([
    "config",
    "--local",
    "user.email",
    "41898282+github-actions[bot]@users.noreply.github.com",
  ]);
  try {
    await git(cherryPick);
  } catch (error) {
    await abortCherryPick(git);
    if (await gitIsAncestor(git, plannedSource.mergeCommitOid, target.oid)) {
      return result({
        sourcePullRequest: prNumber,
        sourceCommit: plannedSource.mergeCommitOid,
        targetBranch,
        backportBranch,
      }, "already-contained");
    }
    throw new Error("backport cherry-pick conflict; manual resolution is required", { cause: error });
  }

  const backportCommit = (await git(["rev-parse", "HEAD"])).stdout.trim().toLowerCase();
  if (!GIT_OID.test(backportCommit) || backportCommit === target.oid) {
    throw new Error("backport commit is invalid");
  }
  await git([
    "push",
    "--porcelain",
    "--atomic",
    `--force-with-lease=refs/heads/${backportBranch}:`,
    "origin",
    `HEAD:refs/heads/${backportBranch}`,
  ]);
  let created;
  try {
    created = await github.createBackportPullRequest(expected);
  } catch (error) {
    await cleanupFailedCreation({
      github,
      git,
      backportBranch,
      targetBranch,
      backportCommit,
    });
    throw new Error("pull request creation failed", { cause: error });
  }
  return result({
    sourcePullRequest: prNumber,
    sourceCommit: plannedSource.mergeCommitOid,
    targetBranch,
    backportBranch,
  }, "created", created);
}

module.exports = {
  BACKPORT_EVIDENCE_MARKER,
  backportBranchName,
  backportPullRequestBody,
  runBackport,
};
