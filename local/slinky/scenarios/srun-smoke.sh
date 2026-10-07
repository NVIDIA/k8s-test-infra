#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Smoke check: Slurm schedules GPU jobs onto the mock GPUs slurmd discovered.
#
# Asserts three things, each of which can fail while every slurmd pod reads
# Ready:
#   1. Every Slurm node is idle and registered every GPU its slurmd pod holds.
#      Nothing configures a count, so a slurmd that found no GPUs
#      (AutoDetect=nvidia read no driver files) still leaves its node idle,
#      just with Gres=(null).
#   2. A job asking for 2 GPUs is given exactly 2, and they are the node's
#      profile rather than the mock library's built-in default, although the
#      job inherits no MOCK_NVML_CONFIG from the shell that ran srun.
#   3. A job asking for every GPU on every node runs once on each of them, so
#      Slurm accepted the whole fleet's GRES, not just one node's.

set -euo pipefail

# _NAMESPACE in local/slinky/slinky.tiltfile.
NAMESPACE="${NAMESPACE:-slurm}"
# The NodeSet's nvidia.com/gpu limit in local/slinky/slurm.values.yaml.
GPUS_PER_NODE="${GPUS_PER_NODE:-4}"
# How long srun waits for an allocation before giving up rather than queueing.
IMMEDIATE_S="${IMMEDIATE_S:-60}"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

slurm() {
  kubectl -n "${NAMESPACE}" exec slurm-controller-0 -c slurmctld -- "$@"
}

printf '==> checking every Slurm node registered %s GPUs\n' "${GPUS_PER_NODE}"
nodes=$(slurm sinfo -N -h -o '%N %T %G') || fail "sinfo failed; is slurm-controller-0 running in ${NAMESPACE}?"
[[ -n "${nodes}" ]] || fail "Slurm has no nodes"
while read -r name state gres; do
  [[ "${state}" == "idle" ]] \
    || fail "node ${name} is ${state}, not idle: $(slurm sinfo -n "${name}" -h -o '%E')"
  # AutoDetect types the GRES by model, e.g. gpu:nvidia_gb200:4(S:0).
  [[ "${gres}" =~ ^gpu:([^:]+:)?${GPUS_PER_NODE}(\(|$) ]] \
    || fail "node ${name} registered GRES '${gres}', expected gpu:${GPUS_PER_NODE}"
done <<<"${nodes}"
node_count=$(wc -l <<<"${nodes}" | tr -d ' ')
printf 'OK: %s nodes idle with gpu:%s\n' "${node_count}" "${GPUS_PER_NODE}"

printf '==> running a 2-GPU job\n'
out=$(slurm srun -N1 --immediate="${IMMEDIATE_S}" --gres=gpu:2 \
  bash -c 'echo "$(hostname) $CUDA_VISIBLE_DEVICES"; nvidia-smi --query-gpu=name --format=csv,noheader | head -1') \
  || fail "the 2-GPU job did not run"
read -r job_node visible <<<"$(head -1 <<<"${out}")"
job_gpu=$(sed -n 2p <<<"${out}")
IFS=, read -ra allocated <<<"${visible}"
[[ "${#allocated[@]}" -eq 2 ]] \
  || fail "job asked for 2 GPUs but CUDA_VISIBLE_DEVICES='${visible}'"
printf 'OK: job on %s was allocated GPUs %s\n' "${job_node}" "${visible}"

# GFD labels the node from the profile nvml-mock serves, with spaces as dashes.
product=$(kubectl get node "${job_node}" -o jsonpath='{.metadata.labels.nvidia\.com/gpu\.product}')
[[ "${job_gpu// /-}" == "${product}" ]] \
  || fail "job reports GPU '${job_gpu}' but ${job_node} is labelled '${product}'; the job's mock library did not load the node's profile"
printf 'OK: job GPU matches the node profile: %s\n' "${job_gpu}"

printf '==> running a job on every GPU of all %s nodes\n' "${node_count}"
hosts=$(slurm srun -N"${node_count}" --immediate="${IMMEDIATE_S}" --gres=gpu:"${GPUS_PER_NODE}" hostname) \
  || fail "the whole-fleet job did not run"
ran=$(sort -u <<<"${hosts}" | wc -l | tr -d ' ')
[[ "${ran}" -eq "${node_count}" ]] \
  || fail "whole-fleet job ran on ${ran} of ${node_count} nodes: ${hosts//$'\n'/ }"
printf 'OK: ran on %s\n' "$(sort -u <<<"${hosts}" | paste -sd' ' -)"

printf '\n==> slurm-smoke passed: Slurm scheduled GPU jobs onto %s nodes of mock %s.\n' \
  "${node_count}" "${product}"
