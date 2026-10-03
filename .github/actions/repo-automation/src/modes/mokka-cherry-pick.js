"use strict";

const {
  createMokkaEvidence,
  parseMokkaEvidence,
} = require("../mokka-evidence.js");
const { gitIsAncestor } = require("../git.js");

const EXPECTED_REPOSITORY = "NVIDIA/k8s-test-infra";
const EXPECTED_REPOSITORY_ID = "733665780";
const EXPECTED_TARGET_BRANCH = "main";
const MAX_PULL_REQUEST_NUMBER = 2147483647;
const MAX_TARGET_ATTEMPTS = 3;
const GIT_OID = /^[0-9a-f]{40}$/;
const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const ACTION_MARKER_PREFIX = "mokka-cherry-pick-action-id";

function canonicalPullRequestNumber(value) {
  if (typeof value !== "string" || !/^[1-9][0-9]*$/.test(value)) {
    throw new TypeError("invalid pull request number");
  }
  const number = Number(value);
  if (!Number.isInteger(number) || number > MAX_PULL_REQUEST_NUMBER) {
    throw new TypeError("invalid pull request number");
  }
  return number;
}

function requireOid(value, name) {
  if (typeof value !== "string" || !GIT_OID.test(value)) {
    throw new TypeError(`invalid ${name}`);
  }
  return value;
}

function repositoryIdentity(value) {
  return `${value?.owner}/${value?.repo}`.toLowerCase();
}

function mokkaBranchName(actionId) {
  if (typeof actionId !== "string" || !UUID_V4.test(actionId)) {
    throw new TypeError("invalid action ID");
  }
  return `mokka/cherry-pick/${actionId}`;
}

function mokkaPullRequestTitle(prNumber, targetBranch) {
  return `Mokka: cherry-pick #${prNumber} to ${targetBranch}`;
}

function validateInvocation(input) {
  if (input === null || typeof input !== "object" || Array.isArray(input)) {
    throw new TypeError("invalid Mokka invocation");
  }
  if (input.github === null || typeof input.github !== "object" || typeof input.git !== "function") {
    throw new TypeError("invalid Mokka dependencies");
  }
  if (input.dryRun !== false) throw new TypeError("Mokka cherry-pick must not run in dry-run mode");
  const prNumber = canonicalPullRequestNumber(input.prNumber);
  const sourceSha = requireOid(input.sourceSha, "source SHA");
  if (input.targetBranch !== EXPECTED_TARGET_BRANCH) throw new TypeError("invalid target branch");
  const headBranch = mokkaBranchName(input.actionId);
  const uploadBranch = `mokka/cherry-pick-upload/${input.actionId}`;
  if (input.repository !== EXPECTED_REPOSITORY) throw new TypeError("invalid repository");
  if (input.repositoryId !== EXPECTED_REPOSITORY_ID) throw new TypeError("invalid repository ID");
  const workflowSha = requireOid(input.workflowSha, "workflow SHA");
  return {
    actionId: input.actionId,
    github: input.github,
    git: input.git,
    headBranch,
    uploadBranch,
    prNumber,
    repository: input.repository,
    sourceSha,
    targetBranch: input.targetBranch,
    workflowSha,
  };
}

function eligibleSource(value, expected) {
  const repository = EXPECTED_REPOSITORY.toLowerCase();
  if (
    value === null
    || typeof value !== "object"
    || Array.isArray(value)
    || value.number !== expected.prNumber
    || value.state !== "closed"
    || value.merged !== true
    || value.mergeCommitOid !== expected.sourceSha
    || value.baseBranch === expected.targetBranch
    || repositoryIdentity(value.baseRepository) !== repository
    || repositoryIdentity(value.headRepository) !== repository
  ) throw new Error("source pull request is not eligible");
  return {
    number: value.number,
    state: value.state,
    merged: value.merged,
    mergeCommitOid: value.mergeCommitOid,
    baseBranch: value.baseBranch,
    baseRepository: {
      owner: value.baseRepository.owner,
      repo: value.baseRepository.repo,
    },
    headRepository: {
      owner: value.headRepository.owner,
      repo: value.headRepository.repo,
    },
  };
}

function exactSource(left, right) {
  return JSON.stringify(left) === JSON.stringify(right);
}

function canonicalCommit(value, sourceSha) {
  if (
    value === null
    || typeof value !== "object"
    || Array.isArray(value)
    || value.sha !== sourceSha
    || !Array.isArray(value.parents)
    || value.parents.length !== 1
    || !GIT_OID.test(value.parents[0])
  ) throw new Error("source commit must match the requested SHA and have one canonical parent");
  return value;
}

function canonicalBranch(value, name) {
  if (value === null || value?.name !== name || !GIT_OID.test(value?.oid)) {
    throw new Error(`invalid branch state: ${name}`);
  }
  return { name: value.name, oid: value.oid };
}

async function requireTargetFastForward(git, previousOid, nextOid) {
  if (previousOid === nextOid) return;
  let isAncestor;
  try {
    isAncestor = await gitIsAncestor(git, previousOid, nextOid);
  } catch (error) {
    throw new Error("target branch history changed or could not be verified before write", { cause: error });
  }
  if (!isAncestor) throw new Error("target branch history changed before write");
}

function summary(input, outcome, pullRequest) {
  return {
    status: "complete",
    outcome,
    sourcePullRequest: input.prNumber,
    sourceCommit: input.sourceSha,
    targetBranch: input.targetBranch,
    headBranch: input.headBranch,
    pullRequest: { number: pullRequest.number, url: pullRequest.url },
  };
}

function actionMarker(actionId) {
  return `<!-- ${ACTION_MARKER_PREFIX}: ${actionId} -->`;
}

function exactResultPullRequest(pullRequest, input, branchOid) {
  if (
    pullRequest === null
    || typeof pullRequest !== "object"
    || pullRequest.state !== "open"
    || pullRequest.draft !== true
    || pullRequest.base !== input.targetBranch
    || pullRequest.head !== input.headBranch
    || pullRequest.headOid !== branchOid
    || pullRequest.title !== mokkaPullRequestTitle(input.prNumber, input.targetBranch)
    || !Number.isInteger(pullRequest.number)
    || pullRequest.number < 1
    || pullRequest.number > MAX_PULL_REQUEST_NUMBER
    || pullRequest.url !== `https://github.com/${EXPECTED_REPOSITORY}/pull/${pullRequest.number}`
  ) return false;
  const evidence = parseMokkaEvidence(pullRequest.body);
  return evidence !== null
    && evidence.actionId === input.actionId
    && evidence.sourcePullRequest === input.prNumber
    && evidence.sourceSha === input.sourceSha
    && evidence.targetBranch === input.targetBranch
    && GIT_OID.test(evidence.targetBaseSha)
    && evidence.producedHeadSha === branchOid
    && evidence.workflowCommitSha === input.workflowSha
    && evidence.headBranch === input.headBranch
    && evidence.pullRequestUrl === pullRequest.url;
}

function canonicalMessage(value) {
  return typeof value === "string" ? value.replace(/\n+$/u, "") : null;
}

function exactMokkaTrailers(message, input) {
  const canonical = canonicalMessage(message);
  if (canonical === null) return false;
  const lines = canonical.split("\n");
  const trailerLines = lines.filter((line) => /^[ \t]*mokka-(?:source-sha|action-id)\s*:/iu.test(line));
  return trailerLines.length === 2
    && lines.at(-2) === `Mokka-Source-SHA: ${input.sourceSha}`
    && lines.at(-1) === `Mokka-Action-ID: ${input.actionId}`;
}

function exactProducedCommit(value, branchOid, targetBaseSha, input) {
  return value !== null
    && typeof value === "object"
    && !Array.isArray(value)
    && value.sha === branchOid
    && Array.isArray(value.parents)
    && value.parents.length === 1
    && value.parents[0] === targetBaseSha
    && GIT_OID.test(value.tree)
    && exactMokkaTrailers(value.message, input)
    && value.verification?.verified === true
    && value.verification?.hasSignature === true;
}

async function existingResult(input, source, branch, pullRequests) {
  if (branch === null && pullRequests.length === 0) return null;
  if (
    branch === null
    || branch.name !== input.headBranch
    || !GIT_OID.test(branch.oid)
    || pullRequests.length !== 1
    || !exactResultPullRequest(pullRequests[0], input, branch.oid)
  ) throw new Error("derived Mokka branch or pull request collision");
  const targetBaseSha = parseMokkaEvidence(pullRequests[0].body).targetBaseSha;
  const producedCommit = await input.github.getMokkaCommit(branch.oid);
  if (!exactProducedCommit(producedCommit, branch.oid, targetBaseSha, input)) {
    throw new Error("derived Mokka branch or pull request collision");
  }

  const [liveSourceValue, liveBranch, livePullRequests, liveProducedCommit] = await Promise.all([
    input.github.getPullRequest(input.prNumber),
    input.github.getBranch(input.headBranch),
    input.github.findMokkaPullRequests(input.headBranch, input.targetBranch),
    input.github.getMokkaCommit(branch.oid),
  ]);
  const liveSource = eligibleSource(liveSourceValue, input);
  if (
    !exactSource(source, liveSource)
    || liveBranch?.name !== branch.name
    || liveBranch?.oid !== branch.oid
    || !Array.isArray(livePullRequests)
    || livePullRequests.length !== 1
    || JSON.stringify(livePullRequests[0]) !== JSON.stringify(pullRequests[0])
    || !exactResultPullRequest(livePullRequests[0], input, branch.oid)
    || !exactProducedCommit(liveProducedCommit, branch.oid, targetBaseSha, input)
  ) throw new Error("derived Mokka state changed during duplicate verification");
  return summary(input, "already-exists", pullRequests[0]);
}

function stripMokkaTrailers(message) {
  if (typeof message !== "string") throw new TypeError("commit message must be text");
  const kept = [];
  let skipContinuation = false;
  for (const line of message.split("\n")) {
    if (/^[ \t]*mokka-(?:source-sha|action-id)\s*:/i.test(line)) {
      skipContinuation = true;
      continue;
    }
    if (skipContinuation && /^[ \t]/.test(line)) continue;
    skipContinuation = false;
    kept.push(line);
  }
  const cleaned = kept.join("\n").replace(/\n+$/u, "");
  if (cleaned.trim() === "") throw new Error("source commit message is empty after trailer cleanup");
  return cleaned;
}

async function deleteExactBranch(input, branchName, expectedOid) {
  const [pullRequests, branch] = await Promise.all([
    input.github.findMokkaPullRequests(branchName),
    input.github.getBranch(branchName),
  ]);
  if (!Array.isArray(pullRequests) || pullRequests.length !== 0) {
    throw new Error("cleanup branch has a pull request");
  }
  if (branch?.name !== branchName || branch?.oid !== expectedOid) {
    throw new Error("cleanup lease state is not exact");
  }
  await input.git([
    "push",
    "--porcelain",
    "--atomic",
    `--force-with-lease=refs/heads/${branchName}:${expectedOid}`,
    "origin",
    `:refs/heads/${branchName}`,
  ]);
}

async function cleanupBeforeDraft(input, provisionalSha, signedSha, finalRefCreated) {
  try {
    await deleteExactBranch(input, input.uploadBranch, provisionalSha);
    const finalBranch = await input.github.getBranch(input.headBranch);
    if (finalBranch !== null) {
      if (!finalRefCreated || finalBranch.oid !== signedSha) {
        throw new Error("final branch ownership is uncertain");
      }
      await deleteExactBranch(input, input.headBranch, signedSha);
    }
  } catch (error) {
    throw new Error(
      `manual investigation required: MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION action_id=${input.actionId} head_branch=${input.headBranch}`,
      { cause: error },
    );
  }
}

function validateCreatedPullRequest(created, expected, producedHeadSha) {
  if (
    created === null
    || typeof created !== "object"
    || !Number.isInteger(created.number)
    || created.number < 1
    || created.number > MAX_PULL_REQUEST_NUMBER
    || created.url !== `https://github.com/${EXPECTED_REPOSITORY}/pull/${created.number}`
    || created.state !== "open"
    || created.draft !== true
    || created.base !== expected.base
    || created.head !== expected.head
    || created.headOid !== producedHeadSha
    || created.title !== expected.title
    || created.body !== expected.body
  ) throw new Error("pull request response is not the requested draft");
}

async function runMokkaCherryPick(rawInput) {
  const input = validateInvocation(rawInput);
  const [sourceValue, commitValue, targetValue] = await Promise.all([
    input.github.getPullRequest(input.prNumber),
    input.github.getCommit(input.sourceSha),
    input.github.getBranch(input.targetBranch),
  ]);
  const source = eligibleSource(sourceValue, input);
  canonicalCommit(commitValue, input.sourceSha);
  const target = canonicalBranch(targetValue, input.targetBranch);

  const [branch, pullRequests] = await Promise.all([
    input.github.getBranch(input.headBranch),
    input.github.findMokkaPullRequests(input.headBranch, input.targetBranch),
  ]);
  if (!Array.isArray(pullRequests)) throw new TypeError("derived pull request lookup is invalid");
  const duplicate = await existingResult(input, source, branch, pullRequests);
  if (duplicate !== null) return duplicate;

  let lastTargetOid = requireOid(
    (await input.git(["rev-parse", "HEAD"])).stdout.trim().toLowerCase(),
    "local target SHA",
  );
  await input.git(["config", "--local", "user.name", "mokka[bot]"]);
  await input.git([
    "config",
    "--local",
    "user.email",
    "mokka[bot]@users.noreply.github.com",
  ]);
  await input.git(["fetch", "--no-tags", "origin", input.sourceSha]);
  let producedHeadSha;
  let targetBaseSha;
  for (let attempt = 0; attempt < MAX_TARGET_ATTEMPTS; attempt += 1) {
    await input.git(["fetch", "--no-tags", "origin", `refs/heads/${input.targetBranch}`]);
    const fetchedTargetOid = requireOid(
      (await input.git(["rev-parse", "FETCH_HEAD"])).stdout.trim().toLowerCase(),
      "fetched target SHA",
    );
    await requireTargetFastForward(input.git, target.oid, fetchedTargetOid);
    if (fetchedTargetOid !== lastTargetOid) {
      await requireTargetFastForward(input.git, lastTargetOid, fetchedTargetOid);
      lastTargetOid = fetchedTargetOid;
    }
    const fetchedTarget = canonicalBranch(
      await input.github.getBranch(input.targetBranch),
      input.targetBranch,
    );
    if (fetchedTarget.oid !== fetchedTargetOid) continue;

    await input.git(["checkout", "--detach", fetchedTargetOid]);
    try {
      await input.git(["cherry-pick", input.sourceSha]);
    } catch (error) {
      try {
        await input.git(["cherry-pick", "--abort"]);
      } catch (abortError) {
        throw new Error("cherry-pick cleanup failed", { cause: abortError });
      }
      throw new Error("cherry-pick conflict", { cause: error });
    }
    const message = (await input.git(["show", "-s", "--format=%B", "HEAD"])).stdout;
    await input.git([
      "commit",
      "--amend",
      "--no-gpg-sign",
      "--message",
      stripMokkaTrailers(message),
      "--trailer",
      `Mokka-Source-SHA: ${input.sourceSha}`,
      "--trailer",
      `Mokka-Action-ID: ${input.actionId}`,
    ]);
    producedHeadSha = requireOid(
      (await input.git(["rev-parse", "HEAD"])).stdout.trim().toLowerCase(),
      "produced head SHA",
    );
    if (producedHeadSha === fetchedTargetOid) {
      throw new Error("produced head SHA must differ from target");
    }

    const [liveSourceValue, liveTargetValue] = await Promise.all([
      input.github.getPullRequest(input.prNumber),
      input.github.getBranch(input.targetBranch),
    ]);
    let liveSource;
    try {
      liveSource = eligibleSource(liveSourceValue, input);
    } catch (error) {
      throw new Error("source pull request changed before write", { cause: error });
    }
    const liveTarget = canonicalBranch(liveTargetValue, input.targetBranch);
    if (!exactSource(source, liveSource)) throw new Error("source pull request changed before write");
    if (liveTarget.oid !== fetchedTargetOid) continue;
    targetBaseSha = fetchedTargetOid;
    break;
  }
  if (targetBaseSha === undefined) throw new Error("target branch changed before write");

  const provisionalSha = producedHeadSha;
  const treeSha = requireOid(
    (await input.git(["rev-parse", "HEAD^{tree}"])).stdout.trim().toLowerCase(),
    "produced tree SHA",
  );
  const commitMessage = (await input.git(["show", "-s", "--format=%B", "HEAD"])).stdout;
  let signedSha;
  let finalRefCreated = false;
  try {
    await input.git([
      "push",
      "--porcelain",
      "--atomic",
      `--force-with-lease=refs/heads/${input.uploadBranch}:`,
      "origin",
      `HEAD:refs/heads/${input.uploadBranch}`,
    ]);
  } catch (error) {
    await cleanupBeforeDraft(input, provisionalSha, signedSha, finalRefCreated);
    throw new Error("provisional commit upload failed", { cause: error });
  }

  try {
    const signedCommit = await input.github.createMokkaCommit({
      message: commitMessage,
      tree: treeSha,
      parents: [targetBaseSha],
    });
    signedSha = requireOid(signedCommit?.sha, "verified commit SHA");
    if (
      signedSha === provisionalSha
      || canonicalMessage(signedCommit.message) !== canonicalMessage(commitMessage)
      || signedCommit.tree !== treeSha
      || !Array.isArray(signedCommit.parents)
      || signedCommit.parents.length !== 1
      || signedCommit.parents[0] !== targetBaseSha
      || signedCommit.verification?.verified !== true
      || signedCommit.verification?.hasSignature !== true
    ) throw new Error("GitHub did not return the requested verified commit");

    const [liveSourceValue, liveTargetValue] = await Promise.all([
      input.github.getPullRequest(input.prNumber),
      input.github.getBranch(input.targetBranch),
    ]);
    const liveSource = eligibleSource(liveSourceValue, input);
    const liveTarget = canonicalBranch(liveTargetValue, input.targetBranch);
    if (!exactSource(source, liveSource)) throw new Error("source pull request changed before final branch creation");
    if (liveTarget.oid !== targetBaseSha) throw new Error("target branch changed before final branch creation");

    const createdRef = await input.github.createMokkaRef(input.headBranch, signedSha);
    if (createdRef?.name !== input.headBranch || createdRef?.oid !== signedSha) {
      throw new Error("verified commit ref response does not match the request");
    }
    finalRefCreated = true;
    await deleteExactBranch(input, input.uploadBranch, provisionalSha);
    const finalBranch = await input.github.getBranch(input.headBranch);
    if (finalBranch?.name !== input.headBranch || finalBranch.oid !== signedSha) {
      throw new Error("verified commit branch changed before pull request creation");
    }
    producedHeadSha = signedSha;
  } catch (error) {
    await cleanupBeforeDraft(input, provisionalSha, signedSha, finalRefCreated);
    throw new Error("verified commit creation failed", { cause: error });
  }

  const expected = {
    base: input.targetBranch,
    body: actionMarker(input.actionId),
    draft: true,
    head: input.headBranch,
    title: mokkaPullRequestTitle(input.prNumber, input.targetBranch),
  };
  let created;
  try {
    created = await input.github.createMokkaPullRequest(expected);
  } catch (error) {
    throw new Error(
      `manual investigation required: MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION action_id=${input.actionId} head_branch=${input.headBranch}`,
      { cause: error },
    );
  }
  try {
    validateCreatedPullRequest(created, expected, producedHeadSha);
  } catch (error) {
    throw new Error(
      `manual investigation required: MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION action_id=${input.actionId} pull_request_url=${created?.url ?? "unknown"}`,
      { cause: error },
    );
  }
  const evidence = createMokkaEvidence({
    actionId: input.actionId,
    sourcePullRequest: input.prNumber,
    sourceSha: input.sourceSha,
    targetBranch: input.targetBranch,
    targetBaseSha,
    producedHeadSha,
    workflowCommitSha: input.workflowSha,
    headBranch: input.headBranch,
    pullRequestUrl: created.url,
  });
  try {
    await input.github.updateMokkaPullRequestBody(created.number, evidence);
  } catch (error) {
    throw new Error(
      `manual investigation required: MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION action_id=${input.actionId} pull_request_url=${created.url}`,
      { cause: error },
    );
  }
  return summary(input, "created", created);
}

module.exports = {
  mokkaBranchName,
  mokkaPullRequestTitle,
  runMokkaCherryPick,
  stripMokkaTrailers,
};
