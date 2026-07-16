# Plan 3 Task 8 report

## Result

- Metadata mode now reads the unique action-owned canonical policy comment and
  preserves exact LGTM and retest provenance only when command state is valid
  and bound to the live pull request head.
- Missing, malformed, duplicated, misplaced, and wrong-head command state is
  reset to null current-head provenance. Reset applies
  `do-not-merge/needs-approval` first, then removes case-insensitive `lgtm` and
  `approved`, while preserving hold and unrelated labels.
- Metadata policy, ownership, size, areas, and reviewers are recomputed on every
  delivery. Existing eligible reviewers are preserved case-insensitively and
  only newly needed non-author reviewers are requested.
- The live PR identity is fenced before writes and again immediately before the
  canonical comment. The comment is written last with policy, command-state,
  and metadata-head markers on lines one through three. A final-fence failure
  carries bounded partial state and never publishes stale evidence.
- Mutation failures stop immediately and reruns converge. Dry-run performs all
  reads and planning with zero writes. Reset comments use only the fixed visible
  explanation `Review state reset: pull request head changed.`

## RED -> GREEN evidence

- Initial RED: 54/58 focused cases passed; four failed for the intended absent
  behavior: the second live-head fence, reset mutation ordering, same-head
  provenance preservation, and malformed-state authority removal.
- Adversarial RED: 65/67 focused cases passed; the added failures proved the
  missing bounded final-fence summary and case-normalized reviewer expectation.
- Final focused GREEN: 69/69 passed, including wrong/missing/stale metadata,
  missing/duplicated/misplaced command state, multiple owned comments, hostile
  user lookalikes, mixed-case labels and reviewers, a newly changed ownership
  area, both head races, convergent mutation failures, and all three dry-run
  state classes.

## Verification

- Full action suite with loopback permission: 726/726 passed.
- ESLint: passed.
- `npm run package`: passed; two builds were byte-identical.
- Generated `dist/index.js`: 893 kB, SHA-256
  `e9aa5b840ae78d4f30e0c8ce0027fdaec22b622cda28f8d9aa6a496205a3b48f`.
- `git diff --check`: passed.
- Source scan found no GraphQL or workflow/ref consumption added by Task 8.
- The configured mutation hook was unavailable. Five targeted source mutants
  were applied one at a time and all five were killed by focused contracts:
  inverted same-head preservation, bypassed complete-comment validation,
  omitted needs-approval, reversed authority-label removal order, and disabled
  the final live-head fence. Every mutant was restored before final gates.
- No workflow file changed, so actionlint is not applicable to Task 8.
