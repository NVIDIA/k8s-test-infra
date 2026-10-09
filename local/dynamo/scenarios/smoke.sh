#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Smoke check: the Dynamo mocker graph runs on a mock GPU and serves a request.
#
# Asserts four things, each of which can fail while every pod reads Running:
#   1. The decode worker sits on a node that still advertises nvidia.com/gpu.
#      If nvml-mock goes away, the GPU Operator scales its operands to zero and
#      the node's allocatable drops to 0, but the worker keeps running.
#   2. nvidia-smi inside the worker sees exactly the one GPU it requested. More
#      means the device plugin did not scope the CDI injection; none means the
#      mock driver never reached the container.
#   3. That GPU is the node's profile, not the mock library's built-in default.
#   4. A chat completion round-trips frontend -> router -> mocker and returns
#      exactly max_tokens tokens. The mocker emits random token ids, so the
#      content itself is meaningless; the token count is what proves the
#      worker, not the frontend, produced it.

set -euo pipefail

# _NAMESPACE and _DGD_NAME in local/dynamo/dynamo.tiltfile.
NAMESPACE="dynamo-system"
DGD="qwen3"
MODEL="Qwen/Qwen3-0.6B"
MAX_TOKENS="${MAX_TOKENS:-8}"
MODEL_WAIT_S="${MODEL_WAIT_S:-120}"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

pod_of() {
  kubectl -n "${NAMESPACE}" get pods \
    -l "nvidia.com/dynamo-graph-deployment-name=${DGD},nvidia.com/dynamo-component-type=$1" \
    --field-selector status.phase=Running \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

printf '==> waiting for the %s graph to report Ready\n' "${DGD}"
kubectl -n "${NAMESPACE}" wait "dynamographdeployment/${DGD}" --for=condition=Ready --timeout=300s >/dev/null \
  || fail "DynamoGraphDeployment ${DGD} never became Ready; kubectl -n ${NAMESPACE} describe dgd ${DGD}"

worker=$(pod_of decode)
frontend=$(pod_of frontend)
[[ -n "${worker}" ]] || fail "no Running decode worker for ${DGD}"
[[ -n "${frontend}" ]] || fail "no Running frontend for ${DGD}"

node=$(kubectl -n "${NAMESPACE}" get pod "${worker}" -o jsonpath='{.spec.nodeName}')
allocatable=$(kubectl get node "${node}" -o jsonpath='{.status.allocatable.nvidia\.com/gpu}')
product=$(kubectl get node "${node}" -o jsonpath='{.metadata.labels.nvidia\.com/gpu\.product}')
[[ "${allocatable:-0}" -gt 0 ]] \
  || fail "worker ${worker} runs on ${node}, which advertises no nvidia.com/gpu; check that nvml-mock and the GPU Operator device plugin are running there"
printf 'OK: worker %s on %s (%s, %s allocatable GPUs)\n' "${worker}" "${node}" "${product}" "${allocatable}"

gpus=$(kubectl -n "${NAMESPACE}" exec "${worker}" -- nvidia-smi -L) \
  || fail "nvidia-smi failed inside ${worker}; the mock driver was not injected"
gpu_count=$(grep -c '^GPU ' <<<"${gpus}" || true)
[[ "${gpu_count}" -eq 1 ]] \
  || fail "worker requested 1 GPU but nvidia-smi lists ${gpu_count}:"$'\n'"${gpus}"
printf 'OK: worker sees its one GPU: %s\n' "${gpus}"

# GFD derives gpu.product from the profile nvml-mock serves on the node, with
# spaces turned into dashes. A worker whose mock library never found that
# profile falls back to the engine's built-in default device, which can carry
# the same GPU model under a different name, so only an exact match proves the
# worker reads the node's profile.
worker_gpu=$(kubectl -n "${NAMESPACE}" exec "${worker}" -- \
  nvidia-smi --query-gpu=name --format=csv,noheader)
[[ "${worker_gpu// /-}" == "${product}" ]] \
  || fail "worker reports GPU '${worker_gpu}' but ${node} is labelled '${product}'; the mock library inside the worker did not load the node's profile (is the nvml-mock NRI plugin running?)"
printf 'OK: worker GPU matches the node profile: %s\n' "${worker_gpu}"

# The graph reports Ready before the frontend has discovered the worker, so
# for a few seconds after it /v1/models is empty and a completion 404s. Wait
# for the model to be listed, from inside the frontend pod like the request
# below. A frontend that never lists it is the cold-start case in the Dynamo
# guide's Troubleshooting.
printf '==> waiting for the frontend to list %s\n' "${MODEL}"
waited=$(kubectl -n "${NAMESPACE}" exec "${frontend}" -- python3 -c '
import json, sys, time, urllib.request
start = time.monotonic()
while True:
    try:
        models = json.load(urllib.request.urlopen("http://localhost:8000/v1/models", timeout=5))
        if any(m.get("id") == sys.argv[1] for m in models.get("data", [])):
            break
    except (OSError, ValueError):
        pass
    if time.monotonic() - start > float(sys.argv[2]):
        sys.exit(1)
    time.sleep(2)
print(round(time.monotonic() - start))
' "${MODEL}" "${MODEL_WAIT_S}") \
  || fail "the ${DGD} frontend did not list ${MODEL} within ${MODEL_WAIT_S}s; if the decode worker is Running, delete the frontend pod so its replacement discovers it"
printf 'OK: frontend lists %s (after %ss)\n' "${MODEL}" "${waited}"

# Sent from inside the frontend pod: the API server's service proxy rejects a
# JSON POST from `kubectl create --raw`, and this keeps the check independent of
# the dynamo-frontend port-forward. The planner image ships python3, not curl.
printf '==> sending a chat completion through the frontend\n'
response=$(kubectl -n "${NAMESPACE}" exec "${frontend}" -- python3 -c '
import json, sys, urllib.request
body = {"model": sys.argv[1], "max_tokens": int(sys.argv[2]),
        "messages": [{"role": "user", "content": "Say hello from a mock GPU."}]}
req = urllib.request.Request("http://localhost:8000/v1/chat/completions", method="POST",
                             headers={"Content-Type": "application/json"},
                             data=json.dumps(body).encode())
print(urllib.request.urlopen(req, timeout=60).read().decode())
' "${MODEL}" "${MAX_TOKENS}") || fail "chat completion request to the ${DGD} frontend failed"

completion_tokens=$(jq -r '.usage.completion_tokens' <<<"${response}")
[[ "${completion_tokens}" == "${MAX_TOKENS}" ]] \
  || fail "expected ${MAX_TOKENS} completion tokens, got '${completion_tokens}': ${response}"
printf 'OK: %s generated %s tokens: %s\n' "${MODEL}" "${completion_tokens}" \
  "$(jq -c '.choices[0].message.content' <<<"${response}")"

printf '\n==> dynamo-smoke passed: Dynamo served %s from a mocker worker on mock GPU %s.\n' \
  "${MODEL}" "${product}"
