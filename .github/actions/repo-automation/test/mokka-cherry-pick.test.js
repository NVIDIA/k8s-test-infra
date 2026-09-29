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
const PROVISIONAL_SHA = "8".repeat(40);
const TREE_SHA = "7".repeat(40);
const WORKFLOW_SHA = "5".repeat(40);
const SOURCE_PR = 42;
const TARGET_BRANCH = "main";
const PRODUCED_MESSAGE = `feat: source change\n\nbody\n\nMokka-Source-SHA: ${SOURCE_SHA}\nMokka-Action-ID: ${ACTION_ID}\n`;

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
    createMokkaCommit: [],
    createMokkaRef: [],
    createMokkaPullRequest: [],
    findMokkaPullRequests: [],
    getBranch: [],
    getCommit: [],
    getMokkaCommit: [],
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
  let producedCommitCalls = 0;

  return {
    calls,
    async getPullRequest(prNumber) {
      calls.getPullRequest.push({ prNumber });
      const index = Math.min(calls.getPullRequest.length - 1, pullRequests.length - 1);
      return clone(pullRequests[index]);
    },
    async getCommit(sha) {
      calls.getCommit.push({ sha });
      if (sha === SOURCE_SHA) {
        return clone(overrides.commit ?? {
          sha: SOURCE_SHA,
          parents: [SOURCE_PARENT_SHA],
        });
      }
      assert.equal(sha, PRODUCED_SHA);
      const producedCommits = overrides.producedCommits ?? [{
        sha: PRODUCED_SHA,
        parents: [TARGET_SHA],
        message: PRODUCED_MESSAGE,
        tree: TREE_SHA,
      }];
      const index = Math.min(producedCommitCalls, producedCommits.length - 1);
      producedCommitCalls += 1;
      return clone(producedCommits[index]);
    },
    async getMokkaCommit(sha) {
      calls.getMokkaCommit.push({ sha });
      const commit = await this.getCommit(sha);
      return {
        ...commit,
        verification: {
          verified: overrides.existingVerifiedCommit !== false,
          hasSignature: overrides.existingVerifiedCommit !== false,
        },
      };
    },
    async getBranch(name) {
      calls.getBranch.push({ branch: name });
      if (name === TARGET_BRANCH) {
        const targetCalls = calls.getBranch.filter((call) => call.branch === TARGET_BRANCH).length;
        return { name, oid: targets[Math.min(targetCalls - 1, targets.length - 1)] };
      }
      if (name.startsWith("mokka/cherry-pick-upload/")) {
        if (overrides.missingUploadBranchAfterCommit && calls.createMokkaCommit.length > 0) return null;
        if (overrides.uploadBranchOid !== undefined) return { name, oid: overrides.uploadBranchOid };
        return calls.createMokkaCommit.length === 0 ? null : { name, oid: PROVISIONAL_SHA };
      }
      if (branch !== null) return { name, oid: branch };
      if (calls.createMokkaRef.length === 0) return null;
      return { name, oid: PRODUCED_SHA };
    },
    async createMokkaCommit(request) {
      calls.createMokkaCommit.push(clone(request));
      return {
        sha: PRODUCED_SHA,
        message: overrides.createdCommitMessage ?? request.message,
        tree: TREE_SHA,
        parents: [TARGET_SHA],
        verification: {
          verified: overrides.verifiedCommit !== false,
          hasSignature: overrides.verifiedCommit !== false,
        },
      };
    },
    async createMokkaRef(name, sha) {
      calls.createMokkaRef.push({ name, sha });
      if (overrides.createRefError !== undefined) throw overrides.createRefError;
      branch = sha;
      return { name, oid: sha };
    },
    async findMokkaPullRequests(head, base) {
      calls.findMokkaPullRequests.push({ head, base });
      if (base === undefined && head === `mokka/cherry-pick-upload/${ACTION_ID}`) {
        return clone(overrides.foreignBaseUploadPullRequests ?? []);
      }
      if (base === undefined && head === mokkaBranchName(ACTION_ID)) {
        return clone([...results, ...(overrides.foreignBasePullRequests ?? [])]);
      }
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
  let fetchedSignedCommit = false;
  let showCalls = 0;
  const git = async (args) => {
    calls.push([...args]);
    if (args[0] === "rev-parse" && args[1] === "HEAD") {
      revParseCalls += 1;
      return { stdout: `${revParseCalls === 1 ? TARGET_SHA : PROVISIONAL_SHA}\n`, stderr: "" };
    }
    if (args[0] === "rev-parse" && args[1] === "FETCH_HEAD") {
      return { stdout: `${fetchedSignedCommit ? PRODUCED_SHA : TARGET_SHA}\n`, stderr: "" };
    }
    if (args[0] === "rev-parse" && args[1] === "HEAD^{tree}") {
      return { stdout: `${TREE_SHA}\n`, stderr: "" };
    }
    if (args[0] === "rev-parse" && args[1] === `${PRODUCED_SHA}^{tree}`) {
      return { stdout: `${TREE_SHA}\n`, stderr: "" };
    }
    if (args[0] === "rev-parse" && args[1] === `${PRODUCED_SHA}^`) {
      return { stdout: `${TARGET_SHA}\n`, stderr: "" };
    }
    if (args[0] === "show") {
      showCalls += 1;
      return {
        stdout: showCalls === 1
          ? overrides.message ?? "feat: source change\n\nMokka-Source-SHA: forged\n continuation\nbody\n"
          : `feat: source change\n\nbody\n\nMokka-Source-SHA: ${SOURCE_SHA}\nMokka-Action-ID: ${ACTION_ID}\n`,
        stderr: "",
      };
    }
    if (args[0] === "fetch" && args.at(-1) === PRODUCED_SHA) fetchedSignedCommit = true;
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

function existingDraftPullRequest(overrides = {}) {
  return {
    number: 1000,
    url: `https://github.com/${REPOSITORY}/pull/1000`,
    state: "open",
    draft: true,
    base: TARGET_BRANCH,
    head: mokkaBranchName(ACTION_ID),
    headOid: PRODUCED_SHA,
    title: mokkaPullRequestTitle(SOURCE_PR, TARGET_BRANCH),
    body: createMokkaEvidence(expectedEvidence()),
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
      `--force-with-lease=refs/heads/mokka/cherry-pick-upload/${ACTION_ID}:`,
      "origin",
      `HEAD:refs/heads/mokka/cherry-pick-upload/${ACTION_ID}`,
    ],
  );
  const amend = recorder.calls.find((args) => args[0] === "commit");
  assert.ok(amend.includes(`Mokka-Source-SHA: ${SOURCE_SHA}`));
  assert.ok(amend.includes(`Mokka-Action-ID: ${ACTION_ID}`));
  assert.equal(amend.some((value) => value.includes("forged")), false);
  assert.equal(recorder.calls.some((args) => args[0] === "merge"), false);
});

test("promotes only a GitHub-verified commit with the cherry-picked tree and checked main parent", async () => {
  const github = githubRecorder();
  const recorder = gitRecorder();

  await runMokkaCherryPick(invocation({ github, git: recorder.git }));

  assert.deepEqual(github.calls.createMokkaCommit, [{
    message: `feat: source change\n\nbody\n\nMokka-Source-SHA: ${SOURCE_SHA}\nMokka-Action-ID: ${ACTION_ID}\n`,
    tree: TREE_SHA,
    parents: [TARGET_SHA],
  }]);
  assert.deepEqual(github.calls.createMokkaRef, [{ name: mokkaBranchName(ACTION_ID), sha: PRODUCED_SHA }]);
  const pushes = recorder.calls.filter((args) => args[0] === "push");
  assert.equal(pushes.length, 2);
  const uploadBranch = `mokka/cherry-pick-upload/${ACTION_ID}`;
  assert.ok(pushes[0].includes(`--force-with-lease=refs/heads/${uploadBranch}:`));
  assert.ok(pushes[1].includes(`--force-with-lease=refs/heads/${uploadBranch}:${PROVISIONAL_SHA}`));
  assert.equal(github.calls.createMokkaPullRequest.length, 1);
});

test("an unverified GitHub commit removes the exact provisional branch before any pull request", async () => {
  const github = githubRecorder({ verifiedCommit: false });
  const recorder = gitRecorder();

  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /verified commit/,
  );

  assert.equal(github.calls.createMokkaPullRequest.length, 0);
  const pushes = recorder.calls.filter((args) => args[0] === "push");
  assert.equal(pushes.length, 2);
  assert.ok(pushes[1].includes(`:refs/heads/mokka/cherry-pick-upload/${ACTION_ID}`));
});

test("cleanup requires a confirmed upload ref after GitHub commit verification fails", async () => {
  const github = githubRecorder({
    verifiedCommit: false,
    missingUploadBranchAfterCommit: true,
  });
  const recorder = gitRecorder();

  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION/,
  );

  assert.equal(github.calls.createMokkaRef.length, 0);
  assert.equal(github.calls.createMokkaPullRequest.length, 0);
  assert.equal(recorder.calls.filter((args) => args[0] === "push").length, 1);
});

test("a changed signed commit message removes the provisional branch before any pull request", async () => {
  const github = githubRecorder({ createdCommitMessage: "missing action trailers" });
  const recorder = gitRecorder();
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /verified commit/,
  );
  assert.equal(github.calls.createMokkaRef.length, 0);
  assert.equal(github.calls.createMokkaPullRequest.length, 0);
  assert.equal(recorder.calls.filter((args) => args[0] === "push").length, 2);
});

test("GitHub may normalize only trailing commit-message newlines", async () => {
  const github = githubRecorder({ createdCommitMessage: PRODUCED_MESSAGE.replace(/\n+$/u, "") });
  const recorder = gitRecorder();
  const result = await runMokkaCherryPick(invocation({ github, git: recorder.git }));
  assert.equal(result.outcome, "created");
  assert.equal(github.calls.createMokkaRef.length, 1);
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
  const github = githubRecorder({ uploadBranchOid: "6".repeat(40) });
  const recorder = gitRecorder({ pushError: new Error("stale lease") });
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION/,
  );
  assert.deepEqual(github.calls.createMokkaPullRequest, []);
});

test("an ambiguous upload push with no visible branch requires manual investigation", async () => {
  const github = githubRecorder();
  const recorder = gitRecorder({ pushError: new Error("response lost") });
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION/,
  );
  assert.equal(github.calls.createMokkaCommit.length, 0);
  assert.equal(github.calls.createMokkaRef.length, 0);
  assert.equal(github.calls.createMokkaPullRequest.length, 0);
});

test("pull request creation errors retain the signed branch for manual investigation", async (t) => {
  await t.test("even when the pull request lookup is empty", async () => {
    const github = githubRecorder({ createError: new Error("create failed") });
    const recorder = gitRecorder();
    await assert.rejects(
      () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
      /MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION/,
    );
    assert.equal(recorder.calls.filter((args) => args[0] === "push").length, 2);
    assert.equal(github.calls.createMokkaRef.length, 1);
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
    assert.equal(recorder.calls.filter((args) => args[0] === "push").length, 2);
  });

});

test("a pull request from the upload head to another base blocks cleanup", async () => {
  const github = githubRecorder({
    verifiedCommit: false,
    foreignBaseUploadPullRequests: [{ number: 1001, base: "release-branch" }],
  });
  const recorder = gitRecorder();
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION/,
  );
  assert.equal(recorder.calls.filter((args) => args[0] === "push").length, 1);
  assert.equal(github.calls.createMokkaRef.length, 0);
});

test("evidence update failure leaves the draft and branch for manual investigation", async () => {
  const github = githubRecorder({ updateError: new Error("patch failed") });
  const recorder = gitRecorder();
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /manual investigation/,
  );
  assert.equal(recorder.calls.filter((args) => args[0] === "push").length, 2);
});

test("an exact duplicate returns the existing result and mismatched evidence is a collision", async () => {
  const existing = existingDraftPullRequest();
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

test("an unsigned existing draft is a collision", async () => {
  const github = githubRecorder({
    branch: PRODUCED_SHA,
    results: [existingDraftPullRequest()],
    existingVerifiedCommit: false,
  });
  const recorder = gitRecorder();
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /collision/,
  );
  assert.deepEqual(recorder.calls, []);
});

test("an existing draft with changed Mokka trailers is a collision", async () => {
  const github = githubRecorder({
    branch: PRODUCED_SHA,
    results: [existingDraftPullRequest()],
    producedCommits: [{
      sha: PRODUCED_SHA,
      parents: [TARGET_SHA],
      tree: TREE_SHA,
      message: `feat: source change\n\nbody\n\nMokka-Source-SHA: ${"6".repeat(40)}\nMokka-Action-ID: ${ACTION_ID}\n`,
    }],
  });
  const recorder = gitRecorder();
  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /collision/,
  );
  assert.deepEqual(recorder.calls, []);
});

test("an existing draft remains exact after main advances", async () => {
  const github = githubRecorder({
    branch: PRODUCED_SHA,
    results: [existingDraftPullRequest()],
    targetShas: ["6".repeat(40)],
  });
  const recorder = gitRecorder();

  const result = await runMokkaCherryPick(invocation({ github, git: recorder.git }));

  assert.equal(result.outcome, "already-exists");
  assert.deepEqual(github.calls.getCommit, [{ sha: SOURCE_SHA }, { sha: PRODUCED_SHA }, { sha: PRODUCED_SHA }]);
  assert.deepEqual(recorder.calls, []);
  assert.deepEqual(github.calls.createMokkaPullRequest, []);
});

test("an existing draft requires its commit parent to match the recorded target base", async () => {
  const github = githubRecorder({
    branch: PRODUCED_SHA,
    results: [existingDraftPullRequest()],
    producedCommits: [{ sha: PRODUCED_SHA, parents: ["6".repeat(40)], message: PRODUCED_MESSAGE, tree: TREE_SHA }],
  });
  const recorder = gitRecorder();

  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /collision|changed during duplicate verification/,
  );
  assert.deepEqual(recorder.calls, []);
  assert.deepEqual(github.calls.createMokkaPullRequest, []);
});

test("an existing draft rechecks its commit parent before returning", async () => {
  const github = githubRecorder({
    branch: PRODUCED_SHA,
    results: [existingDraftPullRequest()],
    producedCommits: [
      { sha: PRODUCED_SHA, parents: [TARGET_SHA], message: PRODUCED_MESSAGE, tree: TREE_SHA },
      { sha: PRODUCED_SHA, parents: ["6".repeat(40)], message: PRODUCED_MESSAGE, tree: TREE_SHA },
    ],
  });
  const recorder = gitRecorder();

  await assert.rejects(
    () => runMokkaCherryPick(invocation({ github, git: recorder.git })),
    /changed during duplicate verification/,
  );
  assert.deepEqual(recorder.calls, []);
  assert.deepEqual(github.calls.createMokkaPullRequest, []);
});
