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
const HEAD_64 = "c".repeat(64);
const OLD_HEAD_64 = "d".repeat(64);

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

function pendingReview(id, user, commitOid = HEAD) {
  return { id, user, state: "PENDING", commitOid };
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

function effectiveReview(user, overrides = {}) {
  return {
    user,
    state: "APPROVED",
    commitOid: HEAD,
    reviewId: 1,
    submittedAt: "2026-07-16T10:01:00Z",
    ...overrides,
  };
}

function selection(overrides = {}) {
  return selectApprovers({
    files: [changedFile("src/main.go", ["alice", "bob"])],
    effectiveReviews: [],
    requested: [],
    headOid: HEAD,
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
    }],
    coveredPaths: [],
    uncoveredPaths: ["src/main.go"],
  });
});

test("binds normalized 40-or-64-character review OIDs to the exact current head", async (t) => {
  await t.test("matching 64-character OID", () => {
    const result = evaluation({
      headOid: HEAD_64.toUpperCase(),
      reviews: [review(1, "alice", "APPROVED", HEAD_64.toUpperCase())],
    });
    assert.equal(result.approved, true);
    assert.equal(result.effectiveReviews[0].commitOid, HEAD_64);
  });

  await t.test("stale 64-character OID", () => {
    const result = evaluation({
      headOid: HEAD_64,
      reviews: [review(1, "alice", "APPROVED", OLD_HEAD_64)],
    });
    assert.equal(result.approved, false);
  });

  await t.test("mixed-length OIDs are not equal", () => {
    const result = evaluation({
      headOid: "e".repeat(64),
      reviews: [review(1, "alice", "APPROVED", "E".repeat(40))],
    });
    assert.equal(result.approved, false);
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
  assert.deepEqual(commentOnly.effectiveReviews.map(({ state }) => state), ["COMMENTED"]);
});

test("ignores REST-valid unsubmitted PENDING reviews before or after approval", () => {
  const variants = [
    [pendingReview(2, "ALICE"), review(1, "alice", "APPROVED")],
    [review(1, "alice", "APPROVED"), pendingReview(2, "ALICE", null)],
  ];

  for (const reviews of variants) {
    const result = evaluation({ reviews });
    assert.equal(result.approved, true);
    assert.deepEqual(result.effectiveReviews.map(({ state }) => state), ["APPROVED"]);
  }

  const pendingOnly = evaluation({ reviews: [pendingReview(1, "alice", null)] });
  assert.equal(pendingOnly.approved, false);
  assert.deepEqual(pendingOnly.effectiveReviews, []);
});

test("accepts nullable submitted commit OIDs but never grants them head authority", () => {
  const superseded = evaluation({
    reviews: [
      review(1, "alice", "APPROVED", null),
      review(2, "alice", "APPROVED", HEAD),
    ],
  });
  assert.equal(superseded.approved, true);
  assert.equal(superseded.effectiveReviews[0].commitOid, HEAD);

  const latestIsNullable = evaluation({
    reviews: [
      review(1, "alice", "APPROVED", HEAD),
      review(2, "alice", "APPROVED", null),
    ],
  });
  assert.equal(latestIsNullable.approved, false);
  assert.equal(latestIsNullable.effectiveReviews[0].commitOid, null);
  assert.deepEqual(latestIsNullable.uncoveredPaths, ["src/main.go"]);
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
  assert.deepEqual(result.effectiveReviews.map(({ user }) => user), ["outsider", "pr-author"]);
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
      effectiveReview("old-head", {
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

test("selectApprovers independently binds approval records to its exact head OID", async (t) => {
  const files = [changedFile("x.go", ["alice"])];

  await t.test("current 64-character head suppresses a duplicate request", () => {
    assert.deepEqual(selection({
      files,
      headOid: HEAD_64,
      effectiveReviews: [effectiveReview("alice", {
        commitOid: HEAD_64.toUpperCase(),
      })],
    }), { selected: [], uncoveredPaths: [] });
  });

  await t.test("stale 64-character approval does not suppress a request", () => {
    assert.deepEqual(selection({
      files,
      headOid: HEAD_64,
      effectiveReviews: [effectiveReview("alice", { commitOid: OLD_HEAD_64 })],
    }), { selected: ["alice"], uncoveredPaths: [] });
  });

  await t.test("mixed-length approval does not suppress a request", () => {
    assert.deepEqual(selection({
      files,
      headOid: "f".repeat(64),
      effectiveReviews: [effectiveReview("alice", { commitOid: "F".repeat(40) })],
    }), { selected: ["alice"], uncoveredPaths: [] });
  });

  await t.test("nullable approval OID does not suppress a request", () => {
    assert.deepEqual(selection({
      files,
      effectiveReviews: [effectiveReview("alice", { commitOid: null })],
    }), { selected: ["alice"], uncoveredPaths: [] });
  });

  await t.test("externally asserted approval authority is rejected", () => {
    assert.throws(() => selection({
      files,
      headOid: HEAD,
      effectiveReviews: [{
        ...effectiveReview("alice", { commitOid: OLD_HEAD }),
        approved: true,
      }],
    }), /effective review.*unknown/i);
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
    ["pending with submitted time", () => evaluation({
      reviews: [review(1, "alice", "PENDING")],
    }), /pending review.*unsubmitted/i],
    ["submitted state without time", () => evaluation({
      reviews: [{ id: 1, user: "alice", state: "APPROVED", commitOid: HEAD }],
    }), /submitted review.*time/i],
    ["pending review without commit field", () => evaluation({
      reviews: [{ id: 1, user: "alice", state: "PENDING" }],
    }), /review commit OID/i],
    ["pending review with null time", () => evaluation({
      reviews: [{ ...pendingReview(1, "alice"), submittedAt: null }],
    }), /pending review.*unsubmitted/i],
    ["unsupported state", () => evaluation({
      reviews: [review(1, "alice", "OUTDATED")],
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
      effectiveReviews: [
        effectiveReview("alice"),
        effectiveReview("ALICE", { state: "CHANGES_REQUESTED" }),
      ],
    }), /effective review user.*unique/i],
    ["selection forged approval boolean", () => selection({
      effectiveReviews: [{ ...effectiveReview("alice"), approved: true }],
    }), /effective review.*unknown/i],
    ["selection missing head", () => selectApprovers({
      files: [changedFile("x.go", ["alice"])],
      effectiveReviews: [],
      requested: [],
      author: "author",
    }), /head OID/i],
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
