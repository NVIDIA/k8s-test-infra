"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { evaluateDco } = require("../src/dco.js");

const botPolicy = Object.freeze([
  Object.freeze({
    login: "dependabot[bot]",
    emails: Object.freeze(["49699333+dependabot[bot]@users.noreply.github.com"]),
  }),
  Object.freeze({
    login: "github-actions[bot]",
    emails: Object.freeze(["41898282+github-actions[bot]@users.noreply.github.com"]),
  }),
]);

function commit({
  sha = "0123456789abcdef",
  name = "Alice Example",
  email = "alice@example.com",
  message = "feat: signed change\n\nSigned-off-by: Alice Example <alice@example.com>",
  login = "alice",
  parents = [{ sha: "parent" }],
} = {}) {
  return {
    sha,
    commit: { author: { name, email }, message },
    author: login === null ? null : { login },
    parents,
  };
}

test("accepts a matching Signed-off-by identity in the final trailer block", () => {
  assert.deepEqual(evaluateDco([commit()], botPolicy), {
    valid: true,
    failures: [],
    exempted: [],
  });
});

test("matches normalized author identity case-insensitively", () => {
  const input = commit({
    name: "Alice   van Example",
    email: "Alice@Example.COM",
    message: [
      "feat: portable trailers",
      "",
      "Reviewed-by: Release Team <release@example.com>",
      " continuation metadata",
      "sIgNeD-oFf-bY: alice van example <ALICE@example.com>",
      "",
      "",
    ].join("\r\n"),
  });

  assert.deepEqual(evaluateDco([input], botPolicy), {
    valid: true,
    failures: [],
    exempted: [],
  });
});

test("parses only a complete final contiguous trailer block", async (t) => {
  const cases = [
    [
      "signoff-like body text",
      "feat: unsigned\n\nSigned-off-by: Alice Example <alice@example.com>\n\nThis is body text, not a trailer.",
    ],
    ["malformed trailer", "feat: unsigned\n\nSigned-off-by Alice Example <alice@example.com>"],
    [
      "continued signoff identity",
      "feat: unsigned\n\nSigned-off-by: Alice Example\n <alice@example.com>",
    ],
    [
      "non-trailer mixed into final paragraph",
      "feat: unsigned\n\nclosing prose\nSigned-off-by: Alice Example <alice@example.com>",
    ],
    [
      "trailer not separated from subject",
      "feat: unsigned\nSigned-off-by: Alice Example <alice@example.com>",
    ],
  ];

  for (const [name, message] of cases) {
    await t.test(name, () => {
      const result = evaluateDco([commit({ sha: name, message })], botPolicy);

      assert.equal(result.valid, false);
      assert.deepEqual(result.exempted, []);
      assert.equal(result.failures.length, 1);
      assert.equal(result.failures[0].sha, name);
      assert.match(result.failures[0].reason, /no well-formed Signed-off-by trailer/);
    });
  }
});

test("checks missing, mismatched, and multiple wrong signoffs on every human commit", () => {
  const result = evaluateDco([
    commit({ sha: "missing", message: "feat: unsigned" }),
    commit({
      sha: "other-person",
      message: "feat: wrong signer\n\nSigned-off-by: Bob Example <bob@example.com>",
    }),
    commit({
      sha: "multiple-wrong",
      message: [
        "feat: wrong signers",
        "",
        "Signed-off-by: Bob Example <bob@example.com>",
        "Signed-off-by: Carol Example <carol@example.com>",
      ].join("\n"),
    }),
  ], botPolicy);

  assert.equal(result.valid, false);
  assert.deepEqual(result.exempted, []);
  assert.deepEqual(result.failures.map(({ sha }) => sha), [
    "missing",
    "other-person",
    "multiple-wrong",
  ]);
  assert.match(result.failures[0].reason, /Alice Example <alice@example\.com>/);
  assert.match(result.failures[1].reason, /Bob Example <bob@example\.com>/);
  assert.match(result.failures[2].reason, /Bob Example <bob@example\.com>/);
  assert.match(result.failures[2].reason, /Carol Example <carol@example\.com>/);
});

test("checks merge commits like every other human commit", () => {
  const merge = commit({
    sha: "merge-sha",
    message: "Merge branch 'topic' into main",
    parents: [{ sha: "first" }, { sha: "second" }],
  });

  const result = evaluateDco([merge], botPolicy);

  assert.equal(result.valid, false);
  assert.equal(result.failures.length, 1);
  assert.equal(result.failures[0].sha, "merge-sha");
});

test("exempts only an exact configured bot login and author email pair", () => {
  const exactBot = commit({
    sha: "exact-bot",
    name: "dependabot[bot]",
    email: "49699333+dependabot[bot]@users.noreply.github.com",
    login: "dependabot[bot]",
    message: "build(deps): bump dependency",
  });
  const exactWorkflowBot = commit({
    sha: "exact-workflow-bot",
    name: "github-actions[bot]",
    email: "41898282+github-actions[bot]@users.noreply.github.com",
    login: "github-actions[bot]",
    message: "chore: generated update",
  });

  assert.deepEqual(evaluateDco([exactBot, exactWorkflowBot], botPolicy), {
    valid: true,
    failures: [],
    exempted: ["exact-bot", "exact-workflow-bot"],
  });
});

test("does not infer bot status from suffixes, bot-like email, or one matching field", () => {
  const candidates = [
    commit({ sha: "suffix", name: "unlisted[bot]", login: "unlisted[bot]", message: "chore: bot-like" }),
    commit({
      sha: "email-only",
      email: "49699333+dependabot[bot]@users.noreply.github.com",
      login: "alice",
      message: "chore: bot-like",
    }),
    commit({
      sha: "login-only",
      email: "attacker@example.com",
      login: "dependabot[bot]",
      message: "chore: bot-like",
    }),
    commit({
      sha: "login-case",
      email: "49699333+dependabot[bot]@users.noreply.github.com",
      login: "Dependabot[bot]",
      message: "chore: bot-like",
    }),
    commit({
      sha: "email-case",
      email: "49699333+DEPENDABOT[bot]@users.noreply.github.com",
      login: "dependabot[bot]",
      message: "chore: bot-like",
    }),
  ];

  const result = evaluateDco(candidates, botPolicy);

  assert.equal(result.valid, false);
  assert.deepEqual(result.exempted, []);
  assert.deepEqual(result.failures.map(({ sha }) => sha), candidates.map(({ sha }) => sha));
});

test("returns every failure and reason without internal truncation", () => {
  const unsigned = Array.from({ length: 40 }, (_, index) => commit({
    sha: `unsigned-${String(index).padStart(2, "0")}`,
    message: `feat: unsigned ${index}`,
  }));

  const result = evaluateDco(unsigned, botPolicy);

  assert.equal(result.valid, false);
  assert.equal(result.failures.length, unsigned.length);
  assert.deepEqual(result.failures.map(({ sha }) => sha), unsigned.map(({ sha }) => sha));
  for (const failure of result.failures) {
    assert.equal(typeof failure.reason, "string");
    assert.notEqual(failure.reason, "");
  }
});

test("preserves original identities in safe diagnostics without echoing commit messages", () => {
  const sensitiveMessage = "feat: use private-body-marker-7d3a\n\nSigned-off-by: bOB signer <BOB@Example.COM>";
  const result = evaluateDco([commit({
    sha: "safe-diagnostic",
    name: "ALIce Example",
    email: "ALICE@Example.COM",
    message: sensitiveMessage,
  })], botPolicy);

  assert.equal(result.valid, false);
  assert.match(result.failures[0].reason, /ALIce Example <ALICE@Example\.COM>/);
  assert.match(result.failures[0].reason, /bOB signer <BOB@Example\.COM>/);
  assert.doesNotMatch(result.failures[0].reason, /private-body-marker-7d3a/);
  assert.doesNotMatch(JSON.stringify(result), /private-body-marker/);
});

test("preserves ordinary Unicode identities in mismatch diagnostics", () => {
  const result = evaluateDco([commit({
    sha: "unicode-identities",
    name: "Ålice 李",
    email: "alice@例え.example",
    message: "feat: unicode identity\n\nSigned-off-by: Боб Example <bob@пример.example>",
  })], botPolicy);

  assert.deepEqual(result, {
    valid: false,
    failures: [{
      sha: "unicode-identities",
      reason: "commit author Ålice 李 <alice@例え.example> does not match Signed-off-by trailer(s): Боб Example <bob@пример.example>",
    }],
    exempted: [],
  });
});

test("never echoes unsafe mismatched Signed-off-by identities", async (t) => {
  const escape = String.fromCharCode(0x1b);
  const nul = String.fromCharCode(0x00);
  const c1 = String.fromCharCode(0x85);
  const lineSeparator = "\u2028";
  const paragraphSeparator = "\u2029";
  const bidiOverride = "\u202e";
  const bidiIsolate = "\u2066";
  const cases = [
    ["ESC in name", `Mallory${escape}sentinel`, "mallory@example.com", escape],
    ["C0 NUL in email", "Mallory", `mallory${nul}@example.com`, nul],
    ["C1 in name", `Mallory${c1}sentinel`, "mallory@example.com", c1],
    ["C1 in email", "Mallory", `mallory${c1}@example.com`, c1],
    ["line separator in name", `Mallory${lineSeparator}sentinel`, "mallory@example.com", lineSeparator],
    ["paragraph separator in email", "Mallory", `mallory${paragraphSeparator}@example.com`, paragraphSeparator],
    ["bidi override in name", `Mallory${bidiOverride}sentinel`, "mallory@example.com", bidiOverride],
    ["bidi isolate in email", "Mallory", `mallory${bidiIsolate}@example.com`, bidiIsolate],
  ];

  for (const [name, signerName, signerEmail, unsafeSentinel] of cases) {
    await t.test(name, () => {
      const sha = `unsafe-signoff-${name}`;
      const result = evaluateDco([commit({
        sha,
        message: `feat: unsafe signer\n\nSigned-off-by: ${signerName} <${signerEmail}>`,
      })], botPolicy);

      assert.deepEqual(result, {
        valid: false,
        failures: [{ sha, reason: "Signed-off-by identity is unsafe" }],
        exempted: [],
      });
      assert.equal(JSON.stringify(result).includes(unsafeSentinel), false);
      assert.equal(result.failures[0].reason.includes("Mallory"), false);
    });
  }
});

test("never echoes unsafe commit author identities", async (t) => {
  const escape = String.fromCharCode(0x1b);
  const nul = String.fromCharCode(0x00);
  const c1 = String.fromCharCode(0x85);
  const lineSeparator = "\u2028";
  const paragraphSeparator = "\u2029";
  const bidiOverride = "\u202e";
  const bidiIsolate = "\u2066";
  const cases = [
    ["ESC in name", `Alice${escape}sentinel`, "alice@example.com", escape],
    ["C0 NUL in email", "Alice", `alice${nul}@example.com`, nul],
    ["C1 in name", `Alice${c1}sentinel`, "alice@example.com", c1],
    ["C1 in email", "Alice", `alice${c1}@example.com`, c1],
    ["line separator in name", `Alice${lineSeparator}sentinel`, "alice@example.com", lineSeparator],
    ["paragraph separator in email", "Alice", `alice${paragraphSeparator}@example.com`, paragraphSeparator],
    ["bidi override in name", `Alice${bidiOverride}sentinel`, "alice@example.com", bidiOverride],
    ["bidi isolate in email", "Alice", `alice${bidiIsolate}@example.com`, bidiIsolate],
  ];

  for (const [name, authorName, authorEmail, unsafeSentinel] of cases) {
    await t.test(name, () => {
      const sha = `unsafe-author-${name}`;
      const result = evaluateDco([commit({
        sha,
        name: authorName,
        email: authorEmail,
      })], botPolicy);

      assert.deepEqual(result, {
        valid: false,
        failures: [{ sha, reason: "commit author identity is unsafe" }],
        exempted: [],
      });
      assert.equal(JSON.stringify(result).includes(unsafeSentinel), false);
      assert.equal(result.failures[0].reason.includes("Alice"), false);
    });
  }
});

test("fails unsafe linked-author identity closed without echoing it", () => {
  const bidiOverride = "\u202e";
  const input = commit({ sha: "unsafe-login", login: `alice${bidiOverride}admin` });

  assert.deepEqual(evaluateDco([input], botPolicy), {
    valid: false,
    failures: [{ sha: "unsafe-login", reason: "linked author identity is unsafe" }],
    exempted: [],
  });
});

test("rejects unsafe bot identities and SHAs with safe TypeErrors", async (t) => {
  const controls = [
    ["ESC", String.fromCharCode(0x1b)],
    ["C0 NUL", String.fromCharCode(0x00)],
    ["C1", String.fromCharCode(0x85)],
    ["line separator", "\u2028"],
    ["paragraph separator", "\u2029"],
    ["bidi override", "\u202e"],
    ["format isolate", "\u2066"],
  ];

  for (const [name, unsafeSentinel] of controls) {
    await t.test(`${name} in bot login`, () => {
      assert.throws(
        () => evaluateDco([commit()], [{
          login: `bot${unsafeSentinel}sentinel`,
          emails: ["bot@example.com"],
        }]),
        (error) => {
          assert.equal(error.name, "TypeError");
          assert.equal(error.message, "botPolicy[0].login must be a safe non-empty string");
          assert.equal(error.message.includes(unsafeSentinel), false);
          return true;
        },
      );
    });

    await t.test(`${name} in bot email`, () => {
      assert.throws(
        () => evaluateDco([commit()], [{
          login: "bot",
          emails: [`bot${unsafeSentinel}@example.com`],
        }]),
        (error) => {
          assert.equal(error.name, "TypeError");
          assert.equal(error.message, "botPolicy[0].emails[0] must be a safe non-empty string");
          assert.equal(error.message.includes(unsafeSentinel), false);
          return true;
        },
      );
    });
  }

  await t.test("unsafe SHA", () => {
    const unsafeSentinel = "\u202e";
    assert.throws(
      () => evaluateDco([{ ...commit(), sha: `abc${unsafeSentinel}def` }], botPolicy),
      (error) => {
        assert.equal(error.name, "TypeError");
        assert.equal(error.message, "commits[0].sha must be a safe non-empty string");
        assert.equal(error.message.includes(unsafeSentinel), false);
        return true;
      },
    );
  });
});

test("rejects invalid commit and bot-policy structures instead of guessing", async (t) => {
  const invalidArguments = [
    ["commits null", null, botPolicy],
    ["commits object", {}, botPolicy],
    ["commit null", [null], botPolicy],
    ["missing SHA", [{ ...commit(), sha: undefined }], botPolicy],
    ["missing nested commit", [{ sha: "bad", author: { login: "alice" } }], botPolicy],
    ["missing message", [{ ...commit(), commit: { author: { name: "Alice", email: "alice@example.com" } } }], botPolicy],
    ["missing author name", [{ ...commit(), commit: { author: { email: "alice@example.com" }, message: "unsigned" } }], botPolicy],
    ["empty author identity", [commit({ name: "" })], botPolicy],
    ["invalid linked author", [{ ...commit(), author: {} }], botPolicy],
    ["bot policy null", [commit()], null],
    ["bot policy empty", [commit()], []],
    ["bot entry null", [commit()], [null]],
    ["bot login empty", [commit()], [{ login: "", emails: ["bot@example.com"] }]],
    ["bot emails missing", [commit()], [{ login: "bot", emails: [] }]],
    ["bot email invalid", [commit()], [{ login: "bot", emails: [null] }]],
    ["bot unknown key", [commit()], [{ login: "bot", emails: ["bot@example.com"], suffix: "[bot]" }]],
  ];

  for (const [name, commits, policy] of invalidArguments) {
    await t.test(name, () => {
      assert.throws(() => evaluateDco(commits, policy), { name: "TypeError" });
    });
  }
});
