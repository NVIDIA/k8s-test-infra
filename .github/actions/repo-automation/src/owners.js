"use strict";

const YAML = require("yaml");

const CONTROL_CHARACTERS = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;
const GITHUB_LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const ALIAS_NAME = /^[A-Za-z0-9](?:[A-Za-z0-9_-]{0,126}[A-Za-z0-9])?$/;

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function isSafeText(value) {
  return typeof value === "string" && value !== "" && !CONTROL_CHARACTERS.test(value);
}

function safeDiagnosticKey(value) {
  return isSafeText(value) && /^[A-Za-z0-9_.-]+$/.test(value) ? value : "key";
}

function scanYamlNode(node, findings) {
  if (node === null || node === undefined) {
    return;
  }

  if (node.anchor) {
    findings.anchor = true;
  }
  if (YAML.isAlias(node)) {
    findings.alias = true;
    return;
  }
  if (YAML.isMap(node)) {
    for (const pair of node.items) {
      if (YAML.isScalar(pair.key) && pair.key.value === "<<") {
        findings.merge = true;
      }
      scanYamlNode(pair.key, findings);
      scanYamlNode(pair.value, findings);
    }
    return;
  }
  if (YAML.isSeq(node)) {
    for (const item of node.items) {
      scanYamlNode(item, findings);
    }
  }
}

function parseStrictYaml(source, documentName) {
  if (typeof source !== "string") {
    throw new TypeError(`${documentName} source must be a string`);
  }

  const document = YAML.parseDocument(source, { merge: false, uniqueKeys: true });
  if (document.errors.length > 0) {
    const duplicate = document.errors.some((error) => error.code === "DUPLICATE_KEY");
    throw new TypeError(
      duplicate
        ? `${documentName} YAML keys must be unique`
        : `${documentName} contains invalid YAML`,
    );
  }

  const findings = { alias: false, anchor: false, merge: false };
  scanYamlNode(document.contents, findings);
  if (findings.merge) {
    throw new TypeError(`${documentName} YAML merge keys are not allowed`);
  }
  if (findings.alias) {
    throw new TypeError(`${documentName} YAML aliases are not allowed`);
  }
  if (findings.anchor) {
    throw new TypeError(`${documentName} YAML anchors are not allowed`);
  }

  return document.toJS({ maxAliasCount: 0 });
}

function rejectUnknownKeys(value, allowedKeys, location) {
  for (const key of Object.keys(value).sort()) {
    if (!allowedKeys.includes(key)) {
      throw new TypeError(`${location}.${safeDiagnosticKey(key)} is unknown`);
    }
  }
}

function normalizeOwnerPath(ownerPath) {
  if (!isSafeText(ownerPath) || ownerPath.includes("\\")) {
    throw new TypeError("OWNERS path must be a safe repository path");
  }

  const normalized = ownerPath.startsWith("/") ? ownerPath : `/${ownerPath}`;
  const segments = normalized.slice(1).split("/");
  if (
    segments.length === 0
    || segments.some((segment) => segment === "" || segment === "." || segment === "..")
    || segments.at(-1) !== "OWNERS"
  ) {
    throw new TypeError("OWNERS path must be a safe repository path");
  }
  return normalized;
}

function validateOwnerToken(value, field) {
  if (
    !isSafeText(value)
    || (!GITHUB_LOGIN.test(value) && !ALIAS_NAME.test(value))
  ) {
    throw new TypeError(`${field} member must be a GitHub login or alias name`);
  }
}

function parseOwnerList(value, field) {
  if (value === undefined) {
    return [];
  }
  if (!Array.isArray(value)) {
    throw new TypeError(`${field} must be an array`);
  }
  for (const member of value) {
    validateOwnerToken(member, field);
  }
  return [...value];
}

function parseLabels(value) {
  if (value === undefined) {
    return [];
  }
  if (!Array.isArray(value)) {
    throw new TypeError("labels must be an array");
  }
  for (const label of value) {
    if (!isSafeText(label)) {
      throw new TypeError("labels member must be a safe non-empty string");
    }
  }
  return [...value];
}

function parseOptions(value) {
  if (value === undefined) {
    return { no_parent_owners: false };
  }
  if (!isRecord(value)) {
    throw new TypeError("options must be an object");
  }
  rejectUnknownKeys(value, ["no_parent_owners"], "options");
  if (
    value.no_parent_owners !== undefined
    && typeof value.no_parent_owners !== "boolean"
  ) {
    throw new TypeError("options.no_parent_owners must be a boolean");
  }
  return { no_parent_owners: value.no_parent_owners ?? false };
}

function parseOwnersFile(text, ownerPath) {
  const path = normalizeOwnerPath(ownerPath);
  const value = parseStrictYaml(text, "OWNERS");
  if (!isRecord(value)) {
    throw new TypeError("OWNERS must be an object");
  }
  rejectUnknownKeys(value, ["reviewers", "approvers", "labels", "options"], "OWNERS");

  return {
    path,
    reviewers: parseOwnerList(value.reviewers, "reviewers"),
    approvers: parseOwnerList(value.approvers, "approvers"),
    labels: parseLabels(value.labels),
    options: parseOptions(value.options),
  };
}

function validateAliasMap(aliases) {
  if (!(aliases instanceof Map)) {
    throw new TypeError("aliases must be a Map");
  }

  for (const [name, members] of aliases) {
    if (!isSafeText(name) || !ALIAS_NAME.test(name)) {
      throw new TypeError("alias name must be safe");
    }
    if (!Array.isArray(members) || members.length === 0) {
      throw new TypeError("alias group must be a non-empty array");
    }
    for (const member of members) {
      if (!isSafeText(member) || !GITHUB_LOGIN.test(member) || aliases.has(member)) {
        throw new TypeError("alias member must be a direct GitHub login");
      }
    }
  }
}

function parseAliases(text) {
  const value = parseStrictYaml(text, "OWNERS_ALIASES");
  if (!isRecord(value)) {
    throw new TypeError("OWNERS_ALIASES must be an object");
  }
  rejectUnknownKeys(value, ["aliases"], "OWNERS_ALIASES");
  if (!isRecord(value.aliases)) {
    throw new TypeError("aliases must be an object");
  }

  const aliases = new Map();
  for (const [name, members] of Object.entries(value.aliases)) {
    if (!isSafeText(name) || !ALIAS_NAME.test(name)) {
      throw new TypeError("alias name must be safe");
    }
    if (!Array.isArray(members) || members.length === 0) {
      throw new TypeError("alias group must be a non-empty array");
    }
    aliases.set(name, [...members]);
  }
  validateAliasMap(aliases);
  return aliases;
}

function validateChangedPath(changedPath) {
  if (
    !isSafeText(changedPath)
    || changedPath.startsWith("/")
    || changedPath.includes("\\")
  ) {
    throw new TypeError("changed path must be a safe repository-relative path");
  }
  const segments = changedPath.split("/");
  if (segments.some((segment) => segment === "" || segment === "." || segment === "..")) {
    throw new TypeError("changed path must be a safe repository-relative path");
  }
}

function compareCaseInsensitive(left, right) {
  const normalizedLeft = left.toLowerCase();
  const normalizedRight = right.toLowerCase();
  if (normalizedLeft < normalizedRight) {
    return -1;
  }
  if (normalizedLeft > normalizedRight) {
    return 1;
  }
  return left < right ? -1 : left > right ? 1 : 0;
}

function uniqueSorted(values) {
  const canonical = new Map();
  for (const value of values) {
    const key = value.toLowerCase();
    if (!canonical.has(key)) {
      canonical.set(key, key);
    }
  }
  return [...canonical.values()].sort(compareCaseInsensitive);
}

function validateActiveOwnerFile(ownerFile) {
  if (!isRecord(ownerFile)) {
    throw new TypeError("active OWNERS declaration must be an object");
  }
  const path = normalizeOwnerPath(ownerFile.path);
  const reviewers = parseOwnerList(ownerFile.reviewers, "reviewers");
  const approvers = parseOwnerList(ownerFile.approvers, "approvers");
  const labels = parseLabels(ownerFile.labels);
  const options = parseOptions(ownerFile.options);
  return { path, reviewers, approvers, labels, options };
}

function normalizePolicy(policy) {
  if (!isRecord(policy) || !Array.isArray(policy.activeOwnerFiles)) {
    throw new TypeError("policy.activeOwnerFiles must be an array");
  }
  const activeOwnerFiles = new Set();
  for (const ownerPath of policy.activeOwnerFiles) {
    activeOwnerFiles.add(normalizeOwnerPath(ownerPath));
  }

  if (!isSafeText(policy.pullRequestAuthor) || !GITHUB_LOGIN.test(policy.pullRequestAuthor)) {
    throw new TypeError("policy.pullRequestAuthor must be a GitHub login");
  }
  const pullRequestAuthor = policy.pullRequestAuthor.toLowerCase();
  return { activeOwnerFiles, pullRequestAuthor };
}

function activeDeclarations(ownerFiles, activeOwnerFiles) {
  if (!Array.isArray(ownerFiles)) {
    throw new TypeError("ownerFiles must be an array");
  }

  const declarations = new Map();
  for (const ownerFile of ownerFiles) {
    if (!isRecord(ownerFile) || typeof ownerFile.path !== "string") {
      continue;
    }
    const candidatePath = ownerFile.path.startsWith("/")
      ? ownerFile.path
      : `/${ownerFile.path}`;
    if (!activeOwnerFiles.has(candidatePath)) {
      continue;
    }
    const declaration = validateActiveOwnerFile(ownerFile);
    if (declarations.has(declaration.path)) {
      throw new TypeError("activeOwnerFiles must resolve to unique OWNERS declarations");
    }
    declarations.set(declaration.path, declaration);
  }
  return declarations;
}

function ownerPathsForChangedPath(changedPath) {
  const segments = changedPath.split("/");
  segments.pop();
  const result = [];
  for (let length = segments.length; length >= 0; length -= 1) {
    const directory = segments.slice(0, length).join("/");
    result.push(directory === "" ? "/OWNERS" : `/${directory}/OWNERS`);
  }
  return result;
}

function declarationsForPath(changedPath, declarations) {
  const selected = [];
  for (const ownerPath of ownerPathsForChangedPath(changedPath)) {
    const declaration = declarations.get(ownerPath);
    if (declaration === undefined) {
      continue;
    }
    selected.push(declaration);
    if (declaration.options.no_parent_owners) {
      break;
    }
  }
  return selected.reverse();
}

function expandOwnerToken(token, aliases) {
  if (aliases.has(token)) {
    return aliases.get(token);
  }
  if (GITHUB_LOGIN.test(token)) {
    return [token];
  }
  throw new TypeError(`unknown alias ${token}`);
}

function resolveFile(changedPath, declarations, aliases, pullRequestAuthor) {
  const reviewers = [];
  const approvers = [];
  for (const declaration of declarationsForPath(changedPath, declarations)) {
    for (const reviewer of declaration.reviewers) {
      reviewers.push(...expandOwnerToken(reviewer, aliases));
    }
    for (const approver of declaration.approvers) {
      approvers.push(...expandOwnerToken(approver, aliases));
    }
  }

  const excludeAuthor = (login) => login.toLowerCase() !== pullRequestAuthor;
  return {
    path: changedPath,
    reviewers: uniqueSorted(reviewers).filter(excludeAuthor),
    approvers: uniqueSorted(approvers).filter(excludeAuthor),
  };
}

function resolveOwners(paths, ownerFiles, aliases, policy) {
  if (!Array.isArray(paths)) {
    throw new TypeError("paths must be an array");
  }
  for (const changedPath of paths) {
    validateChangedPath(changedPath);
  }
  validateAliasMap(aliases);
  const { activeOwnerFiles, pullRequestAuthor } = normalizePolicy(policy);
  const declarations = activeDeclarations(ownerFiles, activeOwnerFiles);

  const files = paths
    .map((changedPath) => resolveFile(
      changedPath,
      declarations,
      aliases,
      pullRequestAuthor,
    ))
    .sort((left, right) => compareCaseInsensitive(left.path, right.path));
  const reviewerCandidates = uniqueSorted(files.flatMap((file) => file.reviewers));
  const approverCandidates = uniqueSorted(files.flatMap((file) => file.approvers));
  const uncoveredPaths = files
    .filter((file) => file.reviewers.length === 0 && file.approvers.length === 0)
    .map((file) => file.path);

  return { files, reviewerCandidates, approverCandidates, uncoveredPaths };
}

module.exports = { parseAliases, parseOwnersFile, resolveOwners };
