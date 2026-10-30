---
name: rebase
description: Rebase the current branch onto the latest main and resolve conflicts
disable-model-invocation: true
argument-hint: "[base-branch] [--no-push]"
---

# Rebase onto latest main

Arguments from the user (may be empty): `$ARGUMENTS`
- A branch name overrides the base branch.
- `--no-push` (or any wording like "don't push", "no push", "local only") disables the push in step 7.

Never `git rebase --abort` or drop the user's commits without asking.

## 1. Preflight

- `git status` — if a rebase is already in progress, do the single-commit check from step 2, then skip to step 4 (resolve conflicts).
- Stop and report if: not a git repo, detached HEAD, or the current branch *is* the base branch.
- Working tree dirty? Ask the user: stash (`git stash push -u`, then `git stash pop` at the end) or stop.

## 2. Pick and fetch the base

- Remote: `upstream` if it exists (fork workflow), else `origin`.
- Branch: the branch named in the arguments if given, else the remote's default branch
  (`git symbolic-ref --short refs/remotes/<remote>/HEAD`, falling back to `main`, then `master`).
- `git fetch <remote> <branch>` — always rebase onto `<remote>/<branch>`, never a stale local `main`.
- Note the old HEAD SHA (`git rev-parse HEAD`) for the report, and show `git log --oneline <remote>/<branch>..HEAD`.
- Single-commit PR? Before rebasing, count the PR's commits: `git rev-list --count <remote>/<branch>..origin/<current-branch>` (the branch as last pushed, i.e. what the PR shows; don't fetch it, since that would defeat the lease in step 7). If it was never pushed, count `<remote>/<branch>..<current-branch>` instead. If the count is 1, step 5 squashes the branch back into one commit.
- If `git merge-base --is-ancestor <remote>/<branch> HEAD` succeeds, it's already up to date: skip to step 5.

## 3. Rebase

```bash
git rebase --signoff -S <remote>/<branch>
```

(`--signoff`, not `-s`: for rebase `-s` means `--strategy`.) If signing fails, stop and report the error. Don't retry unsigned.

## 4. Resolve conflicts (repeat until the rebase finishes)

While a rebase is running, **"ours" = the base (upstream + already-replayed commits) and "theirs" = the user's commit being replayed.**

1. `git diff --name-only --diff-filter=U` to list conflicted files; `git show REBASE_HEAD` to see what the user's commit intended.
2. Find out why upstream changed the same code: `git log --oneline -p ORIG_HEAD..<remote>/<branch> -- <file>`.
3. Edit each file so that **both intents survive** (e.g., upstream added a parameter and the commit changed the format → keep the parameter and the new format). Don't just pick a side.
4. Lockfiles/generated files: take the base version, then regenerate them with the project's tool if the commit changed dependencies.
5. Delete/modify conflicts: decide from the intent, then `git rm` or `git add`.
6. If the intent is ambiguous or the two sides are incompatible, stop and ask the user, leaving the rebase in progress.
7. Make sure no markers are left: `git diff --check` and `grep -nE '^(<<<<<<<|>>>>>>>)( |$)' <files>`.
8. `git add <files>` then `git rebase --continue` (the Claude Code shell already sets `GIT_EDITOR=true`).
9. If git says the commit is now empty (upstream already has the change), run `git rebase --skip` and mention it in the report.

## 5. Squash single-commit PRs back to one commit

Skip this unless step 2 counted a single-commit PR and `git rev-list --count <remote>/<branch>..HEAD` is now more than 1. Then fold everything into one commit that keeps the first commit's message and author (run it as one shell call so `$first` survives):

```bash
first=$(git rev-list --reverse <remote>/<branch>..HEAD | head -n1)
git reset --soft <remote>/<branch> && git commit -s -S -C "$first"
```

If signing fails, `git reset --soft ORIG_HEAD` restores the rebased commits; then stop and report the error.

## 6. Verify

- `git status` is clean with no rebase in progress; `git log --oneline <remote>/<branch>..HEAD` shows the user's commits.
- Every rebased commit is signed and signed off. Check signatures through the raw header, since `%G?` reports `N` for SSH signatures without an allowedSignersFile:
  ```bash
  for c in $(git rev-list <remote>/<branch>..HEAD); do git cat-file -p "$c" | grep -q '^gpgsig' || echo "UNSIGNED $c"; done
  git log --format='%h %(trailers:key=Signed-off-by,valueonly,separator=%x2C )' <remote>/<branch>..HEAD
  ```
- If you resolved conflicts, run the project's quick build/lint/tests for the touched code (from the Makefile, package scripts, or CLAUDE.md). Ask before starting anything slow.

## 7. Push (default) and report

Push by default unless the arguments disabled it. Only push when the rebase finished and every step 6 check passed. If anything failed or is still unresolved, don't push and say why.

- Push to the branch's own remote (the fork `origin`, never `upstream`):
  - If the branch has an upstream: `git push --force-with-lease`
  - If it has none yet: `git push --force-with-lease -u origin HEAD`
- Skip the push when nothing changed (step 2 found it already up to date and the remote branch matches `HEAD`).
- If the lease is rejected (someone else pushed to the branch), stop. Don't fall back to `--force`. Show `git log --oneline HEAD..@{u}` and ask the user.

Report briefly: base used, commits replayed, each conflict and how you resolved it, skipped commits, the squash (subjects of the folded-in commits), verification results, push result, and the old HEAD SHA (undo: `git reset --hard <sha>`).
