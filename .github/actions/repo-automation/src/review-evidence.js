"use strict";

const { evaluateApprovalCoverage } = require("./approval-coverage.js");
const { parseCommands } = require("./commands/parser.js");

function validHumanIdentity(identity, expectedLogin) {
  return identity?.resolved === true
    && identity?.deleted === false
    && identity?.type === "User"
    && typeof identity.login === "string"
    && identity.login.toLowerCase() === expectedLogin;
}

function currentRoleAllows(evidence, kind, ownership) {
  const reviewers = new Set(ownership.reviewerCandidates);
  const approvers = new Set(ownership.approverCandidates);
  if (kind === "approval") {
    return evidence.actorRole === "approver" && approvers.has(evidence.actor);
  }
  return (
    (evidence.actorRole === "reviewer" && reviewers.has(evidence.actor))
    || (evidence.actorRole === "approver" && approvers.has(evidence.actor))
  );
}

async function validReviewApprovals({ github, effectiveReviews, ownership, pullRequest }) {
  const eligible = new Set(ownership.approverCandidates);
  const validated = [];
  for (const review of effectiveReviews) {
    if (
      review.state !== "APPROVED"
      || review.commitOid !== pullRequest.headOid
      || review.user === pullRequest.author.toLowerCase()
      || !eligible.has(review.user)
    ) continue;
    try {
      const identity = await github.getUserIdentity(review.user);
      if (validHumanIdentity(identity, review.user)) validated.push(review);
    } catch {
      // Identity resolution fails closed for this review.
    }
  }
  return validated;
}

// Native approval ignores COMMENTED after a decision. Explicit review LGTM uses
// the latest submitted review body instead, so removal or replacement revokes it.
function latestSubmittedReviews(reviews) {
  const byActor = new Map();
  for (const review of reviews
    .filter((candidate) => candidate.state !== "PENDING")
    .toSorted((left, right) => (
      Date.parse(left.submittedAt) - Date.parse(right.submittedAt) || left.id - right.id
    ))) {
    byActor.set(review.user.toLowerCase(), { ...review, user: review.user.toLowerCase() });
  }
  return [...byActor.values()];
}

async function validReviewLgtms({ github, reviews, ownership, pullRequest, context }) {
  const validated = [];
  const approvers = new Set(ownership.approverCandidates);
  for (const candidate of latestSubmittedReviews(reviews)) {
    const actor = candidate.user;
    const actorRole = approvers.has(actor) ? "approver" : "reviewer";
    if (
      !["APPROVED", "COMMENTED"].includes(candidate.state)
      || candidate.commitOid !== pullRequest.headOid
      || actor === pullRequest.author.toLowerCase()
      || !currentRoleAllows({ actor, actorRole }, "lgtm", ownership)
    ) continue;
    try {
      const [review, identity] = await Promise.all([
        github.getPullRequestReview(pullRequest.number, candidate.id),
        github.getUserIdentity(actor),
      ]);
      if (
        review?.id !== candidate.id
        || review.user !== actor
        || review.state !== candidate.state
        || review.commitOid !== pullRequest.headOid
        || review.submittedAt !== candidate.submittedAt
        || !validHumanIdentity(identity, actor)
      ) continue;
      const parsed = parseCommands(review.body);
      if (
        parsed.diagnostics.length !== 0
        || !parsed.commands.some((command) => command.name === "lgtm")
      ) continue;
      validated.push({
        ...context,
        actor,
        actorRole,
        sourceType: "review",
        sourceId: review.id,
        createdAt: new Date(review.submittedAt).toISOString(),
      });
    } catch {
      // Missing live review or human identity cannot grant LGTM.
    }
  }
  return validated;
}

async function loadReviewEvidence({ github, reviews, ownership, pullRequest, context }) {
  if (!Array.isArray(reviews) || reviews.length > 1000) {
    throw new TypeError("pull request review scan exceeds limit");
  }
  const result = evaluateApprovalCoverage({
    files: ownership.files.map((file) => ({ path: file.path, approvers: file.approvers })),
    reviews,
    headOid: pullRequest.headOid,
    author: pullRequest.author,
  });
  const [approvals, lgtms] = await Promise.all([
    validReviewApprovals({ github, effectiveReviews: result.effectiveReviews, ownership, pullRequest }),
    validReviewLgtms({ github, reviews, ownership, pullRequest, context }),
  ]);
  return { approvals, lgtms };
}

module.exports = { currentRoleAllows, loadReviewEvidence, validHumanIdentity };
