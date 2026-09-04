#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# macOS ships bash 3.2 and every demo script starts with #!/usr/bin/env bash, so
# a bash 4 builtin aborts the demo with "command not found" on a stock Mac.
# nv-sentinel/run.sh shipped `mapfile` and died at run.sh:99, immediately after
# creating the cluster and before the demo showed anything, while its README
# advertised Apple Silicon. This scans every demo script for the constructs that
# bite and names the file, the line and the reason.
#
# The fixture cases are not decoration. Three demo scripts deliberately mention
# `mapfile` in a prose comment warning about this exact trap, so a scanner that
# fired on comments would be permanently red and would be deleted; a scanner
# that tolerates comments by matching nothing at all is equally worthless. Every
# run therefore proves both halves against fixtures: each construct written as
# code is flagged with its own reason, and the same constructs written as
# comments are not flagged.
#
# Written for bash 3.2 itself: no mapfile, no associative arrays, no ${var,,}.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEMO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
SELF="${SCRIPT_DIR}/$(basename "${BASH_SOURCE[0]}")"
FAILURES=0

# pattern%reason, one per line. The separator is '%' rather than '|' because
# every useful pattern below needs ERE alternation; a pattern must therefore
# never contain '%'. The "checks table is well formed" case at the bottom fails
# if a future row breaks that rule, so this cannot rot into silently truncated
# patterns that match everything.
#
# The version in brackets is the bash release that introduced the construct.
CHECKS='(^|[^[:alnum:]_.-])mapfile[[:space:]]%mapfile is a bash 4.0 builtin
(^|[^[:alnum:]_.-])readarray[[:space:]]%readarray is a bash 4.0 builtin
(^|[^[:alnum:]_.-])(declare|local|typeset)[[:space:]]+-[A-Za-z]*A([[:space:]]|$)%associative arrays (declare -A) are bash 4.0
(^|[^[:alnum:]_.-])(declare|local|typeset)[[:space:]]+-[A-Za-z]*n([[:space:]]|$)%namerefs (declare -n) are bash 4.3
(^|[^[:alnum:]_.-])(declare|typeset)[[:space:]]+-[A-Za-z]*g([[:space:]]|$)%declare -g is bash 4.2
\$\{[A-Za-z0-9_]+(\[[^]]*\])?(,,|,|\^\^|\^)%${var,,} and ${var^^} case modification is bash 4.0
\$\{[A-Za-z0-9_]+(\[[^]]*\])?@[A-Za-z]\}%${var@Q} parameter transformation is bash 4.4
(^|[^[:alnum:]_.-])wait[[:space:]]+-n([[:space:]]|$)%wait -n is bash 4.3
&>>%&>> append redirection is bash 4.0
\|&%|& as shorthand for 2>&1 | is bash 4.0
(^|[^[:alnum:]_.-])coproc([[:space:]]|$)%coproc is bash 4.0
(^|[^[:alnum:]_.-])shopt[[:space:]].*globstar%shopt -s globstar is bash 4.0
(^|[^[:alnum:]_.-])shopt[[:space:]].*lastpipe%shopt -s lastpipe is bash 4.2
(^|[^[:alnum:]_.-])read[[:space:]]+-[A-Za-z]*N([[:space:]]|$)%read -N is bash 4.1
(^|[^[:alnum:]_.-])read[[:space:]]+-[A-Za-z]*i([[:space:]]|$)%read -i is bash 4.0
;;&%the ;;& case fall-through is bash 4.0
\[\[[[:space:]]+-v[[:space:]]%[[ -v name ]] is bash 4.2
(^|[^[:alnum:]_.-])(EPOCHSECONDS|EPOCHREALTIME)([^[:alnum:]_]|$)%EPOCHSECONDS and EPOCHREALTIME are bash 5.0
(^|[^[:alnum:]_.-])SRANDOM([^[:alnum:]_]|$)%SRANDOM is bash 5.1'

# Strip comments before matching. A leading-whitespace-or-start-of-line '#'
# begins a comment for our purposes, which is what the four intentional
# "no mapfile" warnings in this tree look like. It over-strips a '#' that sits
# inside a double-quoted string after a space, which can only lose a hit, never
# invent one; the fixture cases below are what keep the code half honest.
strip_comments() {
    sed 's/[[:space:]]#.*$//; s/^#.*$//' "$1"
}

# Scans one directory of *.sh files. Prints "path:line  reason" per hit and
# leaves the count in SCAN_HITS. Every loop is fed by a heredoc rather than a
# pipe so it runs in this shell and SCAN_HITS survives.
SCAN_HITS=0
scan() {
    local dir="$1" skip="${2:-}"
    local file pattern reason hit stripped
    SCAN_HITS=0
    while IFS= read -r file; do
        [ -z "${file}" ] && continue
        if [ -n "${skip}" ] && [ "${file}" = "${skip}" ]; then
            continue
        fi
        stripped="$(strip_comments "${file}")"
        while IFS='%' read -r pattern reason; do
            [ -z "${pattern}" ] && continue
            while IFS= read -r hit; do
                [ -z "${hit}" ] && continue
                printf '%s:%s  %s\n' "${file}" "${hit%%:*}" "${reason}"
                SCAN_HITS=$((SCAN_HITS + 1))
            done <<INNER
$(printf '%s\n' "${stripped}" | grep -nE "${pattern}")
INNER
        done <<TABLE
${CHECKS}
TABLE
    done <<FILES
$(find "${dir}" -type f -name '*.sh' | sort)
FILES
}

fail() {
    echo "FAIL $1"
    FAILURES=$((FAILURES + 1))
}

pass() {
    echo "ok   $1"
}

###############################################################################
# Case 0: the checks table is well formed. A row whose pattern swallowed the
# separator would leave an empty reason and a pattern that matches far more than
# it should, which is exactly the failure mode that makes a guard useless while
# it still exits 0.
###############################################################################
ROWS=0
while IFS='%' read -r pattern reason; do
    ROWS=$((ROWS + 1))
    if [ -z "${pattern}" ] || [ -z "${reason}" ]; then
        fail "checks row ${ROWS} does not split into pattern and reason"
    fi
    case "${reason}" in
        *%*) fail "checks row ${ROWS} has more than one '%' separator" ;;
    esac
done <<TABLE
${CHECKS}
TABLE
if [ "${ROWS}" -lt 10 ]; then
    fail "only ${ROWS} checks defined; the table has been gutted"
else
    pass "checks table is well formed (${ROWS} constructs)"
fi

###############################################################################
# Case 1: the scan actually reaches the demo scripts. "No bash 4 constructs
# found" means nothing if the file list was empty, which a moved directory or a
# broken find would produce silently.
###############################################################################
SCANNED=0
SAW_SENTINEL=0
while IFS= read -r f; do
    [ -z "${f}" ] && continue
    [ "${f}" = "${SELF}" ] && continue
    SCANNED=$((SCANNED + 1))
    case "${f}" in
        */nv-sentinel/run.sh) SAW_SENTINEL=1 ;;
    esac
done <<FILES
$(find "${DEMO_DIR}" -type f -name '*.sh' | sort)
FILES
if [ "${SCANNED}" -lt 5 ]; then
    fail "only ${SCANNED} demo scripts found under ${DEMO_DIR}; the scan is empty"
else
    pass "scan covers ${SCANNED} demo scripts"
fi
if [ "${SAW_SENTINEL}" -ne 1 ]; then
    fail "nv-sentinel/run.sh was not in the scan list"
else
    pass "nv-sentinel/run.sh is in the scan list"
fi

###############################################################################
# Case 2: every construct in the table is detected when written as code. This
# is per-construct: a pattern that stops matching takes its own reason out of
# the output and only that case goes red.
###############################################################################
FIXDIR="$(mktemp -d 2>/dev/null)" || FIXDIR=""
if [ -z "${FIXDIR}" ] || [ ! -d "${FIXDIR}" ]; then
    echo "FATAL: mktemp -d failed (TMPDIR=${TMPDIR:-unset}). Without fixtures the" >&2
    echo "       scan below would be green whether or not it discriminates." >&2
    exit 1
fi
trap 'rm -rf "${FIXDIR}"' EXIT

mkdir -p "${FIXDIR}/code" "${FIXDIR}/comment"
cat >"${FIXDIR}/code/bash4.sh" <<'FIXTURE'
#!/usr/bin/env bash
mapfile -t ARR < <(printf 'a\n')
readarray -t ARR2 < <(printf 'a\n')
declare -A MAP
local -A LMAP
typeset -A TMAP
declare -n REF=ARR
declare -g GLOBAL_THING=1
LOWER="${NAME,,}"
UPPER="${NAME^^}"
QUOTED="${NAME@Q}"
wait -n
runthing &>>/var/log/thing
runthing |& cat
coproc CO { cat; }
shopt -s globstar
shopt -s lastpipe
read -N 4 CHUNK
read -i default -e ANSWER
case x in a) echo a ;;& *) echo b ;; esac
echo "${EPOCHSECONDS}"
echo "${SRANDOM}"
[[ -v NAME ]] && echo yes
FIXTURE

scan "${FIXDIR}/code" >"${FIXDIR}/code.out"
MISSED=0
while IFS='%' read -r pattern reason; do
    [ -z "${reason}" ] && continue
    if ! grep -qF -- "${reason}" "${FIXDIR}/code.out"; then
        fail "code fixture was not flagged for: ${reason}"
        MISSED=$((MISSED + 1))
    fi
done <<TABLE
${CHECKS}
TABLE
if [ "${MISSED}" -eq 0 ]; then
    pass "every construct in the table is flagged when written as code"
fi

###############################################################################
# Case 3: the same constructs written as comments are NOT flagged. Without this
# the cheapest way to make the guard green is to make it match nothing, and the
# three intentional "no mapfile" warnings already in this tree would break it.
###############################################################################
cat >"${FIXDIR}/comment/warned.sh" <<'FIXTURE'
#!/usr/bin/env bash
# Written for bash 3.2: no mapfile -t, no readarray -t, no declare -A MAP,
# no declare -n REF, no declare -g X, no ${NAME,,}, no ${NAME^^}, no ${NAME@Q},
# no wait -n, no coproc CO, no shopt -s globstar, no shopt -s lastpipe,
# no read -N 4 X, no read -i default X, no [[ -v NAME ]], no ${EPOCHSECONDS},
# no ${SRANDOM}, no thing &>>/var/log/thing, no thing |& cat, no ;;& in case.
echo "portable"   # not even mapfile -t ARR in a trailing comment
FIXTURE

scan "${FIXDIR}/comment" >/dev/null
if [ "${SCAN_HITS}" -ne 0 ]; then
    fail "comment fixture produced ${SCAN_HITS} hit(s); the guard fires on prose"
    scan "${FIXDIR}/comment" | sed 's/^/      /'
else
    pass "constructs mentioned only in comments are not flagged"
fi

###############################################################################
# Case 4: the real scan. This is the guard itself.
###############################################################################
# Redirected to a file rather than captured with $(...): command substitution
# forks, and SCAN_HITS set inside a subshell would never reach this comparison.
scan "${DEMO_DIR}" "${SELF}" >"${FIXDIR}/real.out"
if [ "${SCAN_HITS}" -ne 0 ]; then
    echo "FAIL ${SCAN_HITS} bash 4 construct(s) in the demo scripts:"
    sed 's/^/      /' "${FIXDIR}/real.out"
    FAILURES=$((FAILURES + SCAN_HITS))
else
    pass "all demo scripts are bash 3.2 compatible"
fi

if [ "${FAILURES}" -ne 0 ]; then
    echo "${FAILURES} failure(s)"
    exit 1
fi
echo "all portability tests passed"
