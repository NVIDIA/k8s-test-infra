"use strict";

const crypto = require("node:crypto");

const CONTROL_CHARACTERS = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;
const GITHUB_LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const REPOSITORY_NAME = /^[A-Za-z0-9_.-]{1,100}$/;
const FILE_KEYS = ["additions", "deletions", "path", "reviewers"];
const OPTION_KEYS = ["author", "candidates", "files", "requested", "seed", "target"];
const SEED_KEYS = ["owner", "pr", "repo"];
const SAMPLE_SPACE = 0x10000000000000;

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function isSafeText(value) {
  return typeof value === "string" && value !== "" && !CONTROL_CHARACTERS.test(value);
}

function validateLogin(value, field) {
  if (!isSafeText(value) || !GITHUB_LOGIN.test(value)) {
    throw new TypeError(`${field} must be a GitHub login`);
  }
  return value.toLowerCase();
}

function normalizeLogins(values, field) {
  if (!Array.isArray(values)) {
    throw new TypeError(`${field} must be an array`);
  }

  const normalized = new Set();
  for (const value of values) {
    normalized.add(validateLogin(value, `${field} member`));
  }
  return [...normalized].sort();
}

function rejectUnknownKeys(value, allowedKeys, field) {
  if (Object.keys(value).some((key) => !allowedKeys.includes(key))) {
    throw new TypeError(`${field} contains an unknown field`);
  }
}

function validateFilePath(value) {
  if (
    !isSafeText(value)
    || value.startsWith("/")
    || value.includes("\\")
  ) {
    throw new TypeError("file path must be a safe repository-relative path");
  }

  const segments = value.split("/");
  if (segments.some((segment) => segment === "" || segment === "." || segment === "..")) {
    throw new TypeError("file path must be a safe repository-relative path");
  }
  return value;
}

function validateLineCount(value, field) {
  if (!Number.isSafeInteger(value) || value < 0) {
    throw new TypeError(`${field} must be a non-negative safe integer`);
  }
  return value;
}

function compareText(left, right) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function normalizeFiles(files) {
  if (!Array.isArray(files)) {
    throw new TypeError("files must be an array");
  }

  const paths = new Set();
  const normalized = files.map((file) => {
    if (!isRecord(file)) {
      throw new TypeError("file must be an object");
    }
    rejectUnknownKeys(file, FILE_KEYS, "file");

    const path = validateFilePath(file.path);
    if (paths.has(path)) {
      throw new TypeError("file paths must be unique");
    }
    paths.add(path);

    const additions = validateLineCount(file.additions, "file additions");
    const deletions = validateLineCount(file.deletions, "file deletions");
    if (!Number.isSafeInteger(additions + deletions)) {
      throw new TypeError("file changed lines must be a non-negative safe integer");
    }

    return {
      path,
      reviewers: normalizeLogins(file.reviewers, "file reviewers"),
      changedLines: additions + deletions,
    };
  });

  return normalized.sort((left, right) => compareText(left.path, right.path));
}

function normalizeSeed(seed) {
  if (!isRecord(seed)) {
    throw new TypeError("seed must be an object");
  }
  rejectUnknownKeys(seed, SEED_KEYS, "seed");

  const owner = validateLogin(seed.owner, "seed owner");
  if (
    !isSafeText(seed.repo)
    || !REPOSITORY_NAME.test(seed.repo)
    || seed.repo === "."
    || seed.repo === ".."
  ) {
    throw new TypeError("seed repository must be a valid repository name");
  }
  if (!Number.isSafeInteger(seed.pr) || seed.pr <= 0) {
    throw new TypeError("seed PR must be a positive safe integer");
  }

  return { owner, repo: seed.repo.toLowerCase(), pr: seed.pr };
}

function weightedRaceKey(seedText, login, weight) {
  const digest = crypto
    .createHash("sha256")
    .update(`${seedText}:${login}`)
    .digest("hex");
  const sample = Number.parseInt(digest.slice(0, 13), 16);
  const uniform = (sample + 1) / (SAMPLE_SPACE + 1);
  return -Math.log(uniform) / Math.max(1, weight);
}

function candidateDetails(candidates, files, seed) {
  const details = [];
  for (const login of candidates) {
    const reviewedPaths = files
      .filter((file) => file.reviewers.includes(login))
      .map((file) => file.path);
    if (reviewedPaths.length === 0) {
      continue;
    }

    let weight = 0;
    for (const file of files) {
      if (file.reviewers.includes(login)) {
        weight += file.changedLines;
        if (!Number.isSafeInteger(weight)) {
          throw new TypeError("candidate changed lines must be a non-negative safe integer");
        }
      }
    }
    details.push({
      login,
      reviewedPaths,
      raceKey: weightedRaceKey(seed, login, weight),
    });
  }
  return details;
}

function coveredPaths(reviewers, details) {
  const covered = new Set();
  for (const reviewer of reviewers) {
    const candidate = details.get(reviewer);
    for (const path of candidate.reviewedPaths) {
      covered.add(path);
    }
  }
  return covered;
}

function compareRace(left, right) {
  return left.raceKey - right.raceKey || compareText(left.login, right.login);
}

function chooseCandidate(remaining, uncovered) {
  const ranked = remaining.map((candidate) => ({
    ...candidate,
    uncoveredCount: candidate.reviewedPaths.reduce(
      (count, path) => count + Number(uncovered.has(path)),
      0,
    ),
  }));
  const maximumCoverage = Math.max(...ranked.map((candidate) => candidate.uncoveredCount));
  const covering = maximumCoverage > 0
    ? ranked.filter((candidate) => candidate.uncoveredCount === maximumCoverage)
    : ranked;
  return covering.sort(compareRace)[0];
}

function selectReviewers(options) {
  if (!isRecord(options)) {
    throw new TypeError("options must be an object");
  }
  rejectUnknownKeys(options, OPTION_KEYS, "options");

  const candidates = normalizeLogins(options.candidates, "candidates");
  const files = normalizeFiles(options.files);
  const seed = normalizeSeed(options.seed);
  if (!Number.isSafeInteger(options.target) || options.target <= 0) {
    throw new TypeError("target must be a positive safe integer");
  }
  const author = validateLogin(options.author, "author");
  const requested = normalizeLogins(options.requested, "requested");

  const eligibleLogins = candidates.filter((login) => login !== author);
  const details = candidateDetails(
    eligibleLogins,
    files,
    `${seed.owner}/${seed.repo}#${seed.pr}`,
  );
  const detailsByLogin = new Map(details.map((candidate) => [candidate.login, candidate]));
  const preserved = requested
    .filter((login) => detailsByLogin.has(login))
    .slice(0, options.target);
  const selected = [];
  const covered = coveredPaths(preserved, detailsByLogin);
  const used = new Set(preserved);

  while (used.size < options.target) {
    const remaining = details.filter((candidate) => !used.has(candidate.login));
    if (remaining.length === 0) {
      break;
    }

    const uncovered = new Set(
      files.filter((file) => !covered.has(file.path)).map((file) => file.path),
    );
    const chosen = chooseCandidate(remaining, uncovered);
    selected.push(chosen.login);
    used.add(chosen.login);
    for (const path of chosen.reviewedPaths) {
      covered.add(path);
    }
  }

  const uncoveredPaths = files
    .filter((file) => !covered.has(file.path))
    .map((file) => file.path);

  return {
    selected: selected.sort(),
    preserved,
    uncoveredPaths,
  };
}

module.exports = { selectReviewers };
