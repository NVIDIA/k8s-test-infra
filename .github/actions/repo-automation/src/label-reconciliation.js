"use strict";

function normalizedName(label) {
  return label.name.toLowerCase();
}

function compareByName(left, right) {
  const leftName = normalizedName(left);
  const rightName = normalizedName(right);

  if (leftName < rightName) {
    return -1;
  }
  if (leftName > rightName) {
    return 1;
  }
  return 0;
}

function validateDeclaredLabels(declaredLabels) {
  if (!Array.isArray(declaredLabels)) {
    throw new Error("Invalid declared labels: expected an array");
  }

  const names = new Set();
  for (const label of declaredLabels) {
    if (
      label === null
      || typeof label !== "object"
      || Array.isArray(label)
      || typeof label.name !== "string"
      || label.name.trim() === ""
      || typeof label.color !== "string"
      || !/^[0-9a-f]{6}$/.test(label.color)
      || typeof label.description !== "string"
      || label.description.trim() === ""
    ) {
      throw new Error("Invalid declared labels: each label requires a name, color, and description");
    }

    const name = normalizedName(label);
    if (names.has(name)) {
      throw new Error(`Invalid declared labels: duplicate label name: ${label.name}`);
    }
    names.add(name);
  }
}

function indexExistingLabels(existingLabels) {
  if (!Array.isArray(existingLabels)) {
    throw new Error("Invalid existing labels: expected an array");
  }

  const labelsByName = new Map();
  for (const label of existingLabels) {
    if (
      label === null
      || typeof label !== "object"
      || Array.isArray(label)
      || typeof label.name !== "string"
      || label.name.trim() === ""
    ) {
      throw new Error("Invalid existing labels: each label requires a name");
    }

    const name = normalizedName(label);
    if (labelsByName.has(name)) {
      throw new Error(`Duplicate existing label name: ${label.name}`);
    }
    labelsByName.set(name, label);
  }

  return labelsByName;
}

function planLabelChanges(declaredLabels, existingLabels) {
  validateDeclaredLabels(declaredLabels);
  const existingByName = indexExistingLabels(existingLabels);
  const declaredNames = new Set();
  const creates = [];
  const updates = [];
  const unchanged = [];

  for (const declared of declaredLabels) {
    const name = normalizedName(declared);
    declaredNames.add(name);
    const existing = existingByName.get(name);

    if (existing === undefined) {
      creates.push(declared);
    } else if (
      existing.color !== declared.color
      || existing.description !== declared.description
    ) {
      updates.push(declared);
    } else {
      unchanged.push(declared);
    }
  }

  const unmanaged = existingLabels.filter((label) => !declaredNames.has(normalizedName(label)));

  return {
    creates: creates.sort(compareByName),
    updates: updates.sort(compareByName),
    unchanged: unchanged.sort(compareByName),
    unmanaged: unmanaged.sort(compareByName),
  };
}

module.exports = { planLabelChanges };
