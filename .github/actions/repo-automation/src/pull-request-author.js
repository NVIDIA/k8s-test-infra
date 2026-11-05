"use strict";

const GITHUB_LOGIN = /^(?!.*--)[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;

// Pull request authors include GitHub App bots such as dependabot[bot]; commenters and reviewers do not.
function validAuthorContext(value) {
  if (typeof value !== "string" || /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/u.test(value)) return false;
  return GITHUB_LOGIN.test(value.endsWith("[bot]") ? value.slice(0, -5) : value);
}

module.exports = { validAuthorContext };
