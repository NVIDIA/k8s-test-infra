"use strict";

const TRAILER_LINE = /^([A-Za-z0-9][A-Za-z0-9-]*):[ \t]*([\s\S]*)$/;
const CONTINUATION_LINE = /^[ \t]+([\s\S]+)$/;
const UNSAFE_TEXT = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u;
const EMAIL = /^[^<>\s@]+@[^<>\s@]+$/;

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function isSafeNonEmptyString(value) {
  return typeof value === "string"
    && !UNSAFE_TEXT.test(value)
    && value.trim() !== "";
}

function requireNonEmptyString(value, name) {
  if (typeof value !== "string" || value.trim() === "") {
    throw new TypeError(`${name} must be a non-empty string`);
  }
}

function requireSafeNonEmptyString(value, name) {
  if (!isSafeNonEmptyString(value)) {
    throw new TypeError(`${name} must be a safe non-empty string`);
  }
}

function requireEmail(value, name) {
  requireSafeNonEmptyString(value, name);
  if (!EMAIL.test(value) || value !== value.trim()) {
    throw new TypeError(`${name} must be an email address`);
  }
}

function validateBotPolicy(botPolicy) {
  if (!Array.isArray(botPolicy) || botPolicy.length === 0) {
    throw new TypeError("botPolicy must be a non-empty array");
  }

  for (let index = 0; index < botPolicy.length; index += 1) {
    const bot = botPolicy[index];
    if (!isRecord(bot)) {
      throw new TypeError(`botPolicy[${index}] must be an object`);
    }
    const keys = Object.keys(bot).sort();
    if (keys.length !== 2 || keys[0] !== "emails" || keys[1] !== "login") {
      throw new TypeError(`botPolicy[${index}] must contain exactly login and emails`);
    }
    requireSafeNonEmptyString(bot.login, `botPolicy[${index}].login`);
    if (bot.login !== bot.login.trim()) {
      throw new TypeError(`botPolicy[${index}].login must not have surrounding whitespace`);
    }
    if (!Array.isArray(bot.emails) || bot.emails.length === 0) {
      throw new TypeError(`botPolicy[${index}].emails must be a non-empty array`);
    }
    for (let emailIndex = 0; emailIndex < bot.emails.length; emailIndex += 1) {
      requireEmail(bot.emails[emailIndex], `botPolicy[${index}].emails[${emailIndex}]`);
    }
  }
}

function validateCommit(entry, index) {
  if (!isRecord(entry)) {
    throw new TypeError(`commits[${index}] must be an object`);
  }
  requireSafeNonEmptyString(entry.sha, `commits[${index}].sha`);
  if (entry.sha !== entry.sha.trim()) {
    throw new TypeError(`commits[${index}].sha must not have surrounding whitespace`);
  }
  if (!isRecord(entry.commit)) {
    throw new TypeError(`commits[${index}].commit must be an object`);
  }
  if (typeof entry.commit.message !== "string") {
    throw new TypeError(`commits[${index}].commit.message must be a string`);
  }
  if (!isRecord(entry.commit.author)) {
    throw new TypeError(`commits[${index}].commit.author must be an object`);
  }
  requireNonEmptyString(entry.commit.author.name, `commits[${index}].commit.author.name`);
  requireNonEmptyString(entry.commit.author.email, `commits[${index}].commit.author.email`);
  if (
    isSafeNonEmptyString(entry.commit.author.email)
    && (!EMAIL.test(entry.commit.author.email) || entry.commit.author.email !== entry.commit.author.email.trim())
  ) {
    throw new TypeError(`commits[${index}].commit.author.email must be an email address`);
  }

  if (entry.author !== null) {
    if (!isRecord(entry.author)) {
      throw new TypeError(`commits[${index}].author must be an object or null`);
    }
    requireNonEmptyString(entry.author.login, `commits[${index}].author.login`);
    if (isSafeNonEmptyString(entry.author.login) && entry.author.login !== entry.author.login.trim()) {
      throw new TypeError(`commits[${index}].author.login must not have surrounding whitespace`);
    }
  }
}

function parseFinalTrailers(message) {
  const lines = message.replace(/\r\n/g, "\n").split("\n");
  while (lines.length > 0 && lines[lines.length - 1].trim() === "") {
    lines.pop();
  }

  let separator = -1;
  for (let index = lines.length - 1; index >= 0; index -= 1) {
    if (lines[index].trim() === "") {
      separator = index;
      break;
    }
  }
  if (separator < 0 || separator === lines.length - 1) {
    return [];
  }

  const trailers = [];
  for (const line of lines.slice(separator + 1)) {
    const trailerMatch = TRAILER_LINE.exec(line);
    if (trailerMatch !== null) {
      trailers.push({
        key: trailerMatch[1],
        value: trailerMatch[2],
        continued: false,
      });
      continue;
    }

    const continuationMatch = CONTINUATION_LINE.exec(line);
    if (continuationMatch === null || trailers.length === 0) {
      return [];
    }
    const trailer = trailers[trailers.length - 1];
    trailer.value += `\n${continuationMatch[1]}`;
    trailer.continued = true;
  }
  return trailers;
}

function parseIdentity(value) {
  if (UNSAFE_TEXT.test(value)) {
    return { identity: null, unsafe: true };
  }
  const match = /^([^<>\r\n]+?)[ \t]+<([^<>\r\n]+)>$/.exec(value.trim());
  if (match === null || !EMAIL.test(match[2])) {
    return { identity: null, unsafe: false };
  }
  const identity = { name: match[1].trim(), email: match[2] };
  if (!isSafeNonEmptyString(identity.name) || !isSafeNonEmptyString(identity.email)) {
    return { identity: null, unsafe: true };
  }
  return { identity, unsafe: false };
}

function normalizedName(name) {
  return name.trim().replace(/[ \t]+/g, " ").toLowerCase();
}

function normalizedEmail(email) {
  return email.trim().toLowerCase();
}

function identitiesMatch(left, right) {
  return normalizedName(left.name) === normalizedName(right.name)
    && normalizedEmail(left.email) === normalizedEmail(right.email);
}

function formatIdentity(identity) {
  return `${identity.name.trim().replace(/[ \t]+/g, " ")} <${identity.email}>`;
}

function identitySafetyFailure(entry) {
  if (
    !isSafeNonEmptyString(entry.commit.author.name)
    || !isSafeNonEmptyString(entry.commit.author.email)
  ) {
    return { sha: entry.sha, reason: "commit author identity is unsafe" };
  }
  if (entry.author !== null && !isSafeNonEmptyString(entry.author.login)) {
    return { sha: entry.sha, reason: "linked author identity is unsafe" };
  }
  return null;
}

function matchingBot(entry, botPolicy) {
  if (entry.author === null) {
    return null;
  }
  return botPolicy.find((bot) => (
    entry.author.login === bot.login
    && bot.emails.includes(entry.commit.author.email)
  )) ?? null;
}

function dcoFailure(entry) {
  const author = entry.commit.author;
  const authorDisplay = formatIdentity(author);
  const signoffTrailers = parseFinalTrailers(entry.commit.message)
    .filter(({ key }) => key.toLowerCase() === "signed-off-by");
  const parsedSignoffs = signoffTrailers.map((trailer) => (
    trailer.continued
      ? { identity: null, unsafe: false }
      : parseIdentity(trailer.value)
  ));
  if (parsedSignoffs.some(({ unsafe }) => unsafe)) {
    return { sha: entry.sha, reason: "Signed-off-by identity is unsafe" };
  }
  const malformed = parsedSignoffs.some(({ identity }) => identity === null);
  const signoffs = parsedSignoffs
    .map(({ identity }) => identity)
    .filter((identity) => identity !== null);

  if (malformed || signoffs.length === 0) {
    return {
      sha: entry.sha,
      reason: `commit author ${authorDisplay} has no well-formed Signed-off-by trailer`,
    };
  }
  if (signoffs.some((identity) => identitiesMatch(identity, author))) {
    return null;
  }
  return {
    sha: entry.sha,
    reason: `commit author ${authorDisplay} does not match Signed-off-by trailer(s): ${signoffs.map(formatIdentity).join(", ")}`,
  };
}

function evaluateDco(commits, botPolicy) {
  if (!Array.isArray(commits)) {
    throw new TypeError("commits must be an array");
  }
  validateBotPolicy(botPolicy);
  commits.forEach(validateCommit);

  const failures = [];
  const exempted = [];
  for (const entry of commits) {
    const safetyFailure = identitySafetyFailure(entry);
    if (safetyFailure !== null) {
      failures.push(safetyFailure);
      continue;
    }
    if (matchingBot(entry, botPolicy) !== null) {
      exempted.push(entry.sha);
      continue;
    }
    const failure = dcoFailure(entry);
    if (failure !== null) {
      failures.push(failure);
    }
  }
  return { valid: failures.length === 0, failures, exempted };
}

module.exports = { evaluateDco };
