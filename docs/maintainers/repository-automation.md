# Repository automation

This repository uses one packaged JavaScript action for repository policy. The
workflows use trusted code and policy from the default branch. They do not run
code from a pull request head with a write token.

## Supported foundation

The foundation provides these functions:

1. Additive label synchronization from `.github/repo-automation/labels.yml`.
2. Pull request metadata and reviewer reconciliation.
3. Guarded `/lgtm`, `/approve`, `/hold`, `/unhold`, `/retest`, `/backport`, and
   `/cherry-pick` commands.
4. Review-change observation.
5. A stable `repository-automation/merge-policy` check and GitHub native
   auto-merge.
6. Generic backport pull requests for explicitly allowed target branches.
7. Explicit Mokka cherry-pick dispatch for its validated contract.

`/backport <branch>` and `/cherry-pick <branch>` are aliases for the generic
backport command. They create a backport pull request for an allowed
`release-*` branch. They do not start the Mokka dispatch workflow.

The foundation does not install Prow or Tide. GitHub branch protection remains
the final merge authority. The automation can enable or disable GitHub native
auto-merge, but it does not call a direct merge endpoint.

## Activation order

Write-capable jobs are disabled when they first land. Enable a stage by setting
its repository variable to the exact lowercase string `true`. An unset variable
or any other value keeps the job disabled.

Activate the functions in this order:

1. Run **Label synchronization** without `apply`. Review the plan. Run it again
   with `apply` only after the plan is correct.
2. Set `REPOSITORY_AUTOMATION_METADATA_ENABLED=true`. Confirm that **PR
   metadata** changes only managed metadata labels and reviewer requests.
3. Set `REPOSITORY_AUTOMATION_COMMANDS_ENABLED=true`. Confirm command
   authorization and duplicate-delivery behavior on a test pull request.
4. Set `REPOSITORY_AUTOMATION_REVIEWS_ENABLED=true`. Confirm that **Merge
   evaluation** receives only the expected completion events and keeps its job
   disabled.
5. Add `repository-automation/merge-policy` as a required branch-protection
   check. Only then set `REPOSITORY_AUTOMATION_MERGE_ENABLED=true` to permit
   merge evaluation to use GitHub native auto-merge.
6. After every configured `release-*` target is protected and exists, set
   `REPOSITORY_AUTOMATION_BACKPORT_ENABLED=true`.
7. After the external caller uses the documented UUID, source SHA, target
   branch, workflow commit SHA, and repository identity contract, review the
   Mokka workflow and packaged action on `main`. Confirm that the `main` branch
   rule rejects force pushes and applies the required merge checks to Mokka
   draft pull requests. Set
   `REPOSITORY_AUTOMATION_MOKKA_REVIEWED_SHA` to the full commit SHA of that
   reviewed version, then set `REPOSITORY_AUTOMATION_MOKKA_ENABLED=true`.

Keep each earlier step active while you validate the next step. Do not enable a
later write path when an earlier validation fails.

## Mokka dispatch contract

Start **Mokka cherry-pick** only from `main`. The caller supplies the `main`
commit SHA it checked. The job runs only when GitHub resolves the selected
`main` ref to that SHA. The caller stops if its preflight sees a different SHA.
If GitHub selects a different SHA, the job skips. A later `main` move does not
change the selected commit for that run. The caller must check the new commit
before it retries.

The job loads automation from the selected `main` commit and compares the Mokka
workflow, action metadata, and packaged action byte for byte with the commit
in `REPOSITORY_AUTOMATION_MOKKA_REVIEWED_SHA`. It does this before it checks
out the target with credentials. Unrelated changes to `main` do not require a
new reviewed SHA. With this workflow version, a change to any compared file
stops the job until a reviewer approves that automation and updates the
reviewed SHA. GitHub branch protection remains the authority for later
workflow changes, including edits to this guard.

The dispatch accepts exactly five required string inputs:

- `pull_request_number`: the number of a merged pull request in
  `NVIDIA/k8s-test-infra`.
- `source_sha`: the lowercase, 40-character merge commit SHA for that pull
  request.
- `target_branch`: the exact value `main`.
- `action_id`: a canonical lowercase UUIDv4 that makes the request
  idempotent.
- `workflow_commit_sha`: the lowercase, 40-character `main` commit SHA that
  the caller checked for this dispatch.

The action also verifies GitHub repository ID `733665780`, validates that the
source pull request belongs to this repository, and rejects a source pull
request that was based on the target branch. The source SHA must be the merged
pull request commit and must have one parent. The action creates or reuses a
`mokka/cherry-pick/<action_id>` branch and opens a draft pull request. It does
not merge the pull request.

Before each cherry-pick attempt, the action fetches the current target branch
and starts from that commit. If `main` advances before the upload, the action
fetches it and retries the cherry-pick, up to three attempts. It rejects a
target history rewrite, a source pull request change, or a cherry-pick
conflict. The action uploads the result tree on a temporary branch, then asks
GitHub to create a signed commit with the checked target commit as its parent.
It requires GitHub to report a valid signature before it creates the final
`mokka/cherry-pick/<action_id>` branch. The temporary branch is removed with
an exact lease before the draft pull request is opened. The result records the
target commit used for the signed commit. If the action detects that `main`
moved before final branch creation, it stops after it removes the temporary
branch; review the failure before retrying the dispatch. A failed cleanup
requires manual investigation.
If GitHub reports an error while creating the draft pull request, the action
keeps the final branch for manual investigation because the request may have
succeeded without a response.

If `main` advances after the action creates the final branch, GitHub branch
protection controls whether the draft pull request can merge.

Mokka dispatch does not support dry-run mode. Its action input must be the
exact string `false`. Keep `REPOSITORY_AUTOMATION_MOKKA_ENABLED` unset until
the external caller meets this contract. A missing or malformed reviewed SHA
fails the job before any checkout.

## Security and operations

- Keep top-level workflow permissions empty. Grant permissions per job.
- Keep every external action pinned to a complete commit SHA.
- Keep the automation checkout separate from the target checkout.
- Resolve the default branch through GitHub and check out its exact commit SHA.
- Do not enable credentials, submodules, or LFS in the automation checkout.
- Treat comments and event payload text as hints. The action refetches live
  GitHub state before it writes.
- Review the action job summary for each invocation.
- Keep the scheduled evaluator because it repairs missed or delayed events.

To stop repository writes, set the applicable activation variable to `false`.
If merge evaluation is disabled, also disable native auto-merge on open pull
requests that it previously armed. Do not remove the required merge check until
maintainers select and document a replacement gate.
