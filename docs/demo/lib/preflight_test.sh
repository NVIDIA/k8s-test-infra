#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Drives demo::preflight through PATH shims. Every case asserts the exact
# documented exit code, so a guard that stops discriminating goes red.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB="${SCRIPT_DIR}/preflight.sh"
FAILURES=0

# Build a shim directory. Each shim is a real executable on PATH, so this
# exercises the same lookup the demo does rather than stubbing a function.
make_shims() {
    # Abort loudly rather than limping on. A sandbox that denies writes under
    # $TMPDIR makes mktemp fail, and every later case then fails against an
    # empty SHIMDIR, producing a page of red that has nothing to do with the
    # code under test. Fail once, with the reason.
    SHIMDIR="$(mktemp -d 2>/dev/null)" || SHIMDIR=""
    if [ -z "${SHIMDIR}" ] || [ ! -d "${SHIMDIR}" ]; then
        echo "FATAL: mktemp -d failed (TMPDIR=${TMPDIR:-unset}). Cannot build" >&2
        echo "       PATH shims, so no case below would mean anything. This is" >&2
        echo "       usually a sandbox denying writes under TMPDIR." >&2
        exit 1
    fi
    KIND_MARKER="${SHIMDIR}/kind-was-called"
    cat >"${SHIMDIR}/kind" <<EOF
#!/usr/bin/env bash
touch "${KIND_MARKER}"
exit 0
EOF
    chmod +x "${SHIMDIR}/kind"
}

# $1 = version string, $2 = newline-separated releases `helm list --short`
# should report (default: none).
write_helm() {
    cat >"${SHIMDIR}/helm" <<EOF
#!/usr/bin/env bash
[ "\$1" = "version" ] && { echo "$1"; exit 0; }
[ "\$1" = "list" ] && { printf '%s' "${2:-}"; exit 0; }
exit 0
EOF
    chmod +x "${SHIMDIR}/helm"
}

# A helm whose `list` fails outright, to prove the check degrades to a warning
# rather than blocking a legitimate install.
write_helm_list_broken() {
    cat >"${SHIMDIR}/helm" <<EOF
#!/usr/bin/env bash
[ "\$1" = "version" ] && { echo "$1"; exit 0; }
[ "\$1" = "list" ] && exit 1
exit 0
EOF
    chmod +x "${SHIMDIR}/helm"
}

# $1 = current-context output ("" means none), $2 = cluster-info exit code,
# $3 = API server URL (default loopback, i.e. a real local Kind node),
# $4 = the context's namespace ("" means the context pins none).
#
# `config view` has to discriminate on the jsonpath, because the preflight
# asks it for the server AND for the namespace. A shim that answered both with
# one string would make the namespace assertion pass on the server URL.
write_kubectl() {
    local server="${3:-https://127.0.0.1:6443}" ns="${4:-}"
    cat >"${SHIMDIR}/kubectl" <<EOF
#!/usr/bin/env bash
all="\$*"
case "\$1 \$2" in
  "config current-context") [ -n "$1" ] && { echo "$1"; exit 0; } || exit 1 ;;
  "cluster-info "*|"cluster-info") exit $2 ;;
  "get nodes") echo "node1 Ready"; exit 0 ;;
  "config view")
      case "\${all}" in
        *cluster.server*)     echo "${server}" ;;
        *context.namespace*)  echo "${ns}" ;;
      esac
      exit 0 ;;
esac
exit 0
EOF
    chmod +x "${SHIMDIR}/kubectl"
}

write_docker() {
    cat >"${SHIMDIR}/docker" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
    chmod +x "${SHIMDIR}/docker"
}

# stdout and stderr are captured SEPARATELY, then concatenated into "out".
# Keeping them apart matters: the announcement goes to stdout and every
# refusal goes to stderr via demo::die, so a check that reads the combined
# stream can pass on the announcement while the refusal message is empty.
# check_stdout / check_stderr read the isolated streams.
_capture() {
    cat "${SHIMDIR}/stdout" "${SHIMDIR}/err" >"${SHIMDIR}/out"
}

run_preflight() {
    env PATH="${SHIMDIR}:/usr/bin:/bin" "$@" \
        bash -c "source '${LIB}'; demo::preflight" </dev/null \
        >"${SHIMDIR}/stdout" 2>"${SHIMDIR}/err"
    local rc=$?
    _capture
    echo "${rc}"
}

# Runs demo::preflight and then evaluates $1 in the SAME shell, so a case can
# assert on the variables the library exports to its callers.
run_preflight_post() {
    local post="$1"
    shift
    env PATH="${SHIMDIR}:/usr/bin:/bin" "$@" \
        bash -c "source '${LIB}'; demo::preflight; ${post}" </dev/null \
        >"${SHIMDIR}/stdout" 2>"${SHIMDIR}/err"
    local rc=$?
    _capture
    echo "${rc}"
}

# Same, but drives the interactive branch: $1 is fed on stdin and $2 is a
# prelude evaluated after the library is sourced, used to override
# demo::is_tty. Overriding that one function is what makes exit code 4
# reachable without allocating a pty; everything else stays the real code.
run_preflight_prompt() {
    local stdin_text="$1" prelude="$2"
    shift 2
    printf '%s\n' "${stdin_text}" \
        | env PATH="${SHIMDIR}:/usr/bin:/bin" "$@" \
            bash -c "source '${LIB}'; ${prelude}; demo::preflight" \
            >"${SHIMDIR}/stdout" 2>"${SHIMDIR}/err"
    local rc=$?
    _capture
    echo "${rc}"
}

FORCE_TTY='demo::is_tty() { return 0; }'

# demo::image_parts is a pure function, so drive it directly. run_image_parts
# echoes the EXIT CODE (for the digest rejection); image_parts_value echoes
# the "repo|tag" it produced.
run_image_parts() {
    env PATH="${SHIMDIR}:/usr/bin:/bin" \
        bash -c "source '${LIB}'; demo::image_parts \"\$1\"" _ "$1" \
        >"${SHIMDIR}/stdout" 2>"${SHIMDIR}/err"
    local rc=$?
    _capture
    echo "${rc}"
}

image_parts_value() {
    env PATH="${SHIMDIR}:/usr/bin:/bin" \
        bash -c "source '${LIB}'; demo::image_parts \"\$1\"" _ "$1" 2>/dev/null
}

check() {
    local name="$1" expected="$2" actual="$3"
    if [ "${expected}" != "${actual}" ]; then
        echo "FAIL ${name}: expected exit ${expected}, got ${actual}"
        sed 's/^/      /' "${SHIMDIR}/out"
        FAILURES=$((FAILURES + 1))
    else
        echo "ok   ${name}"
    fi
}

check_output() {
    local name="$1" needle="$2"
    if ! grep -q "${needle}" "${SHIMDIR}/out"; then
        echo "FAIL ${name}: output did not mention '${needle}'"
        sed 's/^/      /' "${SHIMDIR}/out"
        FAILURES=$((FAILURES + 1))
    else
        echo "ok   ${name}"
    fi
}

# Stream-scoped variants. check_output reads the combined stream and is kept
# for assertions where the stream genuinely does not matter.
check_stream() {
    local name="$1" stream="$2" needle="$3"
    if ! grep -q "${needle}" "${SHIMDIR}/${stream}"; then
        echo "FAIL ${name}: ${stream} did not contain '${needle}'"
        echo "      --- stdout ---"; sed 's/^/      /' "${SHIMDIR}/stdout"
        echo "      --- stderr ---"; sed 's/^/      /' "${SHIMDIR}/err"
        FAILURES=$((FAILURES + 1))
    else
        echo "ok   ${name}"
    fi
}

check_stdout() { check_stream "$1" stdout "$2"; }
check_stderr() { check_stream "$1" err "$2"; }

check_output_absent() {
    local name="$1" needle="$2"
    if grep -q "${needle}" "${SHIMDIR}/out"; then
        echo "FAIL ${name}: output should not have mentioned '${needle}'"
        sed 's/^/      /' "${SHIMDIR}/out"
        FAILURES=$((FAILURES + 1))
    else
        echo "ok   ${name}"
    fi
}

# Case 0: PATH hygiene, a precondition for case 1. "Missing kubectl exits 3"
# only proves something if kubectl is genuinely unreachable under the test
# PATH. On a host where kubectl is installed in /usr/bin, case 1 would pass
# for the wrong reason, so assert the precondition instead of assuming it.
make_shims
if env PATH="${SHIMDIR}:/usr/bin:/bin" bash -c 'command -v kubectl' >/dev/null 2>&1; then
    echo "FAIL test PATH still resolves kubectl, so case 1 cannot discriminate"
    FAILURES=$((FAILURES + 1))
else
    echo "ok   kubectl is genuinely unreachable under the test PATH"
fi

# Case 1: kubectl missing entirely.
make_shims; write_helm "v3.8.0+g1234"
check "missing kubectl exits 3" 3 "$(run_preflight)"
check_stderr "missing kubectl names the tool" "kubectl not found on PATH"

# Case 2: helm too old for OCI.
make_shims; write_helm "v3.7.2+g1234"; write_kubectl "kind-demo" 0
check "helm 3.7 exits 3" 3 "$(run_preflight)"
check_stderr "helm 3.7 links install docs" "helm.sh/docs/intro/install"

# Case 3: helm 3.8 is accepted (boundary, must NOT be rejected).
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "kind-demo" 0
check "helm 3.8 is accepted" 0 "$(run_preflight)"

# Case 4: helm 4.x is accepted.
make_shims; write_helm "v4.2.4+g3900f43"; write_kubectl "kind-demo" 0
check "helm 4.x is accepted" 0 "$(run_preflight)"

# Case 5: no current context at all.
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "" 0
check "no context exits 2" 2 "$(run_preflight)"
check_stderr "no context points at quickstart" "quickstart"

# Case 6: context exists but the API server is unreachable.
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "kind-demo" 1
check "unreachable cluster exits 2" 2 "$(run_preflight)"

# Case 7: the discriminating guard. A healthy kind context must succeed
# WITHOUT the preflight ever shelling out to kind. If a future edit makes the
# demo create a cluster behind the reader's back, the marker file appears and
# this goes red.
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "kind-demo" 0
rc="$(run_preflight)"
check "healthy kind context exits 0" 0 "${rc}"
if [ -e "${KIND_MARKER}" ]; then
    echo "FAIL preflight must not invoke kind"
    FAILURES=$((FAILURES + 1))
else
    echo "ok   preflight did not invoke kind"
fi

# Case 8: a non-throwaway context with no TTY FAILS CLOSED. This used to
# proceed, which meant `./demo.sh </dev/null`, a wrapper script or a CI job
# installed a privileged DaemonSet into a production context with no
# confirmation whatsoever.
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "prod-cluster" 0
check "non-throwaway + no TTY fails closed with 4" 4 "$(run_preflight)"
# Read stderr ONLY, and match a phrase from the refusal sentence itself. The
# previous form read the combined stream for "prod-cluster", which the
# announcement on stdout already prints, so emptying demo::die left it green.
check_stderr "fail-closed refusal names the context" "refusing to install into 'prod-cluster'"
check_stderr "fail-closed names the deliberate override" "DEMO_ASSUME_YES=true to proceed deliberately"

# Case 9: DEMO_ASSUME_YES is honored.
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "prod-cluster" 0
check "DEMO_ASSUME_YES proceeds" 0 "$(run_preflight DEMO_ASSUME_YES=true)"

###############################################################################
# Cases 10-19 exist because cases 1-9 do not discriminate the branches they
# cover. Two were proven inert by deleting the code they nominally test and
# watching the suite stay green; each is now paired with a mutation that turns
# it red. See the report for both repros.
###############################################################################

# Case 10: the confirmation path is reachable and announces itself.
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "prod-cluster" 0
check "non-throwaway on a TTY prompts, declining exits 4" 4 \
    "$(run_preflight_prompt "not-the-context" "${FORCE_TTY}")"
check_stdout "prompt says it is not a throwaway cluster" "not a throwaway Kind cluster"
check_stderr "declined confirmation says nothing was changed" "aborting without changing anything"

# Case 11: the converse. A loopback kind context must NOT be treated as
# unrecognized, on a TTY or otherwise.
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "kind-demo" 0
check "loopback kind context exits 0" 0 "$(run_preflight)"
check_output_absent "loopback kind context is not challenged" "not a throwaway Kind cluster"

# Case 12: typing the context name back proceeds.
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "prod-cluster" 0
check "typing the context name proceeds" 0 \
    "$(run_preflight_prompt "prod-cluster" "${FORCE_TTY}")"

# Case 13: a loopback kind context on a TTY must NOT prompt. Stdin carries a
# wrong answer, so if the throwaway branch is ever dropped this exits 4.
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "kind-demo" 0
check "loopback kind context on a TTY does not prompt" 0 \
    "$(run_preflight_prompt "not-the-context" "${FORCE_TTY}")"
check_output_absent "loopback kind context asks for no confirmation" "Type the context name"

# Case 14: DEMO_ASSUME_YES, for real this time. Case 9 cannot fail: with
# stdin at /dev/null the fail-closed branch is what it actually exercised,
# so DELETING the whole assume-yes branch left the suite 24/24 green. Forcing
# a TTY and supplying a WRONG answer means only the assume-yes branch can
# produce exit 0 here: without it the answer is compared and rejected as 4.
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "prod-cluster" 0
check "DEMO_ASSUME_YES skips the prompt on a TTY" 0 \
    "$(run_preflight_prompt "wrong-answer-entirely" "${FORCE_TTY}" DEMO_ASSUME_YES=true)"
check_output_absent "DEMO_ASSUME_YES asks nothing" "Type the context name"

# Case 15: a kind-* NAME is not proof of a throwaway cluster.
# `kubectl config rename-context prod kind-prod` must not buy a silent pass,
# so a kind-* name pointing at a REMOTE server takes the confirmation path.
make_shims; write_helm "v3.8.0+g1234"
write_kubectl "kind-prod" 0 "https://k8s.prod.example.com:6443"
check "kind-* name with a remote server is challenged" 4 "$(run_preflight)"
check_stderr "remote kind-* is named in the refusal" "refusing to install into 'kind-prod'"

# Case 16: and the corroboration is the SERVER, not merely a non-loopback
# string. localhost and ::1 are throwaway too.
make_shims; write_helm "v3.8.0+g1234"
write_kubectl "kind-demo" 0 "https://localhost:6443"
check "kind-* on localhost is throwaway" 0 "$(run_preflight)"
make_shims; write_helm "v3.8.0+g1234"
write_kubectl "kind-demo" 0 "https://[::1]:6443"
check "kind-* on ::1 is throwaway" 0 "$(run_preflight)"

# Case 17: the announcement names the namespace the DaemonSet lands in. That
# is the one thing the reader confirms, and it used to be absent entirely.
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "kind-demo" 0
check "announcement exits 0" 0 "$(run_preflight DEMO_NAMESPACE=mokka-failure)"
check_stdout "announcement names the target namespace" "namespace : mokka-failure"

# Case 18: BUILD_LOCAL=true reports missing build tooling as the documented
# exit 3, rather than a raw "command not found" after creating a cluster.
make_shims; write_helm "v3.8.0+g1234"; write_kubectl "kind-demo" 0; write_docker
rm -f "${SHIMDIR}/kind"
check "BUILD_LOCAL without kind exits 3" 3 "$(run_preflight BUILD_LOCAL=true)"
check_stderr "missing kind names the tool" "kind not found on PATH"

# Case 19: a digest-pinned image is rejected. The chart renders
# "{{ repository }}:{{ tag }}" and has no image.digest, so splitting on the
# digest's colon would render an invalid reference that only fails at pull
# time. Exit 5 is the documented code for config the chart cannot express.
make_shims
check "digest ref is rejected with 5" 5 "$(run_image_parts 'ghcr.io/nvidia/nvml-mock@sha256:abc123')"
check_stderr "digest rejection explains why" "cannot express"
check "tag ref splits" "nvml-mock|demo" "$(image_parts_value 'nvml-mock:demo')"
check "registry port is not mistaken for a tag" "registry:5000/nvml-mock|latest" \
    "$(image_parts_value 'registry:5000/nvml-mock')"
check "published default splits" "ghcr.io/nvidia/nvml-mock|latest" \
    "$(image_parts_value 'ghcr.io/nvidia/nvml-mock:latest')"

###############################################################################
# Cases 20-24 close gaps the round-1 suite left open. Each was found by
# deleting the thing it covers and watching 39/39 stay green.
###############################################################################

# Case 20: the announcement must actually name the cluster. This is the whole
# point of the guard: it tells a reader which cluster is about to receive a
# privileged hostPath DaemonSet. Deleting the context from that line was
# invisible to every other check. Server and node count were equally
# uncovered, so assert all three, on stdout, where the announcement lives.
make_shims; write_helm "v3.8.0+g1234"
write_kubectl "kind-demo" 0 "https://127.0.0.1:6443"
check "announcement exits 0" 0 "$(run_preflight)"
check_stdout "announcement names the context" "context   : kind-demo"
check_stdout "announcement names the server" "server    : https://127.0.0.1:6443"
check_stdout "announcement names the node count" "nodes     : 1"

# Case 21: the prior namespace is captured from the context, not assumed to
# be "default", and it reaches both the announcement and the undo hint. A
# reader working in "team-a" must be sent back to "team-a".
make_shims; write_helm "v3.8.0+g1234"
write_kubectl "kind-demo" 0 "https://127.0.0.1:6443" "team-a"
check "prior-namespace run exits 0" 0 \
    "$(run_preflight_post 'echo "UNDO: $(demo::namespace_undo_hint)"')"
check_stdout "announcement shows the context's own namespace" "namespace : team-a"
check_stdout "undo hint restores the prior namespace" "UNDO: kubectl config set-context kind-demo --namespace=team-a"

# Case 22: a context that pins no namespace falls back to "default", so the
# undo hint stays correct rather than empty.
make_shims; write_helm "v3.8.0+g1234"
write_kubectl "kind-demo" 0 "https://127.0.0.1:6443" ""
check "no-prior-namespace run exits 0" 0 \
    "$(run_preflight_post 'echo "UNDO: $(demo::namespace_undo_hint)"')"
check_stdout "unset namespace falls back to default" "UNDO: kubectl config set-context kind-demo --namespace=default"

# Case 23: DEMO_KUBE_CONTEXT is what pins every downstream kubectl and helm
# call, so an empty one would silently un-pin the whole demo.
make_shims; write_helm "v3.8.0+g1234"
write_kubectl "kind-demo" 0 "https://127.0.0.1:6443"
check "context-export run exits 0" 0 \
    "$(run_preflight_post 'echo "EXPORTED:[${DEMO_KUBE_CONTEXT}]"')"
check_stdout "DEMO_KUBE_CONTEXT is exported to callers" "EXPORTED:\[kind-demo\]"

# Case 24: helm is required, not just kubectl. PATH hygiene first, for the
# same reason as case 0: on a host with helm in /usr/bin this would otherwise
# pass for the wrong reason.
make_shims
if env PATH="${SHIMDIR}:/usr/bin:/bin" bash -c 'command -v helm' >/dev/null 2>&1; then
    echo "FAIL test PATH still resolves helm, so the next case cannot discriminate"
    FAILURES=$((FAILURES + 1))
else
    echo "ok   helm is genuinely unreachable under the test PATH"
fi
write_kubectl "kind-demo" 0
check "missing helm exits 3" 3 "$(run_preflight)"
check_stderr "missing helm names the tool" "helm not found on PATH"

###############################################################################
# Cases 25-26 cover two failures that 53 passing checks missed entirely,
# because both only appear against a real API server. They are static
# assertions about the two demo scripts rather than about the library, which
# is the cheapest place to catch either regression.
###############################################################################

STANDALONE="${SCRIPT_DIR}/../standalone/demo.sh"
FAILURE_INJECTION="${SCRIPT_DIR}/../failure-injection/run.sh"

# EVERY script-level guard below reads this, never the raw file. A guard that
# greps the whole file passes when the code it checks is commented out, since
# the comment still carries the literal. That defect class was found twice in
# this suite, so it is now structurally impossible to reintroduce one guard at
# a time: there is a single stripped view and all the guards share it.
code_lines() {
    grep -v '^[[:space:]]*#' "$1"
}

# Both scripts, every time. Checking one and not its sibling is the same
# asymmetry that shipped the original defect, mirrored into the guards.
DEMO_SCRIPTS="${STANDALONE} ${FAILURE_INJECTION}"

for f in ${DEMO_SCRIPTS}; do
    if [ ! -f "${f}" ]; then
        echo "FAIL demo script not found: ${f}"
        FAILURES=$((FAILURES + 1))
    fi
done

# Prove the stripped view actually differs from the raw file, so a future edit
# that makes code_lines a no-op cannot quietly disarm every guard at once.
_raw_n="$(wc -l < "${FAILURE_INJECTION}" | tr -d ' ')"
_code_n="$(code_lines "${FAILURE_INJECTION}" | wc -l | tr -d ' ')"
if [ "${_code_n}" -lt "${_raw_n}" ]; then
    echo "ok   code_lines strips comments (${_raw_n} raw -> ${_code_n} code)"
else
    echo "FAIL code_lines is not stripping anything, so every guard below is blind to commented-out code"
    FAILURES=$((FAILURES + 1))
fi

# Case 25: the two demos must not collide on a shared cluster.
#
# A namespace cannot separate them. The chart creates a ClusterRole and a
# ClusterRoleBinding named after the release (templates/rbac.yaml), and those
# are cluster-scoped, so with both demos on RELEASE_NAME=nvml-mock the second
# install dies with "ClusterRole \"nvml-mock\" in namespace \"\" exists and
# cannot be imported into the current release". Distinct release names are the
# actual mechanism; distinct namespaces are necessary but not sufficient.
FI_RELEASE="$(code_lines "${FAILURE_INJECTION}" | sed -n 's/^RELEASE_NAME="\([^"]*\)".*/\1/p' | head -1)"
SA_RELEASE="$(code_lines "${STANDALONE}"        | sed -n 's/^RELEASE_NAME="\([^"]*\)".*/\1/p' | head -1)"
FI_NS="$(code_lines "${FAILURE_INJECTION}" | sed -n 's/^: "${NAMESPACE:=\([^}]*\)}".*/\1/p' | head -1)"
SA_NS="$(code_lines "${STANDALONE}"        | sed -n 's/^: "${NAMESPACE:=\([^}]*\)}".*/\1/p' | head -1)"

if [ -z "${FI_RELEASE}" ] || [ -z "${SA_RELEASE}" ]; then
    echo "FAIL could not extract release names (FI='${FI_RELEASE}' SA='${SA_RELEASE}')"
    FAILURES=$((FAILURES + 1))
else
    echo "ok   extracted release names (FI='${FI_RELEASE}' SA='${SA_RELEASE}')"
fi
check "demo release names differ (cluster-scoped RBAC would collide)" \
    "differ" "$([ "${FI_RELEASE}" != "${SA_RELEASE}" ] && echo differ || echo same)"
check "demo namespaces differ" \
    "differ" "$([ "${FI_NS}" != "${SA_NS}" ] && echo differ || echo same)"

# Every derived name follows the release name through nvml-mock.fullname,
# which collapses to the release name only when the release name contains the
# chart name. If either stopped containing it, fullname would become
# "<release>-nvml-mock" and CONFIGMAP_NAME / daemonset/<release> would both
# point at objects that do not exist. Checked for BOTH releases: asserting it
# of one and not the other is how P8 slipped through.
for _pair in "standalone:${SA_RELEASE}" "failure-injection:${FI_RELEASE}"; do
    _who="${_pair%%:*}"; _rel="${_pair#*:}"
    case "${_rel}" in
        *nvml-mock*) echo "ok   ${_who} release '${_rel}' keeps fullname collapsed" ;;
        *) echo "FAIL ${_who} release '${_rel}' does not contain the chart name, so nvml-mock.fullname would not collapse to it"
           FAILURES=$((FAILURES + 1)) ;;
    esac
done

for f in ${DEMO_SCRIPTS}; do
    base="$(basename "$(dirname "${f}")")"

    # --- release-name derivation -------------------------------------------
    if code_lines "${f}" | grep -q '^RELEASE_NAME='; then
        echo "ok   ${base} defines RELEASE_NAME"
    else
        echo "FAIL ${base} must define RELEASE_NAME and derive from it"
        FAILURES=$((FAILURES + 1))
    fi

    # No literal release name in a resource reference, in EITHER script.
    # `daemonset/nvml-mock` in run.sh waits on the other demo's DaemonSet.
    if code_lines "${f}" | grep -qE 'daemonset/[a-z]|helm uninstall [a-z]|helm upgrade --install [a-z]'; then
        echo "FAIL ${base} hardcodes a release name in a resource reference"
        code_lines "${f}" | grep -nE 'daemonset/[a-z]|helm uninstall [a-z]|helm upgrade --install [a-z]' | sed 's/^/      /'
        FAILURES=$((FAILURES + 1))
    else
        echo "ok   ${base} hardcodes no release name in resource references"
    fi

    # --- pod selectors ------------------------------------------------------
    if code_lines "${f}" | grep -q 'app.kubernetes.io/instance=${RELEASE_NAME}'; then
        echo "ok   ${base} pins pods by instance, not just chart name"
    else
        echo "FAIL ${base} must select pods by app.kubernetes.io/instance=\${RELEASE_NAME}"
        FAILURES=$((FAILURES + 1))
    fi

    # Any code line naming the chart-name label MUST also carry the instance
    # label. Matching on the presence of both, rather than on a trailing
    # space, is what closes the end-of-line variant.
    if code_lines "${f}" | grep 'app\.kubernetes\.io/name=' | grep -qv 'instance='; then
        echo "FAIL ${base}: has a chart-name selector with no instance pin"
        code_lines "${f}" | grep -n 'app\.kubernetes\.io/name=' | grep -v 'instance=' | sed 's/^/      /'
        FAILURES=$((FAILURES + 1))
    else
        echo "ok   ${base} has no chart-name-only pod selector"
    fi

    # --- install budget -----------------------------------------------------
    # Count the helm calls and require EVERY one to wait on the shared budget.
    # Comparing --wait against --timeout alone passes when both drop by one,
    # which is how a single un-budgeted `helm upgrade --reuse-values` hid.
    n_helm="$(code_lines "${f}" | grep -c '^[[:space:]]*helm upgrade')"
    n_wait="$(code_lines "${f}" | grep -c -- '--wait')"
    n_budget="$(code_lines "${f}" | grep -c -- '--timeout "$(demo::install_timeout)"')"
    if [ "${n_helm}" -lt 1 ]; then
        echo "FAIL ${base}: no helm upgrade call found at all"
        FAILURES=$((FAILURES + 1))
    elif [ "${n_wait}" != "${n_helm}" ] || [ "${n_budget}" != "${n_helm}" ]; then
        echo "FAIL ${base}: ${n_helm} helm call(s) but ${n_wait} --wait and ${n_budget} using demo::install_timeout"
        code_lines "${f}" | grep -nE 'helm upgrade|--wait|--timeout' | sed 's/^/      /'
        FAILURES=$((FAILURES + 1))
    else
        echo "ok   ${base}: all ${n_helm} helm call(s) wait on demo::install_timeout"
    fi

    if code_lines "${f}" | grep -qE -- '--timeout[= ]"?[0-9]+[hms]'; then
        echo "FAIL ${base}: carries a hardcoded --timeout duration instead of the shared budget"
        code_lines "${f}" | grep -nE -- '--timeout[= ]"?[0-9]+[hms]' | sed 's/^/      /'
        FAILURES=$((FAILURES + 1))
    else
        echo "ok   ${base} hardcodes no --timeout duration"
    fi

    if code_lines "${f}" | grep -q -- '--set-string updateStrategy.rollingUpdate.maxUnavailable=100%'; then
        echo "ok   ${base} rolls every node at once on re-runs"
    else
        echo "FAIL ${base} must pass --set-string updateStrategy.rollingUpdate.maxUnavailable=100%"
        FAILURES=$((FAILURES + 1))
    fi

    # --- notices and refusals ----------------------------------------------
    if code_lines "${f}" | grep -q 'demo::announce_pull'; then
        echo "ok   ${base} announces the pull before installing"
    else
        echo "FAIL ${base} must call demo::announce_pull before its first install"
        FAILURES=$((FAILURES + 1))
    fi

    if code_lines "${f}" | grep -q 'demo::require_no_sibling_release'; then
        echo "ok   ${base} refuses to co-locate with the other demo"
    else
        echo "FAIL ${base} must call demo::require_no_sibling_release before installing"
        FAILURES=$((FAILURES + 1))
    fi
done

# Each demo must name the OTHER demo's release, not its own, or the check is a
# no-op that can never fire.
if code_lines "${STANDALONE}" | grep -q 'demo::require_no_sibling_release "nvml-mock-failure"'; then
    echo "ok   standalone watches for the failure-injection release"
else
    echo "FAIL standalone must pass the failure-injection release name as the sibling"
    FAILURES=$((FAILURES + 1))
fi
if code_lines "${FAILURE_INJECTION}" | grep -q 'demo::require_no_sibling_release "nvml-mock"'; then
    echo "ok   failure-injection watches for the standalone release"
else
    echo "FAIL failure-injection must pass the standalone release name as the sibling"
    FAILURES=$((FAILURES + 1))
fi

# Case 30: the co-location refusal. Selector pinning keeps each demo's kubectl
# calls on its own pods, but nothing scopes the hostPaths both DaemonSets
# mount, so a co-located run leaves the shared mock config failure-injected and
# any real GPU workload on those nodes reads it. Both demos exited 0 while that
# happened on a live cluster, which is why this refuses rather than warns.
run_sibling() {
    local sibling="$1" mine="$2"
    shift 2
    env PATH="${SHIMDIR}:/usr/bin:/bin" "$@" \
        bash -c "source '${LIB}'; demo::preflight >/dev/null; demo::require_no_sibling_release '${sibling}' '${mine}'" \
        </dev/null >"${SHIMDIR}/stdout" 2>"${SHIMDIR}/err"
    local rc=$?
    _capture
    echo "${rc}"
}

# The sibling IS installed in the target namespace: refuse with 4.
make_shims; write_kubectl "kind-demo" 0 "https://127.0.0.1:6443"
write_helm "v3.8.0+g1234" "nvml-mock
cert-manager"
check "co-located sibling refuses with 4" 4 \
    "$(run_sibling nvml-mock nvml-mock-failure DEMO_NAMESPACE=mokka)"
check_stderr "refusal names the sibling release" "the 'nvml-mock' demo is already installed"
check_stderr "refusal explains the shared host state" "/var/lib/nvml-mock"
check_stderr "refusal offers a way out" "helm uninstall nvml-mock -n mokka"

# The sibling is NOT there: proceed. Without this the check could be a
# constant refusal and the case above would still pass.
make_shims; write_kubectl "kind-demo" 0 "https://127.0.0.1:6443"
write_helm "v3.8.0+g1234" "cert-manager
ingress-nginx"
check "no sibling present proceeds" 0 \
    "$(run_sibling nvml-mock nvml-mock-failure DEMO_NAMESPACE=mokka-failure)"

# An empty namespace proceeds too.
make_shims; write_kubectl "kind-demo" 0 "https://127.0.0.1:6443"
write_helm "v3.8.0+g1234" ""
check "empty namespace proceeds" 0 \
    "$(run_sibling nvml-mock nvml-mock-failure DEMO_NAMESPACE=mokka-failure)"

# A release whose name merely CONTAINS the sibling's must not trip it: the
# match is exact, so "nvml-mock-failure" does not read as "nvml-mock".
make_shims; write_kubectl "kind-demo" 0 "https://127.0.0.1:6443"
write_helm "v3.8.0+g1234" "nvml-mock-failure"
check "similarly named release does not false-positive" 0 \
    "$(run_sibling nvml-mock nvml-mock-failure DEMO_NAMESPACE=mokka-failure)"

# DEMO_ASSUME_YES overrides, consistent with the rest of the preflight, but
# says what it is overriding.
make_shims; write_kubectl "kind-demo" 0 "https://127.0.0.1:6443"
write_helm "v3.8.0+g1234" "nvml-mock"
check "DEMO_ASSUME_YES overrides the refusal" 0 \
    "$(run_sibling nvml-mock nvml-mock-failure DEMO_NAMESPACE=mokka DEMO_ASSUME_YES=true)"
check_stderr "override still warns about shared host state" "share"

# A helm that cannot list degrades to a warning rather than blocking a
# legitimate install.
make_shims; write_kubectl "kind-demo" 0 "https://127.0.0.1:6443"
write_helm_list_broken "v3.8.0+g1234"
check "unlistable namespace does not block the install" 0 \
    "$(run_sibling nvml-mock nvml-mock-failure DEMO_NAMESPACE=mokka)"
check_stderr "unlistable namespace is reported" "could not list releases"

if [ "${FAILURES}" -ne 0 ]; then
    echo "${FAILURES} failure(s)"
    exit 1
fi
echo "all preflight tests passed"
