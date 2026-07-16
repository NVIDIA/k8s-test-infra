"use strict";

const fs = require("node:fs");
const path = require("node:path");

const YAML = require("yaml");

const CONFIG_DIRECTORY = path.join(".github", "repo-automation");
const CONFIG_NAMES = ["policy", "labels", "areas"];

class ConfigError extends Error {
  constructor(errors) {
    super(`Invalid repository automation configuration:\n${errors.map((error) => `- ${error}`).join("\n")}`);
    this.name = "ConfigError";
  }
}

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function addError(errors, configPath, message) {
  errors.push(`${configPath}: ${message}`);
}

function rejectUnknownKeys(value, allowedKeys, configPath, errors) {
  if (!isRecord(value)) {
    return;
  }

  for (const key of Object.keys(value).sort()) {
    if (!allowedKeys.includes(key)) {
      addError(errors, `${configPath}.${key}`, "unknown key");
    }
  }
}

function requireRecord(value, configPath, errors) {
  if (!isRecord(value)) {
    addError(errors, configPath, "must be an object");
    return false;
  }
  return true;
}

function requireNonEmptyString(value, configPath, errors) {
  if (typeof value !== "string" || value.trim() === "") {
    addError(errors, configPath, "must be a non-empty string");
    return false;
  }
  return true;
}

function requireStringArray(value, configPath, errors) {
  if (!Array.isArray(value) || value.length === 0) {
    addError(errors, configPath, "must be a non-empty array");
    return false;
  }

  for (let index = 0; index < value.length; index += 1) {
    requireNonEmptyString(value[index], `${configPath}[${index}]`, errors);
  }
  return true;
}

function validateSchemaVersion(value, configPath, errors) {
  if (value !== 1) {
    addError(errors, `${configPath}.schemaVersion`, "must be 1");
  }
}

function validatePolicy(policy, errors) {
  if (!requireRecord(policy, "policy", errors)) {
    return;
  }

  rejectUnknownKeys(
    policy,
    [
      "schemaVersion",
      "protectedBranches",
      "activeOwnerFiles",
      "review",
      "commands",
      "merge",
      "bots",
      "sizeThresholds",
    ],
    "policy",
    errors,
  );
  validateSchemaVersion(policy.schemaVersion, "policy", errors);
  requireStringArray(policy.protectedBranches, "policy.protectedBranches", errors);
  requireStringArray(policy.activeOwnerFiles, "policy.activeOwnerFiles", errors);

  if (requireRecord(policy.review, "policy.review", errors)) {
    rejectUnknownKeys(policy.review, ["reviewerTarget"], "policy.review", errors);
    if (!Number.isInteger(policy.review.reviewerTarget) || policy.review.reviewerTarget < 1) {
      addError(errors, "policy.review.reviewerTarget", "must be a positive integer");
    }
  }

  if (requireRecord(policy.commands, "policy.commands", errors)) {
    rejectUnknownKeys(policy.commands, ["retestCooldownSeconds"], "policy.commands", errors);
    if (
      !Number.isInteger(policy.commands.retestCooldownSeconds)
      || policy.commands.retestCooldownSeconds < 0
    ) {
      addError(
        errors,
        "policy.commands.retestCooldownSeconds",
        "must be a non-negative integer",
      );
    }
  }

  if (requireRecord(policy.merge, "policy.merge", errors)) {
    rejectUnknownKeys(policy.merge, ["method"], "policy.merge", errors);
    if (policy.merge.method !== "SQUASH") {
      addError(errors, "policy.merge.method", "must be SQUASH");
    }
  }

  if (!Array.isArray(policy.bots) || policy.bots.length === 0) {
    addError(errors, "policy.bots", "must be a non-empty array");
  } else {
    for (let index = 0; index < policy.bots.length; index += 1) {
      const botPath = `policy.bots[${index}]`;
      const bot = policy.bots[index];
      if (!requireRecord(bot, botPath, errors)) {
        continue;
      }
      rejectUnknownKeys(bot, ["login", "emails"], botPath, errors);
      requireNonEmptyString(bot.login, `${botPath}.login`, errors);
      requireStringArray(bot.emails, `${botPath}.emails`, errors);
    }
  }

  if (requireRecord(policy.sizeThresholds, "policy.sizeThresholds", errors)) {
    rejectUnknownKeys(policy.sizeThresholds, ["S", "M", "L", "XL"], "policy.sizeThresholds", errors);
    for (const size of ["S", "M", "L", "XL"]) {
      const threshold = policy.sizeThresholds[size];
      if (!Number.isInteger(threshold) || threshold < 0) {
        addError(errors, `policy.sizeThresholds.${size}`, "must be a non-negative integer");
      }
    }
  }
}

function validateLabels(labels, errors) {
  if (!requireRecord(labels, "labels", errors)) {
    return new Set();
  }

  rejectUnknownKeys(labels, ["schemaVersion", "labels"], "labels", errors);
  validateSchemaVersion(labels.schemaVersion, "labels", errors);

  const names = new Set();
  if (!Array.isArray(labels.labels) || labels.labels.length === 0) {
    addError(errors, "labels.labels", "must be a non-empty array");
    return names;
  }

  for (let index = 0; index < labels.labels.length; index += 1) {
    const labelPath = `labels.labels[${index}]`;
    const label = labels.labels[index];
    if (!requireRecord(label, labelPath, errors)) {
      continue;
    }

    rejectUnknownKeys(label, ["name", "color", "description"], labelPath, errors);
    if (requireNonEmptyString(label.name, `${labelPath}.name`, errors)) {
      if (names.has(label.name)) {
        addError(errors, `${labelPath}.name`, "must be unique");
      }
      names.add(label.name);
    }
    if (typeof label.color !== "string" || !/^[0-9a-f]{6}$/.test(label.color)) {
      addError(errors, `${labelPath}.color`, "must be a six-digit lowercase hexadecimal color");
    }
    requireNonEmptyString(label.description, `${labelPath}.description`, errors);
  }

  return names;
}

function validateAreas(areas, declaredLabels, errors) {
  if (!requireRecord(areas, "areas", errors)) {
    return;
  }

  rejectUnknownKeys(areas, ["schemaVersion", "areas"], "areas", errors);
  validateSchemaVersion(areas.schemaVersion, "areas", errors);
  if (!Array.isArray(areas.areas) || areas.areas.length === 0) {
    addError(errors, "areas.areas", "must be a non-empty array");
    return;
  }

  for (let index = 0; index < areas.areas.length; index += 1) {
    const areaPath = `areas.areas[${index}]`;
    const area = areas.areas[index];
    if (!requireRecord(area, areaPath, errors)) {
      continue;
    }

    rejectUnknownKeys(area, ["paths", "labels"], areaPath, errors);
    requireStringArray(area.paths, `${areaPath}.paths`, errors);
    if (requireStringArray(area.labels, `${areaPath}.labels`, errors)) {
      for (let labelIndex = 0; labelIndex < area.labels.length; labelIndex += 1) {
        if (!declaredLabels.has(area.labels[labelIndex])) {
          addError(
            errors,
            `${areaPath}.labels[${labelIndex}]`,
            `references undeclared label ${area.labels[labelIndex]}`,
          );
        }
      }
    }
  }
}

function scanYamlNode(node, configName, errors) {
  if (YAML.isAlias(node)) {
    addError(errors, `${configName}.alias`, "YAML aliases are not allowed");
    return;
  }

  if (YAML.isMap(node)) {
    for (const pair of node.items) {
      if (YAML.isScalar(pair.key) && pair.key.value === "<<") {
        addError(errors, `${configName}.merge`, "YAML merge keys are not allowed");
      }
      scanYamlNode(pair.key, configName, errors);
      scanYamlNode(pair.value, configName, errors);
    }
  } else if (YAML.isSeq(node)) {
    for (const item of node.items) {
      scanYamlNode(item, configName, errors);
    }
  }
}

function parseConfig(source, configName, errors) {
  const document = YAML.parseDocument(source, { merge: false, uniqueKeys: true });
  if (document.errors.length > 0) {
    for (const error of document.errors) {
      addError(errors, `${configName}.yaml`, error.message.split("\n", 1)[0]);
    }
    return undefined;
  }

  scanYamlNode(document.contents, configName, errors);
  return YAML.parse(source, { merge: false, uniqueKeys: true });
}

function validateConfig(config) {
  const errors = [];
  if (!requireRecord(config, "config", errors)) {
    throw new ConfigError(errors);
  }

  rejectUnknownKeys(config, CONFIG_NAMES, "config", errors);
  validatePolicy(config.policy, errors);
  const declaredLabels = validateLabels(config.labels, errors);
  validateAreas(config.areas, declaredLabels, errors);

  if (errors.length > 0) {
    throw new ConfigError(errors);
  }
  return config;
}

function loadConfig(rootDir) {
  const errors = [];
  const config = {};

  for (const name of CONFIG_NAMES) {
    const configPath = path.resolve(rootDir, CONFIG_DIRECTORY, `${name}.yml`);
    try {
      config[name] = parseConfig(fs.readFileSync(configPath, "utf8"), name, errors);
    } catch (error) {
      addError(errors, `${name}.yaml`, error.message.split("\n", 1)[0]);
    }
  }

  if (errors.length > 0) {
    throw new ConfigError(errors);
  }
  return validateConfig(config);
}

module.exports = { loadConfig, validateConfig };
