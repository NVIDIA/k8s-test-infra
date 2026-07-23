"use strict";

const { loadConfig } = require("./config.js");
const { createGitHubClient } = require("./github-client.js");
const { syncLabels } = require("./modes/label-sync.js");
const { runCommand } = require("./modes/command.js");
const { runMetadata } = require("./modes/metadata.js");
const { runMergeEvaluate } = require("./modes/merge-evaluate.js");
const { runBackport } = require("./modes/backport.js");

async function run(dependencies) {
  const { core } = dependencies;
  const mode = core.getInput("mode", { required: true });
  if (mode !== "label-sync" && mode !== "metadata" && mode !== "command" && mode !== "merge-evaluate" && mode !== "backport") {
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
  let config;
  if (mode === "metadata" || mode === "command" || mode === "merge-evaluate" || mode === "backport") {
    try {
      config = loadConfig(workspace);
    } catch {
      config = undefined;
    }
  } else {
    config = loadConfig(workspace);
  }
  let summary;
  try {
    if (mode === "label-sync") {
      summary = await syncLabels({
        github: client,
        declaredLabels: config.labels.labels,
        dryRun,
      });
    } else if (mode === "metadata") {
      summary = await runMetadata({
        event: dependencies.event,
        github: client,
        config,
        dryRun,
      });
    } else if (mode === "command") {
      summary = await runCommand({
        event: dependencies.event,
        github: client,
        config,
        dryRun,
        now: dependencies.now,
      });
    } else if (mode === "backport") {
      summary = await runBackport({
        event: dependencies.event,
        eventName: dependencies.eventName,
        github: client,
        config,
        dryRun,
        prNumber: core.getInput("pr-number"),
        targetBranch: core.getInput("target-branch"),
        now: dependencies.now,
      });
    } else {
      summary = await runMergeEvaluate({
        event: dependencies.event,
        eventName: dependencies.eventName,
        github: client,
        config,
        dryRun,
        prNumber: core.getInput("pr-number"),
      });
    }
  } catch (error) {
    if (error?.summary !== undefined) {
      core.setOutput("summary", JSON.stringify(error.summary));
    }
    throw error;
  }
  core.setOutput("summary", JSON.stringify(summary));
  return summary;
}

async function executeAction() {
  let core = null;
  try {
    const [coreModule, github] = await Promise.all([
      import(/* webpackMode: "eager" */ "@actions/core"),
      import(/* webpackMode: "eager" */ "@actions/github"),
    ]);
    core = coreModule;
    const { owner, repo } = github.context.repo;
    const octokit = github.getOctokit(process.env.GITHUB_TOKEN);
    await run({
      core,
      octokit,
      owner,
      repo,
      event: github.context.payload,
      eventName: github.context.eventName,
    });
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    if (core === null) {
      process.stderr.write(`${message}\n`);
      process.exitCode = 1;
    } else {
      core.setFailed(message);
    }
  }
}

if (require.main === module) {
  executeAction();
}

module.exports = { run };
