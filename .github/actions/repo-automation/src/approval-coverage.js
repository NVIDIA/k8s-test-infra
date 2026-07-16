"use strict";

const CONTROL_CHARACTERS = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;
const GITHUB_LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const GIT_OID = /^[0-9a-fA-F]{40}$/;
const UTC_TIMESTAMP = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{3}))?Z$/;
const FILE_KEYS = ["approvers", "path"];
const REVIEW_KEYS = ["commitOid", "id", "state", "submittedAt", "user"];
const EFFECTIVE_REVIEW_KEYS = [
  "approved",
  "commitOid",
  "reviewId",
  "state",
  "submittedAt",
  "user",
];
const EVALUATION_KEYS = ["author", "files", "headOid", "reviews"];
const SELECTION_KEYS = ["author", "effectiveReviews", "files", "requested"];
const REVIEW_STATES = new Set([
  "APPROVED",
  "CHANGES_REQUESTED",
  "COMMENTED",
  "DISMISSED",
]);

function isPlainObject(value) {
  return value !== null
    && typeof value === "object"
    && !Array.isArray(value)
    && Object.getPrototypeOf(value) === Object.prototype;
}

function requirePlainObject(value, field) {
  if (!isPlainObject(value)) {
    throw new TypeError(`${field} must be a plain object`);
  }
}

function rejectUnknownKeys(value, allowedKeys, field) {
  if (Object.keys(value).some((key) => !allowedKeys.includes(key))) {
    throw new TypeError(`${field} contains an unknown field`);
  }
}

function isSafeText(value) {
  return typeof value === "string" && value !== "" && !CONTROL_CHARACTERS.test(value);
}

function normalizeLogin(value, field) {
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
    normalized.add(normalizeLogin(value, `${field} member`));
  }
  return [...normalized].sort(compareText);
}

function validateFilePath(value) {
  if (!isSafeText(value) || value.startsWith("/") || value.includes("\\")) {
    throw new TypeError("file path must be a safe repository-relative path");
  }
  const segments = value.split("/");
  if (segments.some((segment) => segment === "" || segment === "." || segment === "..")) {
    throw new TypeError("file path must be a safe repository-relative path");
  }
  return value;
}

function compareText(left, right) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function normalizeFiles(files) {
  if (!Array.isArray(files) || files.length === 0) {
    throw new TypeError("files must be a non-empty array");
  }

  const paths = new Set();
  const normalized = files.map((file) => {
    requirePlainObject(file, "file");
    rejectUnknownKeys(file, FILE_KEYS, "file");
    const path = validateFilePath(file.path);
    if (paths.has(path)) {
      throw new TypeError("file paths must be unique");
    }
    paths.add(path);
    return {
      path,
      approvers: normalizeLogins(file.approvers, "file approvers"),
    };
  });
  return normalized.sort((left, right) => compareText(left.path, right.path));
}

function normalizeOid(value, field) {
  if (!isSafeText(value) || !GIT_OID.test(value)) {
    throw new TypeError(`${field} must be a 40-character Git OID`);
  }
  return value.toLowerCase();
}

function normalizeReviewState(value, field) {
  if (!isSafeText(value) || !REVIEW_STATES.has(value)) {
    throw new TypeError(`${field} must be a supported review state`);
  }
  return value;
}

function isLeapYear(year) {
  return year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
}

function daysInMonth(year, month) {
  const lengths = [31, isLeapYear(year) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return lengths[month - 1] ?? 0;
}

function normalizeTimestamp(value, field) {
  if (!isSafeText(value)) {
    throw new TypeError(`${field} must be a UTC timestamp`);
  }
  const match = UTC_TIMESTAMP.exec(value);
  if (match === null) {
    throw new TypeError(`${field} must be a UTC timestamp`);
  }

  const [year, month, day, hour, minute, second] = match
    .slice(1, 7)
    .map((part) => Number.parseInt(part, 10));
  if (
    year < 1970
    || month < 1
    || month > 12
    || day < 1
    || day > daysInMonth(year, month)
    || hour > 23
    || minute > 59
    || second > 59
  ) {
    throw new TypeError(`${field} must be a UTC timestamp`);
  }

  const sortKey = Date.parse(value);
  if (!Number.isFinite(sortKey)) {
    throw new TypeError(`${field} must be a UTC timestamp`);
  }
  return { value, sortKey };
}

function normalizeReviewId(value, field) {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new TypeError(`${field} must be a positive safe integer`);
  }
  return value;
}

function normalizeReviews(reviews) {
  if (!Array.isArray(reviews)) {
    throw new TypeError("reviews must be an array");
  }

  const ids = new Set();
  const normalized = reviews.map((review) => {
    requirePlainObject(review, "review");
    rejectUnknownKeys(review, REVIEW_KEYS, "review");
    const id = normalizeReviewId(review.id, "review id");
    if (ids.has(id)) {
      throw new TypeError("review ids must be unique");
    }
    ids.add(id);
    const submittedAt = normalizeTimestamp(review.submittedAt, "review submitted time");
    return {
      id,
      user: normalizeLogin(review.user, "review user"),
      state: normalizeReviewState(review.state, "review state"),
      commitOid: normalizeOid(review.commitOid, "review commit OID"),
      submittedAt: submittedAt.value,
      submittedSortKey: submittedAt.sortKey,
    };
  });

  return normalized.sort((left, right) => (
    left.submittedSortKey - right.submittedSortKey || left.id - right.id
  ));
}

// COMMENTED is not an effective review decision in GitHub's protection model.
// It therefore cannot create approval and does not revoke an earlier decision.
function reduceReviews(reviews, headOid, author) {
  const byUser = new Map();
  for (const review of reviews) {
    const previous = byUser.get(review.user);
    if (review.state === "COMMENTED" && previous !== undefined) {
      continue;
    }
    byUser.set(review.user, review);
  }

  return [...byUser.values()]
    .map((review) => ({
      user: review.user,
      state: review.state,
      commitOid: review.commitOid,
      reviewId: review.id,
      submittedAt: review.submittedAt,
      approved: review.state === "APPROVED"
        && review.commitOid === headOid
        && review.user !== author,
    }))
    .sort((left, right) => compareText(left.user, right.user));
}

function coverageFor(files, approvers) {
  const coveredPaths = files
    .filter((file) => file.approvers.some((approver) => approvers.has(approver)))
    .map((file) => file.path);
  const covered = new Set(coveredPaths);
  const uncoveredPaths = files
    .filter((file) => !covered.has(file.path))
    .map((file) => file.path);
  return { coveredPaths, uncoveredPaths };
}

function evaluateApprovalCoverage(options) {
  requirePlainObject(options, "options");
  rejectUnknownKeys(options, EVALUATION_KEYS, "options");
  const files = normalizeFiles(options.files);
  const reviews = normalizeReviews(options.reviews);
  const headOid = normalizeOid(options.headOid, "head OID");
  const author = normalizeLogin(options.author, "author");
  const effectiveReviews = reduceReviews(reviews, headOid, author);
  const approvedApprovers = new Set(
    effectiveReviews.filter((review) => review.approved).map((review) => review.user),
  );
  const { coveredPaths, uncoveredPaths } = coverageFor(files, approvedApprovers);

  return {
    approved: uncoveredPaths.length === 0,
    effectiveReviews,
    coveredPaths,
    uncoveredPaths,
  };
}

function normalizeEffectiveReviews(reviews) {
  if (!Array.isArray(reviews)) {
    throw new TypeError("effectiveReviews must be an array");
  }

  const users = new Set();
  return reviews.map((review) => {
    requirePlainObject(review, "effective review");
    rejectUnknownKeys(review, EFFECTIVE_REVIEW_KEYS, "effective review");
    const user = normalizeLogin(review.user, "effective review user");
    if (users.has(user)) {
      throw new TypeError("effective review users must be unique");
    }
    users.add(user);
    const state = normalizeReviewState(review.state, "effective review state");
    if (typeof review.approved !== "boolean" || (review.approved && state !== "APPROVED")) {
      throw new TypeError("effective review approval must match its state");
    }
    return {
      user,
      state,
      commitOid: normalizeOid(review.commitOid, "effective review commit OID"),
      reviewId: normalizeReviewId(review.reviewId, "effective review id"),
      submittedAt: normalizeTimestamp(
        review.submittedAt,
        "effective review submitted time",
      ).value,
      approved: review.approved,
    };
  });
}

function candidatePaths(files, author) {
  const candidates = new Map();
  for (const file of files) {
    for (const approver of file.approvers) {
      if (approver === author) {
        continue;
      }
      if (!candidates.has(approver)) {
        candidates.set(approver, new Set());
      }
      candidates.get(approver).add(file.path);
    }
  }
  return candidates;
}

function coverPaths(covered, paths) {
  for (const path of paths) {
    covered.add(path);
  }
}

function selectApprovers(options) {
  requirePlainObject(options, "options");
  rejectUnknownKeys(options, SELECTION_KEYS, "options");
  const files = normalizeFiles(options.files);
  const effectiveReviews = normalizeEffectiveReviews(options.effectiveReviews);
  const requested = normalizeLogins(options.requested, "requested");
  const author = normalizeLogin(options.author, "author");
  const candidates = candidatePaths(files, author);
  const covered = new Set();
  const used = new Set();

  for (const review of effectiveReviews) {
    if (!review.approved || review.user === author || !candidates.has(review.user)) {
      continue;
    }
    coverPaths(covered, candidates.get(review.user));
    used.add(review.user);
  }
  for (const user of requested) {
    if (user === author || !candidates.has(user)) {
      continue;
    }
    coverPaths(covered, candidates.get(user));
    used.add(user);
  }

  const selected = [];
  while (true) {
    const ranked = [...candidates]
      .filter(([user]) => !used.has(user))
      .map(([user, paths]) => ({
        user,
        paths,
        newlyCovered: [...paths].reduce(
          (count, path) => count + Number(!covered.has(path)),
          0,
        ),
      }))
      .filter((candidate) => candidate.newlyCovered > 0)
      .sort((left, right) => (
        right.newlyCovered - left.newlyCovered || compareText(left.user, right.user)
      ));
    if (ranked.length === 0) {
      break;
    }
    const chosen = ranked[0];
    selected.push(chosen.user);
    used.add(chosen.user);
    coverPaths(covered, chosen.paths);
  }

  return {
    selected: selected.sort(compareText),
    uncoveredPaths: files
      .filter((file) => !covered.has(file.path))
      .map((file) => file.path),
  };
}

module.exports = { evaluateApprovalCoverage, selectApprovers };
