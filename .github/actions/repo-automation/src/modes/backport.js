"use strict";

const { validateConfig } = require("../config.js");
const { CHERRY_PICK_LABEL_PREFIX, isManagedCherryPickLabel } = require("../managed-labels.js");

const BACKPORT_STATUS_MARKER = "<!-- repo-automation-backport-status:v1 -->";
const BACKPORT_REF_PREFIX = "heads/backport/";
const GIT_OID = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;

// Mirrors branchAllowed in modes/merge-evaluate.js and matchesCherryPickPattern
// in commands/executor.js: exact match or a single trailing "*" prefix wildcard.
// Replicated to keep the backport mode decoupled from the command component.
function matchesCherryPickPattern(branch, patterns) {
  if (!Array.isArray(patterns)) return false;
  return patterns.some((pattern) => {
    if (pattern === branch) return true;
    if (typeof pattern !== "string" || !pattern.endsWith("*") || pattern.slice(0, -1).includes("*")) return false;
    return branch.startsWith(pattern.slice(0, -1));
  });
}

function configurationValid(config) {
  try {
    validateConfig(config);
    return true;
  } catch {
    return false;
  }
}

// The mode-level ref-namespace guard. The client's gitRefName is form-only, so
// the backport mode owns the invariant that it can only ever create, update, or
// delete refs under heads/backport/. Every ref write passes through here first.
function assertBackportRef(refName) {
  if (typeof refName !== "string" || !refName.startsWith(BACKPORT_REF_PREFIX)) {
    throw new Error(`refusing ref write outside the backport namespace: ${refName}`);
  }
  return refName;
}

function eventIdentity(event) {
  const prNumber = event?.pull_request?.number;
  const mergeCommitSha = event?.pull_request?.merge_commit_sha;
  if (
    !Number.isSafeInteger(prNumber)
    || prNumber <= 0
    || typeof mergeCommitSha !== "string"
    || !GIT_OID.test(mergeCommitSha.toLowerCase())
  ) {
    throw new TypeError("event must identify a merged pull request and its squash commit");
  }
  return { prNumber, mergeCommitSha: mergeCommitSha.toLowerCase() };
}

function livePullRequest(value, prNumber) {
  if (
    value === null
    || typeof value !== "object"
    || Array.isArray(value)
    || value.number !== prNumber
    || typeof value.title !== "string"
    || value.title === ""
    || typeof value.author !== "string"
    || typeof value.state !== "string"
    || typeof value.baseBranch !== "string"
    || value.baseBranch === ""
  ) {
    throw new Error("live pull request state is invalid");
  }
  return value;
}

// The write-time fence analog: the pull request must still be merged, its
// identity unchanged from the planning read, and its live merge commit still
// the exact squash commit named by the event — before any repository write.
// A missing or mismatched mergeCommitOid means the merge the event described is
// no longer the head of record, so every write for this run is refused.
function stablePullRequest(planned, current, mergeCommitSha) {
  return current !== null
    && current.merged === true
    && typeof current.mergeCommitOid === "string"
    && current.mergeCommitOid.toLowerCase() === mergeCommitSha
    && planned.number === current.number
    && planned.state === current.state
    && planned.title === current.title
    && planned.headOid === current.headOid
    && planned.baseBranch === current.baseBranch
    && planned.author.toLowerCase() === current.author.toLowerCase();
}

function describeOutcome(target) {
  switch (target.outcome) {
    case "created":
      return `created backport pull request #${target.backportPr.number}`;
    case "already-exists":
      return `backport pull request #${target.backportPr.number} already exists`;
    case "conflicts":
      return "could not be cherry-picked cleanly; backport manually";
    case "empty":
      return "already present on the target branch; nothing to backport";
    case "invalid-target":
      return "not an eligible backport target";
    case "branch-missing":
      return "target branch does not exist";
    default:
      return "failed to backport; see the workflow logs";
  }
}

function renderBackportStatusComment(targets) {
  const lines = [BACKPORT_STATUS_MARKER, "## Backport status", ""];
  for (const target of targets) {
    lines.push(`- \`${target.branch}\`: ${describeOutcome(target)}`);
  }
  if (targets.some((target) => target.outcome === "conflicts")) {
    lines.push(
      "",
      "Conflicting backports must be applied by hand: check out the target branch, "
      + "run `git cherry-pick -x <merge-commit>`, resolve the conflicts, and open a pull request.",
    );
  }
  return `${lines.join("\n")}\n`;
}

function backportPullRequestBody(prNumber, author, target) {
  return [
    `Automated backport of #${prNumber} to \`${target}\`.`,
    "",
    `Original author: @${author}`,
    "",
    "The original commit message, sign-off, and authorship are preserved; this pull "
    + "request is a normal pull request and remains subject to the usual review and merge policy.",
    "",
  ].join("\n");
}

function backportBranchName(prNumber, target) {
  return `backport/${prNumber}-to-${target}`;
}

// Plan every labelled target from live state: what outcome it will get and,
// for a graftable target, the branch head T and tree(T) needed by the graft.
async function planTargets({ github, prNumber, patterns, baseBranch, labels }) {
  const branches = [...new Set(labels
    .filter((label) => isManagedCherryPickLabel(label))
    .map((label) => label.slice(CHERRY_PICK_LABEL_PREFIX.length)))].sort();
  const plans = [];
  for (const branch of branches) {
    if (!matchesCherryPickPattern(branch, patterns) || branch === baseBranch) {
      plans.push({ branch, action: "invalid-target" });
      continue;
    }
    const targetRef = await github.getBranch(branch);
    if (targetRef === null) {
      plans.push({ branch, action: "branch-missing" });
      continue;
    }
    const backportBranch = backportBranchName(prNumber, branch);
    const [existingBackport, openPr] = await Promise.all([
      github.getBranch(backportBranch),
      github.findOpenPullRequest(backportBranch, branch),
    ]);
    if (existingBackport !== null && openPr !== null) {
      plans.push({ branch, action: "already-exists", backportPr: openPr });
      continue;
    }
    const targetCommit = await github.getCommitInfo(targetRef.oid);
    plans.push({ branch, action: "create", targetHead: targetRef.oid, targetTree: targetCommit.treeOid });
  }
  return plans;
}

async function graftTarget({ github, prNumber, plan, squash, parentTree, title, author }) {
  const backportBranch = backportBranchName(prNumber, plan.branch);
  const backportRef = `heads/${backportBranch}`;
  let refCreated = false;
  try {
    assertBackportRef(backportRef);
    await github.createRef(backportRef, plan.targetHead);
    refCreated = true;
    const graft = await github.createCommit({
      message: `Graft base for ${backportBranch}`,
      treeOid: parentTree,
      parentOids: [plan.targetHead],
    });
    await github.updateRef(backportRef, graft);
    const merge = await github.mergeBranches(backportBranch, squash.oid);
    if (merge.merged !== true) {
      await github.deleteRef(backportRef);
      return { branch: plan.branch, outcome: merge.alreadyMerged === true ? "empty" : "conflicts" };
    }
    const cherry = await github.createCommit({
      message: `${squash.message}\n\n(cherry picked from commit ${squash.oid})`,
      treeOid: merge.treeOid,
      parentOids: [plan.targetHead],
      author: squash.author,
    });
    await github.updateRef(backportRef, cherry);
    if (merge.treeOid === plan.targetTree) {
      await github.deleteRef(backportRef);
      return { branch: plan.branch, outcome: "empty" };
    }
    const backportPr = await github.createPullRequest({
      base: plan.branch,
      head: backportBranch,
      title: `[${plan.branch}] ${title}`,
      body: backportPullRequestBody(prNumber, author, plan.branch),
    });
    return { branch: plan.branch, outcome: "created", backportPr };
  } catch {
    if (refCreated) {
      try {
        await github.deleteRef(backportRef);
      } catch {
        // Best-effort cleanup: a failed delete leaves the surfaced error outcome.
      }
    }
    return { branch: plan.branch, outcome: "error" };
  }
}

async function runBackport({ event, github, config, dryRun, now = () => new Date().toISOString() }) {
  if (typeof dryRun !== "boolean" || typeof now !== "function") {
    throw new TypeError("backport mode inputs are invalid");
  }
  const { prNumber, mergeCommitSha } = eventIdentity(event);
  const planned = livePullRequest(await github.getPullRequest(prNumber), prNumber);
  if (planned.merged !== true) {
    return { status: "skipped", prNumber, targets: [] };
  }

  const squash = await github.getCommitInfo(mergeCommitSha);
  if (!Array.isArray(squash.parents) || squash.parents.length === 0) {
    return { status: "skipped", prNumber, targets: [] };
  }
  const parent = await github.getCommitInfo(squash.parents[0]);
  const labels = await github.listIssueLabels(prNumber);

  const patterns = configurationValid(config)
    ? config.policy.commands.cherryPick.targetBranchPatterns
    : [];
  const plans = await planTargets({
    github,
    prNumber,
    patterns,
    baseBranch: planned.baseBranch,
    labels,
  });
  const existingComment = await github.getPolicyComment(prNumber, BACKPORT_STATUS_MARKER);

  if (dryRun) {
    const targets = plans.map((plan) => (
      plan.action === "create"
        ? { branch: plan.branch, outcome: "created" }
        : plan.action === "already-exists"
          ? { branch: plan.branch, outcome: "already-exists", backportPr: plan.backportPr }
          : { branch: plan.branch, outcome: plan.action }
    ));
    return { status: "planned", prNumber, targets };
  }

  const fenced = await github.getPullRequest(prNumber);
  if (!stablePullRequest(planned, fenced, mergeCommitSha)) {
    throw new Error("pull request state changed after planning; refusing stale writes");
  }

  const targets = [];
  for (const plan of plans) {
    if (plan.action === "create") {
      targets.push(await graftTarget({
        github,
        prNumber,
        plan,
        squash,
        parentTree: parent.treeOid,
        title: planned.title,
        author: planned.author,
      }));
    } else if (plan.action === "already-exists") {
      targets.push({ branch: plan.branch, outcome: "already-exists", backportPr: plan.backportPr });
    } else {
      targets.push({ branch: plan.branch, outcome: plan.action });
    }
  }

  const commentBody = renderBackportStatusComment(targets);
  if (existingComment.body !== commentBody) {
    await github.upsertPolicyComment(prNumber, BACKPORT_STATUS_MARKER, commentBody, existingComment);
  }

  return { status: "complete", prNumber, targets };
}

module.exports = {
  runBackport,
  renderBackportStatusComment,
  assertBackportRef,
  BACKPORT_STATUS_MARKER,
};
