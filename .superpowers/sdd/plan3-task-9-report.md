# Plan 3 Task 9 implementation report

Status: DONE

## Outcome

- Added exact `Commands`, `Review observer`, and `Merge evaluator` adapters.
- Renamed the metadata workflow to exact `PR metadata` and serialized all three
  state writers with the literal non-cancelling `repository-automation-state`
  concurrency group.
- Kept every privileged checkout on the repository default branch with
  credentials and submodules disabled. Remote actions are official and pinned
  to full commit SHAs; the runtime is the local bundled action.
- Kept the observer inert: it has empty permissions and exactly one literal
  colon step, with no checkout, token, expression, artifact, cache, output, or
  repository script.
- Kept automation CI on unprivileged `pull_request`, moved `contents: read` to
  the job, and retained Node 24 clean install, test, lint, packaging, and dist
  parity checks.

## RED -> GREEN evidence

- RED: `npm test -- test/workflow-contract.test.js` failed 5/5 for the intended
  reasons: old metadata identity, three missing workflows, and non-empty CI
  top-level permissions.
- GREEN: the same focused command passed 5/5 after the workflow implementation.
- The contracts parse YAML and pin exact workflow names, paths, triggers,
  source-event identities, permissions, job counts, timeouts, manual input
  types/defaults, checkout trust, concurrency, runtime modes, and official
  action SHAs. They reject command merge behavior, unsafe refs, artifacts,
  caches, outputs, repository code in the observer, and non-default-branch
  privileged checkouts.

## Verification

- Full permitted-loopback suite: 745/745 passed. The first sandboxed full run
  had 744/745 pass and only the known packaged-action localhost bind failed
  with `EPERM`; the authorized rerun passed completely.
- `npm run lint`: passed.
- `npm run package`: passed twice; both builds produced SHA-256
  `1a1ce164c7058e256ca15932e859f0e7c42a721bfc67de0ad20f42dcaa7ab14c`.
- `git diff --exit-code -- .github/actions/repo-automation/dist`: passed; source
  runtime was unchanged and the existing bundle is canonical.
- `actionlint` on all five touched workflows: passed.
- `git diff --check`: passed.
- Security scan found no PR/workflow head refs, artifact/cache actions,
  credential persistence, or enabled submodules. Every remote `uses` entry is
  an `actions/*` action pinned to a 40-character SHA.

## Concurrency caveat

GitHub concurrency is convergent, not a lossless FIFO. With one running and one
pending job per group, a newer pending job may replace an older pending job.
If that replacement affects a pending `Commands` run, the commenter may need
to reissue the command. This task deliberately does not build a separate queue.

No workflow was dispatched and no remote repository setting or other remote
state was changed.

## Independent review fix

The evaluator now grants exact `actions: read` authority so its Task 7 live
`Get workflow run` identity refresh is operational; no Actions write authority
was added. The parsed contract ties that runtime requirement to the evaluator's
exact job permission map. The observer contract also requires its sole job to
have either no permission override or an exactly empty one, preventing a later
job-level token grant.

- RED: focused workflow contracts passed 4/5 and failed only because evaluator
  `actions: read` was absent; the observer isolation contract passed against
  the already-safe absent override.
- GREEN: focused workflow contracts passed 5/5.
- Full permitted-loopback suite passed 745/745; lint, touched-workflow
  actionlint, package, dist parity, diff, and security scans passed.
- The fix made no source-runtime change, and the canonical bundle SHA-256
  remained `1a1ce164c7058e256ca15932e859f0e7c42a721bfc67de0ad20f42dcaa7ab14c`.
- No workflow was dispatched and no remote state was mutated.
