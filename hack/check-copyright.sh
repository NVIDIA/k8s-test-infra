#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

set -euo pipefail

fix=false
if [[ "${1:-}" == "--fix" ]]; then
  fix=true
elif [[ "${1:-}" != "" ]]; then
  printf 'usage: %s [--fix]\n' "$0" >&2
  exit 2
fi

header='SPDX-License-Identifier|SPDX-FileCopyrightText|Licensed under the Apache License|Copyright'
missing=()

add_header() {
  local file=$1
  local temporary
  temporary=$(mktemp "${file}.copyright.XXXXXX")
  trap 'rm -f "$temporary"' RETURN

  {
    if [[ "$file" == *.sh && "$(sed -n '1p' "$file")" == '#!'* ]]; then
      sed -n '1p' "$file"
      printf '# SPDX-License-Identifier: Apache-2.0\n# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION\n\n'
      sed -n '2,$p' "$file"
    else
      case "$file" in
        *.go) printf '// SPDX-License-Identifier: Apache-2.0\n// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION\n\n' ;;
        *.sh) printf '# SPDX-License-Identifier: Apache-2.0\n# SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION\n\n' ;;
      esac
      cat "$file"
    fi
  } > "$temporary"
  local mode
  mode=$(stat -f '%Lp' "$file" 2>/dev/null || stat -c '%a' "$file")
  chmod "$mode" "$temporary"
  mv "$temporary" "$file"
  trap - RETURN
}

while IFS= read -r file; do
  if sed -n '1,20p' "$file" | grep -qE "$header"; then
    continue
  fi

  if "$fix"; then
    add_header "$file"
    printf 'added SPDX header: %s\n' "$file"
  else
    missing+=("$file")
  fi
done < <(git ls-files -- '*.go' '*.sh')

if ((${#missing[@]} > 0)); then
  printf 'missing copyright header:\n' >&2
  printf '  %s\n' "${missing[@]}" >&2
  printf 'run make copyright-fix to add SPDX headers\n' >&2
  exit 1
fi
