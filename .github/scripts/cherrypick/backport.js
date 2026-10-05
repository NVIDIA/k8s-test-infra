/**
 * Copyright 2026 NVIDIA CORPORATION
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Adapted from NVIDIA/nvidia-container-toolkit .github/scripts/backport.js.
// Changes: the pull request number comes from the validated workflow_dispatch
// input, an unmerged pull request is refused, the backport title keeps the
// conventional-commit prefix ("<title> [release-X.Y]"), and messages are plain
// text. This repository squash-merges pull requests from forks, so the PR head
// commits are on no branch of origin: the merge commit GitHub recorded for the
// PR is cherry-picked instead. The verified chain is built on the target commit
// the cherry-pick used, a backport branch that carries commits this workflow
// did not create is never overwritten, a change already on the target is
// reported without a PR, and the run fails when any target branch fails.

module.exports = async ({ github, context, core }) => {
const branches = JSON.parse(process.env.BRANCHES_JSON || '[]');

// The pull request number is validated by inputs.js before this step runs.
const prNumber = Number(process.env.PR_NUMBER);
if (!Number.isSafeInteger(prNumber) || prNumber <= 0) {
  throw new Error('PR_NUMBER is missing or invalid');
}

// Fetch full PR data (needed when triggered via issue_comment)
const { data: pullRequest } = await github.rest.pulls.get({
  owner: context.repo.owner,
  repo: context.repo.repo,
  pull_number: prNumber
});

if (pullRequest.merged !== true) {
  throw new Error(`PR #${prNumber} is not merged; only merged pull requests are cherry-picked`);
}

const sourceSha = pullRequest.merge_commit_sha;
if (typeof sourceSha !== 'string' || !/^[0-9a-f]{40}$/.test(sourceSha)) {
  throw new Error(`PR #${prNumber} has no merge commit to cherry-pick`);
}

const prTitle = pullRequest.title;
const prAuthor = pullRequest.user.login;

const { execSync } = require('child_process');

// A squash commit has one parent. A two-parent merge commit is picked against
// its first parent, the branch the PR merged into.
const sourceParents = execSync(`git rev-list --parents -n 1 ${sourceSha}`, { encoding: 'utf-8' })
  .trim().split(' ').length - 1;
const sourceSubject = execSync(`git log -1 --format=%s ${sourceSha}`, { encoding: 'utf-8' }).trim();
const cherryPick = sourceParents > 1
  ? `git cherry-pick -m 1 -x ${sourceSha}`
  : `git cherry-pick -x ${sourceSha}`;

core.info(`Backporting PR #${prNumber}: "${prTitle}"`);
core.info(`Commit to cherry-pick: ${sourceSha.substring(0, 7)} - ${sourceSubject}`);

// A backport branch holds only commits this workflow created: the verified
// chain from the Git Data API (bot author, GitHub committer), or the local
// cherry-pick a stopped run pushed before re-creating it (this runner's
// committer). Anything else, such as a pushed conflict resolution, an amend or
// a web edit, belongs to a human.
const BOT_AUTHOR = 'github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>';
const API_COMMITTER = 'GitHub <noreply@github.com>';
const LOCAL_COMMITTER = execSync('git var GIT_COMMITTER_IDENT', { encoding: 'utf-8' })
  .trim().replace(/ \d+ [+-]\d{4}$/, '');
const createdHere = ([author, committer]) =>
  (author === BOT_AUTHOR && committer === API_COMMITTER) || committer === LOCAL_COMMITTER;

const results = [];

for (const targetBranch of branches) {
  core.info(`\n========================================`);
  core.info(`Backporting to ${targetBranch}`);
  core.info(`========================================`);
  const backportBranch = `backport-${prNumber}-to-${targetBranch}`;
  try {
    // Create/reset backport branch from target release branch
    core.info(`Creating/resetting branch ${backportBranch} from ${targetBranch}`);
    execSync(`git fetch origin ${targetBranch}:${targetBranch}`, { stdio: 'inherit' });
    // The cherry-pick and the verified chain below both use this exact commit.
    const targetSha = execSync(`git rev-parse ${targetBranch}`, { encoding: 'utf-8' }).trim();

    // A backport branch may carry a human conflict resolution: never overwrite it.
    const remoteRef = `refs/remotes/origin/${backportBranch}`;
    if (execSync(`git ls-remote origin refs/heads/${backportBranch}`, { encoding: 'utf-8' }).trim() !== '') {
      execSync(`git fetch origin +refs/heads/${backportBranch}:${remoteRef}`, { stdio: 'inherit' });
      const identities = execSync(`git log --format='%an <%ae>%x00%cn <%ce>' ${targetSha}..${remoteRef}`, { encoding: 'utf-8' })
        .split('\n').filter(Boolean).map((line) => line.split('\0'));
      if (!identities.every(createdHere)) {
        throw new Error(`${backportBranch} has commits this workflow did not create; merge or delete it before cherry-picking again`);
      }
    }

    execSync(`git checkout -B ${backportBranch} ${targetSha}`, { stdio: 'inherit' });
    let hasConflicts = false;
    core.info(`Cherry-picking ${sourceSha.substring(0, 7)} - ${sourceSubject}`);
    try {
      execSync(cherryPick, {
        encoding: 'utf-8',
        stdio: 'pipe'
      });
    } catch (error) {
      // Check if it's a conflict
      const status = execSync('git status', { encoding: 'utf-8' });
      if (status.includes('Unmerged paths') || status.includes('both modified')) {
        hasConflicts = true;
        core.warning(`Cherry-pick has conflicts for commit ${sourceSha.substring(0, 7)}.`);
        // Add all files (including conflicted ones) and commit
        execSync('git add .', { stdio: 'inherit' });
        try {
          execSync(`git -c core.editor=true cherry-pick --continue`, { stdio: 'inherit' });
        } catch (e) {
          // If continue fails, make a simple commit
          execSync(`git commit --no-edit --allow-empty-message || git commit -m "Cherry-pick ${sourceSha} (with conflicts)"`, { stdio: 'inherit' });
        }
      } else if (error.message && error.message.includes('previous cherry-pick is now empty')) {
        // Handle empty commits (changes already exist in target branch)
        core.info(`Commit ${sourceSha.substring(0, 7)} is empty (changes already in target branch), skipping`);
        execSync('git cherry-pick --skip', { stdio: 'inherit' });
      } else {
        throw error;
      }
    }

    // The change is already on the target: there is nothing to backport, and a
    // PR whose head equals its base would be rejected.
    if (execSync(`git rev-list --count ${targetSha}..HEAD`, { encoding: 'utf-8' }).trim() === '0') {
      await github.rest.issues.createComment({
        owner: context.repo.owner,
        repo: context.repo.repo,
        issue_number: prNumber,
        body: `The change from #${prNumber} is already on \`${targetBranch}\`; no backport PR is needed.`
      });
      results.push({ branch: targetBranch, success: true, alreadyOnTarget: true });
      core.info(`${targetBranch} already has the change; no backport PR`);
      continue;
    }

    // Push the backport branch (force to handle updates)
    core.info(`Pushing ${backportBranch} to origin`);
    execSync(`git push --force-with-lease origin ${backportBranch}`, { stdio: 'inherit' });

    // Re-create each new commit through the Git Data API so the resulting chain shows as "Verified"
    core.info(`Re-creating commits via the Git Data API to get verified signatures`);
    const newCommitShas = execSync(`git log --format=%H ${targetSha}..${backportBranch}`, { encoding: 'utf-8' })
      .trim().split('\n').filter(Boolean).reverse(); // oldest -> newest

    // Parent the chain on the commit the trees were built on, not a re-read of
    // the target branch, which may have advanced since the fetch.
    let parentSha = targetSha;

    for (const sha of newCommitShas) {
      const treeSha = execSync(`git rev-parse ${sha}^{tree}`, { encoding: 'utf-8' }).trim();
      const message = execSync(`git log -1 --format=%B ${sha}`, { encoding: 'utf-8' });

      const { data: newCommit } = await github.rest.git.createCommit({
        owner: context.repo.owner,
        repo: context.repo.repo,
        message,
        tree: treeSha,
        parents: [parentSha]
      });
      parentSha = newCommit.sha;
    }

    core.info(`Repointing ${backportBranch} at signed commit ${parentSha}`);
    await github.rest.git.updateRef({
      owner: context.repo.owner,
      repo: context.repo.repo,
      ref: `heads/${backportBranch}`,
      sha: parentSha,
      force: true
    });

    // Check if a PR already exists for this backport branch
    const { data: existingPRs } = await github.rest.pulls.list({
      owner: context.repo.owner,
      repo: context.repo.repo,
      head: `${context.repo.owner}:${backportBranch}`,
      base: targetBranch,
      state: 'open'
    });
    const existingPR = existingPRs.length > 0 ? existingPRs[0] : null;
    
    // Create pull request
    const commitList = `- \`${sourceSha.substring(0, 7)}\` ${sourceSubject}`;
    
    // Build PR body based on conflict status
    let prBody = `**Automated backport of #${prNumber} to \`${targetBranch}\`**\n\n`;
    
    if (hasConflicts) {
      prBody += `**This PR has merge conflicts that need manual resolution.**

Original PR: #${prNumber}
Original Author: @${prAuthor}

**Cherry-picked commit:**
${commitList}

**Next Steps:**
1. Review the conflicts in the "Files changed" tab
2. Check out this branch locally: \`git fetch origin ${backportBranch} && git checkout ${backportBranch}\`
3. Resolve conflicts manually
4. Push the resolution: \`git push --force-with-lease origin ${backportBranch}\`

---
<details>
<summary>Instructions for resolving conflicts</summary>

\`\`\`bash
git fetch origin ${backportBranch}
git checkout ${backportBranch}
# Resolve conflicts in your editor
git add .
git commit
git push --force-with-lease origin ${backportBranch}
\`\`\`
</details>`;
    } else {
      prBody += `Cherry-pick completed successfully with no conflicts.

Original PR: #${prNumber}
Original Author: @${prAuthor}

**Cherry-picked commit:**
${commitList}

This backport was automatically created by the backport bot.`;
    }

    if (existingPR) {
      // Update existing PR
      core.info(`Found existing PR #${existingPR.number}, updating it`);
      await github.rest.pulls.update({
        owner: context.repo.owner,
        repo: context.repo.repo,
        pull_number: existingPR.number,
        body: prBody,
        draft: hasConflicts
      });
      
      // Update labels
      const currentLabels = existingPR.labels.map(l => l.name);
      const desiredLabels = ['backport', hasConflicts ? 'needs-manual-resolution' : 'auto-backport'];
      
      // Remove old labels if conflict status changed
      if (hasConflicts && currentLabels.includes('auto-backport')) {
        await github.rest.issues.removeLabel({
          owner: context.repo.owner,
          repo: context.repo.repo,
          issue_number: existingPR.number,
          name: 'auto-backport'
        }).catch(() => {}); // Ignore if label doesn't exist
      } else if (!hasConflicts && currentLabels.includes('needs-manual-resolution')) {
        await github.rest.issues.removeLabel({
          owner: context.repo.owner,
          repo: context.repo.repo,
          issue_number: existingPR.number,
          name: 'needs-manual-resolution'
        }).catch(() => {}); // Ignore if label doesn't exist
      }
      
      // Add current labels
      await github.rest.issues.addLabels({
        owner: context.repo.owner,
        repo: context.repo.repo,
        issue_number: existingPR.number,
        labels: desiredLabels
      });
      
      // Comment about the update
      await github.rest.issues.createComment({
        owner: context.repo.owner,
        repo: context.repo.repo,
        issue_number: prNumber,
        body: `Updated existing backport PR for \`${targetBranch}\`: #${existingPR.number}${hasConflicts ? ' (has conflicts)' : ''}`
      });
      
      results.push({
        branch: targetBranch,
        success: true,
        prNumber: existingPR.number,
        prUrl: existingPR.html_url,
        hasConflicts,
        updated: true
      });
      core.info(`Updated backport PR #${existingPR.number}`);
    } else {
      // Create new PR
      const newPR = await github.rest.pulls.create({
        owner: context.repo.owner,
        repo: context.repo.repo,
        title: `${prTitle} [${targetBranch}]`,
        head: backportBranch,
        base: targetBranch,
        body: prBody,
        draft: hasConflicts
      });
      // Add labels
      await github.rest.issues.addLabels({
        owner: context.repo.owner,
        repo: context.repo.repo,
        issue_number: newPR.data.number,
        labels: ['backport', hasConflicts ? 'needs-manual-resolution' : 'auto-backport']
      });
      // Link to original PR
      await github.rest.issues.createComment({
        owner: context.repo.owner,
        repo: context.repo.repo,
        issue_number: prNumber,
        body: `Backport PR created for \`${targetBranch}\`: #${newPR.data.number}${hasConflicts ? ' (has conflicts)' : ''}`
      });
      results.push({
        branch: targetBranch,
        success: true,
        prNumber: newPR.data.number,
        prUrl: newPR.data.html_url,
        hasConflicts,
        updated: false
      });
      core.info(`Created backport PR #${newPR.data.number}`);
    }
  } catch (error) {
    core.error(`Failed to backport to ${targetBranch}: ${error.message}`);
    // Comment on original PR about the failure
    await github.rest.issues.createComment({
      owner: context.repo.owner,
      repo: context.repo.repo,
      issue_number: prNumber,
      body: `Failed to create backport PR for \`${targetBranch}\`\n\nError: ${error.message}\n\nPlease backport manually.`
    });
    results.push({
      branch: targetBranch,
      success: false,
      error: error.message
    });
  } finally {
    // Clean up: go back to main branch
    try {
      execSync('git checkout main', { stdio: 'inherit' });
      execSync(`git branch -D ${backportBranch} 2>/dev/null || true`, { stdio: 'inherit' });
    } catch (e) {
      // Ignore cleanup errors
    }
  }
}

// Summary (console only)
core.info('\n========================================');
core.info('Backport Summary');
core.info('========================================');
for (const result of results) {
  if (result.alreadyOnTarget) {
    core.info(`${result.branch}: already has the change`);
  } else if (result.success) {
    const action = result.updated ? 'Updated' : 'Created';
    core.info(`${result.branch}: ${action} PR #${result.prNumber} ${result.hasConflicts ? '(has conflicts)' : ''}`);
  } else {
    core.error(`${result.branch}: ${result.error}`);
  }
}
// Every branch is attempted and commented first; any failure fails the run.
const failedBranches = results.filter((result) => !result.success).map((result) => result.branch);
if (failedBranches.length > 0) {
  core.setFailed(`Cherry-pick failed for: ${failedBranches.join(', ')}`);
}
return results;
};
