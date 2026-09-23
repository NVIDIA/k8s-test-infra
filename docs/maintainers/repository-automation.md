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
   branch, and repository identity contract, set
   `REPOSITORY_AUTOMATION_MOKKA_ENABLED=true`.

Keep each earlier step active while you validate the next step. Do not enable a
later write path when an earlier validation fails.

## Mokka dispatch contract

Start **Mokka cherry-pick** only from the repository default branch. The job
will not run from another selected workflow ref. It resolves the current
default branch to an exact commit SHA and loads the automation from that SHA.

The dispatch accepts exactly four required string inputs:

- `pull_request_number`: the number of a merged pull request in
  `NVIDIA/k8s-test-infra`.
- `source_sha`: the lowercase, 40-character merge commit SHA for that pull
  request.
- `target_branch`: the exact value `main`.
- `action_id`: a canonical lowercase UUIDv4 that makes the request
  idempotent.

The action also verifies GitHub repository ID `733665780`, validates that the
source pull request belongs to this repository, and rejects a source pull
request that was based on the target branch. The source SHA must be the merged
pull request commit and must have one parent. The action creates or reuses a
`mokka/cherry-pick/<action_id>` branch and opens a draft pull request. It does
not merge the pull request.

Mokka dispatch does not support dry-run mode. Its action input must be the
exact string `false`. Keep `REPOSITORY_AUTOMATION_MOKKA_ENABLED` unset until
the external caller meets this contract.

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
