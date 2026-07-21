"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const {
  currentLgtm,
  parsePolicyState,
  serializePolicyState,
} = require("../src/commands/state.js");

const HEAD = "a".repeat(40);
const OTHER_HEAD = "b".repeat(40);
const CREATED_AT = "2026-07-16T12:34:56.000Z";
const STATE_MARKER_PREFIX = "<!-- repo-automation-state:v1 ";

function lgtm(overrides = {}) {
  return {
    actor: "reviewer-one",
    commentId: 42,
    headOid: HEAD,
    createdAt: CREATED_AT,
    ...overrides,
  };
}

function lastRetest(overrides = {}) {
  return {
    commentId: 43,
    headOid: HEAD,
    createdAt: "2026-07-16T12:35:00.000Z",
    ...overrides,
  };
}

function state(overrides = {}) {
  return {
    headOid: HEAD,
    lgtm: lgtm(),
    lastRetest: lastRetest(),
    ...overrides,
  };
}

test("serializes the exact v1 marker with stable top-level and provenance key order", () => {
  const marker = serializePolicyState({
    lastRetest: {
      createdAt: "2026-07-16T12:35:00.000Z",
      headOid: HEAD,
      commentId: 43,
    },
    lgtm: {
      createdAt: CREATED_AT,
      headOid: HEAD,
      commentId: 42,
      actor: "reviewer-one",
    },
    headOid: HEAD,
  });

  assert.equal(marker, `${STATE_MARKER_PREFIX}{"headOid":"${HEAD}","lgtm":{"actor":"reviewer-one","commentId":42,"headOid":"${HEAD}","createdAt":"${CREATED_AT}"},"lastRetest":{"commentId":43,"headOid":"${HEAD}","createdAt":"2026-07-16T12:35:00.000Z"}} -->`);
  assert.equal(marker.split(STATE_MARKER_PREFIX).length - 1, 1);
});

test("strictly parses a canonical state embedded in the single bot policy comment", () => {
  const expected = state();
  const marker = serializePolicyState(expected);
  const body = `<!-- repo-automation-policy:v1 -->\n## Policy\n\n${marker}\n`;

  assert.deepEqual(parsePolicyState(body), expected);
  assert.equal(serializePolicyState(parsePolicyState(body)), marker);
});

test("represents LGTM cancellation and an absent retest as explicit null state", () => {
  const cancelled = state({ lgtm: null, lastRetest: null });
  const parsed = parsePolicyState(serializePolicyState(cancelled));

  assert.deepEqual(parsed, cancelled);
  assert.equal(currentLgtm(parsed, HEAD), null);
});

test("returns LGTM provenance only when state and provenance match the current head", () => {
  assert.deepEqual(currentLgtm(state(), HEAD), lgtm());
  assert.equal(currentLgtm(state(), OTHER_HEAD), null);
  assert.equal(currentLgtm(state({ headOid: OTHER_HEAD }), HEAD), null);
  assert.equal(currentLgtm(state({ lgtm: lgtm({ headOid: OTHER_HEAD }) }), HEAD), null);
});

test("does not accept a display label or malformed state as LGTM authority", () => {
  assert.equal(currentLgtm(null, HEAD), null);
  assert.equal(currentLgtm({ headOid: HEAD, labels: ["lgtm"] }, HEAD), null);
  assert.equal(currentLgtm(state({ lgtm: null }), HEAD, ["lgtm"]), null);
  assert.equal(currentLgtm(state(), "not-an-object-id"), null);
});

test("fails closed for missing, duplicate, malformed, noncanonical, or unknown state markers", () => {
  const marker = serializePolicyState(state());
  const malformed = `${STATE_MARKER_PREFIX}{not-json} -->`;
  const unknown = marker.replace(`"lastRetest":`, `"unexpected":true,"lastRetest":`);
  const noncanonical = marker.replace(`{"headOid":`, `{ "headOid":`);
  const duplicateKey = marker.replace(`{"headOid":"${HEAD}"`, `{"headOid":"${OTHER_HEAD}","headOid":"${HEAD}"`);

  for (const body of [
    "<!-- repo-automation-policy:v1 -->\n",
    `${marker}\n${marker}`,
    `${marker}\n${malformed}`,
    malformed,
    unknown,
    noncanonical,
    duplicateKey,
    marker.replace(STATE_MARKER_PREFIX, "<!-- repo-automation-state:v2 "),
    marker.replace(" -->", "-->") ,
    null,
    { body: marker },
  ]) {
    assert.equal(parsePolicyState(body), null);
  }
});

test("rejects unsafe OIDs, logins, comment IDs, timestamps, keys, and object prototypes", () => {
  const customPrototype = Object.setPrototypeOf(state(), { privileged: true });
  const cases = [
    state({ headOid: "a".repeat(39) }),
    state({ headOid: "A".repeat(40) }),
    state({ lgtm: lgtm({ actor: "bad_login" }) }),
    state({ lgtm: lgtm({ actor: "reviewer\nforged" }) }),
    state({ lgtm: lgtm({ commentId: 0 }) }),
    state({ lgtm: lgtm({ commentId: Number.MAX_SAFE_INTEGER + 1 }) }),
    state({ lgtm: lgtm({ createdAt: "2026-07-16T12:34:56Z" }) }),
    state({ lgtm: { ...lgtm(), unexpected: true } }),
    state({ lastRetest: { ...lastRetest(), actor: "not-part-of-schema" } }),
    { ...state(), unexpected: true },
    customPrototype,
  ];

  for (const invalid of cases) {
    assert.throws(() => serializePolicyState(invalid), { name: "TypeError" });
  }
});

test("parsed hostile JSON cannot add inherited authority or control text", () => {
  const marker = serializePolicyState(state());
  const hostileMarkers = [
    marker.replace(`"actor":"reviewer-one"`, `"actor":"reviewer-one\\u202e"`),
    marker.replace(`"actor":"reviewer-one"`, `"__proto__":{"admin":true},"actor":"reviewer-one"`),
    marker.replace(`"lgtm":{`, `"lgtm":{"constructor":{"prototype":{"admin":true}},`),
  ];

  for (const body of hostileMarkers) {
    assert.equal(parsePolicyState(body), null);
  }
  assert.equal({}.admin, undefined);
});
