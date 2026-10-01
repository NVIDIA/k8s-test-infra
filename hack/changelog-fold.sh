#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

# Fold pending changie fragments into CHANGELOG.md as a new version section.
# CHANGELOG.md is the source of truth — this edits it in place and does NOT keep
# a per-version archive.
#
# Usage: hack/changelog-fold.sh [--dry-run] X.Y.Z[-PRERELEASE]
#   hack/changelog-fold.sh --dry-run 0.5.0                  # print the section, write nothing
#   CHANGIE=/path/to/changie hack/changelog-fold.sh 0.5.0   # override the binary
#
# Pass the version unprefixed: CHANGELOG.md headings read "## [0.4.0]" while the
# matching git tag is v0.4.0.
#
# A prerelease (0.5.0-rc1) lists only what is new since the previous candidate,
# and parks its fragments in .changes/0.5.0/ instead of deleting them. The final
# release (0.5.0) renders those parked fragments together with the pending ones
# and replaces every 0.5.0-* section, so the release reads as one section that
# covers everything since the previous release.
#
# The section is rendered by `changie batch --dry-run` (no disk writes) and inserted
# immediately before the newest existing version heading. Fragments are moved or
# removed only after the CHANGELOG.md write succeeds.
set -euo pipefail

DRY_RUN=false
if [ "${1:-}" = "--dry-run" ]; then
  DRY_RUN=true
  shift
fi
VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  echo "usage: $0 [--dry-run] X.Y.Z[-PRERELEASE]" >&2
  exit 1
fi
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "'$VERSION' is not an unprefixed semantic version such as 0.5.0 or 0.5.0-rc1" >&2
  exit 1
fi
FINAL="${VERSION%%-*}"

CHANGIE="${CHANGIE:-tmp/bin/changie}"
if [ ! -x "$CHANGIE" ]; then
  echo "changie not found at '$CHANGIE' — run: make changie" >&2
  exit 1
fi

CHANGELOG="CHANGELOG.md"
UNRELEASED=".changes/unreleased"
PARKED=".changes/$FINAL"

# Pending entries belong in .changes/unreleased/. A hand-written "## [Unreleased]"
# heading is the first "## [" match, so the new version would be inserted above it and
# read as older than entries that are not released at all.
if grep -q '^## \[Unreleased\]' "$CHANGELOG"; then
  echo "$CHANGELOG has an '## [Unreleased]' section — move those entries into $UNRELEASED/ fragments first" >&2
  exit 1
fi
if grep -qF "## [$VERSION]" "$CHANGELOG"; then
  echo "$CHANGELOG already has a '## [$VERSION]' section" >&2
  exit 1
fi

include=()
if [ "$VERSION" = "$FINAL" ] && [ -d "$PARKED" ]; then
  include=(--include "$FINAL")
fi

# The ${a[@]+...} form keeps an empty array from tripping `set -u` on bash 3.2 (macOS).
section="$("$CHANGIE" batch "$VERSION" --dry-run ${include[@]+"${include[@]}"})"
if ! grep -q '^- ' <<<"$section"; then
  echo "no fragments to release for $VERSION" >&2
  exit 1
fi
if $DRY_RUN; then
  printf '%s\n' "$section"
  exit 0
fi

# Drop the candidate sections (heading through the line before the next version
# heading) and their link references, then insert the new section before the
# first remaining version heading, one blank line between versions. Falls back to
# appending after the header block if the changelog has no versions yet.
SECTION="$section" DROP="$([ "$VERSION" = "$FINAL" ] && echo "$FINAL-" || true)" awk '
  BEGIN { sec = ENVIRON["SECTION"]; drop = ENVIRON["DROP"] }
  function candidate(s) { return drop != "" && index(s, "[" drop) == 1 }
  /^## \[/ {
    skip = candidate(substr($0, 4))
    if (!skip && !ins) { print sec "\n"; ins = 1 }
  }
  /^\[[^]]+\]: / { skip = candidate($0) }
  !skip { print }
  END { if (!ins) print "\n" sec }
' "$CHANGELOG" > "$CHANGELOG.tmp"
mv "$CHANGELOG.tmp" "$CHANGELOG"

if [ "$VERSION" = "$FINAL" ]; then
  find "$UNRELEASED" -maxdepth 1 -type f -name '*.yaml' -delete
  rm -rf "$PARKED"
else
  mkdir -p "$PARKED"
  find "$UNRELEASED" -maxdepth 1 -type f -name '*.yaml' -exec mv {} "$PARKED/" \;
fi

echo "Folded $VERSION into $CHANGELOG."
