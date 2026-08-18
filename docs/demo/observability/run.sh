#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Demo: observe mock GPUs through a real Prometheus + Grafana stack, then inject
# GPU faults and watch them land on the dashboard as a step change in the
# recorded time series.
#
# Pipeline exercised:
#   nvml-mock (fake libnvidia-ml) --> real dcgm-exporter :9400
#     --> ServiceMonitor (GPU Operator generated) --> Prometheus --> Grafana
#
# Faults are injected with nvml-mock-ctl rather than a `helm upgrade`, because a
# Helm upgrade recycles the exporter pod and breaks the very time series this
# demo exists to render. The CLI writes a node-local overlay that the already
# running exporter picks up within the engine's TTL, so the series stays
# continuous and shows a visible discontinuity.
#
# This is a green-path demo: every phase is expected to succeed, and the script
# FAILS if a metric it injected never reaches Prometheus. It is re-runnable
# (reuses the cluster unless FORCE_RECREATE=true).
set -euo pipefail

# --- Configuration (override via env) ----------------------------------------
: "${CLUSTER_NAME:=nvml-mock-observability}"
KUBE_CONTEXT="kind-${CLUSTER_NAME}"
# Repository and tag are the overridable knobs, not the full reference: the chart
# takes image.repository and image.tag separately, and splitting a combined
# reference back apart mis-handles both a missing tag and a registry with a port.
: "${IMAGE_REPO:=nvml-mock}"
: "${IMAGE_TAG:=observability-demo}"
IMAGE_NAME="${IMAGE_REPO}:${IMAGE_TAG}"
CHART_PATH="deployments/nvml-mock/helm/nvml-mock"
DEMO_DIR="docs/demo/observability"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

: "${GPU_PROFILE:=h100}"
: "${GPU_COUNT:=8}"
: "${FORCE_RECREATE:=false}"
: "${KIND_NODE_IMAGE:=kindest/node:v1.35.0}"

# Not overridable: dashboards/mokka-gpu.json pins these namespace names in its
# restart panel, and a dashboard is static JSON, so an override here would still
# install cleanly and import cleanly while silently dropping that namespace's
# series. The two must move together.
MOKKA_NAMESPACE="mokka"
GPU_OPERATOR_NAMESPACE="gpu-operator"
: "${GPU_OPERATOR_VERSION:=v26.3.3}"
: "${MONITORING_NAMESPACE:=monitoring}"
# kube-prometheus-stack only selects ServiceMonitors labelled release=<this> and
# ignores others silently, so the dcgm-exporter ServiceMonitor's release label is
# --set from this variable at install time rather than hardcoded in
# gpu-operator-values.yaml, where it could drift unnoticed.
: "${KPS_RELEASE:=monitoring}"
: "${KPS_VERSION:=88.3.0}"
: "${GRAFANA_PASSWORD:=mokka}"

# Fault-injection targets.
: "${TARGET_GPU:=0}"
: "${HOT_TEMP_C:=90}"
: "${XID_CODE:=79}"
# DCGM latches the last Xid it observed per device and never retracts it, so a
# re-run that injected XID_CODE again would find the series already carrying it
# and would assert nothing. Phase 2 therefore alternates between two codes: the
# one that is NOT currently reported is injected, so the value the assertion
# waits for can only be there because this run delivered it. 48 is the
# double-bit-ECC Xid, which stays plausible for the ecc_uncorrectable mode being
# injected.
: "${XID_CODE_ALT:=48}"
# Budget for every "did the injected metric reach Prometheus?" poll. It has to
# cover the mock's override TTL plus dcgm-exporter's collect interval plus
# Prometheus' scrape interval; the propagation observed in practice is ~25s, so
# 36 x 5s leaves generous headroom before the demo calls the pipeline broken.
: "${FAULT_POLL_ATTEMPTS:=36}"
: "${FAULT_POLL_INTERVAL_S:=5}"

# Node label pinning the mock + GPU operands to the GPU workers.
GPU_NODE_LABEL="nvml-mock-gpu=true"
# Prometheus Service created by kube-prometheus-stack.
PROM_SVC="${KPS_RELEASE}-kube-prometheus-prometheus"
# Label selector for the GPU Operator's dcgm-exporter pods.
DCGM_SELECTOR="app=nvidia-dcgm-exporter"

info() { echo "==> $*"; }
warn() { echo "WARN: $*" >&2; }
fail() { echo "ERROR: $*" >&2; exit 1; }
kubectl_ctx() { command kubectl --context "${KUBE_CONTEXT}" "$@"; }
observe() { echo "--- \$ $* ---"; "$@" 2>&1 || warn "(non-fatal) command failed: $*"; }

# --- Preflight ----------------------------------------------------------------
for bin in docker kind kubectl helm jq; do
  command -v "${bin}" >/dev/null 2>&1 || fail "${bin} is required"
done
# Phase 2 proves a fresh Xid delivery by injecting whichever of the two codes is
# not already reported, so identical values would make it vacuous again.
[[ "${XID_CODE}" != "${XID_CODE_ALT}" ]] \
  || fail "XID_CODE and XID_CODE_ALT are both ${XID_CODE}; phase 2 alternates between them and needs two distinct codes"

# --- Kind cluster -------------------------------------------------------------
if kind get clusters 2>/dev/null | grep -qxF "${CLUSTER_NAME}"; then
  if [[ "${FORCE_RECREATE}" == "true" ]]; then
    info "Deleting existing Kind cluster '${CLUSTER_NAME}'"
    kind delete cluster --name "${CLUSTER_NAME}"
  else
    info "Reusing existing Kind cluster '${CLUSTER_NAME}' (set FORCE_RECREATE=true to recreate)"
    # The kubeconfig entry may have been pruned since the cluster was created
    # (common when juggling several kind clusters), which would break every
    # later kubectl call. Only re-export when it is actually missing: exporting
    # also repoints the caller's current-context, which is a side effect a
    # healthy reuse has no business causing.
    if ! kubectl config get-contexts "${KUBE_CONTEXT}" >/dev/null 2>&1; then
      kind export kubeconfig --name "${CLUSTER_NAME}"
    fi
  fi
fi
if ! kind get clusters 2>/dev/null | grep -qxF "${CLUSTER_NAME}"; then
  info "Creating Kind cluster '${CLUSTER_NAME}' (1 control-plane + 2 workers, CDI enabled)"
  kind create cluster --name "${CLUSTER_NAME}" \
    --image "${KIND_NODE_IMAGE}" \
    --config="${REPO_ROOT}/${DEMO_DIR}/kind.yaml"
fi

mapfile -t WORKERS < <(kind get nodes --name "${CLUSTER_NAME}" | grep -v control-plane | sort)
[[ "${#WORKERS[@]}" -ge 1 ]] || fail "no worker nodes found"
info "GPU workers: ${WORKERS[*]}"

# --- Label GPU workers + install nvidia-container-toolkit / CDI ---------------
for node in "${WORKERS[@]}"; do
  info "Labeling ${node} with ${GPU_NODE_LABEL}"
  kubectl_ctx label node "${node}" "${GPU_NODE_LABEL}" --overwrite

  # Re-provisioning ends in `systemctl restart containerd`, which cycles the
  # node through NotReady and tears a hole in the GPU metrics series this demo
  # exists to render -- so an already provisioned node is left alone. It also
  # keeps re-runs working without network access.
  #
  # The CDI rewrite below is the last provisioning step, so its content is the
  # only trustworthy completion marker: the toolkit package ships a default,
  # non-CDI config.toml of its own, and probing for mere file existence would
  # silently skip a node whose first run died between install and rewrite --
  # a misconfiguration that only surfaces much later as "no GPUs in
  # dcgm-exporter".
  if docker exec "${node}" grep -q 'mode = "cdi"' /etc/nvidia-container-runtime/config.toml 2>/dev/null; then
    info "Skipping nvidia-container-toolkit install on ${node} (already provisioned)"
    continue
  fi

  info "Installing nvidia-container-toolkit into ${node}"
  docker exec "${node}" bash -c '
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl gpg
# --no-tty/--batch so this works when stdin is not a terminal (e.g. the script
# is run detached / in CI); otherwise gpg tries to open /dev/tty and fails.
curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
  | gpg --no-tty --batch --yes --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list \
  | sed "s#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g" \
  | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
apt-get update -qq
apt-get install -y -qq nvidia-container-toolkit
'
  info "Configuring nvidia-container-runtime (CDI mode) on ${node}"
  docker exec "${node}" nvidia-ctk runtime configure --runtime=containerd --cdi.enabled --set-as-default
  docker exec "${node}" bash -c '
set -e
cat > /etc/nvidia-container-runtime/config.toml <<EOF
[nvidia-container-runtime]
mode = "cdi"

[nvidia-container-runtime.modes.cdi]
default-kind = "nvidia.com/gpu"
spec-dirs = ["/var/run/cdi", "/etc/cdi"]
EOF
systemctl restart containerd
'
done
info "Waiting for all nodes to be Ready"
kubectl_ctx wait --for=condition=Ready nodes --all --timeout=180s

# --- Build + load the nvml-mock image -----------------------------------------
info "Building nvml-mock image: ${IMAGE_NAME}"
docker build -t "${IMAGE_NAME}" -f "${REPO_ROOT}/deployments/nvml-mock/Dockerfile" "${REPO_ROOT}"
info "Loading image into Kind"
kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"

# --- Install nvml-mock (pinned to the GPU workers) ----------------------------
# dynamicMetrics is what makes the dashboard interesting: without it temperature,
# power and utilization are static profile constants and every panel is a flat
# line.
#
# The nodeSelector reuses GPU_NODE_LABEL verbatim so selection cannot drift from
# the label actually applied to the workers above; it must stay --set-string
# because nodeSelector is map[string]string and plain --set would render `true`
# as a YAML boolean, which the API server rejects on unmarshal. This relies on
# the label key containing no dots, which Helm would read as a path separator.
info "Installing nvml-mock (profile=${GPU_PROFILE}, count=${GPU_COUNT}) on the GPU workers"
helm upgrade --install nvml-mock "${REPO_ROOT}/${CHART_PATH}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${MOKKA_NAMESPACE}" --create-namespace \
  --set "image.repository=${IMAGE_REPO}" \
  --set "image.tag=${IMAGE_TAG}" \
  --set "gpu.profile=${GPU_PROFILE}" \
  --set "gpu.count=${GPU_COUNT}" \
  --set gpu.dynamicMetrics.enabled=true \
  --set-string "nodeSelector.${GPU_NODE_LABEL}" \
  --wait --timeout 180s

# --- Install kube-prometheus-stack ---------------------------------------------
# Installed BEFORE the GPU Operator so the ServiceMonitor CRD exists by the time
# the operator tries to create one for dcgm-exporter.
#
# adminPassword must be --set-string: plain --set type-coerces, so an all-digit
# password reaches the chart's b64enc as an int64 and one containing a comma is
# read as a second key path. Either way Grafana ends up with a password the
# dashboard import check below cannot authenticate with.
info "Adding prometheus-community Helm repo + installing kube-prometheus-stack ${KPS_VERSION}"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
helm repo update prometheus-community >/dev/null 2>&1 || helm repo update >/dev/null 2>&1
helm upgrade --install "${KPS_RELEASE}" prometheus-community/kube-prometheus-stack \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${MONITORING_NAMESPACE}" --create-namespace \
  --version "${KPS_VERSION}" \
  -f "${REPO_ROOT}/${DEMO_DIR}/kube-prometheus-stack-values.yaml" \
  --set-string "grafana.adminPassword=${GRAFANA_PASSWORD}" \
  --wait --timeout 10m

# Query Prometheus through the API-server service proxy rather than the host
# port: the same approach tests/e2e/go/assertions/dcgm.go uses, and it keeps the
# assertions working even if the host port mapping is unavailable.
promq() {
  kubectl_ctx get --raw \
    "/api/v1/namespaces/${MONITORING_NAMESPACE}/services/${PROM_SVC}:9090/proxy/api/v1/$1"
}

info "Waiting for the Prometheus API to answer"
prom_ready=false
for _ in $(seq 1 60); do
  if promq "query?query=up" >/dev/null 2>&1; then prom_ready=true; break; fi
  sleep 5
done
[[ "${prom_ready}" == "true" ]] || fail "Prometheus API never became reachable"
info "Prometheus is serving queries"

# --- Install the NVIDIA GPU Operator (dcgm-exporter + ServiceMonitor) ---------
# The ServiceMonitor's release label is passed here instead of being written into
# gpu-operator-values.yaml so it is derived from KPS_RELEASE and cannot drift
# from the kube-prometheus-stack release name -- a mismatch makes Prometheus
# ignore the monitor with no error anywhere.
info "Adding NVIDIA Helm repo + installing GPU Operator ${GPU_OPERATOR_VERSION}"
helm repo add nvidia https://helm.ngc.nvidia.com/nvidia >/dev/null 2>&1 || true
helm repo update nvidia >/dev/null 2>&1 || helm repo update >/dev/null 2>&1
helm upgrade --install gpu-operator nvidia/gpu-operator \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${GPU_OPERATOR_NAMESPACE}" --create-namespace \
  --version "${GPU_OPERATOR_VERSION}" \
  -f "${REPO_ROOT}/${DEMO_DIR}/gpu-operator-values.yaml" \
  --set "dcgmExporter.serviceMonitor.additionalLabels.release=${KPS_RELEASE}" \
  --wait --timeout 10m

info "Waiting for a GPU worker to advertise nvidia.com/gpu"
for _ in $(seq 1 60); do
  alloc=$(kubectl_ctx get node "${WORKERS[0]}" -o 'jsonpath={.status.allocatable.nvidia\.com/gpu}' 2>/dev/null || true)
  [[ -n "${alloc}" && "${alloc}" != "0" ]] && { info "${WORKERS[0]} advertises nvidia.com/gpu=${alloc}"; break; }
  sleep 5
done

info "Waiting for dcgm-exporter to be rolled out"
kubectl_ctx -n "${GPU_OPERATOR_NAMESPACE}" rollout status ds/nvidia-dcgm-exporter --timeout=300s

# The single most fragile link in the demo: if the ServiceMonitor's release label
# does not match the kube-prometheus-stack release, Prometheus ignores it with no
# error at all. Assert the target is actually being scraped.
info "Waiting for Prometheus to scrape the dcgm-exporter target"
target_up=false
for _ in $(seq 1 60); do
  if promq "targets?state=active" \
      | jq -e '.data.activeTargets[] | select(.labels.job | test("dcgm")) | select(.health == "up")' \
      >/dev/null 2>&1; then
    target_up=true; break
  fi
  sleep 5
done
if [[ "${target_up}" != "true" ]]; then
  observe kubectl_ctx -n "${GPU_OPERATOR_NAMESPACE}" get servicemonitor -o yaml
  fail "Prometheus never scraped dcgm-exporter. Check that the ServiceMonitor carries release=${KPS_RELEASE}"
fi
info "dcgm-exporter target is UP in Prometheus"

info "Confirming the DCGM series are present"
for series in DCGM_FI_DEV_GPU_TEMP DCGM_FI_DEV_POWER_USAGE DCGM_FI_DEV_GPU_UTIL; do
  count=$(promq "query?query=${series}" | jq '.data.result | length')
  [[ "${count}" -gt 0 ]] || fail "${series} returned no series"
  info "  ${series}: ${count} series"
done

# --- Provision the in-tree Grafana dashboard -----------------------------------
# The Grafana sidecar imports any ConfigMap labelled grafana_dashboard=1 (the
# selector pinned in kube-prometheus-stack-values.yaml), which keeps the
# dashboard a reviewable file in git rather than click-together UI state.
#
# `create --dry-run=client | apply` rather than a plain `create`: the latter
# fails with AlreadyExists on the second run, and this script must converge.
info "Provisioning the Mokka GPU dashboard"
kubectl_ctx -n "${MONITORING_NAMESPACE}" create configmap mokka-gpu-dashboard \
  --from-file=mokka-gpu.json="${REPO_ROOT}/${DEMO_DIR}/dashboards/mokka-gpu.json" \
  --dry-run=client -o yaml | kubectl_ctx apply -f -
kubectl_ctx -n "${MONITORING_NAMESPACE}" label configmap mokka-gpu-dashboard \
  grafana_dashboard=1 --overwrite

# A ConfigMap the sidecar rejects (bad JSON, wrong label) is indistinguishable
# from a healthy one at the API level, so assert against Grafana's own search
# API -- the only source that proves the dashboard was actually imported.
#
# The query runs from inside the Grafana container rather than through the
# API-server service proxy the way promq does, because /api/search needs Basic
# auth and `kubectl get --raw` cannot carry credentials: Grafana's 401 comes
# back disguised as kubectl's own "You must be logged in to the server", so the
# check would never pass no matter how long it waited.
info "Waiting for Grafana to import the dashboard"
dash_ok=false
for _ in $(seq 1 36); do
  if kubectl_ctx -n "${MONITORING_NAMESPACE}" exec "deploy/${KPS_RELEASE}-grafana" -c grafana -- \
      curl -sf -u "admin:${GRAFANA_PASSWORD}" "http://localhost:3000/api/search?query=Mokka" \
      2>/dev/null | jq -e '.[] | select(.uid == "mokka-gpu")' >/dev/null 2>&1; then
    dash_ok=true; break
  fi
  sleep 5
done
if [[ "${dash_ok}" != "true" ]]; then
  observe kubectl_ctx -n "${MONITORING_NAMESPACE}" logs "deploy/${KPS_RELEASE}-grafana" \
    -c grafana-sc-dashboard --tail=20
  # The poll discards stderr on every attempt, which hides the failure modes
  # that have nothing to do with the sidecar or the JSON: no curl in the Grafana
  # image, or a GRAFANA_PASSWORD Grafana disagrees with. Replay the query once
  # without -f and with -S, so the 401 body and any curl error reach the screen
  # instead of only the guesses below.
  observe kubectl_ctx -n "${MONITORING_NAMESPACE}" exec "deploy/${KPS_RELEASE}-grafana" -c grafana -- \
    curl -sS -i -u "admin:${GRAFANA_PASSWORD}" "http://localhost:3000/api/search?query=Mokka"
  fail "Grafana never imported the dashboard. Read the exec output above first; if it returned the dashboard list cleanly, check the sidecar sees grafana_dashboard=1 and that the JSON parses"
fi
info "Grafana imported the Mokka GPU dashboard"

# --- Fault injection ----------------------------------------------------------
# Everything above exists so that this section can inject a GPU fault and prove
# it lands in Prometheus. Faults go in through nvml-mock-ctl, which writes a
# node-local overrides.yaml the already-running exporter re-reads on the mock
# engine's TTL. No pod is restarted, so the time series stays continuous and the
# dashboard renders a step change instead of a gap.
TARGET_NODE="${WORKERS[0]}"
MOCK_POD=$(kubectl_ctx -n "${MOKKA_NAMESPACE}" get pod -l app.kubernetes.io/name=nvml-mock \
  --field-selector "spec.nodeName=${TARGET_NODE}" -o jsonpath='{.items[0].metadata.name}')
[[ -n "${MOCK_POD}" ]] || fail "no nvml-mock pod found on ${TARGET_NODE}"

mock_ctl() { kubectl_ctx -n "${MOKKA_NAMESPACE}" exec "${MOCK_POD}" -- nvml-mock-ctl "$@"; }

# Witness churn by pod identity and restart count. DaemonSet AGE cannot do it:
# it advances with the wall clock whether or not the DaemonSet ever rolled, so a
# recycled pod is invisible to it.
#
# A selector that matches nothing yields the empty string, which would make the
# before/after comparison below "" != "" -- an assertion that passes however
# badly the pods churned. Refuse to produce that, naming the selector so the
# reader fixes it instead of hunting for phantom churn.
pods_fingerprint() {
  local out
  out=$(kubectl_ctx -n "$1" get pods -l "$2" \
    -o jsonpath='{range .items[*]}{.metadata.name} started={.status.startTime} restarts={.status.containerStatuses[0].restartCount}{"\n"}{end}' \
    | sort)
  [[ -n "${out}" ]] \
    || fail "no pods matched selector '$2' in namespace '$1', so the pod-churn check would compare two empty fingerprints and pass unconditionally; fix the selector"
  printf '%s\n' "${out}"
}
mock_pods_before=$(pods_fingerprint "${MOKKA_NAMESPACE}" "app.kubernetes.io/name=nvml-mock")
dcgm_pods_before=$(pods_fingerprint "${GPU_OPERATOR_NAMESPACE}" "${DCGM_SELECTOR}")

# Read one DCGM series for the target GPU on the target node. The PromQL stays
# label-free so it needs no URL encoding; jq does the matching instead. An
# absent series yields the literal "none" so callers can tell "Prometheus has no
# such sample" apart from a real reading -- the distinction the Xid phase below
# depends on.
#
# The optional label/value pair narrows the match further. gpu+Hostname alone
# does not always identify one series: DCGM_FI_DEV_XID_ERRORS carries err_code,
# and Prometheus keeps serving a superseded code's last sample until it goes
# stale, so two series can describe the same GPU at once during phase 2's code
# rotation. Returning whichever one Prometheus happened to list first would make
# that assertion depend on result order, and an instant query stamps every result
# with the evaluation time rather than the sample time, so recency cannot break
# the tie here either. Callers that can be ambiguous say which series they mean;
# for anyone who does not, ambiguity is a hard error rather than a coin flip.
prom_gpu_value() {
  local series="${1:?prom_gpu_value needs a series name}" label="${2:-}" label_value="${3:-}" sample
  sample=$(promq "query?query=${series}" \
    | jq -r --arg gpu "${TARGET_GPU}" --arg node "${TARGET_NODE}" \
        --arg label "${label}" --arg want "${label_value}" \
        '[.data.result[]
           | select(.metric.gpu == $gpu and .metric.Hostname == $node)
           | select($label == "" or .metric[$label] == $want)
           | .value[1]]
         | if length == 0 then "none"
           elif length == 1 then .[0]
           else "ambiguous:" + join(",") end')
  [[ "${sample}" != ambiguous:* ]] \
    || fail "${series}{gpu=\"${TARGET_GPU}\",Hostname=\"${TARGET_NODE}\"} matched several series at once (values: ${sample#ambiguous:}); the read would not be deterministic, so the caller must name the label that distinguishes them"
  printf '%s\n' "${sample}"
}

# Compare two readings numerically. Dispatching on the operator explicitly (and
# rejecting anything else) keeps a future ">=" typo from quietly degrading into
# the near-vacuous "!=" a catch-all branch would give it.
value_matches() {
  local v="$1" want="$2" op="$3"
  case "${op}" in
  '==') awk -v v="${v}" -v w="${want}" 'BEGIN { exit !(v == w) }' ;;
  '!=') awk -v v="${v}" -v w="${want}" 'BEGIN { exit !(v != w) }' ;;
  *) fail "unsupported comparison operator '${op}'; only == and != are implemented" ;;
  esac
}

# Poll until the target GPU's series satisfies `op` ("==" or "!=") against
# `want`, leaving the observed reading in FAULT_OBSERVED. The optional 4th/5th
# arguments are the label/value pair prom_gpu_value uses to disambiguate.
#
# Injection is instant but Prometheus scrapes on an interval, so a query fired
# straight after an injection legitimately still serves the pre-injection
# sample; hence a poll rather than a fixed sleep. An absent series satisfies
# neither test, so a pipeline that stops delivering the metric runs out of
# attempts instead of passing by default, and expiry is fatal: "the metric never
# arrived" is precisely the outcome this demo exists to catch.
FAULT_OBSERVED=""
await_gpu_value() {
  local series="$1" op="$2" want="$3" label="${4:-}" label_value="${5:-}" cur=""
  local selector="gpu=\"${TARGET_GPU}\",Hostname=\"${TARGET_NODE}\""
  [[ -z "${label}" ]] || selector+=",${label}=\"${label_value}\""
  for _ in $(seq 1 "${FAULT_POLL_ATTEMPTS}"); do
    cur=$(prom_gpu_value "${series}" "${label}" "${label_value}")
    if [[ "${cur}" != "none" ]] && value_matches "${cur}" "${want}" "${op}"; then
      FAULT_OBSERVED="${cur}"
      return 0
    fi
    sleep "${FAULT_POLL_INTERVAL_S}"
  done
  fail "${series}{${selector}} never became ${op} ${want} within ~$((FAULT_POLL_ATTEMPTS * FAULT_POLL_INTERVAL_S))s (last read: ${cur})"
}

# ==============================================================================
# PHASE 1 — HEAT a GPU and assert the step change reaches Prometheus
# ==============================================================================
# Clear the previous run's overrides first. Without this a re-run starts already
# pinned at HOT_TEMP_C, and "the target reads HOT_TEMP_C" would be true before
# anything was injected -- an assertion that cannot fail proves nothing. The
# reset also drops the Xid failure block, but that alone does not give phase 2
# the same guarantee: DCGM latches the last Xid it observed and does not retract
# it when the mock's failure disappears, so phase 2 earns its non-vacuity by
# rotating the injected code instead (see below).
info "PHASE 1: clearing any override left on gpu ${TARGET_GPU} of ${TARGET_NODE} by an earlier run"
mock_ctl reset --gpu "${TARGET_GPU}"

info "Waiting for gpu ${TARGET_GPU} to report a simulator-driven temperature again"
await_gpu_value DCGM_FI_DEV_GPU_TEMP != "${HOT_TEMP_C}"
baseline_temp="${FAULT_OBSERVED}"
info "PHASE 1: baseline DCGM_FI_DEV_GPU_TEMP for gpu ${TARGET_GPU} = ${baseline_temp}C"

info "Heating gpu ${TARGET_GPU} to ${HOT_TEMP_C}C on ${TARGET_NODE} (pod ${MOCK_POD})"
mock_ctl temp --gpu "${TARGET_GPU}" "${HOT_TEMP_C}"

info "Waiting for the heat to reach Prometheus"
# Equality, not >=: the temp command pins the reading with zero variance, so the
# exact injected value is the only correct answer. A >= test would also accept a
# GPU that merely happens to run hot.
await_gpu_value DCGM_FI_DEV_GPU_TEMP == "${HOT_TEMP_C}"
info "OBSERVED: DCGM_FI_DEV_GPU_TEMP for gpu ${TARGET_GPU} stepped ${baseline_temp}C -> ${FAULT_OBSERVED}C in Prometheus"

# A pin that moved every GPU on the node is indistinguishable from an
# `--gpu all` mistake, and would make the dashboard's per-GPU story a lie. Prove
# the siblings kept their own readings -- and that there were siblings to check,
# so an empty result cannot be mistaken for a clean scope.
temp_snapshot=$(promq "query?query=DCGM_FI_DEV_GPU_TEMP")
siblings=$(jq --arg node "${TARGET_NODE}" --arg gpu "${TARGET_GPU}" \
  '[.data.result[] | select(.metric.Hostname == $node and .metric.gpu != $gpu)]' <<<"${temp_snapshot}")
sibling_count=$(jq 'length' <<<"${siblings}")
[[ "${sibling_count}" -gt 0 ]] \
  || fail "no sibling GPU series on ${TARGET_NODE} to compare against; the scope check would be vacuous"
hot_siblings=$(jq -r --argjson hot "${HOT_TEMP_C}" \
  '[.[] | select((.value[1] | tonumber) == $hot) | .metric.gpu] | sort | join(",")' <<<"${siblings}")
[[ -z "${hot_siblings}" ]] \
  || fail "gpu(s) ${hot_siblings} on ${TARGET_NODE} also read ${HOT_TEMP_C}C; the override is not scoped to gpu ${TARGET_GPU}"
info "OBSERVED: the other ${sibling_count} GPUs on ${TARGET_NODE} kept simulator-driven temperatures ($(jq -r '[.[] | "gpu" + .metric.gpu + "=" + .value[1] + "C"] | sort | join(" ")' <<<"${siblings}"))"

# ==============================================================================
# PHASE 2 — Inject an uncorrectable ECC fault and assert the Xid reaches Prometheus
# ==============================================================================
# DCGM_FI_DEV_XID_ERRORS has NO series at all on a healthy cluster: the mock
# delivers Xids through the NVML event set, and dcgm-exporter omits field 230
# entirely while it holds no value for it. The assertion therefore waits for the
# series to carry the code this run injected. A `!= 0` test would be vacuous --
# it compares against a series that does not exist.
#
# DCGM also latches the last Xid it observed per device: clearing the mock's
# failure (the reset in phase 1) does not retract the reported code, so a re-run
# that injected XID_CODE again would find the assertion already satisfied by the
# previous run's residue -- and would equally pass with dcgm-exporter dead, since
# Prometheus keeps serving the last sample for its lookback window. Injecting
# whichever of the two codes is NOT currently reported removes that hole: the
# value being waited for cannot be present until this run's injection arrives.
xid_before=$(prom_gpu_value DCGM_FI_DEV_XID_ERRORS)
xid_want="${XID_CODE}"
if [[ "${xid_before}" != "none" ]] && value_matches "${xid_before}" "${XID_CODE}" '=='; then
  xid_want="${XID_CODE_ALT}"
fi
info "PHASE 2: DCGM_FI_DEV_XID_ERRORS for gpu ${TARGET_GPU} before injection = ${xid_before}"

info "PHASE 2: injecting ecc_uncorrectable with Xid ${xid_want} on gpu ${TARGET_GPU} (rotating away from ${xid_before} so the wait below cannot pass on residue)"
mock_ctl fail --gpu "${TARGET_GPU}" --mode ecc_uncorrectable \
  --after-calls 1 --xid "${xid_want}"

info "Waiting for DCGM_FI_DEV_XID_ERRORS to reach Prometheus carrying Xid ${xid_want}"
await_gpu_value DCGM_FI_DEV_XID_ERRORS == "${xid_want}" err_code "${xid_want}"
info "OBSERVED: DCGM_FI_DEV_XID_ERRORS for gpu ${TARGET_GPU} = ${FAULT_OBSERVED} in Prometheus (expected ${xid_want}, was ${xid_before})"
if [[ "${xid_before}" == "none" ]]; then
  info "  the series did not exist before injection, so this run watched an Xid travel the whole path"
else
  info "  the series moved ${xid_before} -> ${FAULT_OBSERVED}, so this run watched an Xid travel the whole path rather than re-reading a latched value"
fi

# Same scope argument as the temperature pin, phrased against the injected code
# rather than presence: a future dcgm-exporter that reported 0 for healthy GPUs
# would still be correctly scoped.
xid_snapshot=$(promq "query?query=DCGM_FI_DEV_XID_ERRORS")
xid_others=$(jq -r --arg node "${TARGET_NODE}" --arg gpu "${TARGET_GPU}" --argjson xid "${xid_want}" \
  '[.data.result[]
     | select(.metric.Hostname == $node and .metric.gpu != $gpu)
     | select((.value[1] | tonumber) == $xid)
     | .metric.gpu] | sort | join(",")' <<<"${xid_snapshot}")
[[ -z "${xid_others}" ]] \
  || fail "gpu(s) ${xid_others} on ${TARGET_NODE} also report Xid ${xid_want}; the failure is not scoped to gpu ${TARGET_GPU}"
# err_code is printed alongside the identity labels because it is the label the
# dashboard's Xid legend keys on, and the one that makes this line evidence for
# the claim above rather than a restatement of it. The rotation can leave the
# previous code's series briefly un-stale, so pick the one this run injected.
info "OBSERVED: the Xid is scoped to gpu ${TARGET_GPU} with labels $(jq -c --arg node "${TARGET_NODE}" --arg gpu "${TARGET_GPU}" --arg xid "${xid_want}" \
  'first(.data.result[]
     | select(.metric.Hostname == $node and .metric.gpu == $gpu and .metric.err_code == $xid)
     | {Hostname: .metric.Hostname, gpu: .metric.gpu, err_code: .metric.err_code, UUID: .metric.UUID, pci_bus_id: .metric.pci_bus_id})' <<<"${xid_snapshot}")"

# --- Series continuity --------------------------------------------------------
# The whole reason faults are injected through nvml-mock-ctl instead of a helm
# upgrade is that a recycled pod tears a hole in the series the dashboard is
# meant to show. Assert it did not happen, rather than asserting it in a comment.
info "Confirming no pod was recycled by the injection"
mock_pods_after=$(pods_fingerprint "${MOKKA_NAMESPACE}" "app.kubernetes.io/name=nvml-mock")
dcgm_pods_after=$(pods_fingerprint "${GPU_OPERATOR_NAMESPACE}" "${DCGM_SELECTOR}")
if [[ "${mock_pods_before}" != "${mock_pods_after}" || "${dcgm_pods_before}" != "${dcgm_pods_after}" ]]; then
  echo "before:"; echo "${mock_pods_before}"; echo "${dcgm_pods_before}"
  echo "after:";  echo "${mock_pods_after}";  echo "${dcgm_pods_after}"
  # The likeliest innocent cause is the GPU Operator replacing its operands a
  # reconcile period after nvml-mock rolled (#602), which lands after the
  # rollout waits above already reported success. Re-running converges.
  fail "nvml-mock or dcgm-exporter pods were recycled during fault injection, so the metric series has a gap. Re-run to get a continuous one"
fi
info "OBSERVED: same pods, same restart counts, before and after injection:"
printf '%s\n%s\n' "${mock_pods_after}" "${dcgm_pods_after}"

# --- Summary ------------------------------------------------------------------
# DCGM latches the last Xid per device, so re-injecting the code this run just
# delivered would change nothing observable. Point the reader at the other half
# of the rotation, which is the same trick phase 2 uses on itself.
if [[ "${xid_want}" == "${XID_CODE}" ]]; then
  xid_next="${XID_CODE_ALT}"
else
  xid_next="${XID_CODE}"
fi

# The temperature panel queries DCGM_FI_DEV_GPU_TEMP unfiltered, so it renders
# every GPU in the fleet rather than only the faulted node's. Count the series
# Prometheus actually served instead of deriving GPU_COUNT x workers, so the
# number quoted below cannot disagree with what the reader sees.
temp_series_total=$(jq '.data.result | length' <<<"${temp_snapshot}")

# Reported from what this run actually observed, not from the configured
# defaults: the Xid code alternates between runs, so naming XID_CODE here would
# be wrong every other time.
cat <<EOF

==> Observability demo complete: injected GPU faults are recorded in Prometheus.

  Cluster     : ${CLUSTER_NAME} (1 control-plane + ${#WORKERS[@]} workers)
  Grafana     : http://localhost:3000/d/mokka-gpu  (admin / ${GRAFANA_PASSWORD})
  Prometheus  : http://localhost:9090
  Faulted GPU : gpu ${TARGET_GPU} on ${TARGET_NODE}

  What this run showed:
    1. SCRAPE : the real dcgm-exporter read the mock's libnvidia-ml.so, and
                Prometheus found it through the GPU Operator's ServiceMonitor.
    2. HEAT   : DCGM_FI_DEV_GPU_TEMP for gpu ${TARGET_GPU} stepped
                ${baseline_temp}C -> ${HOT_TEMP_C}C, and no pod restarted, so the
                series shows a step change rather than a gap.
    3. FAULT  : an ecc_uncorrectable fault raised Xid ${xid_want}, and
                DCGM_FI_DEV_XID_ERRORS in Prometheus now carries that code.

  What to look at on the dashboard (last 15m):
    - "GPU temperature": ${temp_series_total} lines, one per GPU across all
      ${#WORKERS[@]} workers. One is pinned flat at ${HOT_TEMP_C}C; the other
      ${sibling_count} on ${TARGET_NODE} -- and every GPU on the other workers --
      keep wandering with the simulator.
    - "Last Xid code reported": empty on a healthy fleet, now carrying
      ${xid_want} for ${TARGET_NODE} gpu${TARGET_GPU}. It is a code, not a count.
      Two lines for that GPU right now are the code rotation, not a second
      fault: Prometheus serves the superseded code's last sample until it goes
      stale, which takes a scrape interval or so.

  Inject again by hand:
    MOCK=\$(kubectl --context ${KUBE_CONTEXT} -n ${MOKKA_NAMESPACE} get pod -l app.kubernetes.io/name=nvml-mock \\
      --field-selector spec.nodeName=${TARGET_NODE} -o jsonpath='{.items[0].metadata.name}')
    kubectl --context ${KUBE_CONTEXT} -n ${MOKKA_NAMESPACE} exec \$MOCK -- nvml-mock-ctl temp --gpu ${TARGET_GPU} ${HOT_TEMP_C}
    kubectl --context ${KUBE_CONTEXT} -n ${MOKKA_NAMESPACE} exec \$MOCK -- nvml-mock-ctl fail --gpu ${TARGET_GPU} --mode ecc_uncorrectable --after-calls 1 --xid ${xid_next}
    kubectl --context ${KUBE_CONTEXT} -n ${MOKKA_NAMESPACE} exec \$MOCK -- nvml-mock-ctl reset --gpu ${TARGET_GPU}   # undoes both; DCGM keeps reporting the latched Xid

  Run those one at a time, allowing up to a minute for each to reach the
  dashboard (25-45s in practice). See ${DEMO_DIR}/README.md for the
  panel-by-panel walkthrough and the gotchas.

  Cleanup:
    kind delete cluster --name ${CLUSTER_NAME}
EOF
