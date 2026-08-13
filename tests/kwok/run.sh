#!/usr/bin/env bash
# Copyright (c) 2026, NVIDIA CORPORATION. All rights reserved.
# Licensed under the Apache License, Version 2.0 (the "License");

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

readonly KWOK_VERSION="v0.8.0"
readonly KUBERNETES_VERSION="v1.36.1"
readonly KWOK_CONTROLLER_IMAGE="registry.k8s.io/kwok/kwok:v0.8.0"
readonly KUBE_APISERVER_IMAGE="registry.k8s.io/kube-apiserver:v1.36.1"
readonly KUBE_CONTROLLER_MANAGER_IMAGE="registry.k8s.io/kube-controller-manager:v1.36.1"
readonly ETCD_IMAGE="registry.k8s.io/etcd:3.6.10-0"

readonly NODE_COUNT="${KWOK_NODE_COUNT:-100}"
readonly INITIAL_NODE_COUNT="$(((NODE_COUNT + 1) / 2))"
readonly NODES_PER_RACK="${KWOK_NODES_PER_RACK:-100}"
readonly TIMEOUT_SECONDS="${KWOK_TIMEOUT_SECONDS:-1800}"
readonly WORKERS="${KWOK_WORKERS:-16}"
readonly API_QPS="${KWOK_API_QPS:-1000}"
readonly API_BURST="${KWOK_API_BURST:-2000}"
readonly CLUSTER_NAME="${KWOK_CLUSTER_NAME:-mokka-controller-kwok}"
readonly ARTIFACT_ROOT="${KWOK_ARTIFACT_ROOT:-${REPO_DIR}/_artifacts/kwok}"
readonly RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-${NODE_COUNT}-$$"
readonly ARTIFACT_DIR="${ARTIFACT_ROOT}/${RUN_ID}"
readonly LOCK_DIR="${ARTIFACT_ROOT}/.locks/${CLUSTER_NAME}"
readonly WORK_DIR="${ARTIFACT_DIR}/work"
readonly KUBECONFIG_PATH="${WORK_DIR}/kubeconfig"
readonly CONTROLLER_BIN="${WORK_DIR}/mokka-controller"
readonly ASSERT_BIN="${WORK_DIR}/kwok-assert"
readonly CONTROLLER_LOG="${ARTIFACT_DIR}/controller.log"
readonly CONTROLLER_PID_FILE="${WORK_DIR}/controller.pid"
readonly TIMINGS_FILE="${ARTIFACT_DIR}/timings.jsonl"
readonly CLUSTER_LABEL="${CLUSTER_NAME}"
readonly OWNER_LABEL="tests.mokka.nvidia.com/kwok-cluster"

CLUSTER_CREATED=false
LOCK_HELD=false
CONTROLLER_PID=""

log() {
	printf '%s %s\n' "$(date -u +%FT%TZ)" "$*"
}

die() {
	log "ERROR: $*" >&2
	exit 1
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || die "required command is not installed: $1"
}

validate_uint() {
	case "$2" in
		''|*[!0-9]*) die "$1 must be a positive integer, got $2" ;;
	esac
	(( 10#$2 > 0 )) || die "$1 must be positive, got $2"
}

kctl() {
	kubectl --kubeconfig "${KUBECONFIG_PATH}" "$@"
}

kwok() {
	kwokctl --name "${CLUSTER_NAME}" "$@"
}

record_timing() {
	local state="$1"
	local started_seconds="$2"
	local finished_seconds
	finished_seconds="$(date -u +%s)"
	jq -cn \
		--arg state "${state}" \
		--argjson startedEpochSeconds "${started_seconds}" \
		--argjson finishedEpochSeconds "${finished_seconds}" \
		'{schemaVersion:1,state:$state,startedEpochSeconds:$startedEpochSeconds,finishedEpochSeconds:$finishedEpochSeconds,durationSeconds:($finishedEpochSeconds-$startedEpochSeconds)}' \
		>>"${TIMINGS_FILE}"
}

wait_for() {
	local description="$1"
	shift
	local deadline=$((SECONDS + TIMEOUT_SECONDS))
	while (( SECONDS < deadline )); do
		if "$@"; then
			return 0
		fi
		sleep 2
	done
	log "timed out waiting for ${description} after ${TIMEOUT_SECONDS}s"
	return 1
}

controller_ready() {
	local port
	port="$(cat "${WORK_DIR}/controller.port")"
	curl --fail --silent --max-time 2 "http://127.0.0.1:${port}/readyz" >/dev/null
}

start_controller() {
	[[ -z "${CONTROLLER_PID}" ]] || die "controller is already running"
	local port
	port="$((18081 + ($$ % 1000)))"
	printf '%s\n' "${port}" >"${WORK_DIR}/controller.port"
	log "starting the real host controller against ${KUBECONFIG_PATH}"
	"${CONTROLLER_BIN}" \
		--kubeconfig="${KUBECONFIG_PATH}" \
		--health-bind-address="127.0.0.1:${port}" \
		--leader-election-namespace=default \
		--leader-election-name=mokka-controller-kwok \
		--leader-election-lease-duration=15s \
		--leader-election-renew-deadline=10s \
		--leader-election-retry-period=2s \
		--workers="${WORKERS}" \
		--status-debounce=100ms \
		--status-progress-interval=1s \
		--kube-api-qps="${API_QPS}" \
		--kube-api-burst="${API_BURST}" \
		>>"${CONTROLLER_LOG}" 2>&1 &
	CONTROLLER_PID=$!
	printf '%s\n' "${CONTROLLER_PID}" >"${CONTROLLER_PID_FILE}"
	wait_for "host controller readiness" controller_ready || return 1
}

stop_controller() {
	if [[ -z "${CONTROLLER_PID}" ]]; then
		return
	fi
	if kill -0 "${CONTROLLER_PID}" 2>/dev/null; then
		kill -TERM "${CONTROLLER_PID}"
		local deadline=$((SECONDS + 30))
		while kill -0 "${CONTROLLER_PID}" 2>/dev/null && (( SECONDS < deadline )); do
			sleep 1
		done
		if kill -0 "${CONTROLLER_PID}" 2>/dev/null; then
			log "controller did not stop within 30s; sending KILL"
			kill -KILL "${CONTROLLER_PID}" 2>/dev/null || true
		fi
		wait "${CONTROLLER_PID}" 2>/dev/null || true
	fi
	CONTROLLER_PID=""
	rm -f "${CONTROLLER_PID_FILE}"
}

render_inventory() {
	local racks="$1"
	sed "s/__RACK_COUNT__/${racks}/g" "${SCRIPT_DIR}/inventory.yaml" >"${WORK_DIR}/inventory.yaml"
}

apply_inventory() {
	local racks="$1"
	render_inventory "${racks}"
	kctl apply -f "${WORK_DIR}/inventory.yaml" >/dev/null
}

scale_nodes() {
	local count="$1"
	kwok scale mokka-node --replicas "${count}" --config "${WORK_DIR}/node-resource.yaml"
}

capture_process_metrics() {
	local state="$1"
	if [[ -n "${CONTROLLER_PID}" ]] && kill -0 "${CONTROLLER_PID}" 2>/dev/null; then
		ps -o pid=,ppid=,etime=,time=,rss=,vsz=,pcpu=,pmem=,command= -p "${CONTROLLER_PID}" \
			>"${ARTIFACT_DIR}/${state}.controller-process.txt" 2>&1 || true
	fi
}

capture_metrics() {
	local state="$1"
	kwok logs kube-apiserver >"${ARTIFACT_DIR}/${state}.kube-apiserver.log" 2>&1 || true
	kwok logs etcd >"${ARTIFACT_DIR}/${state}.etcd.log" 2>&1 || true
	kwok logs kwok-controller >"${ARTIFACT_DIR}/${state}.kwok-controller.log" 2>&1 || true
	kwok etcdctl endpoint status -w json >"${ARTIFACT_DIR}/${state}.etcd-status.json" 2>&1 || true
	kwok get components >"${ARTIFACT_DIR}/${state}.components.txt" 2>&1 || true
	kctl get --raw /metrics >"${ARTIFACT_DIR}/${state}.kube-apiserver.metrics.prom" 2>&1 || true
	capture_process_metrics "${state}"
}

assert_state_once() {
	local state="$1"
	local expected_racks="$2"
	local expected_nodes="$3"
	local expected_eligible="$4"
	local expected_allocated="$5"
	local requests_satisfied="$6"
	local state_dir="${WORK_DIR}/state-${state}"
	mkdir -p "${state_dir}"
	kctl get sgpuinventory mokka-kwok -o json >"${state_dir}/inventory.json" || return 1
	kctl get sgpuracks -l mokka.nvidia.com/inventory=mokka-kwok -o json >"${state_dir}/racks.json" || return 1
	kctl get nodes -l "${OWNER_LABEL}=${CLUSTER_LABEL}" -o json >"${state_dir}/nodes.json" || return 1
	local args=(
		--state "${state}"
		--inventory "${state_dir}/inventory.json"
		--racks "${state_dir}/racks.json"
		--nodes "${state_dir}/nodes.json"
		--cluster-label "${CLUSTER_LABEL}"
		--expected-racks "${expected_racks}"
		--nodes-per-rack "${NODES_PER_RACK}"
		--expected-nodes "${expected_nodes}"
		--expected-eligible "${expected_eligible}"
		--expected-allocated "${expected_allocated}"
	)
	if [[ "${requests_satisfied}" == true ]]; then
		args+=(--requests-satisfied)
	fi
	"${ASSERT_BIN}" "${args[@]}" >"${state_dir}/result.json" 2>"${state_dir}/assertion.err"
}

inventory_summary_ready() {
	local expected_racks="$1"
	local expected_eligible="$2"
	local expected_allocated="$3"
	local requests_satisfied="$4"
	local expected_capacity=$((expected_racks * NODES_PER_RACK))
	local expected_pending=$((expected_eligible - expected_allocated))
	local expected_available=$((expected_capacity - expected_allocated))
	local requests_status=False
	if [[ "${requests_satisfied}" == true ]]; then
		requests_status=True
	fi
	kctl get sgpuinventory mokka-kwok -o json | jq -e \
		--argjson racks "${expected_racks}" \
		--argjson capacity "${expected_capacity}" \
		--argjson eligible "${expected_eligible}" \
		--argjson allocated "${expected_allocated}" \
		--argjson available "${expected_available}" \
		--argjson pending "${expected_pending}" \
		--arg requestsStatus "${requests_status}" '
		.status.observedGeneration == .metadata.generation and
		.status.capacity.racks == $racks and
		.status.capacity.nodeSlots == $capacity and
		.status.capacity.gpus == $capacity and
		.status.usage.requestedNodes == $eligible and
		.status.usage.allocatedNodes == $allocated and
		.status.usage.availableNodes == $available and
		.status.usage.pendingNodes == $pending and
		.status.usage.conflictingNodes == 0 and
		.status.usage.projectedNodes == $allocated and
		([.status.conditions[] | select(
			(.type == "Accepted" or .type == "ResolvedRefs" or .type == "Materialized" or .type == "NodesProjected") and
			.status == "True"
		)] | length) == 4 and
		any(.status.conditions[]; .type == "RequestsSatisfied" and .status == $requestsStatus)
	' >/dev/null
}

check_state() {
	local state="$1"
	local started_seconds="$2"
	local expected_racks="$3"
	local expected_nodes="$4"
	local expected_eligible="$5"
	local expected_allocated="$6"
	local requests_satisfied="$7"
	log "waiting for ${state}: racks=${expected_racks} nodes=${expected_nodes} eligible=${expected_eligible} allocated=${expected_allocated}"
	wait_for "inventory summary for ${state}" inventory_summary_ready "${expected_racks}" \
		"${expected_eligible}" "${expected_allocated}" "${requests_satisfied}"
	if ! assert_state_once "${state}" "${expected_racks}" "${expected_nodes}" \
		"${expected_eligible}" "${expected_allocated}" "${requests_satisfied}"; then
		log "full state assertion failed for ${state}: $(cat "${WORK_DIR}/state-${state}/result.json")"
		return 1
	fi
	cp "${WORK_DIR}/state-${state}/inventory.json" "${ARTIFACT_DIR}/${state}.inventory.json"
	cp "${WORK_DIR}/state-${state}/racks.json" "${ARTIFACT_DIR}/${state}.racks.json"
	cp "${WORK_DIR}/state-${state}/nodes.json" "${ARTIFACT_DIR}/${state}.nodes.json"
	cp "${WORK_DIR}/state-${state}/result.json" "${ARTIFACT_DIR}/${state}.result.json"
	record_timing "${state}" "${started_seconds}"
	capture_process_metrics "${state}"
}

snapshot_assignments() {
	local output="$1"
	kctl get nodes -l "${OWNER_LABEL}=${CLUSTER_LABEL}" -o json | jq -S '[.items[] | select(.metadata.annotations["mokka.nvidia.com/sgpu-assignment"] != null) | {name:.metadata.name,uid:.metadata.uid,assignment:.metadata.annotations["mokka.nvidia.com/sgpu-assignment"]}] | sort_by(.name)' >"${output}"
}

capture_failure() {
	set +e
	log "capturing failure diagnostics in ${ARTIFACT_DIR}"
	kctl get sgpuprofiles,sgpuinventories,sgpuracks,nodes,leases -A -o json >"${ARTIFACT_DIR}/failure.resources.json" 2>&1
	kctl get events -A --sort-by=.lastTimestamp >"${ARTIFACT_DIR}/failure.events.txt" 2>&1
	kctl api-resources >"${ARTIFACT_DIR}/failure.api-resources.txt" 2>&1
	capture_metrics failure
}

cleanup() {
	local exit_code=$?
	trap - EXIT INT TERM
	if (( exit_code != 0 )) && [[ "${CLUSTER_CREATED}" == true ]]; then
		capture_failure
	fi
	stop_controller
	if [[ "${CLUSTER_CREATED}" == true ]]; then
		log "deleting owned KWOK cluster ${CLUSTER_NAME}"
		kwok delete cluster >"${ARTIFACT_DIR}/cluster-delete.log" 2>&1 || true
	fi
	if [[ "${LOCK_HELD}" == true ]]; then
		rmdir "${LOCK_DIR}" 2>/dev/null || true
	fi
	if (( exit_code == 0 )); then
		log "PASS: artifacts: ${ARTIFACT_DIR}"
	else
		log "FAIL (${exit_code}): artifacts preserved: ${ARTIFACT_DIR}" >&2
	fi
	exit "${exit_code}"
}

for command in kwokctl kubectl docker go jq curl sed ps awk cmp git; do
	require_command "${command}"
done
validate_uint KWOK_NODE_COUNT "${NODE_COUNT}"
validate_uint KWOK_NODES_PER_RACK "${NODES_PER_RACK}"
validate_uint KWOK_TIMEOUT_SECONDS "${TIMEOUT_SECONDS}"
(( NODES_PER_RACK <= 1024 )) || die "KWOK_NODES_PER_RACK exceeds the Mokka CRD maximum of 1024"
(( NODE_COUNT % NODES_PER_RACK == 0 )) || die "KWOK_NODE_COUNT must be divisible by KWOK_NODES_PER_RACK"
(( NODE_COUNT >= 4 )) || die "KWOK_NODE_COUNT must be at least 4 for lifecycle scenarios"
[[ "${CLUSTER_LABEL}" =~ ^[A-Za-z0-9]([A-Za-z0-9_.-]*[A-Za-z0-9])?$ ]] || die "KWOK_CLUSTER_NAME is not a valid label value"
(( ${#CLUSTER_LABEL} <= 63 )) || die "KWOK_CLUSTER_NAME exceeds the 63-character label-value limit"

mkdir -p "${WORK_DIR}"
trap cleanup EXIT INT TERM
mkdir -p "${ARTIFACT_ROOT}/.locks"
mkdir "${LOCK_DIR}" 2>/dev/null || die "another harness owns the local cluster lock ${LOCK_DIR}"
LOCK_HELD=true

kwokctl --version | grep -F "${KWOK_VERSION}" >/dev/null || die "kwokctl ${KWOK_VERSION} is required"
docker info >/dev/null 2>&1 || die "Docker daemon is unavailable"
if kwokctl get clusters | awk -v name="${CLUSTER_NAME}" '$1 == name { found=1 } END { exit !found }'; then
	die "refusing to reuse or delete existing KWOK cluster ${CLUSTER_NAME}"
fi

jq -cn \
	--arg runID "${RUN_ID}" --arg clusterName "${CLUSTER_NAME}" \
	--arg kwokVersion "${KWOK_VERSION}" --arg kubernetesVersion "${KUBERNETES_VERSION}" \
	--argjson nodeCount "${NODE_COUNT}" --argjson nodesPerRack "${NODES_PER_RACK}" \
	'{schemaVersion:1,runID:$runID,clusterName:$clusterName,kwokVersion:$kwokVersion,kubernetesVersion:$kubernetesVersion,nodeCount:$nodeCount,nodesPerRack:$nodesPerRack}' \
	>"${ARTIFACT_DIR}/run.json"
kwokctl --version >"${ARTIFACT_DIR}/kwokctl-version.txt"
kubectl version --client=true --output=json >"${ARTIFACT_DIR}/kubectl-version.json"
docker version >"${ARTIFACT_DIR}/docker-version.txt"
go version >"${ARTIFACT_DIR}/go-version.txt"
git -C "${REPO_DIR}" status --short --branch >"${ARTIFACT_DIR}/git-status.txt"

sed "s/__CLUSTER_LABEL__/${CLUSTER_LABEL}/g" "${SCRIPT_DIR}/node-resource.yaml" >"${WORK_DIR}/node-resource.yaml"
sed "s/__NODES_PER_RACK__/${NODES_PER_RACK}/g" "${SCRIPT_DIR}/profile.yaml" >"${WORK_DIR}/profile.yaml"

log "building controller and typed assertion helper"
go build -o "${CONTROLLER_BIN}" ./cmd/mokka-controller
go build -o "${ASSERT_BIN}" ./tests/kwok/cmd/kwok-assert

log "creating owned KWOK cluster ${CLUSTER_NAME}"
CLUSTER_CREATED=true
kwok create cluster \
	--runtime=docker \
	--kubeconfig="${KUBECONFIG_PATH}" \
	--disable kube-scheduler \
	--disable-qps-limits \
	--etcd-quota-backend-size=16Gi \
	--etcd-image="${ETCD_IMAGE}" \
	--kube-apiserver-image="${KUBE_APISERVER_IMAGE}" \
	--kube-controller-manager-image="${KUBE_CONTROLLER_MANAGER_IMAGE}" \
	--kwok-controller-image="${KWOK_CONTROLLER_IMAGE}" \
	--timeout=10m \
	--wait=5m \
	>"${ARTIFACT_DIR}/cluster-create.log" 2>&1

log "installing generated Mokka CRDs"
kctl apply --server-side --field-manager=mokka-kwok-poc -f "${REPO_DIR}/deployments/mokka-crds/helm/mokka-crds/crds" >/dev/null
kctl wait --for=condition=Established --timeout=2m crd/sgpuprofiles.mokka.nvidia.com crd/sgpuinventories.mokka.nvidia.com crd/sgpuracks.mokka.nvidia.com
kctl apply --server-side --field-manager=mokka-kwok-poc -f "${WORK_DIR}/profile.yaml" >/dev/null

start_controller

readonly FULL_RACKS="$((NODE_COUNT / NODES_PER_RACK))"
readonly EXTRA_COUNT="${NODES_PER_RACK}"
readonly TOTAL_WITH_EXTRA="$((NODE_COUNT + EXTRA_COUNT))"

scenario_started_seconds="$(date -u +%s)"
scale_nodes "${INITIAL_NODE_COUNT}"
apply_inventory "${FULL_RACKS}"
check_state scale-up-half "${scenario_started_seconds}" "${FULL_RACKS}" "${INITIAL_NODE_COUNT}" "${INITIAL_NODE_COUNT}" "${INITIAL_NODE_COUNT}" true

scenario_started_seconds="$(date -u +%s)"
scale_nodes "${NODE_COUNT}"
check_state steady-state "${scenario_started_seconds}" "${FULL_RACKS}" "${NODE_COUNT}" "${NODE_COUNT}" "${NODE_COUNT}" true
snapshot_assignments "${ARTIFACT_DIR}/restart.before.json"
scenario_started_seconds="$(date -u +%s)"
stop_controller
start_controller
check_state controller-restart "${scenario_started_seconds}" "${FULL_RACKS}" "${NODE_COUNT}" "${NODE_COUNT}" "${NODE_COUNT}" true
snapshot_assignments "${ARTIFACT_DIR}/restart.after.json"
cmp "${ARTIFACT_DIR}/restart.before.json" "${ARTIFACT_DIR}/restart.after.json" || die "assignments changed across controller restart"

readonly REPLACED_NODE="mokka-node-000000"
readonly OLD_UID="$(kctl get node "${REPLACED_NODE}" -o jsonpath='{.metadata.uid}')"
scenario_started_seconds="$(date -u +%s)"
kctl delete node "${REPLACED_NODE}" --wait=true >/dev/null
scale_nodes "${NODE_COUNT}"
readonly NEW_UID="$(kctl get node "${REPLACED_NODE}" -o jsonpath='{.metadata.uid}')"
[[ "${OLD_UID}" != "${NEW_UID}" ]] || die "same-name replacement retained the old UID"
check_state same-name-new-uid "${scenario_started_seconds}" "${FULL_RACKS}" "${NODE_COUNT}" "${NODE_COUNT}" "${NODE_COUNT}" true
jq -cn --arg name "${REPLACED_NODE}" --arg oldUID "${OLD_UID}" --arg newUID "${NEW_UID}" \
	'{schemaVersion:1,name:$name,oldUID:$oldUID,newUID:$newUID}' >"${ARTIFACT_DIR}/replacement.json"

scenario_started_seconds="$(date -u +%s)"
scale_nodes "${TOTAL_WITH_EXTRA}"
check_state capacity-exhaustion "${scenario_started_seconds}" "${FULL_RACKS}" "${TOTAL_WITH_EXTRA}" "${TOTAL_WITH_EXTRA}" "${NODE_COUNT}" false

scenario_started_seconds="$(date -u +%s)"
apply_inventory 0
check_state inventory-shrink "${scenario_started_seconds}" 0 "${TOTAL_WITH_EXTRA}" "${TOTAL_WITH_EXTRA}" 0 false
scenario_started_seconds="$(date -u +%s)"
apply_inventory "${FULL_RACKS}"
check_state inventory-grow "${scenario_started_seconds}" "${FULL_RACKS}" "${TOTAL_WITH_EXTRA}" "${TOTAL_WITH_EXTRA}" "${NODE_COUNT}" false

scenario_started_seconds="$(date -u +%s)"
kctl label node mokka-node-000000 mokka.nvidia.com/sgpu-node- >/dev/null
check_state eligibility-churn-remove "${scenario_started_seconds}" "${FULL_RACKS}" "${TOTAL_WITH_EXTRA}" "$((TOTAL_WITH_EXTRA - 1))" "${NODE_COUNT}" false
scenario_started_seconds="$(date -u +%s)"
kctl label node mokka-node-000000 mokka.nvidia.com/sgpu-node=true --overwrite >/dev/null
check_state eligibility-churn-restore "${scenario_started_seconds}" "${FULL_RACKS}" "${TOTAL_WITH_EXTRA}" "${TOTAL_WITH_EXTRA}" "${NODE_COUNT}" false

capture_metrics final
jq -s '{schemaVersion:1,states:.}' "${ARTIFACT_DIR}"/*.result.json >"${ARTIFACT_DIR}/results.json"
log "all KWOK scalability POC states passed"
