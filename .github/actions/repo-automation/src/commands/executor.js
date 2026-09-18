"use strict";

const { planRetest } = require("../retest.js");
const { authorizeCommand } = require("./authorization.js");
const {
  appendProcessedCommand,
  createEmptyState,
  currentEvidence,
  currentHold,
} = require("./state.js");

const POLICY_LABELS = [
  "lgtm",
  "approved",
  "do-not-merge/hold",
  "do-not-merge/needs-approval",
];
const SAFE_BRANCH = /^(?!-)(?!.*(?:\.\.|@\{|\/\/|\\|[\x00-\x20\x7f~^:?*\[]))[A-Za-z0-9][A-Za-z0-9._\/-]{0,254}$/;

function orderedItems(parsed) {
  return [
    ...parsed.commands.map((command) => ({ type: "command", line: command.line, command })),
    ...parsed.diagnostics.map((diagnostic) => ({ type: "diagnostic", line: diagnostic.line, diagnostic })),
  ].sort((left, right) => left.line - right.line || left.type.localeCompare(right.type));
}

function commandResult(command, status, code) {
  return { line: command.line, name: command.name, status, code };
}

function policyLabelPlan(currentLabels, desired) {
  const current = new Set(currentLabels.map((label) => label.toLowerCase()));
  const desiredSet = new Set(desired);
  return {
    add: POLICY_LABELS.filter((label) => desiredSet.has(label) && !current.has(label)),
    remove: POLICY_LABELS.filter((label) => !desiredSet.has(label) && current.has(label)),
  };
}

function emptyMutations() {
  return {
    addLabels: [],
    removeLabels: [],
    rerunRunIds: [],
    backportRequests: [],
  };
}

function matchesBranchPattern(branch, pattern) {
  if (typeof pattern !== "string" || pattern === "") return false;
  const wildcard = pattern.endsWith("*");
  const prefix = wildcard ? pattern.slice(0, -1) : pattern;
  if (prefix === "" || prefix.includes("*") || !SAFE_BRANCH.test(prefix)) return false;
  return wildcard ? branch.startsWith(prefix) : branch === prefix;
}

function allowedBackportBranch(branch, patterns) {
  return Array.isArray(patterns)
    && patterns.length > 0
    && patterns.length <= 64
    && patterns.some((pattern) => matchesBranchPattern(branch, pattern));
}

function evidence(context, authorization, sourceId, now) {
  return {
    repository: context.repository,
    pullRequest: context.pullRequest,
    actor: authorization.actor,
    actorRole: authorization.actorRole,
    sourceType: "comment",
    sourceId,
    policyDigest: context.policyDigest,
    headOid: context.headOid,
    createdAt: now,
  };
}

function addEvidence(records, record) {
  const retained = records.filter((candidate) => (
    candidate.actor !== record.actor
    && !(candidate.sourceType === record.sourceType && candidate.sourceId === record.sourceId)
  ));
  return [...retained, record];
}

function activeState(input) {
  const state = createEmptyState(input.context);
  state.lgtms = currentEvidence(input.state, "lgtms", input.context);
  state.approvals = currentEvidence(input.state, "approvals", input.context);
  state.hold = currentHold(input.state, input.context);
  state.lastRetest = input.state.lastRetest;
  state.processedCommandIds = [...input.state.processedCommandIds];
  return state;
}

function policyResult(state) {
  const lgtm = state.lgtms.length > 0;
  const approved = state.approvals.length > 0;
  const hold = state.hold !== null;
  return { lgtm, approved, hold, needsApproval: !(lgtm && approved) };
}

function duplicateResult(input) {
  return {
    duplicate: true,
    state: input.state,
    commands: [],
    diagnostics: [],
    policy: policyResult(activeState(input)),
    mutations: emptyMutations(),
  };
}

function planCommandExecution(input) {
  const state = activeState(input);
  if (state.processedCommandIds.includes(input.commentId)) return duplicateResult(input);

  const commands = [];
  const diagnostics = [];
  const mutations = emptyMutations();
  for (const item of orderedItems(input.parsed)) {
    if (item.type === "diagnostic") {
      diagnostics.push({
        line: item.diagnostic.line,
        name: "diagnostic",
        status: "rejected",
        code: item.diagnostic.code,
      });
      continue;
    }

    const command = item.command;
    const authorization = authorizeCommand(command, {
      actor: input.actor,
      author: input.author,
      reviewers: input.reviewers,
      approvers: input.approvers,
      owners: input.owners,
    });
    if (!authorization.allowed) {
      commands.push(commandResult(command, "rejected", authorization.reason));
      continue;
    }

    if (command.name === "lgtm" || command.name === "approve") {
      const key = command.name === "lgtm" ? "lgtms" : "approvals";
      const record = evidence(input.context, authorization, input.commentId, input.now);
      const alreadyRecorded = state[key].some((candidate) => (
        candidate.actor === record.actor
        && candidate.sourceType === record.sourceType
        && candidate.sourceId === record.sourceId
      ));
      if (!alreadyRecorded) state[key] = addEvidence(state[key], record);
      commands.push(commandResult(
        command,
        alreadyRecorded ? "noop" : "applied",
        command.name === "lgtm" ? "lgtm-recorded" : "approval-recorded",
      ));
      continue;
    }

    if (command.name === "hold") {
      const alreadyHeld = state.hold !== null;
      if (!alreadyHeld) {
        state.hold = {
          repository: input.context.repository,
          pullRequest: input.context.pullRequest,
          actor: authorization.actor,
          actorRole: authorization.actorRole,
          sourceType: "comment",
          sourceId: input.commentId,
          createdAt: input.now,
        };
      }
      commands.push(commandResult(command, alreadyHeld ? "noop" : "applied", "hold-recorded"));
      continue;
    }

    if (command.name === "unhold") {
      const hadHold = state.hold !== null;
      state.hold = null;
      commands.push(commandResult(command, hadHold ? "applied" : "noop", "hold-cleared"));
      continue;
    }

    if (command.name === "retest") {
      const retest = planRetest({
        runs: input.runs,
        headOid: input.context.headOid,
        now: input.now,
        lastRetest: state.lastRetest,
        cooldownSeconds: input.cooldownSeconds,
        commentId: input.commentId,
        prNumber: input.context.pullRequest,
        repository: input.context.repository,
        workflowAllowlist: input.retestWorkflowAllowlist,
      });
      mutations.rerunRunIds = retest.rerunRunIds;
      if (retest.rerunRunIds.length > 0) {
        state.lastRetest = {
          commentId: input.commentId,
          headOid: input.context.headOid,
          createdAt: input.now,
        };
      }
      commands.push(commandResult(
        command,
        retest.rerunRunIds.length > 0 ? "applied" : "noop",
        retest.rerunRunIds.length > 0 ? "retest-planned" : retest.reason,
      ));
      continue;
    }

    if (!allowedBackportBranch(command.targetBranch, input.allowedBackportBranches)) {
      commands.push(commandResult(command, "rejected", "target-branch-not-allowed"));
      continue;
    }
    mutations.backportRequests.push({
      command: command.name,
      prNumber: input.context.pullRequest,
      targetBranch: command.targetBranch,
      sourceCommentId: input.commentId,
    });
    commands.push(commandResult(command, "applied", "backport-planned"));
  }

  const processedState = appendProcessedCommand(state, input.commentId, input.historyLimit);
  const policy = policyResult(processedState);
  const labels = policyLabelPlan(input.currentLabels ?? [], [
    ...(policy.lgtm ? ["lgtm"] : []),
    ...(policy.approved ? ["approved"] : []),
    ...(policy.hold ? ["do-not-merge/hold"] : []),
    ...(policy.needsApproval ? ["do-not-merge/needs-approval"] : []),
  ]);
  mutations.addLabels = labels.add;
  mutations.removeLabels = labels.remove;

  return {
    duplicate: false,
    state: processedState,
    commands,
    diagnostics,
    policy,
    mutations,
  };
}

module.exports = { planCommandExecution };
