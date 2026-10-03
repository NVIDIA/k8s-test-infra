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
5. A stable `repository-automation/merge-policy` check, guarded GitHub native
   SQUASH auto-merge enablement, and unsafe auto-merge disarm.
6. Generic backport pull requests for explicitly allowed target branches.
7. Explicit Mokka cherry-pick dispatch for its validated contract.
8. Conflict labels and metadata label repair for all open pull requests,
   including older requests and requests based on another feature branch.
9. Labels-only policy evaluation for a merge controller outside this action.

`/backport <branch>` and `/cherry-pick <branch>` are aliases for the generic
backport command. They create a backport pull request for an allowed
`release-*` branch. They do not start the Mokka dispatch workflow.

The foundation does not install Prow or Tide. GitHub branch protection remains
the final merge authority. The automation publishes its policy check, enables
native SQUASH auto-merge for eligible pull requests, and disables unsafe native
auto-merge requests. It does not call a direct merge endpoint.

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
   check. Only then set `REPOSITORY_AUTOMATION_MERGE_ENABLED=true` to publish
   policy checks, enable native SQUASH auto-merge for eligible pull requests,
   and disarm unsafe native auto-merge requests. This flag applies to all open
   requests selected by events, trusted dispatch completion, and the scheduled
   evaluator; it is not limited to a test PR. For strict SQUASH-only operation,
   repository settings
   must disable merge commits and rebase merges. The evaluator leaves an
   eligible SQUASH request armed and enables an eligible unarmed request. It
   disarms an unsafe method that it observes, but the method can change after
   its final read. If this flag is already enabled, installing this action
   also activates native enablement.
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

To hand the merge to another controller, follow the cutover in
[Labels-only policy evaluation](#labels-only-policy-evaluation).

## Labels for all open pull requests

With `REPOSITORY_AUTOMATION_METADATA_ENABLED=true`, **PR metadata** scans all
open pull requests hourly, at minute 17. GitHub can delay scheduled runs. There is
no creation-date or update-date filter. A push to `main` or a `release-*` branch
also scans open requests based on that exact branch. The scheduled scan covers
every valid base branch in this repository, including stacked pull requests
and PRs created by bots.

The conflict scan adds `needs-rebase` only when GitHub reports `CONFLICTING` for
the current head and base tip. It removes that label only when GitHub reports
`MERGEABLE`. Unknown mergeability, a missing base, inconsistent identity, or a
changed head or base tip defers the request and preserves its labels.
PR branch updates (`synchronize`) also trigger this scan. The event's `before`
and `after` SHAs do not replace the live head, base tip, or mergeability checks.

The metadata scan repairs `kind/*`, `size/*`, `area/*`, and
`do-not-merge/work-in-progress`. It uses the same classifiers as PR metadata.
An invalid title preserves existing kind labels; size, area, and draft labels
can still be repaired. Invalid configuration preserves all existing labels.
The scan refreshes the trusted policy comment even when labels already match.
It validates configuration, title, DCO, and OWNERS before it emits current-head
metadata evidence. Failed validation removes that evidence and records the
diagnostics. It does not request reviewers. It preserves conflict,
approval, hold, and other labels outside its metadata ownership.

The refresh preserves valid command state, including a hold, from the same
trusted bot comment. Duplicate comments, invalid state, or a changed comment
stop the refresh. The API has no atomic compare-and-swap for comments.
Commands and pull request metadata runs for the same pull request share one
concurrency group, so they never rewrite its comment at the same time. Scans
run in their own group, and only the final comment read limits their
read-to-write race with a concurrent command.

Both scans fully read their candidate list before the first mutation and
reject a list above 100 requests instead of silently omitting requests. They
repeat live identity checks before each write. Metadata also checks the base
tip, derived labels, and the exact trusted policy revision. The job summary
records each request as applied, unchanged, deferred, or failed. Each scan also
logs the same bounded JSON with the prefix `Repository automation conflict-labels: `
or `Repository automation metadata-labels: `. These reports can be read through
the job-log API. A later API failure reports earlier writes; it does not claim
that they were rolled back. Review both reports and the current open list before
declaring a sweep complete. Investigate every deferred, failed, or missing request.

An initially correct metadata label set still needs fresh evidence and input
checks. The scan verifies the stored comment and final labels, then repeats its
live input checks. If a successful label update is still absent at the next read, the scan stops and
reports the partial result instead of repeating the same update. The hourly
schedule limits routine API use; the workflow token's actual rate limit is
not assumed. A large first backfill can require another run after an API
failure. Check the per-PR results before retrying.

Approval labels remain part of the guarded merge evaluator. They require its
activation gates and current validated human review, command evidence, or
applicable approver-author authority from trusted OWNERS. A
metadata backfill does not directly grant approval or enable auto-merge.

Completion of a trusted **PR metadata** dispatch triggers the guarded merge
evaluator for the bounded open pull request set. The evaluator reads current
reviews, metadata, and source CI before it sets approval labels and the
merge-policy check. With the merge flag enabled, an eligible request can also
receive native SQUASH auto-merge.

The evaluator accepts only canonical v2 metadata evidence with an explicit
successful validation result. It rejects legacy or ambiguous evidence and
checks the live title and DCO against the checked-out policy. The exact trusted
checkout revision must match the live default branch before and after each
policy evaluation. A revision change blocks the merge policy and disarms native
auto-merge on the same head when possible. The final read of a confirmed
exact-head completed native merge needs no new policy evaluation and performs
no further writes.

A trusted **Review observer** completion normally evaluates its mapped pull
requests. If GitHub returns a valid empty PR mapping, the evaluator reads the
current open PR list, limited to 100 candidates. It checks each candidate's
current review and head before it changes approval labels or the merge-policy
check. Invalid workflow identity or malformed mappings do not start this scan.

### Native SQUASH auto-merge and source CI

The trusted evaluator job enables GitHub native auto-merge with SQUASH. Its
token has `contents: write` and `pull-requests: write` permissions for this
operation. It checks out only the trusted default-branch commit, with checkout
credentials disabled.

The evaluator reads the required source CI from `merge.requiredCI` in
`.github/repo-automation/policy.yml`. Each workflow entry names a workflow file
and, optionally, the changed-path globs that make it required; each check entry
names a check run and the GitHub App id that must publish it. The current list
requires successful current-head runs of **Basic checks** and
**Validate changelog**, plus a successful `DCO` check from the DCO app. It also
requires the action CI, Helm, dependency-integrity, and documentation workflows
when their tracked PR path filters match a changed or renamed path. It uses the
latest run number and current attempt for each required workflow. Runs must
belong to this repository, use the `pull_request` event, and match the current
head and expected workflow path. A populated PR mapping must identify this PR.
An empty mapping can identify the PR through the exact
`@refs/pull/<number>/merge` suffix. An empty mapping with a plain workflow path
can instead match the live source repository, source branch, and head SHA;
its normalized `prNumber` remains `null`. An explicit mapping to another PR
or source ref cannot use this fallback.
Missing or running evidence blocks success. Failed, cancelled, skipped, or
malformed required evidence also blocks success. Incomplete or over-limit API
collections fail closed. The merge-policy check and metadata/review workflows
do not satisfy the source CI gate.

The `files` patterns use a portable subset of glob syntax so that the agent can
evaluate the same list without a minimatch implementation. Configuration
validation rejects anything else, including braces, brackets, `?`, extglob
groups, negation, and `**` inside a segment. Each `/`-separated segment is
either `**` or a run of letters, digits, `.`, `_`, and `-` in which `*` matches
any characters inside that segment, including a leading dot. `**` matches zero
or more whole segments, except as the last segment, where it matches one or
more: `docs/**` matches `docs/a.md` but not `docs`, and `**/OWNERS` matches
`OWNERS`. Matching is case-sensitive and applies to each changed path and to the
previous path of a rename. Source CI can pass only for pull requests into
`main` or a `release-*` branch; any other base stays pending.

For an eligible request, the evaluator first publishes an `action_required`
policy check. It reads authority, metadata, PR identity, and CI again, then
enables native SQUASH auto-merge with `expectedHeadOid` when no request is armed.
It does not retry that mutation. It reads the same evidence again before it
publishes policy success and once more after success. Before success, failed
or revoked evidence keeps the check blocked. After success, the evaluator
attempts to restore a blocking check and disarm the request when it can confirm
the current PR and head identity. A base branch or source identity change for
the same head also triggers this repair. Failed reads or writes can prevent that
repair, and GitHub can already have completed the merge. An existing eligible
SQUASH request is preserved.

These reads and the success check are separate GitHub operations. Evidence can
change between them. The head guard does not pin reviews, labels, CI, or the
base revision. CI evidence is tied to the source head and can be reused across
PRs or base retargets when GitHub omits PR mappings. It does not prove that the
current base tip or a retargeted base branch was tested. With branch protection
`strict=false`, GitHub can merge without an up-to-date base. Required native
reviews and checks remain the final merge controls. A source CI gate in this
action does not make a separately required native CI check redundant.

### Labels-only policy evaluation

The **Merge evaluation** workflow has a second job, `policy-labels`, gated by
`REPOSITORY_AUTOMATION_POLICY_LABELS_ENABLED`. It runs on the same events as the
merge job and reads the same review, command, and approver-author authority from
the trusted default branch. It writes only the `lgtm`, `approved`,
`do-not-merge/hold`, and `do-not-merge/needs-approval` labels. It does not read
source CI, branch protection, or merge state, never publishes the merge-policy
check, and never enables or disables native auto-merge. Its token has
`actions: read`, `contents: read`, `issues: write`, and `pull-requests: write`,
and the job has its own concurrency group.

Before it writes, the job reads the policy comment, the labels, and the pull
request head again. If any of them changed since its evaluation read, it skips
the write and reports `inputs-changed`; the run that made the change triggers the
next evaluation. If the evaluation fails, the job writes nothing and the run
fails. Unlike the merge job, it does not apply a fail-closed label plan, because
that plan would remove an active `do-not-merge/hold`.

Use this job when another controller merges on these labels:

1. Set `REPOSITORY_AUTOMATION_POLICY_LABELS_ENABLED=true` and confirm that a
   **Merge evaluation** run applies labels from its `policy-labels` job.
2. Disable native auto-merge on open pull requests, and replace the required
   `repository-automation/merge-policy` check in branch protection with the
   gate of the new controller.
3. Set `REPOSITORY_AUTOMATION_MERGE_ENABLED=false`.

While both variables are `true`, both jobs write the same labels from separate
concurrency groups. Keep that overlap short.

### Automatic approval for approver authors

A PR author who is a verified human approver in trusted base OWNERS implicitly
approves the changed files within that approver's authority. The evaluator adds
`approved` when those files and any independent approvals cover every changed
file. OWNERS aliases and active nested OWNERS rules apply; `no_parent_owners`
can exclude a root approver. The PR's proposed OWNERS changes cannot grant this
authority. Metadata also accepts those author-owned paths without requesting
a review from the author.

An independent authorized human must still provide `/lgtm` for the current
head. The author cannot give their own PR an LGTM. The evaluator reads author
identity and trusted OWNERS again before success; removed authority revokes
implicit approval. A new head invalidates old LGTM evidence and requires a
fresh coverage check. Holds, required checks, and GitHub's native review
requirements still apply. An `approved` label does not satisfy a required
GitHub approval review or enable auto-merge.

Conversation commands use the same current native approval and review LGTM
evidence as merge evaluation. Approval coverage can combine native reviews,
`/approve` commands, and approver-author authority across separate OWNERS
scopes. A `/hold` command preserves valid approval and LGTM labels. Commands
read trusted OWNERS and validated review evidence again before any write;
changed evidence stops the run. Native reviews remain live evidence and are
not copied into command state.

### Queued commands

GitHub keeps at most one pending run in a concurrency group, so a newer
**Commands** or **PR metadata** run for the same pull request cancels a pending
**Commands** run. Each **Commands** run therefore lists the pull request comments
and first applies the unprocessed command comments that are older than its own
comment, in comment ID order, then its own. A run started by a comment without
commands also does this. Each comment must come from a human account and must not
be edited, and its author's live identity and repository access are checked as
for the comment that started the run. Processed comment IDs are stored in the
policy comment, so a repeated delivery changes nothing.

Comments older than the newest processed command are not applied later, because
that would reorder them after newer commands. Comments newer than the run's own
comment are left to their own run. A caught-up `/lgtm` or `/approve` is recorded
as processed but grants no evidence, because it may have been written before a
push to the head; the reviewer must comment again. The policy comment summary
shows the last processed comment, and the job summary lists every processed
comment ID. A comment whose run was cancelled stays unapplied until the next
**Commands** run for that pull request.

### Dispatched label scan reports

The **PR metadata** workflow also accepts a `workflow_dispatch` request on
`main`, with exactly two string inputs: `request_id` (a canonical lowercase
UUID) and `workflow_commit_sha` (the full lowercase main commit SHA). The
metadata flag must be enabled. The selected workflow commit, run commit,
and input commit must match in `NVIDIA/k8s-test-infra`. The workflow checks
out that exact trusted commit in `control`; it accepts no checkout path or
other caller input.

Dispatch runs `conflict-labels` and `metadata-labels` as two separate action
calls. The metadata scan runs even if the conflict scan fails. After both
calls, the workflow uploads `mokka-label-scan-<request_id>` with the fixed
members `conflict-labels.json` and `metadata-labels.json`, then fails the job
if either scan failed. A failure before a complete candidate list is known
cannot produce a report that claims an empty list.

Each version 1 report contains the request and repository identities, the
workflow commit, mode, dry-run state, complete sorted candidate numbers, and
one result for each candidate. Results use `applied`, `unchanged`, `deferred`,
or `failed`, with a fixed reason and SHA-256 hashes of the input fence and
managed labels. An unprocessed request is `failed` with `not_processed`.
Reports contain no raw titles, node IDs, branch names, or label names. Each
report is limited to 70 KiB and 100 candidates.

Dispatch success requires a fresh label read followed by a final PR fence,
including requests with initially correct labels. An acknowledged update that is still
absent at the next read fails the scan. Unknown mergeability, changed state,
or an invalid policy leaves coverage unresolved and has no output label hash.
The complete reports can thus be checked against a later live observation.
Native PR, push, and scheduled runs keep their existing summary and behavior;
they do not create these report files. Keep the hourly recovery schedule until
the poll-driven dispatch path has passed its live activation checks.

## LGTM in pull request reviews

The merge evaluator accepts an explicit `/lgtm` line in the current body of an
`APPROVED` or `COMMENTED` review. The reviewer must be a verified human, a current
OWNERS reviewer or approver for the changed files, and not the pull request
author. The review must refer to the current pull request head. Human reviews
can grant evidence on a pull request authored by a bot.

Review LGTM is separate from native approval coverage. An approving review
without `/lgtm` does not grant LGTM. A `COMMENTED` review with `/lgtm` does not
grant approval. Other commands in review bodies are not executed. Quoted or
fenced commands do not count, and invalid command syntax does not grant review
LGTM. `/lgtm cancel` is not a supported command.

The latest submitted review from each actor replaces that actor's older review
LGTM. Submission time determines the order; the higher review ID breaks a tie.
A pending review does not replace submitted evidence. The evaluator reads the
current review body, state, author, commit, and submission time again after
label changes, before the success check, and after that check. Removing or
replacing `/lgtm`, dismissing the review, or changing the pull request head
removes that review evidence. Review commands are not stored as historical
authority in the policy comment. Stored
issue-comment LGTM remains separate and must pass its existing live checks.

Bot-authored pull requests can receive metadata labels and human reviewer
requests. Bot reviews are validated and then excluded from human approval
evidence. A bot cannot provide OWNERS, reviewer, approver, or LGTM authority.

Current metadata evidence does not require an earlier conversation command.
A trusted metadata comment can have no command-state record. Unknown,
malformed, duplicate, or wrong-context command-state records remain blocked.

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
Before merge evaluation is disabled, maintainers must disable native auto-merge
on open pull requests. Do not remove the required merge check until maintainers
select and document a replacement gate.
