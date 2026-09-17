"use strict";

const { Buffer } = require("node:buffer");
const path = require("node:path");
const { loadConfig } = require("./config.js");
const { createGitHubClient } = require("./github-client.js");
const { runGit } = require("./git.js");
const { runBackport } = require("./modes/backport.js");
const { runCommand } = require("./modes/command.js");
const { syncLabels } = require("./modes/label-sync.js");
const { runMergeEvaluate } = require("./modes/merge-evaluate.js");
const { runMetadata } = require("./modes/metadata.js");
const { runMokkaCherryPick } = require("./modes/mokka-cherry-pick.js");
const { MAX_SUMMARY_BYTES } = require("./limits.js");

const MAX_BACKPORT_REQUESTS_BYTES = 4096;

function serializeSummary(summary) {
  const serialized = JSON.stringify(summary);
  if (Buffer.byteLength(serialized, "utf8") > MAX_SUMMARY_BYTES) {
    throw new TypeError(`summary must not exceed ${MAX_SUMMARY_BYTES} bytes`);
  }
  return serialized;
}

function fixedWorkingDirectory(workspace, value, purpose) {
  if (value !== "target") throw new TypeError(`invalid ${purpose} working directory`);
  if (typeof workspace !== "string" || !path.isAbsolute(workspace)) {
    throw new TypeError(`invalid ${purpose} workspace for working directory`);
  }
  return path.join(workspace, "target");
}

function controlWorkspace(workspace, value) {
  if (typeof workspace !== "string" || !path.isAbsolute(workspace)) {
    throw new TypeError("invalid workspace for trusted control files");
  }
  if (value === "") return workspace;
  if (value !== "control") throw new TypeError("invalid trusted control directory");
  return path.join(workspace, "control");
}

function serializeBackportRequests(summary) {
  const requests = summary?.backportRequests ?? [];
  if (!Array.isArray(requests)) throw new TypeError("backport requests must be an array");
  const output = requests.map((request) => {
    if (
      request === null
      || typeof request !== "object"
      || !Number.isSafeInteger(request.prNumber)
      || request.prNumber <= 0
      || typeof request.targetBranch !== "string"
      || request.targetBranch === ""
    ) throw new TypeError("backport request output is invalid");
    return { prNumber: request.prNumber, targetBranch: request.targetBranch };
  });
  const serialized = JSON.stringify(output);
  if (Buffer.byteLength(serialized, "utf8") >= MAX_BACKPORT_REQUESTS_BYTES) {
    throw new TypeError(`backport requests must be smaller than ${MAX_BACKPORT_REQUESTS_BYTES} bytes`);
  }
  return serialized;
}

async function publishJobSummary(core, mode, summary) {
  if (core.summary?.addHeading === undefined) return;
  await core.summary
    .addHeading(`Repository automation: ${mode}`, 2)
    .addCodeBlock(serializeSummary(summary), "json")
    .write();
}

async function run(dependencies) {
  const { core } = dependencies;
  const mode = core.getInput("mode", { required: true });
  if (!["label-sync", "metadata", "command", "merge-evaluate", "backport", "mokka-cherry-pick"].includes(mode)) {
    throw new Error(`Unsupported mode: ${mode}`);
  }

  const {
    octokit,
    owner,
    repo,
    workspace = process.env.GITHUB_WORKSPACE,
  } = dependencies;
  const client = dependencies.githubClient ?? createGitHubClient(octokit, owner, repo);
  const dryRun = core.getBooleanInput("dry-run");
  const prNumber = mode === "mokka-cherry-pick"
    ? core.getInput("pull_request_number")
    : core.getInput("pr-number");
  const sourceSha = mode === "mokka-cherry-pick"
    ? core.getInput("source_sha")
    : core.getInput("source-sha");
  const targetBranch = core.getInput("target-branch");
  const actionId = mode === "mokka-cherry-pick"
    ? core.getInput("action_id")
    : core.getInput("action-id");
  const workingDirectoryInput = core.getInput("working-directory");
  const controlDirectoryInput = core.getInput("control-directory");
  const trustedWorkspace = controlWorkspace(workspace, controlDirectoryInput);
  let config;
  if (mode === "mokka-cherry-pick") {
    config = undefined;
  } else if (mode === "metadata") {
    try {
      config = loadConfig(trustedWorkspace);
    } catch {
      config = undefined;
    }
  } else {
    config = loadConfig(trustedWorkspace);
  }
  let summary;
  try {
    switch (mode) {
      case "label-sync":
        summary = await syncLabels({
          github: client,
          declaredLabels: config.labels.labels,
          dryRun,
        });
        break;
      case "metadata":
        summary = await runMetadata({
          event: dependencies.event,
          github: client,
          config,
          dryRun,
        });
        break;
      case "command":
        summary = await runCommand({
          event: dependencies.event,
          github: client,
          config,
          dryRun,
        });
        break;
      case "merge-evaluate":
        summary = await runMergeEvaluate({
          event: dependencies.event,
          eventName: dependencies.eventName,
          github: client,
          config,
          dryRun,
          prNumber,
        });
        break;
      case "backport": {
        const workingDirectory = fixedWorkingDirectory(workspace, workingDirectoryInput, "backport");
        const git = dependencies.git ?? runGit;
        summary = await runBackport({
          github: client,
          git: (args, options) => git(args, {
            ...options,
            cwd: workingDirectory,
          }),
          config,
          dryRun,
          prNumber,
          targetBranch,
          repository: `${owner}/${repo}`,
        });
        break;
      }
      case "mokka-cherry-pick": {
        const workingDirectory = fixedWorkingDirectory(workspace, workingDirectoryInput, "Mokka");
        const git = dependencies.git ?? runGit;
        summary = await runMokkaCherryPick({
          github: client,
          git: (args, options) => git(args, {
            ...options,
            cwd: workingDirectory,
          }),
          dryRun,
          prNumber,
          sourceSha,
          targetBranch,
          actionId,
          repository: `${owner}/${repo}`,
          repositoryId: dependencies.repositoryId,
          workflowSha: dependencies.workflowSha,
        });
        break;
      }
      default:
        throw new Error(`Unsupported mode: ${mode}`);
    }
  } catch (error) {
    if (error?.summary !== undefined) {
      core.setOutput("summary", serializeSummary(error.summary));
    }
    throw error;
  }
  if (mode === "command") {
    core.setOutput("backport-requests", serializeBackportRequests(summary));
  }
  core.setOutput("summary", serializeSummary(summary));
  await publishJobSummary(core, mode, summary);
  return summary;
}

async function executeAction() {
  const [core, github] = await Promise.all([
    import(/* webpackMode: "eager" */ "@actions/core"),
    import(/* webpackMode: "eager" */ "@actions/github"),
  ]);

  try {
    const { owner, repo } = github.context.repo;
    const octokit = github.getOctokit(process.env.GITHUB_TOKEN);
    await run({
      core,
      octokit,
      owner,
      repo,
      event: github.context.payload,
      eventName: github.context.eventName,
      repositoryId: process.env.GITHUB_REPOSITORY_ID,
      workflowSha: process.env.GITHUB_WORKFLOW_SHA,
    });
  } catch (error) {
    core.setFailed(error instanceof Error ? error.message : String(error));
  }
}

if (require.main === module) {
  executeAction();
}

module.exports = { run, serializeSummary };
