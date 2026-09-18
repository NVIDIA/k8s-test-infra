"use strict";

const { execFile: nodeExecFile } = require("node:child_process");

const MAX_GIT_OUTPUT_BYTES = 1024 * 1024;

function validateArguments(args) {
  if (!Array.isArray(args) || args.length === 0) {
    throw new TypeError("git arguments must be a non-empty array");
  }
  for (const argument of args) {
    if (typeof argument !== "string") {
      throw new TypeError("each git argument must be a string");
    }
    if (argument.includes("\0")) {
      throw new TypeError("git arguments must not contain NUL bytes");
    }
  }
}

async function runGit(args, options = {}) {
  validateArguments(args);
  const execFile = options.execFile ?? nodeExecFile;
  if (typeof execFile !== "function") throw new TypeError("execFile must be a function");

  return new Promise((resolve, reject) => {
    execFile("git", [...args], {
      cwd: options.cwd,
      env: options.env ?? process.env,
      encoding: "utf8",
      maxBuffer: MAX_GIT_OUTPUT_BYTES,
      shell: false,
      windowsHide: true,
    }, (error, stdout, stderr) => {
      if (error !== null) {
        if (Number.isInteger(error.code)) error.exitCode = error.code;
        error.stdout = typeof stdout === "string" ? stdout : "";
        error.stderr = typeof stderr === "string" ? stderr : "";
        reject(error);
        return;
      }
      resolve({
        stdout: typeof stdout === "string" ? stdout : "",
        stderr: typeof stderr === "string" ? stderr : "",
      });
    });
  });
}

async function gitIsAncestor(git, ancestor, descendant) {
  if (typeof git !== "function") throw new TypeError("git must be a function");
  try {
    await git(["merge-base", "--is-ancestor", ancestor, descendant]);
    return true;
  } catch (error) {
    if (error?.exitCode === 1) return false;
    throw error;
  }
}

module.exports = { gitIsAncestor, runGit };
