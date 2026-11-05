#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

# Prints the version to publish a chart or image under for a GitHub ref. Only
# a stable vMAJOR.MINOR.PATCH tag (no prerelease, no build metadata, no
# leading zeroes) releases, and the tag decides the version, so Chart.yaml
# never has to carry it. main publishes only -dev versions: every merge to main
# republishes, and a release version published from main is overwritten by the
# next merge.

set -eu

if [[ $# -ne 2 ]]; then
    echo "usage: $0 <github-ref> <chart-version>" >&2
    exit 2
fi

publish_ref=$1
chart_version=$2

if [[ "$publish_ref" =~ ^refs/tags/v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    echo "${publish_ref#refs/tags/v}"
    exit 0
fi

if [[ "$publish_ref" == "refs/heads/main" ]]; then
    if [[ "$chart_version" == *-dev ]]; then
        echo "$chart_version"
        exit 0
    fi
    echo "refusing to publish chart version $chart_version from main: only -dev versions publish from main, and release versions publish from a stable vMAJOR.MINOR.PATCH tag" >&2
    exit 1
fi

echo "refusing to publish from $publish_ref: charts publish only from refs/heads/main and stable vMAJOR.MINOR.PATCH release tags" >&2
exit 1
