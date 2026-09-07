#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

set -eu

if [[ $# -ne 1 ]]; then
    echo "usage: $0 <github-ref>" >&2
    exit 2
fi

publish_ref=$1

if [[ "$publish_ref" == "refs/heads/main" ]] ||
    [[ "$publish_ref" =~ ^refs/tags/v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    exit 0
fi

echo "Publishing is restricted to refs/heads/main and stable semantic-version release tags (refs/tags/vMAJOR.MINOR.PATCH without leading zeroes)." >&2
exit 1
