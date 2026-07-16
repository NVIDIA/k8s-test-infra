"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { classifyTitle } = require("../src/title.js");

const invalidTitle = {
  valid: false,
  type: null,
  scope: null,
  breaking: false,
  description: null,
  label: null,
  error: "invalid pull request title",
};

test("maps each supported Conventional Commit type to exactly one kind label", async (t) => {
  const cases = [
    ["feat: add feature", "feat", "kind/feature"],
    ["fix: repair bug", "fix", "kind/bug"],
    ["docs: explain behavior", "docs", "kind/documentation"],
    ["test: cover behavior", "test", "kind/test"],
    ["refactor: simplify parser", "refactor", "kind/refactor"],
    ["perf: reduce allocations", "perf", "kind/performance"],
    ["build: update build", "build", "kind/ci"],
    ["ci: update workflow", "ci", "kind/ci"],
    ["chore: clean repository", "chore", "kind/cleanup"],
    ["chore(deps): update dependency", "chore", "kind/dependencies"],
    ["revert: restore behavior", "revert", "kind/revert"],
  ];

  for (const [title, type, label] of cases) {
    await t.test(title, () => {
      const result = classifyTitle(title);

      assert.deepEqual(result, {
        valid: true,
        type,
        scope: title === "chore(deps): update dependency" ? "deps" : null,
        breaking: false,
        description: title.slice(title.indexOf(": ") + 2),
        label,
        error: null,
      });
      assert.deepEqual(
        Object.values(result).filter(
          (value) => typeof value === "string" && value.startsWith("kind/"),
        ),
        [label],
      );
    });
  }
});

test("accepts an optional scope and breaking marker", async (t) => {
  const cases = [
    ["feat(api): expose endpoint", "api", false],
    ["feat!: change defaults", null, true],
    ["feat(api)!: replace endpoint", "api", true],
  ];

  for (const [title, scope, breaking] of cases) {
    await t.test(title, () => {
      assert.deepEqual(classifyTitle(title), {
        valid: true,
        type: "feat",
        scope,
        breaking,
        description: title.slice(title.indexOf(": ") + 2),
        label: "kind/feature",
        error: null,
      });
    });
  }
});

test("rejects titles outside the full supported grammar", async (t) => {
  const cases = [
    ["leading whitespace", " feat: add feature"],
    ["missing separator space", "feat:add feature"],
    ["missing description", "feat:"],
    ["empty description", "feat: "],
    ["whitespace-only description", "feat:    "],
    ["bracket-style title", "[Feature] add feature"],
    ["unsupported type", "style: format code"],
    ["line-feed injection", "feat: add feature\nfix: injected"],
    ["carriage-return injection", "feat: add feature\rfix: injected"],
    ["carriage-return line-feed injection", "feat: add feature\r\nfix: injected"],
    ["dependency-like chore scope", "chore(deps-extra): update dependency"],
    ["other chore scope", "chore(tooling): update tooling"],
  ];

  for (const [name, title] of cases) {
    await t.test(name, () => {
      assert.deepEqual(classifyTitle(title), invalidTitle);
      assert.deepEqual(
        Object.values(classifyTitle(title)).filter(
          (value) => typeof value === "string" && value.startsWith("kind/"),
        ),
        [],
      );
    });
  }
});
