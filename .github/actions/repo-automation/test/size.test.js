"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { classifySize } = require("../src/size.js");

const thresholds = Object.freeze({ S: 0, M: 50, L: 250, XL: 1000 });

test("classifies exact changed-line boundaries from additions plus deletions", async (t) => {
  const cases = [
    { additions: 0, deletions: 0, changedLines: 0, label: "size/S" },
    { additions: 24, deletions: 25, changedLines: 49, label: "size/S" },
    { additions: 25, deletions: 25, changedLines: 50, label: "size/M" },
    { additions: 125, deletions: 124, changedLines: 249, label: "size/M" },
    { additions: 125, deletions: 125, changedLines: 250, label: "size/L" },
    { additions: 500, deletions: 499, changedLines: 999, label: "size/L" },
    { additions: 500, deletions: 500, changedLines: 1000, label: "size/XL" },
  ];

  for (const entry of cases) {
    await t.test(`total ${entry.changedLines}`, () => {
      assert.deepEqual(
        classifySize(entry.additions, entry.deletions, thresholds),
        { changedLines: entry.changedLines, label: entry.label },
      );
    });
  }
});

test("counts aggregate numeric totals for added, deleted, and modified file categories", async (t) => {
  const cases = [
    ["added file additions", 49, 0, 49],
    ["deleted file deletions", 0, 49, 49],
    ["modified file additions and deletions", 20, 29, 49],
  ];

  for (const [name, additions, deletions, changedLines] of cases) {
    await t.test(name, () => {
      assert.deepEqual(classifySize(additions, deletions, thresholds), {
        changedLines,
        label: "size/S",
      });
    });
  }
});

test("rejects invalid numeric inputs and issue-like objects", async (t) => {
  const issue = {
    number: 42,
    title: "feat: issue-like input",
    additions: 10,
    deletions: 5,
  };
  const cases = [
    ["negative additions", -1, 0],
    ["negative deletions", 0, -1],
    ["fractional additions", 0.5, 0],
    ["fractional deletions", 0, 0.5],
    ["string additions", "1", 0],
    ["string deletions", 0, "1"],
    ["NaN additions", Number.NaN, 0],
    ["infinite deletions", 0, Number.POSITIVE_INFINITY],
    ["null additions", null, 0],
    ["plain object additions", { value: 1 }, 0],
    ["issue-like additions", issue, 0],
    ["issue-like deletions", 0, issue],
  ];

  for (const [name, additions, deletions] of cases) {
    await t.test(name, () => {
      assert.throws(
        () => classifySize(additions, deletions, thresholds),
        { name: "TypeError" },
      );
    });
  }
});

test("rejects invalid threshold policies", async (t) => {
  const cases = [
    ["null", null],
    ["array", [0, 50, 250, 1000]],
    ["missing size", { S: 0, M: 50, L: 250 }],
    ["unknown size", { ...thresholds, XXL: 2000 }],
    ["non-zero S", { S: 1, M: 50, L: 250, XL: 1000 }],
    ["negative threshold", { S: 0, M: -1, L: 250, XL: 1000 }],
    ["fractional threshold", { S: 0, M: 50.5, L: 250, XL: 1000 }],
    ["string threshold", { S: 0, M: "50", L: 250, XL: 1000 }],
    ["duplicate boundary", { S: 0, M: 50, L: 50, XL: 1000 }],
    ["descending boundary", { S: 0, M: 50, L: 25, XL: 1000 }],
  ];

  for (const [name, invalidThresholds] of cases) {
    await t.test(name, () => {
      assert.throws(
        () => classifySize(10, 5, invalidThresholds),
        { name: "TypeError" },
      );
    });
  }
});
