"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const {
  isManagedMetadataLabel,
  isManagedPolicyLabel,
} = require("../src/managed-labels.js");

test("metadata and policy label authorities are disjoint", () => {
  assert.equal(isManagedMetadataLabel("kind/feature"), true);
  assert.equal(isManagedMetadataLabel("lgtm"), false);
  assert.equal(isManagedPolicyLabel("lgtm"), true);
  assert.equal(isManagedPolicyLabel("approved"), true);
  assert.equal(isManagedPolicyLabel("do-not-merge/hold"), true);
  assert.equal(isManagedPolicyLabel("do-not-merge/needs-approval"), true);
  assert.equal(isManagedPolicyLabel("kind/feature"), false);
  assert.equal(isManagedPolicyLabel("do-not-merge/custom"), false);
});
