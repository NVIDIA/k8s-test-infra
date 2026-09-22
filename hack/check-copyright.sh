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

missing=()

tracked_sources() {
  while IFS= read -r file; do
    case "$file" in
      *.go|*.sh) printf '%s\n' "$file" ;;
      *) [[ -f "$file" ]] && head -n 1 "$file" | grep -q '^#!' && printf '%s\n' "$file" ;;
    esac
  done < <(git ls-files)
}

has_header() {
  local file=$1
  local header
  header=$(sed -n '1,20p' "$file")
  local has_copyright=false
  local has_license=false

  if grep -Eq '^[[:space:]]*(//|#|\*)[[:space:]]*(SPDX-FileCopyrightText:.*Copyright|Copyright)' <<<"$header"; then
    has_copyright=true
  fi
  if grep -Eq '^[[:space:]]*(//|#|\*)[[:space:]]*SPDX-License-Identifier: Apache-2\.0[[:space:]]*$' <<<"$header" ||
     grep -Eq '^[[:space:]]*(//|#|\*)[[:space:]]*Licensed under the Apache License, Version 2\.0' <<<"$header"; then
    has_license=true
  fi

  [[ "$has_copyright" == true && "$has_license" == true ]]
}

add_header() {
  local file=$1
  local temporary
  temporary=$(mktemp "${file}.copyright.XXXXXX")
  trap 'rm -f "$temporary"' RETURN

  {
    if [[ "$(sed -n '1p' "$file")" == '#!'* ]]; then
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
  if has_header "$file"; then
    continue
  fi

  if "$fix"; then
    add_header "$file"
    printf 'added SPDX header: %s\n' "$file"
  else
    missing+=("$file")
  fi
done < <(tracked_sources)

if ((${#missing[@]} > 0)); then
  printf 'missing copyright header:\n' >&2
  printf '  %s\n' "${missing[@]}" >&2
  printf 'run make copyright-fix to add SPDX headers\n' >&2
  exit 1
fi
