"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { policyDigest } = require("../src/policy-digest.js");

function input(overrides = {}) {
  return {
    repository: "nvidia/k8s-test-infra",
    revision: "a".repeat(40),
    policy: { commands: { historyLimit: 256 }, protectedBranches: ["main"] },
    ownerSources: [{ path: "/OWNERS", source: "reviewers: [alice]\n" }],
    aliasesSource: "aliases: {}\n",
    ...overrides,
  };
}

test("policy digest is deterministic and insensitive to object key order", () => {
  const first = policyDigest(input());
  const second = policyDigest(input({
    policy: { protectedBranches: ["main"], commands: { historyLimit: 256 } },
  }));

  assert.match(first, /^[0-9a-f]{64}$/);
  assert.equal(first, second);
});

test("policy digest binds the repository, revision, policy, owners, and aliases", async (t) => {
  const baseline = policyDigest(input());
  const cases = [
    ["repository", { repository: "nvidia/other" }],
    ["revision", { revision: "b".repeat(40) }],
    ["policy", { policy: { commands: { historyLimit: 255 }, protectedBranches: ["main"] } }],
    ["owners", { ownerSources: [{ path: "/OWNERS", source: "reviewers: [bob]\n" }] }],
    ["aliases", { aliasesSource: "aliases: {team: [alice]}\n" }],
  ];
  for (const [name, change] of cases) {
    await t.test(name, () => assert.notEqual(policyDigest(input(change)), baseline));
  }
});

test("policy digest rejects unsafe or ambiguous input", () => {
  assert.throws(() => policyDigest(input({ repository: "NVIDIA/k8s-test-infra" })), TypeError);
  assert.throws(() => policyDigest(input({ ownerSources: [
    { path: "/OWNERS", source: "one" },
    { path: "/OWNERS", source: "two" },
  ] })), TypeError);
  assert.throws(() => policyDigest(input({ policy: { bad: undefined } })), TypeError);
});
