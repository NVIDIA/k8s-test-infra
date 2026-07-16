"use strict";

const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const { selectReviewers } = require("../src/reviewer-selection.js");

const sourcePath = path.join(__dirname, "..", "src", "reviewer-selection.js");

function changedFile(
  filePath,
  reviewers,
  additions = 1,
  deletions = 0,
) {
  return { path: filePath, reviewers, additions, deletions };
}

function selection(overrides = {}) {
  return selectReviewers({
    candidates: ["alice", "bob", "carol"],
    files: [changedFile("src/main.go", ["alice", "bob", "carol"], 8, 2)],
    seed: { owner: "nvidia", repo: "k8s-test-infra", pr: 42 },
    target: 2,
    author: "pr-author",
    requested: [],
    ...overrides,
  });
}

function weightedRaceKey(seed, login, weight) {
  const digest = crypto
    .createHash("sha256")
    .update(`${seed.owner}/${seed.repo}#${seed.pr}:${login}`)
    .digest("hex");
  const sample = Number.parseInt(digest.slice(0, 13), 16);
  const uniform = (sample + 1) / (0x10000000000000 + 1);
  return -Math.log(uniform) / Math.max(1, weight);
}

function assertSafeTypeError(callback, unsafeValue) {
  assert.throws(callback, (error) => {
    assert.equal(error instanceof TypeError, true);
    assert.equal(error.message.includes(unsafeValue), false);
    return true;
  });
}

test("returns an exact, sorted selection that repeats for identical input", () => {
  const first = selection();
  const second = selection({
    candidates: ["carol", "ALICE", "Bob", "alice"],
    files: [changedFile("src/main.go", ["CAROL", "bob", "Alice"], 8, 2)],
  });

  assert.deepEqual(first, second);
  assert.equal(first.selected.length, 2);
  assert.deepEqual(first.selected, [...first.selected].sort());
  assert.equal(new Set(first.selected).size, first.selected.length);
  assert.deepEqual(first.preserved, []);
  assert.deepEqual(first.uncoveredPaths, []);
});

test("isolates deterministic choices across owner, repository, and PR seed fields", () => {
  const candidates = Array.from({ length: 24 }, (_, index) => `reviewer-${index + 1}`);
  const files = [changedFile("all.go", candidates, 1, 0)];
  const base = {
    candidates,
    files,
    seed: { owner: "org-a", repo: "repo-a", pr: 101 },
    target: 12,
    author: "author",
    requested: [],
  };
  const original = selectReviewers(base).selected;

  assert.notDeepEqual(
    selectReviewers({ ...base, seed: { ...base.seed, owner: "org-b" } }).selected,
    original,
  );
  assert.notDeepEqual(
    selectReviewers({ ...base, seed: { ...base.seed, repo: "repo-b" } }).selected,
    original,
  );
  assert.notDeepEqual(
    selectReviewers({ ...base, seed: { ...base.seed, pr: 102 } }).selected,
    original,
  );
});

test("uses additions plus deletions only for files each candidate reviews", () => {
  const seed = { owner: "nvidia", repo: "k8s-test-infra", pr: 73 };
  const candidates = ["alice", "bob", "carol"];
  const files = [
    changedFile("alice.go", ["alice"], 80, 20),
    changedFile("bob.go", ["bob"], 5, 5),
    changedFile("carol.go", ["carol"], 0, 0),
    changedFile("unrelated.bin", ["other-reviewer"], 1_000_000, 1_000_000),
  ];
  const weights = new Map([
    ["alice", 100],
    ["bob", 10],
    ["carol", 0],
  ]);
  const expected = candidates
    .map((login) => ({
      login,
      key: weightedRaceKey(seed, login, weights.get(login)),
    }))
    .sort((left, right) => left.key - right.key || left.login.localeCompare(right.login))
    .slice(0, 2)
    .map(({ login }) => login)
    .sort();

  assert.deepEqual(selectReviewers({
    candidates,
    files,
    seed,
    target: 2,
    author: "author",
    requested: [],
  }).selected, expected);
});

test("selects without replacement and returns fewer reviewers when eligibility is limited", () => {
  const result = selection({
    candidates: ["ALICE", "alice", "Pr-Author"],
    files: [changedFile("main.go", ["alice", "PR-AUTHOR"], 0, 0)],
    author: "pr-author",
  });

  assert.deepEqual(result, {
    selected: ["alice"],
    preserved: [],
    uncoveredPaths: [],
  });
});

test("preserves only eligible non-author requests and counts them toward the target", () => {
  const result = selection({
    candidates: ["alice", "bob", "carol"],
    files: [
      changedFile("one.go", ["alice", "bob"], 4, 1),
      changedFile("two.go", ["carol"], 2, 1),
    ],
    author: "ALICE",
    requested: ["outsider", "Alice", "BOB", "bob"],
  });

  assert.deepEqual(result.preserved, ["bob"]);
  assert.deepEqual(result.selected, ["carol"]);
  assert.deepEqual(result.uncoveredPaths, []);
});

test("caps preserved requests deterministically so the combined result is the exact target", () => {
  const result = selection({
    candidates: ["carol", "Bob", "alice"],
    requested: ["CAROL", "bob", "ALICE"],
    target: 2,
  });

  assert.deepEqual(result, {
    selected: [],
    preserved: ["alice", "bob"],
    uncoveredPaths: [],
  });
});

test("fills path coverage gaps before choosing an already-covered candidate", () => {
  const result = selection({
    candidates: ["alice", "bob", "heavy-old-area"],
    files: [
      changedFile("old/large.go", ["alice", "heavy-old-area"], 50_000, 50_000),
      changedFile("new/small.go", ["bob"], 1, 0),
    ],
    requested: ["Alice"],
  });

  assert.deepEqual(result, {
    selected: ["bob"],
    preserved: ["alice"],
    uncoveredPaths: [],
  });
});

test("returns sorted uncovered paths when the target cannot cover every changed path", () => {
  const result = selection({
    candidates: ["alice", "bob", "carol"],
    files: [
      changedFile("z/only-alice.go", ["alice"], 10, 0),
      changedFile("a/only-bob.go", ["bob"], 10, 0),
      changedFile("m/no-candidate.go", ["outsider"], 10, 0),
    ],
    target: 1,
  });

  assert.equal(result.selected.length, 1);
  assert.deepEqual(result.uncoveredPaths, [...result.uncoveredPaths].sort());
  assert.equal(result.uncoveredPaths.includes("m/no-candidate.go"), true);
  assert.equal(result.uncoveredPaths.length, 2);
});

test("rejects malformed top-level, candidate, file, and request shapes", async (t) => {
  const cases = [
    ["missing options", () => selectReviewers(), /options.*object/i],
    ["unknown option", () => selection({ command: "run" }), /options.*unknown/i],
    ["candidates object", () => selection({ candidates: {} }), /candidates.*array/i],
    ["candidate non-string", () => selection({ candidates: [42] }), /candidate.*login/i],
    ["candidate malformed", () => selection({ candidates: ["bad--login"] }), /candidate.*login/i],
    ["files object", () => selection({ files: {} }), /files.*array/i],
    ["file null", () => selection({ files: [null] }), /file.*object/i],
    [
      "file unknown key",
      () => selection({
        files: [{ ...changedFile("x.go", ["alice"]), changes: 1 }],
      }),
      /file.*unknown/i,
    ],
    ["absolute path", () => selection({ files: [changedFile("/x.go", ["alice"])] }), /file path/i],
    ["duplicate path", () => selection({
      files: [changedFile("x.go", ["alice"]), changedFile("x.go", ["bob"])],
    }), /file path.*unique/i],
    ["reviewers object", () => selection({
      files: [changedFile("x.go", {}, 1, 0)],
    }), /reviewers.*array/i],
    ["reviewer malformed", () => selection({
      files: [changedFile("x.go", ["not_a_login"], 1, 0)],
    }), /reviewer.*login/i],
    ["negative additions", () => selection({
      files: [changedFile("x.go", ["alice"], -1, 0)],
    }), /additions.*non-negative safe integer/i],
    ["fractional deletions", () => selection({
      files: [changedFile("x.go", ["alice"], 1, 0.5)],
    }), /deletions.*non-negative safe integer/i],
    ["string additions", () => selection({
      files: [changedFile("x.go", ["alice"], "1", 0)],
    }), /additions.*non-negative safe integer/i],
    ["unsafe line count", () => selection({
      files: [changedFile("x.go", ["alice"], Number.MAX_SAFE_INTEGER + 1, 0)],
    }), /additions.*non-negative safe integer/i],
    ["requested object", () => selection({ requested: {} }), /requested.*array/i],
    ["requested malformed", () => selection({ requested: ["not_a_login"] }), /requested.*login/i],
  ];

  for (const [name, callback, expected] of cases) {
    await t.test(name, () => assert.throws(callback, expected));
  }
});

test("requires a complete safe seed, a positive target, and a mandatory author", async (t) => {
  const cases = [
    ["seed array", () => selection({ seed: [] }), /seed.*object/i],
    ["seed unknown key", () => selection({
      seed: { owner: "nvidia", repo: "repo", pr: 1, ref: "main" },
    }), /seed.*unknown/i],
    ["missing owner", () => selection({ seed: { repo: "repo", pr: 1 } }), /seed owner/i],
    ["invalid owner", () => selection({
      seed: { owner: "bad--owner", repo: "repo", pr: 1 },
    }), /seed owner/i],
    ["invalid repo", () => selection({
      seed: { owner: "owner", repo: "a/repo", pr: 1 },
    }), /seed repository/i],
    ["zero PR", () => selection({
      seed: { owner: "owner", repo: "repo", pr: 0 },
    }), /seed PR.*positive safe integer/i],
    ["fractional PR", () => selection({
      seed: { owner: "owner", repo: "repo", pr: 1.5 },
    }), /seed PR.*positive safe integer/i],
    ["zero target", () => selection({ target: 0 }), /target.*positive safe integer/i],
    ["fractional target", () => selection({ target: 1.5 }), /target.*positive safe integer/i],
    ["missing author", () => {
      const options = {
        candidates: ["alice"],
        files: [changedFile("x.go", ["alice"])],
        seed: { owner: "owner", repo: "repo", pr: 1 },
        target: 1,
        requested: [],
      };
      return selectReviewers(options);
    }, /author.*login/i],
    ["malformed author", () => selection({ author: "bad--author" }), /author.*login/i],
  ];

  for (const [name, callback, expected] of cases) {
    await t.test(name, () => assert.throws(callback, expected));
  }
});

test("does not echo untrusted control values in diagnostics", async (t) => {
  const unsafeValues = ["alice\nforged", "alice\u001b[31m", "alice\u202eadmin"];

  for (const unsafeValue of unsafeValues) {
    await t.test(JSON.stringify(unsafeValue), () => {
      assertSafeTypeError(() => selection({ candidates: [unsafeValue] }), unsafeValue);
      assertSafeTypeError(
        () => selection({ files: [changedFile(unsafeValue, ["alice"])] }),
        unsafeValue,
      );
      assertSafeTypeError(
        () => selection({ seed: { owner: "owner", repo: unsafeValue, pr: 1 } }),
        unsafeValue,
      );
      assertSafeTypeError(() => selection({ author: unsafeValue }), unsafeValue);
      assertSafeTypeError(() => selection({ requested: [unsafeValue] }), unsafeValue);
    });
  }
});

test("is a pure local selector without random, clock, filesystem, or GitHub access", () => {
  const source = fs.readFileSync(sourcePath, "utf8");

  assert.doesNotMatch(source, /Math\.random|Date\.|new Date|node:fs|@actions\/github/);
});
