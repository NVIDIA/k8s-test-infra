"use strict";

const { loadConfig } = require("./config.js");
const { createGitHubClient } = require("./github-client.js");
const { syncLabels } = require("./modes/label-sync.js");

async function run(dependencies) {
  const { core } = dependencies;
  const mode = core.getInput("mode", { required: true });
  if (mode !== "label-sync") {
    throw new Error(`Unsupported mode: ${mode}`);
  }

  const {
    octokit,
    owner,
    repo,
    workspace = process.env.GITHUB_WORKSPACE,
  } = dependencies;
  const config = loadConfig(workspace);
  const client = createGitHubClient(octokit, owner, repo);
  const summary = await syncLabels({
    github: client,
    declaredLabels: config.labels.labels,
    dryRun: core.getBooleanInput("dry-run"),
  });
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
    await run({ core, octokit, owner, repo });
  } catch (error) {
    core.setFailed(error instanceof Error ? error.message : String(error));
  }
}

if (require.main === module) {
  executeAction();
}

module.exports = { run };
