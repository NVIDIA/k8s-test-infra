"use strict";

async function verifyApproverAuthor(github, ownership, author) {
  if (ownership.authorApprovalPaths.length === 0) return false;
  const login = author.toLowerCase();
  try {
    const identity = await github.getUserIdentity(login);
    return identity?.resolved === true
      && identity?.deleted === false
      && identity?.type === "User"
      && typeof identity.login === "string"
      && identity.login.toLowerCase() === login;
  } catch {
    // An unavailable identity cannot grant author approval.
    return false;
  }
}

module.exports = { verifyApproverAuthor };
