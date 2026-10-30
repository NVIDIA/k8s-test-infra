#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

# Fold pending changie fragments into CHANGELOG.md as a new version section.
# CHANGELOG.md is the source of truth — this edits it in place and does NOT keep
# a per-version archive.
#
# Usage: hack/changelog-fold.sh [--dry-run] X.Y.Z
#   hack/changelog-fold.sh --dry-run 0.5.0                  # print the section, write nothing
#   CHANGIE=/path/to/changie hack/changelog-fold.sh 0.5.0   # override the binary
#
# Pass the version unprefixed: CHANGELOG.md headings read "## [0.4.0]" while the
# matching git tag is v0.4.0.
#
# The section is rendered by `changie batch --dry-run` (no disk writes) and inserted
# immediately before the newest existing version heading. Fragments are removed
# only after the CHANGELOG.md write succeeds.
set -euo pipefail

DRY_RUN=false
if [ "${1:-}" = "--dry-run" ]; then
  DRY_RUN=true
  shift
fi
VERSION="${1:-}"
if [ -z "$VERSION" ]; then
  echo "usage: $0 [--dry-run] X.Y.Z" >&2
  exit 1
fi
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "'$VERSION' is not an unprefixed X.Y.Z version such as 0.5.0" >&2
  exit 1
fi

CHANGIE="${CHANGIE:-tmp/bin/changie}"
if [ ! -x "$CHANGIE" ]; then
  echo "changie not found at '$CHANGIE' — run: make changie" >&2
  exit 1
fi

CHANGELOG="CHANGELOG.md"
UNRELEASED=".changes/unreleased"

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

section="$("$CHANGIE" batch "$VERSION" --dry-run)"
if ! grep -q '^- ' <<<"$section"; then
  echo "no fragments to release for $VERSION" >&2
  exit 1
fi
if $DRY_RUN; then
  printf '%s\n' "$section"
  exit 0
fi

# Insert the new section before the first version heading, one blank line between
# versions. Falls back to appending after the header block if the changelog has no
# versions yet.
SECTION="$section" awk '
  BEGIN { sec = ENVIRON["SECTION"] }
  /^## \[/ && !ins { print sec "\n"; ins = 1 }
  { print }
  END { if (!ins) print "\n" sec }
' "$CHANGELOG" > "$CHANGELOG.tmp"
mv "$CHANGELOG.tmp" "$CHANGELOG"

find "$UNRELEASED" -maxdepth 1 -type f -name '*.yaml' -delete

echo "Folded $VERSION into $CHANGELOG."
