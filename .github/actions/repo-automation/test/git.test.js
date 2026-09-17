"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { gitIsAncestor, runGit } = require("../src/git.js");

test("runGit passes every argument to git without a shell", async () => {
  const calls = [];
  const branch = "release-1.2; touch /tmp/not-created";
  const result = await runGit(["fetch", "origin", branch], {
    cwd: "/work/repository",
    env: { PATH: "/usr/bin" },
    execFile(file, args, options, callback) {
      calls.push({ file, args, options });
      callback(null, "fetched\n", "");
    },
  });

  assert.deepEqual(result, { stdout: "fetched\n", stderr: "" });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].file, "git");
  assert.deepEqual(calls[0].args, ["fetch", "origin", branch]);
  assert.equal(calls[0].options.shell, false);
  assert.equal(calls[0].options.cwd, "/work/repository");
});

test("runGit rejects malformed arguments before execution", async () => {
  let executed = false;
  const execFile = () => { executed = true; };

  await assert.rejects(() => runGit("status", { execFile }), /array/);
  await assert.rejects(() => runGit(["show", "bad\0revision"], { execFile }), /NUL/);
  assert.equal(executed, false);
});

test("gitIsAncestor distinguishes an exact negative result from a git failure", async () => {
  assert.equal(await gitIsAncestor(async () => ({ stdout: "", stderr: "" }), "a", "b"), true);

  const notAncestor = new Error("not ancestor");
  notAncestor.exitCode = 1;
  assert.equal(await gitIsAncestor(async () => { throw notAncestor; }, "a", "b"), false);

  const failure = new Error("repository failure");
  failure.exitCode = 128;
  await assert.rejects(
    () => gitIsAncestor(async () => { throw failure; }, "a", "b"),
    /repository failure/,
  );
});
