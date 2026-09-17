"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const {
  mokkaBranchName,
  mokkaPullRequestTitle,
  runMokkaCherryPick,
  stripMokkaTrailers,
} = require("../src/modes/mokka-cherry-pick.js");
const {
  createMokkaEvidence,
  parseMokkaEvidence,
} = require("../src/mokka-evidence.js");

const ACTION_ID = "123e4567-e89b-42d3-a456-426614174000";
const REPOSITORY = "NVIDIA/k8s-test-infra";
const REPOSITORY_ID = "733665780";
const SOURCE_SHA = "a".repeat(40);
const SOURCE_PARENT_SHA = "3".repeat(40);
const TARGET_SHA = "1".repeat(40);
const PRODUCED_SHA = "4".repeat(40);
const WORKFLOW_SHA = "5".repeat(40);
const SOURCE_PR = 42;
const TARGET_BRANCH = "main";

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function sourcePullRequest(overrides = {}) {
  return {
    number: SOURCE_PR,
    state: "closed",
    merged: true,
    mergeCommitOid: SOURCE_SHA,
    baseBranch: "feature/source-base",
    baseRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    headRepository: { owner: "nvidia", repo: "k8s-test-infra" },
    ...overrides,
  };
}

function githubRecorder(overrides = {}) {
  const calls = {
    createMokkaPullRequest: [],
    findMokkaPullRequests: [],
    getBranch: [],
    getCommit: [],
    getPullRequest: [],
    updateMokkaPullRequestBody: [],
  };
  const pullRequests = overrides.sourcePullRequests ?? [
    sourcePullRequest(),
    sourcePullRequest(),
  ];
  const targets = overrides.targetShas ?? [TARGET_SHA, TARGET_SHA];
  let branch = overrides.branch ?? null;
  let results = overrides.results ?? [];

  return {
    calls,
    async getPullRequest(prNumber) {
      calls.getPullRequest.push({ prNumber });
      const index = Math.min(calls.getPullRequest.length - 1, pullRequests.length - 1);
      return clone(pullRequests[index]);
    },
    async getCommit(sha) {
      calls.getCommit.push({ sha });
      return clone(overrides.commit ?? {
        sha: SOURCE_SHA,
        parents: [SOURCE_PARENT_SHA],
      });
    },
    async getBranch(name) {
      calls.getBranch.push({ branch: name });
      if (name === TARGET_BRANCH) {
        const targetCalls = calls.getBranch.filter((call) => call.branch === TARGET_BRANCH).length;
        return { name, oid: targets[Math.min(targetCalls - 1, targets.length - 1)] };
      }
      return branch === null ? null : { name, oid: branch };
    },
    async findMokkaPullRequests(head, base) {
      calls.findMokkaPullRequests.push({ head, base });
      return clone(results);
    },
    async createMokkaPullRequest(pullRequest) {
      calls.createMokkaPullRequest.push(clone(pullRequest));
      branch = PRODUCED_SHA;
      if (overrides.createError !== undefined) {
        results = overrides.resultsAfterCreateFailure ?? results;
        throw overrides.createError;
      }
      const created = overrides.created ?? {
        number: 1000,
        url: `https://github.com/${REPOSITORY}/pull/1000`,
        state: "open",
        draft: true,
        base: TARGET_BRANCH,
        head: mokkaBranchName(ACTION_ID),
        headOid: PRODUCED_SHA,
        title: mokkaPullRequestTitle(SOURCE_PR, TARGET_BRANCH),
        body: `<!-- mokka-cherry-pick-action-id: ${ACTION_ID} -->`,
      };
      results = [created];
      return clone(created);
    },
    async updateMokkaPullRequestBody(prNumber, body) {
      calls.updateMokkaPullRequestBody.push({ prNumber, body });
      if (overrides.updateError !== undefined) throw overrides.updateError;
      if (results[0] !== undefined) results[0].body = body;
    },
  };
}

function gitRecorder(overrides = {}) {
  const calls = [];
  let revParseCalls = 0;
  const git = async (args) => {
    calls.push([...args]);
    if (args[0] === "rev-parse" && args[1] === "HEAD") {
      revParseCalls += 1;
      return { stdout: `${revParseCalls === 1 ? TARGET_SHA : PRODUCED_SHA}\n`, stderr: "" };
    }
    if (args[0] === "show") {
      return {
        stdout: overrides.message ?? "feat: source change\n\nMokka-Source-SHA: forged\n continuation\nbody\n",
        stderr: "",
      };
    }
    if (args[0] === "cherry-pick" && args[1] !== "--abort" && overrides.conflict === true) {
      const error = new Error("conflict");
      error.exitCode = 1;
      throw error;
    }
    if (args[0] === "push" && overrides.pushError !== undefined) {
      throw overrides.pushError;
    }
    return { stdout: "", stderr: "" };
  };
  return { calls, git };
}

function invocation(overrides = {}) {
  return {
    actionId: overrides.actionId ?? ACTION_ID,
    dryRun: false,
    github: overrides.github ?? githubRecorder(overrides.githubOptions),
    git: overrides.git ?? gitRecorder(overrides.gitOptions).git,
    prNumber: overrides.prNumber ?? String(SOURCE_PR),
    repository: overrides.repository ?? REPOSITORY,
    repositoryId: overrides.repositoryId ?? REPOSITORY_ID,
    sourceSha: overrides.sourceSha ?? SOURCE_SHA,
    targetBranch: overrides.targetBranch ?? TARGET_BRANCH,
    workflowSha: overrides.workflowSha ?? WORKFLOW_SHA,
  };
}

function expectedEvidence(overrides = {}) {
  return {
    actionId: ACTION_ID,
    sourcePullRequest: SOURCE_PR,
    sourceSha: SOURCE_SHA,
    targetBranch: TARGET_BRANCH,
    targetBaseSha: TARGET_SHA,
    producedHeadSha: PRODUCED_SHA,
    workflowCommitSha: WORKFLOW_SHA,
    headBranch: mokkaBranchName(ACTION_ID),
    pullRequestUrl: `https://github.com/${REPOSITORY}/pull/1000`,
    ...overrides,
  };
}

test("creates one deterministic draft pull request with authenticated evidence", async () => {
  const github = githubRecorder();
  const recorder = gitRecorder();
  const result = await runMokkaCherryPick(invocation({ github, git: recorder.git }));
  const branch = mokkaBranchName(ACTION_ID);

  assert.deepEqual(result, {
    status: "complete",
    outcome: "created",
    sourcePullRequest: SOURCE_PR,
    sourceCommit: SOURCE_SHA,
    targetBranch: TARGET_BRANCH,
    headBranch: branch,
    pullRequest: {
      number: 1000,
      url: `https://github.com/${REPOSITORY}/pull/1000`,
    },
  });
  assert.deepEqual(github.calls.createMokkaPullRequest, [{
    base: TARGET_BRANCH,
    body: `<!-- mokka-cherry-pick-action-id: ${ACTION_ID} -->`,
    draft: true,
    head: branch,
    title: mokkaPullRequestTitle(SOURCE_PR, TARGET_BRANCH),
  }]);
  assert.equal(github.calls.updateMokkaPullRequestBody.length, 1);
  assert.deepEqual(
    parseMokkaEvidence(github.calls.updateMokkaPullRequestBody[0].body),
    expectedEvidence(),
  );
  assert.deepEqual(
    recorder.calls.find((args) => args[0] === "push"),
    [
      "push",
      "--porcelain",
      "--atomic",
      `--force-with-lease=refs/heads/${branch}:`,
      "origin",
      `HEAD:refs/heads/${branch}`,
    ],
  );
  const amend = recorder.calls.find((args) => args[0] === "commit");
  assert.ok(amend.includes(`Mokka-Source-SHA: ${SOURCE_SHA}`));
  assert.ok(amend.includes(`Mokka-Action-ID: ${ACTION_ID}`));
  assert.equal(amend.some((value) => value.includes("forged")), false);
  assert.equal(recorder.calls.some((args) => args[0] === "merge"), false);
});

test("rejects malformed dispatch and identity values before API or Git access", async (t) => {
  const cases = [
    ["prNumber", "01"],
    ["prNumber", "2147483648"],
    ["prNumber", "42\n"],
    ["sourceSha", SOURCE_SHA.toUpperCase()],
    ["sourceSha", "$(touch /tmp/pwned)"],
    ["sourceSha", `${SOURCE_SHA};echo pwned`],
    ["sourceSha", `-${SOURCE_SHA.slice(1)}`],
    ["targetBranch", "main "],
    ["targetBranch", "main\n"],
    ["actionId", "123e4567-e89b-12d3-a456-426614174000"],
    ["actionId", `${ACTION_ID};echo pwned`],
    ["repository", "fork/k8s-test-infra"],
    ["repositoryId", "1"],
    ["workflowSha", "not-a-sha"],
  ];
  for (const [field, value] of cases) {
    await t.test(`${field}: ${JSON.stringify(value)}`, async () => {
      const github = githubRecorder();
      const recorder = gitRecorder();
      await assert.rejects(
        () => runMokkaCherryPick(invocation({ github, git: recorder.git, [field]: value })),
        /invalid|must|unsupported/,
      );
      assert.deepEqual(github.calls.getPullRequest, []);
      assert.deepEqual(recorder.calls, []);
    });
  }
});

test("requires the exact merged source and one canonical parent", async () => {
  const wrongRepository = githubRecorder({
    sourcePullRequests: [sourcePullRequest({
      headRepository: { owner: "attacker", repo: "k8s-test-infra" },
    })],
  });
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github: wrongRepository })),
    /source pull request is not eligible/,
  );
  assert.deepEqual(wrongRepository.calls.createMokkaPullRequest, []);

  const mergeCommit = githubRecorder({
    commit: { sha: SOURCE_SHA, parents: [SOURCE_PARENT_SHA, "6".repeat(40)] },
  });
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github: mergeCommit })),
    /one canonical parent/,
  );
  assert.deepEqual(mergeCommit.calls.createMokkaPullRequest, []);

  const sameTarget = githubRecorder({
    sourcePullRequests: [sourcePullRequest({ baseBranch: TARGET_BRANCH })],
  });
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github: sameTarget })),
    /source pull request is not eligible/,
  );
  assert.deepEqual(sameTarget.calls.createMokkaPullRequest, []);
});

test("strips Mokka trailers even when hostile whitespace precedes them", () => {
  const cleaned = stripMokkaTrailers([
    "feat: source change",
    "",
    "  Mokka-Source-SHA: forged",
    "\tMokka-Action-ID: forged",
    "body",
    "",
  ].join("\n"));
  assert.equal(cleaned, "feat: source change\n\nbody");
});

test("a cherry-pick conflict aborts and performs no remote write", async () => {
  const github = githubRecorder();
  const recorder = gitRecorder({ conflict: true });
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /cherry-pick conflict/,
  );
  assert.equal(recorder.calls.some((args) => args.join(" ") === "cherry-pick --abort"), true);
  assert.equal(recorder.calls.some((args) => args[0] === "push"), false);
  assert.deepEqual(github.calls.createMokkaPullRequest, []);
});

test("revalidates the source and target before the create-only push", async (t) => {
  await t.test("source changed", async () => {
    const github = githubRecorder({
      sourcePullRequests: [sourcePullRequest(), sourcePullRequest({ mergeCommitOid: "6".repeat(40) })],
    });
    const recorder = gitRecorder();
    await assert.rejects(
      () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
      /source pull request changed before write/,
    );
    assert.equal(recorder.calls.some((args) => args[0] === "push"), false);
  });

  await t.test("target changed", async () => {
    const github = githubRecorder({ targetShas: [TARGET_SHA, "6".repeat(40)] });
    const recorder = gitRecorder();
    await assert.rejects(
      () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
      /target branch changed before write/,
    );
    assert.equal(recorder.calls.some((args) => args[0] === "push"), false);
  });
});

test("a concurrent branch collision cannot create a pull request", async () => {
  const github = githubRecorder();
  const recorder = gitRecorder({ pushError: new Error("stale lease") });
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /stale lease/,
  );
  assert.deepEqual(github.calls.createMokkaPullRequest, []);
});

test("pull request creation failure deletes only the exact leased branch", async (t) => {
  await t.test("exact branch without pull request", async () => {
    const github = githubRecorder({ createError: new Error("create failed") });
    const recorder = gitRecorder();
    await assert.rejects(
      () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
      /pull request creation failed/,
    );
    const branch = mokkaBranchName(ACTION_ID);
    assert.deepEqual(recorder.calls.at(-1), [
      "push",
      "--porcelain",
      "--atomic",
      `--force-with-lease=refs/heads/${branch}:${PRODUCED_SHA}`,
      "origin",
      `:refs/heads/${branch}`,
    ]);
  });

  await t.test("unknown state requires manual investigation", async () => {
    const github = githubRecorder({
      createError: new Error("create failed"),
      resultsAfterCreateFailure: [{ number: 1001 }],
    });
    const recorder = gitRecorder();
    await assert.rejects(
      () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
      /manual investigation/,
    );
    assert.equal(recorder.calls.filter((args) => args[0] === "push").length, 1);
  });
});

test("evidence update failure leaves the draft and branch for manual investigation", async () => {
  const github = githubRecorder({ updateError: new Error("patch failed") });
  const recorder = gitRecorder();
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /manual investigation/,
  );
  assert.equal(recorder.calls.filter((args) => args[0] === "push").length, 1);
});

test("an exact duplicate returns the existing result and mismatched evidence is a collision", async () => {
  const branch = mokkaBranchName(ACTION_ID);
  const body = createMokkaEvidence(expectedEvidence());
  const existing = {
    number: 1000,
    url: `https://github.com/${REPOSITORY}/pull/1000`,
    state: "open",
    draft: true,
    base: TARGET_BRANCH,
    head: branch,
    headOid: PRODUCED_SHA,
    title: mokkaPullRequestTitle(SOURCE_PR, TARGET_BRANCH),
    body,
  };
  const github = githubRecorder({ branch: PRODUCED_SHA, results: [existing] });
  const recorder = gitRecorder();
  const result = await runMokkaCherryPick(invocation({ github, git: recorder.git }));
  assert.equal(result.outcome, "already-exists");
  assert.deepEqual(recorder.calls, []);

  const changed = createMokkaEvidence(expectedEvidence({ workflowCommitSha: "6".repeat(40) }));
  await assert.rejects(
    () => runMokkaCherryPick(invocation({
      github: githubRecorder({ branch: PRODUCED_SHA, results: [{ ...existing, body: changed }] }),
      git: gitRecorder().git,
    })),
    /collision/,
  );
});
