"use strict";

const { planLabelChanges } = require("../label-reconciliation.js");

async function syncLabels({ github, declaredLabels, dryRun }) {
  const existingLabels = await github.listLabels();
  const plan = planLabelChanges(declaredLabels, existingLabels);

  if (!dryRun) {
    for (const label of plan.creates) {
      await github.createLabel(label);
    }
    for (const label of plan.updates) {
      await github.updateLabel(label);
    }
  }

  return plan;
}

module.exports = { syncLabels };
