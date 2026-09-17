"use strict";

const { Buffer } = require("node:buffer");
const { loadConfig } = require("./config.js");
const { createGitHubClient } = require("./github-client.js");
const { runCommand } = require("./modes/command.js");
const { syncLabels } = require("./modes/label-sync.js");
const { runMergeEvaluate } = require("./modes/merge-evaluate.js");
const { runMetadata } = require("./modes/metadata.js");
const { MAX_SUMMARY_BYTES } = require("./limits.js");

function serializeSummary(summary) {
  const serialized = JSON.stringify(summary);
  if (Buffer.byteLength(serialized, "utf8") > MAX_SUMMARY_BYTES) {
    throw new TypeError(`summary must not exceed ${MAX_SUMMARY_BYTES} bytes`);
  }
  return serialized;
}

async function run(dependencies) {
  const { core } = dependencies;
  const mode = core.getInput("mode", { required: true });
  if (!["label-sync", "metadata", "command", "merge-evaluate"].includes(mode)) {
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
  const prNumber = core.getInput("pr-number");
  let config;
  if (mode === "metadata") {
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
      default:
        throw new Error(`Unsupported mode: ${mode}`);
    }
  } catch (error) {
    if (error?.summary !== undefined) {
      core.setOutput("summary", serializeSummary(error.summary));
    }
    throw error;
  }
  core.setOutput("summary", serializeSummary(summary));
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
    });
  } catch (error) {
    core.setFailed(error instanceof Error ? error.message : String(error));
  }
}

if (require.main === module) {
  executeAction();
}

module.exports = { run, serializeSummary };
