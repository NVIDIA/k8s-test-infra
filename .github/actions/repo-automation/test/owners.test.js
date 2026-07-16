"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const { loadConfig } = require("../src/config.js");
const { parseAliases, parseOwnersFile, resolveOwners } = require("../src/owners.js");

const repositoryRoot = path.resolve(__dirname, "../../../..");
const fixtureRoot = path.join(__dirname, "fixtures", "owners");

function readFixture(...segments) {
  return fs.readFileSync(path.join(fixtureRoot, ...segments), "utf8");
}

function fixtureOwnerFiles() {
  return [
    parseOwnersFile(readFixture("vendor", "OWNERS"), "/vendor/OWNERS"),
    parseOwnersFile(readFixture("no-parent", "OWNERS"), "/no-parent/OWNERS"),
    parseOwnersFile(readFixture("nested", "OWNERS"), "nested/OWNERS"),
    parseOwnersFile(readFixture("root", "OWNERS"), "OWNERS"),
  ];
}

function fixtureAliases() {
  return parseAliases(readFixture("OWNERS_ALIASES"));
}

// pullRequestAuthor is invocation-only PR context. It is deliberately passed
// beside persisted policy here and must not be added to policy.yml's schema.
function resolutionPolicy(activeOwnerFiles, pullRequestAuthor = "pr-author") {
  return { activeOwnerFiles, pullRequestAuthor };
}

function assertRejectsUnsafe(callback, unsafeValue) {
  assert.throws(callback, (error) => {
    assert.equal(error instanceof TypeError, true);
    assert.equal(error.message.includes(unsafeValue), false);
    return true;
  });
}

test("parses and normalizes every supported OWNERS field", () => {
  assert.deepEqual(
    parseOwnersFile(readFixture("root", "OWNERS"), "OWNERS"),
    {
      path: "/OWNERS",
      reviewers: ["root-reviewer", "shared-reviewer"],
      approvers: ["root-approver", "shared-approver"],
      labels: ["area/root"],
      options: { no_parent_owners: false },
    },
  );

  assert.deepEqual(
    parseOwnersFile(readFixture("no-parent", "OWNERS"), "no-parent/OWNERS"),
    {
      path: "/no-parent/OWNERS",
      reviewers: ["isolated-reviewer"],
      approvers: ["isolated-approver"],
      labels: [],
      options: { no_parent_owners: true },
    },
  );
});

test("parses aliases as an explicit map of login lists", () => {
  assert.deepEqual(
    [...fixtureAliases()],
    [["nested_reviewers", ["alias-reviewer", "alias-duplicate", "ALIAS-DUPLICATE"]]],
  );
  assert.deepEqual([...parseAliases("aliases: {}\n")], []);
});

test("rejects non-object OWNERS shapes, unknown keys, and invalid field shapes", async (t) => {
  const cases = [
    ["sequence root", "[]\n", /OWNERS.*object/i],
    ["unknown key", "reviewers: []\nunexpected: true\n", /unexpected.*unknown/i],
    ["reviewers object", "reviewers: {}\n", /reviewers.*array/i],
    ["approvers scalar", "approvers: alice\n", /approvers.*array/i],
    ["labels object", "labels: {}\n", /labels.*array/i],
    ["options sequence", "options: []\n", /options.*object/i],
    [
      "unknown option",
      "options:\n  no_parent_owners: false\n  surprise: true\n",
      /options\.surprise.*unknown/i,
    ],
    [
      "non-boolean no-parent",
      "options:\n  no_parent_owners: yes\n",
      /no_parent_owners.*boolean/i,
    ],
  ];

  for (const [name, source, expected] of cases) {
    await t.test(name, () => {
      assert.throws(() => parseOwnersFile(source, "/OWNERS"), expected);
    });
  }
});

test("rejects non-object alias shapes, unknown keys, and invalid members", async (t) => {
  const cases = [
    ["sequence root", "[]\n", /OWNERS_ALIASES.*object/i],
    ["missing aliases", "{}\n", /aliases.*object/i],
    ["aliases sequence", "aliases: []\n", /aliases.*object/i],
    ["unknown top-level key", "aliases: {}\nextra: true\n", /extra.*unknown/i],
    ["empty member list", "aliases:\n  group: []\n", /group.*non-empty/i],
    ["scalar member list", "aliases:\n  group: alice\n", /group.*array/i],
    ["invalid alias name", "aliases:\n  bad name: [alice]\n", /alias name/i],
    ["invalid member login", "aliases:\n  group: [not_a_login]\n", /member.*GitHub login/i],
  ];

  for (const [name, source, expected] of cases) {
    await t.test(name, () => {
      assert.throws(() => parseAliases(source), expected);
    });
  }
});

test("rejects YAML anchors, aliases, merge keys, and duplicate keys", async (t) => {
  const cases = [
    [
      "OWNERS anchor",
      () => parseOwnersFile("reviewers: &people [alice]\napprovers: []\n", "/OWNERS"),
      /anchor/i,
    ],
    [
      "OWNERS alias",
      () => parseOwnersFile("reviewers: &people [alice]\napprovers: *people\n", "/OWNERS"),
      /alias/i,
    ],
    [
      "OWNERS merge",
      () => parseOwnersFile("base: &base {reviewers: [alice]}\n<<: *base\n", "/OWNERS"),
      /merge/i,
    ],
    [
      "OWNERS duplicate",
      () => parseOwnersFile("reviewers: [alice]\nreviewers: [bob]\n", "/OWNERS"),
      /duplicate|unique/i,
    ],
    [
      "aliases anchor",
      () => parseAliases("aliases: &groups {group: [alice]}\n"),
      /anchor/i,
    ],
    [
      "aliases alias",
      () => parseAliases("groups: &groups {group: [alice]}\naliases: *groups\n"),
      /alias/i,
    ],
    [
      "aliases merge",
      () => parseAliases("base: &base {group: [alice]}\naliases: {<<: *base}\n"),
      /merge/i,
    ],
    [
      "aliases duplicate",
      () => parseAliases("aliases: {group: [alice], group: [bob]}\n"),
      /duplicate|unique/i,
    ],
  ];

  for (const [name, callback, expected] of cases) {
    await t.test(name, () => assert.throws(callback, expected));
  }
});

test("rejects invalid and log-control owner or alias entries without echoing them", async (t) => {
  const unsafeValues = ["alice\nforged", "alice\u001b[31m", "alice\u202eadmin"];

  for (const unsafeValue of unsafeValues) {
    await t.test(JSON.stringify(unsafeValue), () => {
      const encoded = JSON.stringify(unsafeValue);
      assertRejectsUnsafe(
        () => parseOwnersFile(`reviewers: [${encoded}]\n`, "/OWNERS"),
        unsafeValue,
      );
      assertRejectsUnsafe(
        () => parseAliases(`aliases:\n  group: [${encoded}]\n`),
        unsafeValue,
      );
    });
  }
});

test("does not silently add transitive aliases", () => {
  assert.throws(
    () => parseAliases("aliases:\n  first_group: [second_group]\n  second_group: [alice]\n"),
    /member.*GitHub login/i,
  );
});

test("uses the root OWNERS declaration as fallback", () => {
  const result = resolveOwners(
    ["docs/readme.md"],
    fixtureOwnerFiles(),
    fixtureAliases(),
    resolutionPolicy(["/OWNERS"]),
  );

  assert.deepEqual(result, {
    files: [{
      path: "docs/readme.md",
      reviewers: ["root-reviewer", "shared-reviewer"],
      approvers: ["root-approver", "shared-approver"],
    }],
    reviewerCandidates: ["root-reviewer", "shared-reviewer"],
    approverCandidates: ["root-approver", "shared-approver"],
    uncoveredPaths: [],
  });
});

test("inherits parent owners, expands aliases, and deduplicates logins case-insensitively", () => {
  const result = resolveOwners(
    ["nested/deeper/file.go"],
    fixtureOwnerFiles(),
    fixtureAliases(),
    resolutionPolicy(["/nested/OWNERS", "/OWNERS"]),
  );

  assert.deepEqual(result, {
    files: [{
      path: "nested/deeper/file.go",
      reviewers: [
        "alias-duplicate",
        "alias-reviewer",
        "nested-reviewer",
        "root-reviewer",
        "shared-reviewer",
      ],
      approvers: ["nested-approver", "root-approver", "shared-approver"],
    }],
    reviewerCandidates: [
      "alias-duplicate",
      "alias-reviewer",
      "nested-reviewer",
      "root-reviewer",
      "shared-reviewer",
    ],
    approverCandidates: ["nested-approver", "root-approver", "shared-approver"],
    uncoveredPaths: [],
  });
});

test("stops parent inheritance at no_parent_owners", () => {
  const result = resolveOwners(
    ["no-parent/deeper/file.go"],
    fixtureOwnerFiles(),
    fixtureAliases(),
    resolutionPolicy(["/OWNERS", "/no-parent/OWNERS"]),
  );

  assert.deepEqual(result.files, [{
    path: "no-parent/deeper/file.go",
    reviewers: ["isolated-reviewer"],
    approvers: ["isolated-approver"],
  }]);
  assert.deepEqual(result.reviewerCandidates, ["isolated-reviewer"]);
  assert.deepEqual(result.approverCandidates, ["isolated-approver"]);
  assert.deepEqual(result.uncoveredPaths, []);
});

test("returns sorted files, candidates, and uncovered paths for multiple changed paths", () => {
  const result = resolveOwners(
    ["missing/z.go", "nested/z.go", "no-parent/z.go", "nested/a.go", "missing/a.go"],
    fixtureOwnerFiles().reverse(),
    fixtureAliases(),
    resolutionPolicy(["/no-parent/OWNERS", "/nested/OWNERS"]),
  );

  assert.deepEqual(result.files.map((file) => file.path), [
    "missing/a.go",
    "missing/z.go",
    "nested/a.go",
    "nested/z.go",
    "no-parent/z.go",
  ]);
  assert.deepEqual(result.reviewerCandidates, [
    "alias-duplicate",
    "alias-reviewer",
    "isolated-reviewer",
    "nested-reviewer",
    "shared-reviewer",
  ]);
  assert.deepEqual(result.approverCandidates, [
    "isolated-approver",
    "nested-approver",
    "shared-approver",
  ]);
  assert.deepEqual(result.uncoveredPaths, ["missing/a.go", "missing/z.go"]);
  assert.deepEqual(result.files.slice(0, 2), [
    { path: "missing/a.go", reviewers: [], approvers: [] },
    { path: "missing/z.go", reviewers: [], approvers: [] },
  ]);
});

test("excludes the PR author case-insensitively and reports author-only coverage missing", () => {
  const ownerFiles = [
    parseOwnersFile(
      "reviewers: [pr-author, other-reviewer]\napprovers: [PR-AUTHOR, other-approver]\n",
      "/OWNERS",
    ),
    parseOwnersFile(
      "reviewers: [pr-author]\napprovers: [PR-AUTHOR]\noptions: {no_parent_owners: true}\n",
      "/author-only/OWNERS",
    ),
  ];
  const result = resolveOwners(
    ["root.go", "author-only/file.go"],
    ownerFiles,
    new Map(),
    resolutionPolicy(["/OWNERS", "/author-only/OWNERS"], "Pr-AuThOr"),
  );

  assert.deepEqual(result.files, [
    { path: "author-only/file.go", reviewers: [], approvers: [] },
    { path: "root.go", reviewers: ["other-reviewer"], approvers: ["other-approver"] },
  ]);
  assert.deepEqual(result.reviewerCandidates, ["other-reviewer"]);
  assert.deepEqual(result.approverCandidates, ["other-approver"]);
  assert.deepEqual(result.uncoveredPaths, ["author-only/file.go"]);
});

test("fails closed on an unresolvable alias reference", () => {
  const ownerFiles = [parseOwnersFile("reviewers: [missing_alias]\n", "/OWNERS")];

  assert.throws(
    () => resolveOwners(
      ["file.go"],
      ownerFiles,
      fixtureAliases(),
      resolutionPolicy(["/OWNERS"]),
    ),
    /unknown alias.*missing_alias/i,
  );
});

test("matches activeOwnerFiles exactly and never treats glob text as discovery", () => {
  const ownerFiles = fixtureOwnerFiles();
  const ignored = resolveOwners(
    ["vendor/dependency/file.go"],
    ownerFiles,
    fixtureAliases(),
    resolutionPolicy(["/vendor/**/OWNERS"]),
  );
  const explicitlyActive = resolveOwners(
    ["vendor/dependency/file.go"],
    ownerFiles,
    fixtureAliases(),
    resolutionPolicy(["/vendor/OWNERS"]),
  );

  assert.deepEqual(ignored.reviewerCandidates, []);
  assert.deepEqual(ignored.approverCandidates, []);
  assert.deepEqual(ignored.uncoveredPaths, ["vendor/dependency/file.go"]);
  assert.deepEqual(explicitlyActive.reviewerCandidates, ["untrusted-vendor-reviewer"]);
  assert.deepEqual(explicitlyActive.approverCandidates, ["untrusted-vendor-approver"]);
  assert.deepEqual(explicitlyActive.uncoveredPaths, []);
});

test("normalizes owner file paths but rejects unsafe repository paths", async (t) => {
  assert.equal(parseOwnersFile("reviewers: []\n", "nested/OWNERS").path, "/nested/OWNERS");

  const invalidOwnerPaths = ["/nested//OWNERS", "nested\\OWNERS", "nested/../OWNERS", "/not-owners"];
  for (const ownerPath of invalidOwnerPaths) {
    await t.test(`owner path ${JSON.stringify(ownerPath)}`, () => {
      assert.throws(
        () => parseOwnersFile("reviewers: []\n", ownerPath),
        /OWNERS path/i,
      );
    });
  }

  for (const changedPath of ["/absolute.go", "./dot.go", "../escape.go", "bad\\path.go", "a//b"] ) {
    await t.test(`changed path ${JSON.stringify(changedPath)}`, () => {
      assert.throws(
        () => resolveOwners(
          [changedPath],
          fixtureOwnerFiles(),
          fixtureAliases(),
          resolutionPolicy(["/OWNERS"]),
        ),
        /changed path/i,
      );
    });
  }
});

test("keeps only the root OWNERS active in production policy", () => {
  assert.deepEqual(loadConfig(repositoryRoot).policy.activeOwnerFiles, ["/OWNERS"]);
  assert.equal(
    fs.readFileSync(path.join(repositoryRoot, "OWNERS_ALIASES"), "utf8"),
    "aliases: {}\n",
  );
});
