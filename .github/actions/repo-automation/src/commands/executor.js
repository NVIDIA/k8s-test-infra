"use strict";

const { evaluateApprovalCoverage, selectApprovers } = require("../approval-coverage.js");
const { authorizeCommand, eligibleAssignmentTargets } = require("./authorization.js");
const { currentLgtm } = require("./state.js");
const { planRetest } = require("../retest.js");

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

  for (const item of orderedItems(input.parsed)) {
    if (item.type === "diagnostic") {
      results.push(fixedResult(item.line, "diagnostic", "reject", "rejected", item.diagnostic.code));
      continue;
    }
    const command = item.command;
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
    }
  }

  const approval = evaluateApprovalCoverage({
    files: input.ownedFiles.map((file) => ({ path: file.path, approvers: file.approvers })),
    reviews: input.reviews,
    headOid: input.headOid,
    author: input.author,
  });
  const lgtm = input.authorityValid === true && currentLgtm(state, input.headOid) !== null;
  const approved = input.authorityValid === true && approval.approved;
  let selected = [];
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
  const addAssignees = [...assignees].filter((login) => !initialAssignees.has(login)).sort();
  const removeAssignees = [...initialAssignees].filter((login) => !assignees.has(login)).sort();

  return {
    state,
    items: results,
    commands: results.filter((result) => result.name !== "diagnostic"),
    diagnostics: results.filter((result) => result.name === "diagnostic"),
    policy: {
      lgtm,
      approved,
      hold,
      needsApproval,
      uncoveredPaths: approval.uncoveredPaths,
      approverUncoveredPaths,
      requestApprovers: selected,
    },
    mutations: {
      addAssignees,
      removeAssignees,
      requestReviewers: selected,
      addLabels: labels.add,
      removeLabels: labels.remove,
      rerunRunIds: [...rerunRunIds].sort((left, right) => left - right),
    },
  };
}

module.exports = { planCommandExecution };
