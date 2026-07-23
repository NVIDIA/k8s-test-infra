"use strict";

const { evaluateApprovalCoverage, selectApprovers } = require("../approval-coverage.js");
const { authorizeCommand, eligibleAssignmentTargets } = require("./authorization.js");
const { currentLgtm } = require("./state.js");
const { planRetest } = require("../retest.js");
const { CHERRY_PICK_LABEL_PREFIX } = require("../managed-labels.js");

const MAX_ASSIGNMENT_TARGETS = 20;
const POLICY_LABELS = [
  "lgtm",
  "approved",
  "do-not-merge/hold",
  "do-not-merge/needs-approval",
];

function orderedItems(parsed) {
  return [
    ...parsed.commands.map((command) => ({ type: "command", line: command.line, command })),
    ...parsed.diagnostics.map((diagnostic) => ({ type: "diagnostic", line: diagnostic.line, diagnostic })),
  ].sort((left, right) => left.line - right.line || (left.type < right.type ? -1 : 1));
}

function fixedResult(line, name, operation, status, code, details = {}) {
  return { line, name, operation, status, code, ...details };
}

function loginSet(values) {
  return new Set(values.map((value) => value.toLowerCase()));
}

function copyState(state) {
  return {
    headOid: state.headOid,
    lgtm: state.lgtm === null ? null : { ...state.lgtm },
    lastRetest: state.lastRetest === null ? null : { ...state.lastRetest },
  };
}

function policyLabelPlan(currentLabels, desired) {
  const current = new Map(currentLabels.map((label) => [label.toLowerCase(), label]));
  const desiredSet = new Set(desired);
  return {
    add: POLICY_LABELS.filter((label) => desiredSet.has(label) && !current.has(label)),
    remove: POLICY_LABELS.filter((label) => !desiredSet.has(label) && current.has(label)),
  };
}

// Mirrors branchAllowed in modes/merge-evaluate.js: exact match or a single
// trailing "*" prefix wildcard. Replicated (not imported) to keep this command
// component decoupled from the merge-evaluate mode module.
function matchesCherryPickPattern(branch, patterns) {
  if (!Array.isArray(patterns)) return false;
  return patterns.some((pattern) => {
    if (pattern === branch) return true;
    if (typeof pattern !== "string" || !pattern.endsWith("*") || pattern.slice(0, -1).includes("*")) return false;
    return branch.startsWith(pattern.slice(0, -1));
  });
}

function planCommandExecution(input) {
  const state = copyState(input.state);
  const initialAssignees = loginSet(input.currentAssignees);
  const assignees = new Set(initialAssignees);
  let hold = input.currentLabels.some((label) => label.toLowerCase() === "do-not-merge/hold");
  let lgtmApplied = false;
  const rerunRunIds = new Set();
  const results = [];
  const reviewers = [...new Set(input.ownedFiles.flatMap((file) => file.reviewers))];
  const approvers = [...new Set(input.ownedFiles.flatMap((file) => file.approvers))];
  const open = input.open !== false;
  const cherryPickPatterns = Array.isArray(input.cherryPickPatterns) ? input.cherryPickPatterns : [];
  const cherryPickTargets = input.cherryPickTargets instanceof Map ? input.cherryPickTargets : new Map();
  const currentLabelSet = new Set(input.currentLabels);
  const desiredCherryPick = new Map();
  const cherryPickPresent = (label) => (
    desiredCherryPick.has(label) ? desiredCherryPick.get(label) : currentLabelSet.has(label)
  );

  for (const item of orderedItems(input.parsed)) {
    if (item.type === "diagnostic") {
      results.push(fixedResult(item.line, "diagnostic", "reject", "rejected", item.diagnostic.code));
      continue;
    }
    const command = item.command;
    if (open === false && command.name !== "cherry-pick") {
      results.push(fixedResult(command.line, command.name, command.operation, "rejected", "pr-not-open"));
      continue;
    }
    if (command.name !== "help" && input.authorityValid !== true) {
      results.push(fixedResult(command.line, command.name, command.operation, "rejected", "policy-unavailable"));
      continue;
    }
    if (
      (command.name === "assign" || command.name === "unassign")
      && (command.users.length > MAX_ASSIGNMENT_TARGETS || input.assignmentFanoutExceeded === true)
    ) {
      results.push(fixedResult(command.line, command.name, command.operation, "rejected", "too-many-targets"));
      continue;
    }

    if (command.name === "lgtm" && command.operation === "cancel" && state.lgtm === null) {
      results.push(fixedResult(command.line, command.name, command.operation, "noop", "lgtm-cleared"));
      continue;
    }

    const authorization = authorizeCommand(command, {
      actor: input.actor,
      author: input.author,
      reviewers,
      approvers,
      lgtmGiver: currentLgtm(state, input.headOid)?.actor ?? null,
      currentAssignees: [...assignees],
      merged: input.merged === true,
    });
    if (!authorization.allowed) {
      results.push(fixedResult(command.line, command.name, command.operation, "rejected", authorization.reason));
      continue;
    }

    if (command.name === "help") {
      results.push(fixedResult(command.line, command.name, command.operation, "applied", "help"));
      continue;
    }
    if (command.name === "lgtm") {
      if (command.operation === "cancel") {
        state.lgtm = null;
        results.push(fixedResult(command.line, command.name, command.operation, "applied", "lgtm-cleared"));
      } else if (
        state.lgtm?.commentId === input.commentId
        && state.lgtm?.headOid === input.headOid
        && state.lgtm?.actor === input.actor.login.toLowerCase()
      ) {
        lgtmApplied = true;
        results.push(fixedResult(command.line, command.name, command.operation, "noop", "lgtm-recorded"));
      } else {
        state.lgtm = {
          actor: input.actor.login.toLowerCase(),
          commentId: input.commentId,
          headOid: input.headOid,
          createdAt: input.now,
        };
        lgtmApplied = true;
        results.push(fixedResult(command.line, command.name, command.operation, "applied", "lgtm-recorded"));
      }
      continue;
    }
    if (command.name === "hold") {
      const desired = command.operation === "apply";
      const changed = hold !== desired;
      hold = desired;
      results.push(fixedResult(
        command.line,
        command.name,
        command.operation,
        changed ? "applied" : "noop",
        desired ? (changed ? "hold-recorded" : "hold-already-recorded") : (changed ? "hold-cleared" : "hold-already-clear"),
      ));
      continue;
    }
    if (command.name === "assign" || command.name === "unassign") {
      const eligibility = eligibleAssignmentTargets(command.users, {
        reviewers,
        approvers,
        participants: input.participants,
        targetPermissions: input.targetPermissions,
      });
      if (command.name === "assign") {
        for (const login of eligibility.eligible) assignees.add(login);
      } else {
        for (const login of eligibility.eligible) assignees.delete(login);
      }
      results.push(fixedResult(
        command.line,
        command.name,
        command.operation,
        eligibility.eligible.length === 0 ? "noop" : "applied",
        command.name === "assign" ? "assignment-planned" : "unassignment-planned",
        eligibility,
      ));
      continue;
    }
    if (command.name === "retest") {
      const retest = planRetest({
        runs: input.runs,
        headOid: input.headOid,
        now: input.now,
        lastRetest: state.lastRetest,
        cooldownSeconds: input.cooldownSeconds,
        commentId: input.commentId,
        prNumber: input.prNumber,
        repository: input.repository,
      });
      if (retest.rerunRunIds.length > 0) {
        state.lastRetest = {
          commentId: input.commentId,
          headOid: input.headOid,
          createdAt: input.now,
        };
        for (const runId of retest.rerunRunIds) rerunRunIds.add(runId);
      }
      results.push(fixedResult(
        command.line,
        command.name,
        command.operation,
        retest.rerunRunIds.length > 0 ? "applied" : "noop",
        retest.rerunRunIds.length > 0 ? "retest-planned" : retest.reason,
      ));
      continue;
    }
    if (command.name === "cherry-pick") {
      if (open === false && input.merged !== true) {
        results.push(fixedResult(command.line, command.name, command.operation, "rejected", "cherry-pick-pr-not-merged"));
        continue;
      }
      if (!matchesCherryPickPattern(command.branch, cherryPickPatterns) || command.branch === input.baseRef) {
        results.push(fixedResult(command.line, command.name, command.operation, "rejected", "cherry-pick-invalid-target"));
        continue;
      }
      const target = cherryPickTargets.get(command.branch);
      if (target === undefined || target.exists !== true) {
        results.push(fixedResult(command.line, command.name, command.operation, "rejected", "cherry-pick-branch-missing"));
        continue;
      }
      const label = `${CHERRY_PICK_LABEL_PREFIX}${command.branch}`;
      const present = cherryPickPresent(label);
      if (command.operation === "apply") {
        if (present) {
          results.push(fixedResult(command.line, command.name, command.operation, "noop", "cherry-pick-noop"));
        } else {
          desiredCherryPick.set(label, true);
          results.push(fixedResult(command.line, command.name, command.operation, "applied", "cherry-pick-planned"));
        }
      } else if (present) {
        desiredCherryPick.set(label, false);
        results.push(fixedResult(command.line, command.name, command.operation, "applied", "cherry-pick-cancelled"));
      } else {
        results.push(fixedResult(command.line, command.name, command.operation, "noop", "cherry-pick-noop"));
      }
      continue;
    }
  }

  const addAssignees = [...assignees].filter((login) => !initialAssignees.has(login)).sort();
  const removeAssignees = [...initialAssignees].filter((login) => !assignees.has(login)).sort();
  const addCherryPickLabels = [];
  const removeCherryPickLabels = [];
  for (const [label, present] of desiredCherryPick) {
    if (present && !currentLabelSet.has(label)) addCherryPickLabels.push(label);
    if (!present && currentLabelSet.has(label)) removeCherryPickLabels.push(label);
  }
  addCherryPickLabels.sort();
  removeCherryPickLabels.sort();

  // A non-open PR (admitted only for cherry-pick label management) never
  // reconciles policy authority labels or reviewer requests — the command's
  // sole side effect there is the cherry-pick label add/remove.
  let policy;
  let addLabels = [];
  let removeLabels = [];
  let selected = [];
  if (open) {
    const approval = evaluateApprovalCoverage({
      files: input.ownedFiles.map((file) => ({ path: file.path, approvers: file.approvers })),
      reviews: input.reviews,
      headOid: input.headOid,
      author: input.author,
    });
    const lgtm = input.authorityValid === true && currentLgtm(state, input.headOid) !== null;
    const approved = input.authorityValid === true && approval.approved;
    let approverUncoveredPaths = approval.uncoveredPaths;
    if (input.authorityValid === true && lgtmApplied && lgtm) {
      const selection = selectApprovers({
        files: input.ownedFiles.map((file) => ({ path: file.path, approvers: file.approvers })),
        effectiveReviews: approval.effectiveReviews,
        requested: input.requestedReviewers,
        headOid: input.headOid,
        author: input.author,
      });
      selected = selection.selected;
      approverUncoveredPaths = selection.uncoveredPaths;
    }
    const needsApproval = !(lgtm && approved);
    const desiredLabels = [
      ...(lgtm ? ["lgtm"] : []),
      ...(approved ? ["approved"] : []),
      ...(hold ? ["do-not-merge/hold"] : []),
      ...(needsApproval ? ["do-not-merge/needs-approval"] : []),
    ];
    const labels = policyLabelPlan(input.currentLabels, desiredLabels);
    addLabels = labels.add;
    removeLabels = labels.remove;
    policy = {
      lgtm,
      approved,
      hold,
      needsApproval,
      uncoveredPaths: approval.uncoveredPaths,
      approverUncoveredPaths,
      requestApprovers: selected,
    };
  } else {
    policy = {
      lgtm: false,
      approved: false,
      hold: false,
      needsApproval: false,
      uncoveredPaths: [],
      approverUncoveredPaths: [],
      requestApprovers: [],
    };
  }

  return {
    state,
    items: results,
    commands: results.filter((result) => result.name !== "diagnostic"),
    diagnostics: results.filter((result) => result.name === "diagnostic"),
    policy,
    mutations: {
      addAssignees,
      removeAssignees,
      requestReviewers: selected,
      addLabels,
      removeLabels,
      rerunRunIds: [...rerunRunIds].sort((left, right) => left - right),
      addCherryPickLabels,
      removeCherryPickLabels,
    },
  };
}

module.exports = { planCommandExecution };
