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

// The sorted, de-duplicated set of cherry-pick target branches carried by the
// live issue labels.
function branchesFromLabels(labels) {
  return [...new Set(labels
    .filter((label) => isManagedCherryPickLabel(label))
    .map((label) => label.slice(CHERRY_PICK_LABEL_PREFIX.length)))].sort();
}

// A dispatched pr-number arrives as a decimal string action input; validate and
// parse it into the same positive-integer domain the event path derives.
function dispatchPrNumber(value) {
  if (typeof value !== "string" || !/^[1-9][0-9]*$/.test(value)) {
    throw new TypeError("workflow_dispatch pr-number input must be a positive integer");
  }
  const parsed = Number.parseInt(value, 10);
  if (!Number.isSafeInteger(parsed) || parsed <= 0) {
    throw new TypeError("workflow_dispatch pr-number input must be a positive integer");
  }
  return parsed;
}

// Plan every target branch from live state: what outcome it will get and,
// for a graftable target, the branch head T and tree(T) needed by the graft.
async function planTargets({ github, prNumber, patterns, baseBranch, branches }) {
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
    plans.push({
      branch,
      action: "create",
      targetHead: targetRef.oid,
      targetTree: targetCommit.treeOid,
      // A branch left behind by a closed backport PR (or a crashed prior run) is
      // reusable state, not a reason to fail: force-update it instead of a
      // createRef that would 422 and wedge the target forever.
      existingBranch: existingBackport !== null,
    });
  }
  return plans;
}

async function graftTarget({ github, prNumber, plan, squash, title, author }) {
  const backportBranch = backportBranchName(prNumber, plan.branch);
  const backportRef = `heads/${backportBranch}`;
  let refCreated = false;
  try {
    assertBackportRef(backportRef);
    if (plan.existingBranch === true) {
      await github.updateRef(backportRef, plan.targetHead);
    } else {
      await github.createRef(backportRef, plan.targetHead);
    }
    refCreated = true;
    // Graft: tree of the target branch, parented on the squash parent P, so
    // merge-base(graft, M) = P and mergeBranches replays only diff(P->M) onto
    // the release tree. The transposed orientation (tree of P parented on the
    // release head) instead merges the whole main-vs-release delta.
    const graft = await github.createCommit({
      message: `Graft base for ${backportBranch}`,
      treeOid: plan.targetTree,
      parentOids: [squash.parents[0]],
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

async function runBackport({
  event,
  eventName,
  github,
  config,
  dryRun,
  prNumber: prNumberInput,
  targetBranch = "",
  now = () => new Date().toISOString(),
}) {
  if (typeof dryRun !== "boolean" || typeof now !== "function") {
    throw new TypeError("backport mode inputs are invalid");
  }
  // A dispatch run carries no pull_request payload: the pr-number and
  // target-branch come from the action inputs and the squash oid from the live
  // read. Closed/labeled events keep their event-sha identity and fence exactly.
  const dispatch = eventName === "workflow_dispatch";
  let prNumber;
  let mergeCommitSha;
  if (dispatch) {
    prNumber = dispatchPrNumber(prNumberInput);
  } else {
    ({ prNumber, mergeCommitSha } = eventIdentity(event));
  }
  const planned = livePullRequest(await github.getPullRequest(prNumber), prNumber);
  if (planned.merged !== true) {
    return { status: "skipped", prNumber, targets: [] };
  }
  if (dispatch) {
    // For a dispatch run the live merge commit is the squash anchor and the
    // fence source; stablePullRequest still requires the fence re-read to carry
    // this exact oid, so a merge that changed after planning refuses every write.
    if (typeof planned.mergeCommitOid !== "string" || !GIT_OID.test(planned.mergeCommitOid.toLowerCase())) {
      return { status: "skipped", prNumber, targets: [] };
    }
    mergeCommitSha = planned.mergeCommitOid.toLowerCase();
  }

  const squash = await github.getCommitInfo(mergeCommitSha);
  if (!Array.isArray(squash.parents) || squash.parents.length === 0) {
    return { status: "skipped", prNumber, targets: [] };
  }

  const patterns = configurationValid(config)
    ? config.policy.commands.cherryPick.targetBranchPatterns
    : [];
  // A scoped dispatch processes only its input branch and never reads labels,
  // so it proceeds even before the just-added cherry-pick label is visible.
  const branches = dispatch && targetBranch !== ""
    ? [targetBranch]
    : branchesFromLabels(await github.listIssueLabels(prNumber));
  const plans = await planTargets({
    github,
    prNumber,
    patterns,
    baseBranch: planned.baseBranch,
    branches,
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
        title: planned.title,
        author: planned.author,
      }));
    } else if (plan.action === "already-exists") {
      targets.push({ branch: plan.branch, outcome: "already-exists", backportPr: plan.backportPr });
    } else {
      targets.push({ branch: plan.branch, outcome: plan.action });
    }
  }

  // A merged PR with no cherry-pick targets has nothing to report; only touch
  // the PR when there is a target or an existing status comment to keep current,
  // so ordinary merges never collect an empty "## Backport status" comment.
  const commentBody = renderBackportStatusComment(targets);
  const shouldComment = targets.length > 0 || existingComment.body !== null;
  if (shouldComment && existingComment.body !== commentBody) {
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
