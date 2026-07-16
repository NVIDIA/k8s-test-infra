"use strict";

const { loadConfig } = require("./config.js");
const { createGitHubClient } = require("./github-client.js");
const { syncLabels } = require("./modes/label-sync.js");
const { runCommand } = require("./modes/command.js");
const { runMetadata } = require("./modes/metadata.js");

async function run(dependencies) {
  const { core } = dependencies;
  const mode = core.getInput("mode", { required: true });
  if (mode !== "label-sync" && mode !== "metadata" && mode !== "command") {
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
  if (mode === "metadata" || mode === "command") {
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
    } else {
      summary = await runCommand({
        event: dependencies.event,
        github: client,
        config,
        dryRun,
        now: dependencies.now,
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
  const [core, github] = await Promise.all([
    import(/* webpackMode: "eager" */ "@actions/core"),
    import(/* webpackMode: "eager" */ "@actions/github"),
  ]);

  try {
    const { owner, repo } = github.context.repo;
    const octokit = github.getOctokit(process.env.GITHUB_TOKEN);
    await run({ core, octokit, owner, repo, event: github.context.payload });
  } catch (error) {
    core.setFailed(error instanceof Error ? error.message : String(error));
  }
}

if (require.main === module) {
  executeAction();
}

module.exports = { run };
