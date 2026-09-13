#!/bin/sh
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
echo "Module                  Size  Used by"
for dir in /sys/module/*/; do
    [ -r "$dir"coresize ] || continue
    read -r size < "$dir"coresize
    if [ -r "$dir"refcnt ]; then
        read -r refcnt < "$dir"refcnt
    else
        refcnt=-2
    fi
    used_by=
    for holder in "$dir"holders/*; do
        [ -e "$holder" ] || [ -L "$holder" ] || continue
        used_by="${used_by}${holder##*/},"
    done
    name=${dir%/}
    printf '%-19s %8s  %s' "${name##*/}" "$size" "$refcnt"
    [ -n "$used_by" ] && printf ' %s' "${used_by%,}"
    echo
done
