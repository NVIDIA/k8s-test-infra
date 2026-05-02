#!/usr/bin/env bash
#
# lint_deploy_schedule.sh — fail if .github/workflows/deploy.yaml introduces
# a `schedule:` trigger without a paired `# ADR-allowed-schedule:` marker
# comment on the line above.
#
# Usage: lint_deploy_schedule.sh [path-to-deploy.yaml]
# Default path: .github/workflows/deploy.yaml
#
# Why: spec docs/plans/2026-04-30-dashboard-duration-options-design.md
# (decision Q7) — the fetcher's stateless-full-pull cost model assumes
# deploys are infrequent. A scheduled trigger silently invalidates that
# assumption. Future re-introduction of a schedule must be an explicit
# design decision, not an incidental edit.

set -euo pipefail

DEPLOY_YAML="${1:-.github/workflows/deploy.yaml}"

if [[ ! -f "$DEPLOY_YAML" ]]; then
  echo "::error::file not found: $DEPLOY_YAML"
  exit 2
fi

# Find any line beginning with `schedule:` (after optional indent) inside the
# `on:` block. We don't try to parse YAML; a grep + adjacent-line check is
# sufficient for the fail-loud guardrail.
schedule_lines=$(grep -n -E '^\s*schedule:\s*$' "$DEPLOY_YAML" || true)

if [[ -z "$schedule_lines" ]]; then
  echo "OK: no schedule: trigger in $DEPLOY_YAML"
  exit 0
fi

# For each match, look at the preceding line. If it contains
# `ADR-allowed-schedule:` the schedule is explicitly approved.
unmarked=0
while IFS=: read -r lineno _; do
  prev=$((lineno - 1))
  if [[ $prev -lt 1 ]]; then
    unmarked=1
    echo "::error file=$DEPLOY_YAML,line=$lineno::schedule: at top of file with no ADR marker"
    continue
  fi
  prev_text=$(sed -n "${prev}p" "$DEPLOY_YAML")
  if [[ "$prev_text" =~ ADR-allowed-schedule: ]]; then
    echo "OK: schedule: at line $lineno has ADR marker on line $prev"
    continue
  fi
  unmarked=1
  echo "::error file=$DEPLOY_YAML,line=$lineno::schedule: trigger requires '# ADR-allowed-schedule: <ref>' comment on the preceding line"
done <<< "$schedule_lines"

if [[ $unmarked -ne 0 ]]; then
  echo
  echo "Spec docs/plans/2026-04-30-dashboard-duration-options-design.md (decision Q7) requires"
  echo "scheduled triggers to be paired with an ADR comment because the fetcher's cost model"
  echo "assumes deploys are on-demand. To add a schedule:, add the marker:"
  echo
  echo "  # ADR-allowed-schedule: docs/plans/<your-adr>.md"
  echo "  schedule:"
  echo "    - cron: ..."
  echo
  exit 1
fi

exit 0
