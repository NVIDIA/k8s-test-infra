"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const {
  evaluateApprovalCoverage,
  selectApprovers,
} = require("../src/approval-coverage.js");

const sourcePath = path.join(__dirname, "..", "src", "approval-coverage.js");
const HEAD = "a".repeat(40);
const OLD_HEAD = "b".repeat(40);

function changedFile(filePath, approvers) {
  return { path: filePath, approvers };
}

function review(id, user, state, commitOid = HEAD, submittedAt) {
  return {
    id,
    user,
    state,
    commitOid,
    submittedAt: submittedAt ?? `2026-07-16T10:${String(id).padStart(2, "0")}:00Z`,
  };
}

function evaluation(overrides = {}) {
  return evaluateApprovalCoverage({
    files: [changedFile("src/main.go", ["alice", "bob"])],
    reviews: [review(1, "alice", "APPROVED")],
    headOid: HEAD,
    author: "pr-author",
    ...overrides,
  });
}

function effectiveReview(user, approved = true, overrides = {}) {
  return {
    user,
    state: approved ? "APPROVED" : "CHANGES_REQUESTED",
    commitOid: HEAD,
    reviewId: 1,
    submittedAt: "2026-07-16T10:01:00Z",
    approved,
    ...overrides,
  };
}

function selection(overrides = {}) {
  return selectApprovers({
    files: [changedFile("src/main.go", ["alice", "bob"])],
    effectiveReviews: [],
    requested: [],
    author: "pr-author",
    ...overrides,
  });
}

function assertSafeTypeError(callback, unsafeValue) {
  assert.throws(callback, (error) => {
    assert.equal(error instanceof TypeError, true);
    assert.equal(error.message.includes(unsafeValue), false);
    return true;
  });
}

test("counts only a current-head approval from an applicable non-author approver", () => {
  assert.deepEqual(evaluation(), {
    approved: true,
    effectiveReviews: [{
      user: "alice",
      state: "APPROVED",
      commitOid: HEAD,
      reviewId: 1,
      submittedAt: "2026-07-16T10:01:00Z",
      approved: true,
    }],
    coveredPaths: ["src/main.go"],
    uncoveredPaths: [],
  });

  assert.deepEqual(evaluation({
    reviews: [review(1, "alice", "APPROVED", OLD_HEAD)],
  }), {
    approved: false,
    effectiveReviews: [{
      user: "alice",
      state: "APPROVED",
      commitOid: OLD_HEAD,
      reviewId: 1,
      submittedAt: "2026-07-16T10:01:00Z",
      approved: false,
    }],
    coveredPaths: [],
    uncoveredPaths: ["src/main.go"],
  });
});

test("reduces each user case-insensitively by submitted time and review id", () => {
  const result = evaluation({
    reviews: [
      review(30, "BOB", "APPROVED", HEAD, "2026-07-16T10:03:00Z"),
      review(20, "Alice", "CHANGES_REQUESTED", HEAD, "2026-07-16T10:02:00Z"),
      review(10, "ALICE", "APPROVED", HEAD, "2026-07-16T10:01:00Z"),
      review(31, "bob", "DISMISSED", HEAD, "2026-07-16T10:03:00Z"),
    ],
  });

  assert.equal(result.approved, false);
  assert.deepEqual(result.coveredPaths, []);
  assert.deepEqual(result.uncoveredPaths, ["src/main.go"]);
  assert.deepEqual(result.effectiveReviews.map(({ user, state, reviewId }) => ({
    user,
    state,
    reviewId,
  })), [
    { user: "alice", state: "CHANGES_REQUESTED", reviewId: 20 },
    { user: "bob", state: "DISMISSED", reviewId: 31 },
  ]);
});

test("a later COMMENTED review preserves an approval but never creates one", () => {
  const preserved = evaluation({
    reviews: [
      review(1, "alice", "APPROVED"),
      review(2, "ALICE", "COMMENTED"),
    ],
  });
  assert.equal(preserved.approved, true);
  assert.deepEqual(preserved.effectiveReviews.map(({ state, reviewId }) => ({
    state,
    reviewId,
  })), [{ state: "APPROVED", reviewId: 1 }]);

  const commentOnly = evaluation({ reviews: [review(1, "alice", "COMMENTED")] });
  assert.equal(commentOnly.approved, false);
  assert.deepEqual(commentOnly.effectiveReviews.map(({ state, approved }) => ({
    state,
    approved,
  })), [{ state: "COMMENTED", approved: false }]);
});

test("later CHANGES_REQUESTED and DISMISSED reviews cancel prior approvals", async (t) => {
  for (const state of ["CHANGES_REQUESTED", "DISMISSED"]) {
    await t.test(state, () => {
      const result = evaluation({
        reviews: [
          review(1, "alice", "APPROVED"),
          review(2, "alice", state, OLD_HEAD),
        ],
      });
      assert.equal(result.approved, false);
      assert.deepEqual(result.coveredPaths, []);
      assert.equal(result.effectiveReviews[0].state, state);
      assert.equal(result.effectiveReviews[0].approved, false);
    });
  }
});

test("never counts the author or a non-applicable reviewer", () => {
  const result = evaluation({
    files: [changedFile("src/main.go", ["pr-author", "alice"])],
    reviews: [
      review(1, "PR-AUTHOR", "APPROVED"),
      review(2, "outsider", "APPROVED"),
    ],
  });

  assert.equal(result.approved, false);
  assert.deepEqual(result.coveredPaths, []);
  assert.deepEqual(result.uncoveredPaths, ["src/main.go"]);
  assert.deepEqual(result.effectiveReviews.map(({ user, approved }) => ({ user, approved })), [
    { user: "outsider", approved: true },
    { user: "pr-author", approved: false },
  ]);
});

test("requires hierarchical per-file approver coverage without a repository-wide shortcut", () => {
  const files = [
    changedFile("README.md", ["root-approver"]),
    changedFile("nested/main.go", ["nested-approver", "shared-approver"]),
    changedFile("isolated/main.go", ["isolated-approver"]),
  ];
  const partial = evaluation({
    files,
    reviews: [
      review(1, "root-approver", "APPROVED"),
      review(2, "nested-approver", "APPROVED"),
    ],
  });
  assert.equal(partial.approved, false);
  assert.deepEqual(partial.coveredPaths, ["README.md", "nested/main.go"]);
  assert.deepEqual(partial.uncoveredPaths, ["isolated/main.go"]);

  const complete = evaluation({
    files,
    reviews: [...partial.effectiveReviews.map((item, index) => review(
      index + 1,
      item.user,
      item.state,
      item.commitOid,
      item.submittedAt,
    )), review(3, "isolated-approver", "APPROVED")],
  });
  assert.equal(complete.approved, true);
  assert.deepEqual(complete.uncoveredPaths, []);
});

test("does not accept labels or other display state as approval evidence", () => {
  assert.throws(
    () => evaluateApprovalCoverage({
      files: [changedFile("x.go", ["alice"])],
      reviews: [],
      headOid: HEAD,
      author: "author",
      labels: ["approved"],
    }),
    /options.*unknown/i,
  );
});

test("greedily selects maximum new path coverage then lowercase login", () => {
  const files = [
    changedFile("a.go", ["alice", "bob"]),
    changedFile("b.go", ["alice", "carol"]),
    changedFile("c.go", ["bob", "carol"]),
    changedFile("d.go", ["zoe"]),
  ];

  assert.deepEqual(selection({ files }), {
    selected: ["alice", "bob", "zoe"],
    uncoveredPaths: [],
  });
});

test("existing approvals and pending eligible requests cover paths without duplicate requests", () => {
  const files = [
    changedFile("a.go", ["alice"]),
    changedFile("b.go", ["bob", "carol"]),
    changedFile("c.go", ["carol"]),
  ];
  const result = selection({
    files,
    effectiveReviews: [
      effectiveReview("ALICE"),
      effectiveReview("outsider"),
      effectiveReview("old-head", false, {
        state: "APPROVED",
        commitOid: OLD_HEAD,
      }),
    ],
    requested: ["BOB", "bob", "outsider", "PR-AUTHOR"],
  });

  assert.deepEqual(result, {
    selected: ["carol"],
    uncoveredPaths: [],
  });
});

test("returns only truly uncoverable paths after exhausting eligible approvers", () => {
  const result = selection({
    files: [
      changedFile("z/author-only.go", ["PR-AUTHOR"]),
      changedFile("a/no-owners.go", []),
      changedFile("m/covered.go", ["alice"]),
    ],
  });

  assert.deepEqual(result, {
    selected: ["alice"],
    uncoveredPaths: ["a/no-owners.go", "z/author-only.go"],
  });
});

test("normalizes approvers and requested users case-insensitively", () => {
  assert.deepEqual(selection({
    files: [changedFile("x.go", ["ALICE", "alice", "Bob"])],
    requested: ["BOB", "bob"],
    author: "AUTHOR",
  }), {
    selected: [],
    uncoveredPaths: [],
  });
});

test("rejects malformed top-level, file, review, and effective-review schemas", async (t) => {
  const inherited = Object.create({ files: [] });
  inherited.reviews = [];
  inherited.headOid = HEAD;
  inherited.author = "author";
  const cases = [
    ["missing options", () => evaluateApprovalCoverage(), /options.*object/i],
    ["custom prototype", () => evaluateApprovalCoverage(inherited), /options.*plain object/i],
    ["empty files", () => evaluation({ files: [] }), /files.*non-empty array/i],
    ["files object", () => evaluation({ files: {} }), /files.*non-empty array/i],
    ["file null", () => evaluation({ files: [null] }), /file.*plain object/i],
    ["file unknown", () => evaluation({
      files: [{ ...changedFile("x.go", ["alice"]), reviewers: ["alice"] }],
    }), /file.*unknown/i],
    ["absolute path", () => evaluation({
      files: [changedFile("/x.go", ["alice"])],
    }), /file path/i],
    ["traversal path", () => evaluation({
      files: [changedFile("a/../x.go", ["alice"])],
    }), /file path/i],
    ["duplicate path", () => evaluation({
      files: [changedFile("x.go", ["alice"]), changedFile("x.go", ["bob"])],
    }), /file path.*unique/i],
    ["approvers object", () => evaluation({
      files: [changedFile("x.go", {})],
    }), /approvers.*array/i],
    ["bad approver", () => evaluation({
      files: [changedFile("x.go", ["not_a_login"])],
    }), /approver.*login/i],
    ["reviews object", () => evaluation({ reviews: {} }), /reviews.*array/i],
    ["review array", () => evaluation({ reviews: [[]] }), /review.*plain object/i],
    ["review unknown", () => evaluation({
      reviews: [{ ...review(1, "alice", "APPROVED"), body: "/approve" }],
    }), /review.*unknown/i],
    ["zero review id", () => evaluation({
      reviews: [review(0, "alice", "APPROVED")],
    }), /review id.*positive safe integer/i],
    ["bad user", () => evaluation({
      reviews: [review(1, "bad--login", "APPROVED")],
    }), /review user.*login/i],
    ["bad state", () => evaluation({
      reviews: [review(1, "alice", "PENDING")],
    }), /review state/i],
    ["bad commit", () => evaluation({
      reviews: [review(1, "alice", "APPROVED", "main")],
    }), /review commit OID/i],
    ["bad timestamp", () => evaluation({
      reviews: [review(1, "alice", "APPROVED", HEAD, "yesterday")],
    }), /review submitted time/i],
    ["duplicate id", () => evaluation({
      reviews: [review(1, "alice", "APPROVED"), review(1, "bob", "APPROVED")],
    }), /review id.*unique/i],
    ["bad head", () => evaluation({ headOid: "main" }), /head OID/i],
    ["bad author", () => evaluation({ author: "bad--author" }), /author.*login/i],
    ["selection effective object", () => selection({ effectiveReviews: {} }), /effectiveReviews.*array/i],
    ["selection duplicate effective user", () => selection({
      effectiveReviews: [effectiveReview("alice"), effectiveReview("ALICE", false)],
    }), /effective review user.*unique/i],
    ["selection forged approval state", () => selection({
      effectiveReviews: [effectiveReview("alice", true, { state: "DISMISSED" })],
    }), /effective review approval/i],
    ["selection requested object", () => selection({ requested: {} }), /requested.*array/i],
  ];

  for (const [name, callback, expected] of cases) {
    await t.test(name, () => assert.throws(callback, expected));
  }
});

test("rejects unsafe controls and prototype-bearing nested records without echo", async (t) => {
  const unsafeValues = ["alice\nforged", "alice\u001b[31m", "alice\u202eadmin"];
  for (const unsafeValue of unsafeValues) {
    await t.test(JSON.stringify(unsafeValue), () => {
      assertSafeTypeError(() => evaluation({ author: unsafeValue }), unsafeValue);
      assertSafeTypeError(() => evaluation({
        files: [changedFile(unsafeValue, ["alice"])],
      }), unsafeValue);
      assertSafeTypeError(() => evaluation({
        reviews: [review(1, unsafeValue, "APPROVED")],
      }), unsafeValue);
      assertSafeTypeError(() => selection({ requested: [unsafeValue] }), unsafeValue);
    });
  }

  const file = Object.create({ path: "x.go" });
  file.approvers = ["alice"];
  assert.throws(() => evaluation({ files: [file] }), /file.*plain object/i);
});

test("is pure and does not consult labels, clocks, filesystems, or GitHub APIs", () => {
  const source = fs.readFileSync(sourcePath, "utf8");
  assert.doesNotMatch(
    source,
    /@actions\/github|node:fs|Math\.random|Date\.now|new Date|\blabels\b/,
  );
});
