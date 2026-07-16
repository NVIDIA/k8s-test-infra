"use strict";

function copyLabel(label) {
  return {
    name: label.name,
    color: label.color,
    description: label.description,
  };
}

function sensitiveValues(error) {
  const values = [];
  const headers = error?.request?.request?.headers;
  if (headers && typeof headers === "object") {
    for (const [name, value] of Object.entries(headers)) {
      if (/authorization|token|secret/i.test(name) && typeof value === "string") {
        values.push(value);
      }
    }
  }
  return values;
}

function normalizeError(operation, error) {
  let detail = error instanceof Error ? error.message : String(error);
  for (const value of sensitiveValues(error)) {
    detail = detail.replaceAll(value, "[REDACTED]");
  }

  const normalized = new Error(`${operation} failed: ${detail}`);
  normalized.name = "GitHubClientError";
  normalized.operation = operation;
  if (Number.isInteger(error?.status)) {
    normalized.status = error.status;
  }
  return normalized;
}

function createGitHubClient(octokit, owner, repo) {
  async function call(operation, request) {
    try {
      return await request();
    } catch (error) {
      throw normalizeError(operation, error);
    }
  }

  return {
    async listLabels() {
      const labels = await call("listLabels", () => octokit.paginate(
        octokit.rest.issues.listLabelsForRepo,
        { owner, repo, per_page: 100 },
      ));
      return labels.map(copyLabel);
    },

    async createLabel(label) {
      const response = await call("createLabel", () => octokit.rest.issues.createLabel({
        owner,
        repo,
        ...copyLabel(label),
      }));
      return copyLabel(response.data);
    },

    async updateLabel(label) {
      const requested = copyLabel(label);
      const response = await call("updateLabel", () => octokit.rest.issues.updateLabel({
        owner,
        repo,
        name: requested.name,
        new_name: requested.name,
        color: requested.color,
        description: requested.description,
      }));
      return copyLabel(response.data);
    },
  };
}

module.exports = { createGitHubClient };
