#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Shared preflight for the portable nvml-mock demos.
#
# These demos install into the cluster your current context points at, which
# is a deliberate change from the older behaviour of pinning a kind context.
# The confirmation below is what replaces that pin: the chart installs a
# privileged DaemonSet with hostPath mounts, so an accidental run against a
# real cluster is worth one keystroke to prevent.
#
# Exit codes are contract, asserted by preflight_test.sh:
#   2  no usable cluster
#   3  missing or too-old tooling
#   4  refusing to install: confirmation declined, a non-interactive run
#      against a cluster that is not a throwaway one, or the other demo's
#      release already present in the target namespace
#   5  configuration this chart cannot express (a digest-pinned image)
#
# Written for bash 3.2 (stock macOS): no mapfile, no associative arrays,
# no ${var,,}.

DEMO_EXIT_NO_CLUSTER=2
DEMO_EXIT_NO_TOOL=3
DEMO_EXIT_DECLINED=4
DEMO_EXIT_BAD_CONFIG=5

# Set by demo::preflight to the context it announced and confirmed. Callers
# pin every kubectl and helm call to it, so a kubeconfig that changes under
# the demo cannot redirect the install to a cluster the reader never saw.
DEMO_KUBE_CONTEXT=""

# Set by demo::preflight to the namespace that was current for that context
# BEFORE the demo touched anything, or "default" when the context pins none.
# standalone/demo.sh prints it as a truthful undo hint instead of assuming the
# reader was on "default".
DEMO_PRIOR_NAMESPACE=""

# Set by demo::preflight to the cluster's node count. The demos use it to tell
# the reader how long a cold image pull is going to take.
DEMO_NODE_COUNT=""

demo::die() {
    echo "ERROR: $*" >&2
}

demo::require_tool() {
    local tool="$1" url="$2"
    if ! command -v "${tool}" >/dev/null 2>&1; then
        demo::die "${tool} not found on PATH. Install it: ${url}"
        exit "${DEMO_EXIT_NO_TOOL}"
    fi
}

demo::require_helm_version() {
    local raw major minor
    raw="$(helm version --short 2>/dev/null)"
    major="$(printf '%s' "${raw}" | sed -n 's/^v\{0,1\}\([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1/p')"
    minor="$(printf '%s' "${raw}" | sed -n 's/^v\{0,1\}\([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\2/p')"
    if [ -z "${major}" ] || [ -z "${minor}" ]; then
        demo::die "could not parse a version from 'helm version --short' output: '${raw}'"
        exit "${DEMO_EXIT_NO_TOOL}"
    fi
    # 3.8 is the DOCUMENTED project minimum (docs/quickstart.md), not a
    # measured one. Rendering this chart under 3.2.4, 3.5.4, 3.6.3, 3.7.2 and
    # 3.8.2 puts the real floor at 3.6: anything older dies with "parse error
    # at (nvml-mock/templates/_helpers.tpl:156): unclosed action", while 3.6.3
    # and 3.8.2 produce byte-identical output. The check stays at 3.8 so the
    # demos agree with the documented number rather than quietly inventing a
    # second one.
    if [ "${major}" -lt 3 ] || { [ "${major}" -eq 3 ] && [ "${minor}" -lt 8 ]; }; then
        # Do NOT say "the chart is served from an OCI registry" here. That is true
        # only for the published-chart path in docs/quickstart.md and the with-fgo
        # demo; the portable demos install from the local chart directory, and
        # nothing else in this repo asserts a 3.8 floor (both Chart.yaml files
        # constrain only kubeVersion, and CI's setup-helm pins no version).
        demo::die "helm ${raw} is older than 3.8, the minimum documented in docs/quickstart.md: https://helm.sh/docs/intro/install/"
        exit "${DEMO_EXIT_NO_TOOL}"
    fi
}

# Tools the BUILD_LOCAL path needs. Called by the demos BEFORE they create a
# cluster, because demo::preflight runs after that block and a missing docker
# or kind would otherwise surface as a raw "command not found" partway
# through, having already created a cluster.
demo::require_build_tools() {
    demo::require_tool kubectl "https://kubernetes.io/docs/tasks/tools/"
    demo::require_tool docker "https://docs.docker.com/get-docker/"
    demo::require_tool kind "https://kind.sigs.k8s.io/docs/user/quick-start/#installation"
}

# Seam for the interactive check. Overridable so preflight_test.sh can drive
# the confirmation path (and therefore exit code 4) without allocating a pty;
# production callers never touch it.
demo::is_tty() {
    [ -t 0 ]
}

# A "throwaway" cluster is one it is safe to install a privileged DaemonSet
# into without asking. A kind-* NAME alone does not prove that: a context is
# just a label, and `kubectl config rename-context prod kind-prod` would buy
# a silent pass. Require the API server to be loopback as well, which is what
# actually distinguishes a local Kind node from a remote cluster.
#
# $1 = context name, $2 = API server URL
demo::is_throwaway() {
    local ctx="$1" server="$2"
    case "${ctx}" in
        kind-*) ;;
        *) return 1 ;;
    esac
    case "${server}" in
        https://127.0.0.1:* | http://127.0.0.1:* | \
        https://localhost:* | http://localhost:* | \
        "https://[::1]:"* | "http://[::1]:"*) return 0 ;;
        *) return 1 ;;
    esac
}

demo::preflight() {
    demo::require_tool kubectl "https://kubernetes.io/docs/tasks/tools/"
    demo::require_tool helm "https://helm.sh/docs/intro/install/"
    demo::require_helm_version

    # Belt and braces: the demos already call demo::require_build_tools before
    # their cluster block, but re-check here so any other caller of the
    # library gets the documented exit 3 too.
    if [ "${BUILD_LOCAL:-false}" = "true" ]; then
        demo::require_build_tools
    fi

    local ctx server nodes ns answer
    ctx="$(kubectl config current-context 2>/dev/null)" || ctx=""
    if [ -z "${ctx}" ]; then
        demo::die "no current kubectl context. This demo installs into the cluster your KUBECONFIG points at. To create a throwaway one, follow the quickstart: docs/quickstart.md"
        exit "${DEMO_EXIT_NO_CLUSTER}"
    fi

    if ! kubectl cluster-info >/dev/null 2>&1; then
        demo::die "context '${ctx}' is set but its API server is not reachable. Fix your KUBECONFIG, or create a throwaway cluster with the quickstart: docs/quickstart.md"
        exit "${DEMO_EXIT_NO_CLUSTER}"
    fi

    # Callers run under `set -euo pipefail`, where a failing command
    # substitution would abort the demo with kubectl's exit code instead of
    # the documented one. These two lines are cosmetic, so degrade instead.
    server="$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null)" || server="unknown"
    nodes="$(kubectl get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')" || nodes="unknown"

    # The namespace the privileged DaemonSet actually lands in is the one
    # thing a reader most needs to confirm, so announce it rather than only
    # the cluster. DEMO_NAMESPACE is set by each demo before calling us.
    ns="$(kubectl config view --minify -o jsonpath='{.contexts[0].context.namespace}' 2>/dev/null)" || ns=""
    DEMO_PRIOR_NAMESPACE="${ns:-default}"
    # shellcheck disable=SC2034  # read by the demos that source this file
    DEMO_NODE_COUNT="${nodes}"

    printf 'Target for a privileged DaemonSet with hostPath mounts:\n'
    printf '  context   : %s\n  server    : %s\n  nodes     : %s\n  namespace : %s\n' \
        "${ctx}" "${server}" "${nodes}" \
        "${DEMO_NAMESPACE:-${DEMO_PRIOR_NAMESPACE}}"

    # shellcheck disable=SC2034  # read by the demos that source this file
    DEMO_KUBE_CONTEXT="${ctx}"

    if demo::is_throwaway "${ctx}" "${server}"; then
        return 0
    fi

    if [ "${DEMO_ASSUME_YES:-false}" = "true" ]; then
        echo "Not a throwaway Kind cluster; DEMO_ASSUME_YES=true, proceeding without confirmation."
        return 0
    fi

    # Fail closed when nobody can answer. A non-interactive run (a CI job, a
    # wrapper script, `./demo.sh </dev/null`) previously installed into any
    # context with no confirmation at all, which is the accident this guard
    # exists to prevent. Loopback Kind contexts still pass above, so
    # automation against a throwaway cluster keeps working untouched.
    if ! demo::is_tty; then
        demo::die "refusing to install into '${ctx}' non-interactively: it is not a throwaway Kind cluster and stdin is not a TTY, so the confirmation cannot be answered. Re-run on a terminal, or set DEMO_ASSUME_YES=true to proceed deliberately."
        exit "${DEMO_EXIT_DECLINED}"
    fi

    printf '\nThis is not a throwaway Kind cluster. The demo installs a privileged\n'
    printf 'DaemonSet with hostPath mounts into the cluster above.\n'
    printf "Type the context name to continue: "
    # EOF (Ctrl-D) leaves answer empty rather than aborting the caller with
    # read's exit code, so a declined confirmation still reports code 4.
    read -r answer || answer=""
    if [ "${answer}" != "${ctx}" ]; then
        demo::die "confirmation did not match '${ctx}'; aborting without changing anything"
        exit "${DEMO_EXIT_DECLINED}"
    fi
    return 0
}

# How long to let `helm --wait` run.
#
# The old value was 120s, which cannot be met on the cluster this path exists
# to serve. Measured on a cold 4-node cluster, pulling
# ghcr.io/nvidia/nvml-mock:latest (about 100 MB) took 7m58s on one node, and
# helm gave up after two minutes:
#
#   Error: UPGRADE FAILED: resource DaemonSet/mokka/nvml-mock not ready.
#   status: InProgress, message: Updated: 1/4 ... context deadline exceeded
#
# 15m covers that measurement with roughly 2x headroom. On a bandwidth-limited
# link, where concurrent pulls share one pipe rather than each getting their
# own, raise it: HELM_TIMEOUT=30m ./demo.sh
#
# A note on maxUnavailable, because the obvious explanation is wrong. It gates
# UPDATES, not creation: a fresh DaemonSet creates all of its pods at once
# whatever the setting is, so the parallel pulls on a first install come free
# from the DaemonSet controller, not from the override. Measured directly:
# at the chart default of 25% a fresh 4-node DaemonSet still created all four
# pods in the same second, while a rolling update serialised them (08:44:43,
# :46, :50, :52) and only went parallel at 100% (all four at 08:44:56). The
# override is still load-bearing, just for re-runs against a cluster that
# already has the release, and for failure-injection, whose three
# `helm upgrade --reuse-values` scenarios are all rolling updates.
demo::install_timeout() {
    echo "${HELM_TIMEOUT:-15m}"
}

# Tell the reader why the next few minutes are silent. A first run against a
# cluster that has never pulled this image looks identical to a hang.
demo::announce_pull() {
    local image="$1"
    printf 'Installing %s across %s node(s), waiting up to %s.\n' \
        "${image}" "${DEMO_NODE_COUNT:-?}" "$(demo::install_timeout)"
    printf 'A cluster that has not cached this image pulls about 100 MB per\n'
    printf 'node first, which took ~8 minutes per node when measured. All\n'
    printf 'nodes pull at once, so this is minutes of silence, not a hang.\n'
    printf 'Raise it with HELM_TIMEOUT=30m if your link is slow.\n'
}

# Refuse to install alongside the other demo's release in the same namespace.
#
# The selector pinning keeps each demo's KUBECTL calls on its own pods, but it
# cannot keep them off each other's HOST state, and nothing in the chart can.
# Both DaemonSets mount the same non-release-scoped hostPaths
# (/var/lib/nvml-mock, /var/run/cdi, /run/nvidia, and the NFD features dir),
# so co-locating them leaves one demo's config in those paths and the other's
# overwritten. Measured on a live cluster: after a co-located run, the single
# shared /var/lib/nvml-mock/driver/config/config.yaml that the CDI spec at
# /var/run/cdi/nvidia.yaml points MOCK_NVML_CONFIG at read
#
#   failure: {after_calls: 1, mode: fallen_off_bus, code: 79}
#
# The demos' own pods do not notice, because each mounts its own ConfigMap.
# Any REAL GPU workload scheduled on that node does: it silently consumes the
# failure-injected config, which is the entire thing this mock exists to
# provide honestly. Both scripts exited 0 while that happened.
#
# Exit code 4, the same "refusing to install" code the confirmation path uses,
# and overridable the same way with DEMO_ASSUME_YES=true.
#
# $1 = the sibling demo's release name, $2 = this demo's release name
demo::require_no_sibling_release() {
    local sibling="$1" mine="$2" found=""

    # A missing namespace is not an error here: helm lists nothing and we
    # proceed. A helm that cannot list at all degrades to a warning rather
    # than blocking a legitimate install.
    if ! found="$(helm list --kube-context "${DEMO_KUBE_CONTEXT}"             --namespace "${DEMO_NAMESPACE}" --short 2>/dev/null)"; then
        echo "WARNING: could not list releases in '${DEMO_NAMESPACE}' to check for a co-located demo; continuing." >&2
        return 0
    fi

    if ! printf '%s
' "${found}" | grep -qx "${sibling}"; then
        return 0
    fi

    if [ "${DEMO_ASSUME_YES:-false}" = "true" ]; then
        echo "WARNING: '${sibling}' is already installed in namespace '${DEMO_NAMESPACE}'." >&2
        echo "WARNING: DEMO_ASSUME_YES=true, continuing anyway. The two demos share" >&2
        echo "WARNING: per-node host state, so the mock config left on these nodes" >&2
        echo "WARNING: will be whichever demo wrote last." >&2
        return 0
    fi

    demo::die "refusing to install '${mine}' into namespace '${DEMO_NAMESPACE}': the '${sibling}' demo is already installed there.

Both demos mount the same per-node hostPaths (/var/lib/nvml-mock, /var/run/cdi,
/run/nvidia, and the NFD features directory), and those are not scoped by
release or namespace. Running them together leaves the shared mock config in
whichever state the last one wrote, so a real GPU workload on these nodes can
silently read a failure-injected config while both demos report success.

Use a different namespace, a different cluster, or uninstall the other demo:
  helm uninstall ${sibling} -n ${DEMO_NAMESPACE} --kube-context ${DEMO_KUBE_CONTEXT}
Set DEMO_ASSUME_YES=true to override deliberately."
    exit "${DEMO_EXIT_DECLINED}"
}

# The exact command that undoes the one persistent change a demo makes outside
# the cluster: setting the context's default namespace. Lives here, rather than
# being formatted at each call site, so it is a unit the tests can assert on
# directly. It restores the namespace that was current BEFORE the demo ran, so
# a reader working in "team-a" is sent back to "team-a" and not to "default".
demo::namespace_undo_hint() {
    echo "kubectl config set-context ${DEMO_KUBE_CONTEXT} --namespace=${DEMO_PRIOR_NAMESPACE}"
}

# Image to install. Defaults to the published image so the demo works on a
# cluster you cannot side-load into; BUILD_LOCAL=true opts into the older
# build-and-kind-load path, which stays Kind-only by nature.
demo::image_ref() {
    if [ "${BUILD_LOCAL:-false}" = "true" ]; then
        echo "${LOCAL_IMAGE:-nvml-mock:demo}"
    else
        echo "${NVML_MOCK_IMAGE:-ghcr.io/nvidia/nvml-mock:latest}"
    fi
}

# Split an image reference into the chart's separate image.repository and
# image.tag values, echoed as "<repo>|<tag>". NVML_MOCK_IMAGE is documented as
# overridable, so three shapes reach this:
#   nvml-mock:demo          -> repo=nvml-mock         tag=demo
#   registry:5000/nvml-mock -> repo=registry:5000/... tag=latest
#     (that colon is a port, not a tag: it precedes the last slash)
#   repo@sha256:<hex>       -> rejected, see below
#
# The chart renders the image as "{{ .Values.image.repository }}:{{
# .Values.image.tag }}" (templates/daemonset.yaml, nri-daemonset.yaml) and has
# no image.digest value, so a digest ref cannot be expressed through it at
# all: splitting on its colon would yield the repo "...@sha256" and the tag
# "<hex>", rendering an invalid reference that fails at pull time with a
# confusing error. Reject it here with an actionable message instead.
demo::image_parts() {
    local ref="$1" repo tag tail
    case "${ref}" in
        *@sha256:*)
            demo::die "image reference '${ref}' pins a digest, which this chart cannot express: it builds the image as repository:tag and has no image.digest value. Use a tag, or install the chart directly with a digest-aware values file."
            exit "${DEMO_EXIT_BAD_CONFIG}"
            ;;
    esac
    repo="${ref}"
    tag="latest"
    tail="${ref##*:}"
    case "${tail}" in
        "${ref}" | */*) : ;;
        *) repo="${ref%:*}"; tag="${tail}" ;;
    esac
    echo "${repo}|${tag}"
}
