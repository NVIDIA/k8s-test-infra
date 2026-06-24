#!/bin/bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Validates the mock NCCL 2-pod collective-comms test Job: waits for the
# Indexed Job to complete, then asserts the rank-0 pod reported a positive
# "# Avg bus bandwidth" line (proves the cost model + mock CUDA-event timing
# produced a non-zero busbw across the MPI-free rendezvous).
#
# Usage:
#   validate-nccl.sh [namespace] [job-name]
set -euo pipefail

NS="${1:-default}"
JOB="${2:-nvml-mock-nccl-test}"
TIMEOUT="${NCCL_E2E_TIMEOUT:-180s}"

echo "=== Validating mock NCCL job ns=$NS job=$JOB ==="

echo "Waiting for Job/$JOB to complete (timeout $TIMEOUT)..."
if ! kubectl -n "$NS" wait --for=condition=complete "job/$JOB" --timeout="$TIMEOUT"; then
  echo "FAIL: Job/$JOB did not complete"
  kubectl -n "$NS" describe "job/$JOB" || true
  kubectl -n "$NS" logs -l "app.kubernetes.io/component=nccl-test" --tail=80 || true
  exit 1
fi

# Rank-0 pod prints the nccl-tests-style table + the avg line. Scope the
# selector to this Job's component so we never grab an index-0 pod from an
# unrelated Indexed Job in the same namespace.
POD="$(kubectl -n "$NS" get pods \
        -l batch.kubernetes.io/job-completion-index=0,app.kubernetes.io/component=nccl-test \
        -o jsonpath='{.items[0].metadata.name}')"
if [ -z "$POD" ]; then
  echo "FAIL: could not find rank-0 pod (batch.kubernetes.io/job-completion-index=0)"
  exit 1
fi
echo "Rank-0 pod: $POD"

if ! LOG="$(kubectl -n "$NS" logs "$POD")"; then
  echo "FAIL: could not read logs from rank-0 pod $POD"
  exit 1
fi
echo "=== rank-0 log ==="
echo "$LOG"
echo "=================="

AVG="$(printf '%s\n' "$LOG" | sed -n 's/^# Avg bus bandwidth : //p')"
if [ -z "$AVG" ]; then
  echo "FAIL: no '# Avg bus bandwidth' line in rank-0 log" >&2
  exit 1
fi

printf '%s\n' "$AVG" | awk '{
  if ($1+0 > 0) { print "PASS: NCCL avg busbw = " $1 " GB/s" }
  else { print "FAIL: NCCL busbw not positive (" $1 ")" > "/dev/stderr"; exit 1 }
}'
echo "=== mock NCCL validation PASSED ==="
