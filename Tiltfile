# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Thin orchestrator. Each consumer (GPU Operator, DRA driver, ...) lives in
# its own sub-Tiltfile co-located with its Helm values under local/<consumer>/.
# The nvml-mock stack lives at local/nvml_mock.tiltfile (always on).
#
# Adding a new consumer:
#   1. Create local/<name>/ with <name>.tiltfile exposing install(nvml_mock_releases).
#   2. Create local/<name>/nvml-mock-values.yaml (may be empty header only).
#   3. Add config.define_bool('with-<name>', ...) below.
#   4. Add a load(...) for the new tiltfile.
#   5. Add `if with_<name>: active_consumers.append('<name>')`.
#   6. Add `if with_<name>: <name>_install(nvml_mock_releases)`.
# The nvml-mock stack itself needs no changes to add a consumer.

load('ext://helm_resource', 'helm_repo')
load('./local/nvml_mock.tiltfile', 'build_nvml_mock_image', 'install_single', 'install_fleet')
load('./local/gpu-operator/gpu_operator.tiltfile', gpu_operator_install='install')
load('./local/dra/dra.tiltfile', dra_install='install')

# --- Flags ---------------------------------------------------------------
config.define_string('gpu-profile', args=False,
    usage='GPU profile to simulate (single-node mode only): a100 | h100 | b200 | gb200 | gb300 | l40s | t4')
config.define_string('k8s-context', args=False,
    usage='kubectl context to deploy into (must be a local cluster)')
# Boolean toggles use config.define_bool so `--multi --gpu-operator
# --dra` parses as three separate flags without positional-value
# ambiguity that Tilt's string-flag parser can exhibit for `=X` forms.
config.define_bool('multi', args=False,
    usage='Multi-node fleet mode: install one nvml-mock release per worker in local/kind/multi.kind.yaml')
config.define_bool('gpu-operator', args=False,
    usage='Also deploy NVIDIA GPU Operator on top of nvml-mock')
config.define_bool('dra', args=False,
    usage='Also deploy NVIDIA DRA driver on top of nvml-mock')

cfg = config.parse()

gpu_profile       = cfg.get('gpu-profile', 'a100')
k8s_context       = cfg.get('k8s-context', 'kind-gpu-test')
multi             = cfg.get('multi', False)
with_gpu_operator = cfg.get('gpu-operator', False)
with_dra          = cfg.get('dra', False)

# --- Derived state -------------------------------------------------------
# Ordered list of consumers active in this session. Drives (1) per-consumer
# nvml-mock overlay files that the mock installer picks up, and (2) shared
# nvidia helm-repo labeling in the Tilt UI.
active_consumers = []
if with_gpu_operator:
    active_consumers.append('gpu-operator')
if with_dra:
    active_consumers.append('dra')

# --- Safety guard --------------------------------------------------------
allow_k8s_contexts(k8s_context)

# --- nvml-mock (always on) ----------------------------------------------
build_nvml_mock_image()

if multi:
    nvml_mock_releases = install_fleet(active_consumers)
else:
    nvml_mock_releases = install_single(gpu_profile, active_consumers)

# --- Shared NVIDIA Helm repo --------------------------------------------
# Both consumer subfiles pull from nvidia/... — register the repo once here so
# each subfile can stay agnostic about who else uses it. Labels are attached
# per active consumer so the repo groups next to whichever consumers are on.
if active_consumers:
    helm_repo('nvidia', 'https://helm.ngc.nvidia.com/nvidia', labels=active_consumers)

# --- Consumers -----------------------------------------------------------
if with_gpu_operator:
    gpu_operator_install(nvml_mock_releases)

if with_dra:
    dra_install(nvml_mock_releases)

# --- Test workload -------------------------------------------------------
# GPU validator pod, disabled by default (enable from the Tilt UI). Requests
# one mock GPU, so the device plugin must have registered nvidia.com/gpu
# before the pod can start. Pod spec lives in local/gpu-validator.k8s.yaml so
# it can be kubectl-applied standalone or edited without touching Starlark.
k8s_yaml('local/gpu-validator.k8s.yaml')
k8s_resource('gpu-validator',
    auto_init=False,
    resource_deps=nvml_mock_releases,
    labels=['test'],
)
