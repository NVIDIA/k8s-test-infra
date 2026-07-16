"use strict";

function copyLabel(label) {
  return {
    name: label.name,
    color: label.color,
    description: label.description,
  };
}

function createFakeGitHub(initialLabels = []) {
  const labels = initialLabels.map(copyLabel);
  const calls = {
    listLabels: [],
    createLabel: [],
    updateLabel: [],
  };

  return {
    calls,

    async listLabels() {
      calls.listLabels.push({});
      return labels.map(copyLabel);
    },

    async createLabel(label) {
      const requested = copyLabel(label);
      calls.createLabel.push(requested);
      labels.push(requested);
      return copyLabel(requested);
    },

    async updateLabel(label) {
      const requested = copyLabel(label);
      calls.updateLabel.push(requested);
      const index = labels.findIndex(
        (existing) => existing.name.toLowerCase() === requested.name.toLowerCase(),
      );
      if (index === -1) {
        throw new Error(`cannot update missing label: ${requested.name}`);
      }
      labels[index] = requested;
      return copyLabel(requested);
    },

    snapshot() {
      return labels.map(copyLabel);
    },
  };
}

module.exports = { createFakeGitHub };
