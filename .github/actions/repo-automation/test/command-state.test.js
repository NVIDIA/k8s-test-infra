"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const {
  appendProcessedCommand,
  createEmptyState,
  currentEvidence,
  currentHold,
  parsePolicyState,
  serializePolicyState,
} = require("../src/commands/state.js");

const HEAD = "a".repeat(40);
const NEXT_HEAD = "b".repeat(40);
const DIGEST = "c".repeat(64);
const NEXT_DIGEST = "d".repeat(64);
const CONTEXT = Object.freeze({
  repository: "nvidia/k8s-test-infra",
  pullRequest: 871,
  policyDigest: DIGEST,
  headOid: HEAD,
});

function evidence(overrides = {}) {
  return {
    repository: CONTEXT.repository,
    pullRequest: CONTEXT.pullRequest,
    actor: "reviewer-one",
    actorRole: "reviewer",
    sourceType: "comment",
    sourceId: 123,
    policyDigest: DIGEST,
    headOid: HEAD,
    createdAt: "2026-09-17T12:00:00.000Z",
    ...overrides,
  };
}

function hold(overrides = {}) {
  return {
    repository: CONTEXT.repository,
    pullRequest: CONTEXT.pullRequest,
    actor: "owner-one",
    actorRole: "owner",
    sourceType: "comment",
    sourceId: 124,
    createdAt: "2026-09-17T12:01:00.000Z",
    ...overrides,
  };
}

function populatedState() {
  return {
    ...createEmptyState(CONTEXT),
    lgtms: [evidence()],
    approvals: [evidence({ actor: "approver-one", actorRole: "approver", sourceId: 125 })],
    hold: hold(),
    lastRetest: {
      commentId: 126,
      headOid: HEAD,
      createdAt: "2026-09-17T12:02:00.000Z",
    },
    processedCommandIds: [123, 124, 125, 126],
  };
}

test("round-trips one canonical exact v2 state block", () => {
  const state = populatedState();
  const serialized = serializePolicyState(state);

  assert.match(serialized, /^<!-- repo-automation-state:v2 /);
  assert.deepEqual(parsePolicyState(`status\n${serialized}\n`), state);
});

test("rejects missing, duplicate, malformed, and non-canonical state blocks", () => {
  const serialized = serializePolicyState(populatedState());
  assert.equal(parsePolicyState("status only"), null);
  assert.equal(parsePolicyState(`${serialized}\n${serialized}`), null);
  assert.equal(parsePolicyState(serialized.replace('"pullRequest":871', '"pullRequest":871, "extra":true')), null);
  assert.equal(parsePolicyState(serialized.replace('"processedCommandIds":[123,124,125,126]', '"processedCommandIds":[123,123]')), null);
  assert.equal(parsePolicyState(serialized.replace('"actor":"reviewer-one"', '"actor":"Reviewer-One"')), null);
});

test("binds LGTM and approval evidence to repository, pull request, policy, and exact head", () => {
  const state = populatedState();
  assert.deepEqual(currentEvidence(state, "lgtms", CONTEXT), state.lgtms);
  assert.deepEqual(currentEvidence(state, "approvals", CONTEXT), state.approvals);
  assert.deepEqual(currentEvidence(state, "lgtms", { ...CONTEXT, headOid: NEXT_HEAD }), []);
  assert.deepEqual(currentEvidence(state, "lgtms", { ...CONTEXT, policyDigest: NEXT_DIGEST }), []);
  assert.deepEqual(currentEvidence(state, "lgtms", { ...CONTEXT, repository: "nvidia/other" }), []);
  assert.deepEqual(currentEvidence(state, "lgtms", { ...CONTEXT, pullRequest: 872 }), []);
});

test("keeps a PR-bound hold active across head and policy changes", () => {
  const state = populatedState();
  assert.deepEqual(currentHold(state, CONTEXT), state.hold);
  assert.deepEqual(
    currentHold(state, { ...CONTEXT, headOid: NEXT_HEAD, policyDigest: NEXT_DIGEST }),
    state.hold,
  );
  assert.equal(currentHold(state, { ...CONTEXT, pullRequest: 872 }), null);
});

test("bounds processed command history and rejects duplicate delivery", () => {
  const state = createEmptyState(CONTEXT);
  const first = appendProcessedCommand(state, 10, 2);
  assert.deepEqual(first.processedCommandIds, [10]);
  assert.equal(appendProcessedCommand(first, 10, 2), first);
  const second = appendProcessedCommand(first, 11, 2);
  assert.deepEqual(second.processedCommandIds, [10, 11]);
  assert.throws(() => appendProcessedCommand(second, 12, 2), /history limit/);
});

test("requires exact evidence schemas and actor roles", () => {
  const state = populatedState();
  assert.throws(
    () => serializePolicyState({ ...state, approvals: [evidence({ actorRole: "reviewer" })] }),
    /approval actor role/,
  );
  assert.throws(
    () => serializePolicyState({ ...state, lgtms: [evidence({ sourceId: 0 })] }),
    /source ID/,
  );
  assert.throws(
    () => serializePolicyState({ ...state, hold: hold({ headOid: HEAD }) }),
    /exact v2 schema/,
  );
});
