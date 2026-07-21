"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { planLabelChanges } = require("../src/label-reconciliation.js");

function label(name, color = "123abc", description = `${name} description`) {
  return { name, color, description };
}

test("plans a create for each missing declared label", () => {
  const declared = [label("kind/test")];

  assert.deepEqual(planLabelChanges(declared, []), {
    creates: declared,
    updates: [],
    unchanged: [],
    unmanaged: [],
  });
});

test("plans an update when declared metadata differs", async (t) => {
  await t.test("color differs", () => {
    const declared = [label("kind/bug", "abcdef")];
    const existing = [label("kind/bug", "123456")];

    assert.deepEqual(planLabelChanges(declared, existing).updates, declared);
  });

  await t.test("description differs", () => {
    const declared = [label("kind/bug", "abcdef", "Declared description")];
    const existing = [label("kind/bug", "abcdef", "Existing description")];

    assert.deepEqual(planLabelChanges(declared, existing).updates, declared);
  });
});

test("compares names case-insensitively while preserving declared spelling", () => {
  const declared = [label("Kind/Bug")];
  const existing = [label("kind/bug", "123abc", declared[0].description)];

  assert.deepEqual(planLabelChanges(declared, existing), {
    creates: [],
    updates: [],
    unchanged: declared,
    unmanaged: [],
  });
});

test("plans no operation for an exact match", () => {
  const declared = [label("kind/bug")];

  assert.deepEqual(planLabelChanges(declared, declared), {
    creates: [],
    updates: [],
    unchanged: declared,
    unmanaged: [],
  });
});

test("classifies unmanaged existing labels without planning deletion", () => {
  const existing = [label("legacy/keep")];
  const plan = planLabelChanges([], existing);

  assert.deepEqual(plan, {
    creates: [],
    updates: [],
    unchanged: [],
    unmanaged: existing,
  });
  assert.equal(Object.hasOwn(plan, "deletes"), false);
});

test("sorts every plan category by lowercase label name", () => {
  const declared = [
    label("Zulu"),
    label("echo"),
    label("alpha", "abcdef"),
    label("Golf"),
    label("Bravo"),
    label("foxtrot", "abcdef"),
  ];
  const existing = [
    label("zulu", "123abc", declared[0].description),
    label("ALPHA", "123456"),
    label("omega"),
    label("golf", "123abc", declared[3].description),
    label("Delta"),
    label("Foxtrot", "123456", declared[5].description),
    label("charlie"),
  ];

  const plan = planLabelChanges(declared, existing);

  assert.deepEqual(plan.creates.map(({ name }) => name), ["Bravo", "echo"]);
  assert.deepEqual(plan.updates.map(({ name }) => name), ["alpha", "foxtrot"]);
  assert.deepEqual(plan.unchanged.map(({ name }) => name), ["Golf", "Zulu"]);
  assert.deepEqual(plan.unmanaged.map(({ name }) => name), ["charlie", "Delta", "omega"]);
});

test("fails closed when existing label names are duplicated case-insensitively", () => {
  const existing = [label("kind/bug"), label("Kind/Bug")];

  assert.throws(
    () => planLabelChanges([], existing),
    /duplicate existing label name: Kind\/Bug/i,
  );
});

test("fails closed when declared labels are invalid", async (t) => {
  const invalidCases = [
    ["not an array", null],
    ["not an object", [null]],
    ["blank name", [label("  ")]],
    ["invalid color", [label("kind/bug", "ABCDEF")]],
    ["blank description", [label("kind/bug", "abcdef", "  ")]],
    ["duplicate name", [label("kind/bug"), label("Kind/Bug")]],
  ];

  for (const [name, declared] of invalidCases) {
    await t.test(name, () => {
      assert.throws(() => planLabelChanges(declared, []), /invalid declared labels/i);
    });
  }
});
