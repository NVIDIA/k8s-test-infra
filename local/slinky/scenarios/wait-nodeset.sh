#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Block until a Slinky NodeSet has every desired slurmd pod Ready, then print
# Slurm's view of the nodes.
#
# The slurm-operator creates the NodeSet pods after `helm upgrade` returns, so
# neither helm --wait nor Tilt's helm_resource readiness sees them.
#
# Usage: wait-nodeset.sh [nodeset] [namespace] [timeout-seconds]
set -euo pipefail

NODESET="${1:-slurm-worker-gpu}"
NAMESPACE="${2:-slurm}"
TIMEOUT_S="${3:-600}"

deadline=$(( $(date +%s) + TIMEOUT_S ))
while :; do
  # desired is 0 until the operator has matched the NodeSet to nodes, so an
  # early read of 0/0 must not count as ready. During a rollout the old pods
  # are still Ready, so also require the operator to have observed the current
  # spec and every pod to be on its revision.
  status=$(kubectl -n "$NAMESPACE" get nodeset "$NODESET" \
    -o jsonpath='{.metadata.generation} {.status.observedGeneration} {.status.updatedReplicas} {.status.readyReplicas} {.status.desired}' \
    2>/dev/null || true)
  read -r generation observed updated ready desired <<<"$status" || true
  if [[ "${desired:-}" =~ ^[1-9][0-9]*$ && "${generation:-}" == "${observed:-}" &&
        "${updated:-}" == "$desired" && "${ready:-}" == "$desired" ]]; then
    echo "NodeSet $NAMESPACE/$NODESET: $ready/$desired slurmd pods Ready"
    break
  fi
  if (( $(date +%s) >= deadline )); then
    echo "timed out after ${TIMEOUT_S}s: NodeSet $NAMESPACE/$NODESET" \
      "generation/observed/updated/ready/desired=${status:-<missing>}" >&2
    kubectl -n "$NAMESPACE" get pods -l app.kubernetes.io/instance="$NODESET" -o wide >&2 || true
    exit 1
  fi
  sleep 5
done

# Pod readiness only means slurmd is up; whether Slurm accepted the node (and
# its GPUs) is the column worth reading.
kubectl -n "$NAMESPACE" exec slurm-controller-0 -c slurmctld -- \
  sinfo -N -o '%N %T %G %E'
