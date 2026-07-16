# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Thin orchestrator. Each consumer (GPU Operator, DRA driver, ...) lives in
# its own sub-Tiltfile co-located with its Helm values under local/<consumer>/.
# The nvml-mock stack lives at local/nvml_mock.tiltfile (always on except
# in scenario mode).
#
# Adding a new consumer:
#   1. Create local/<name>/ with <name>.tiltfile exposing install(nvml_mock_releases).
#   2. Create local/<name>/nvml-mock.values.yaml (may be empty header only).
#   3. Add config.define_bool('with-<name>', ...) below.
#   4. Add a load(...) for the new tiltfile.
#   5. Add `if with_<name>: active_consumers.append('<name>')`.
#   6. Add `if with_<name>: <name>_install(nvml_mock_releases)`.
# The nvml-mock stack itself needs no changes to add a consumer.
#
# Adding a new scenario (e.g. compute-domain):
#   Scenarios reshape the nvml-mock stack itself — different image, forced
#   profile, dedicated cluster topology — so they don't fit the additive
#   consumer contract above. See local/compute-domain/compute_domain.tiltfile
#   for the pattern: exports build_images() + install() the way
#   local/nvml_mock.tiltfile does, and the orchestrator calls them as the
#   base install path instead of build_nvml_mock_image() + install_single().

load('ext://helm_resource', 'helm_repo')
load('./local/nvml_mock.tiltfile', 'build_nvml_mock_image', 'install_single', 'install_fleet')
load('./local/compute-domain/compute_domain.tiltfile',
     compute_domain_build_images='build_images',
     compute_domain_install='install',
     compute_domain_daemon_image='DAEMON_IMAGE')
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
config.define_bool('compute-domain', args=False,
    usage='ComputeDomain scenario: 4-worker cluster with GB200 profile + NVLink topology overlay (requires PROFILE=compute-domain cluster)')
config.define_bool('gpu-operator', args=False,
    usage='Also deploy NVIDIA GPU Operator on top of nvml-mock')
config.define_bool('dra', args=False,
    usage='Also deploy NVIDIA DRA driver on top of nvml-mock')

cfg = config.parse()

multi               = cfg.get('multi', False)
with_compute_domain = cfg.get('compute-domain', False)
with_gpu_operator   = cfg.get('gpu-operator', False)
with_dra            = cfg.get('dra', False)

# --- Guardrails ----------------------------------------------------------
# compute-domain forces its own cluster shape (4 workers with clique
# labels, hardcoded worker names in topology.yaml) and its own profile
# (gb200 for NVLink5 fabric APIs), so it cannot compose with --multi
# or with any --gpu-profile the user might pass. --gpu-operator is
# allowed but experimental — the Operator's RuntimeClass path with the
# compute-domain-imex layered image is untested.
if with_compute_domain and multi:
    fail('--compute-domain is mutually exclusive with --multi ' +
         '(compute-domain uses its own 4-worker cluster shape)')

gpu_profile_raw = cfg.get('gpu-profile', None)

if with_compute_domain and gpu_profile_raw != None:
    fail('--compute-domain forces gpu.profile=gb200; do not pass --gpu-profile explicitly')

gpu_profile = gpu_profile_raw or 'a100'

# Default kubectl context matches the cluster name the Makefile creates.
# PROFILE=compute-domain → nvml-mock-compute-domain, otherwise gpu-test.
k8s_context_default = 'kind-nvml-mock-compute-domain' if with_compute_domain else 'kind-gpu-test'
k8s_context         = cfg.get('k8s-context', k8s_context_default)

# --- Derived state -------------------------------------------------------
# Ordered list of consumers active in this session. Drives (1) per-consumer
# nvml-mock overlay files that the mock installer picks up, and (2) shared
# nvidia helm-repo labeling in the Tilt UI.
# Note: compute-domain is a scenario, not a consumer — it doesn't append
# itself here. The scenario's install() explicitly passes its own values.
active_consumers = []

if with_gpu_operator:
    active_consumers.append('gpu-operator')

if with_dra:
    active_consumers.append('dra')

# --- Safety guard --------------------------------------------------------
allow_k8s_contexts(k8s_context)

# --- Base install: nvml-mock stack --------------------------------------
# Compute-domain owns image build and helm install itself (see
# local/compute-domain/compute_domain.tiltfile). In the non-scenario
# path, nvml_mock.tiltfile owns them.
if with_compute_domain:
    compute_domain_build_images(with_dra)
    nvml_mock_releases = compute_domain_install(active_consumers)
elif multi:
    build_nvml_mock_image()
    nvml_mock_releases = install_fleet(active_consumers)
else:
    build_nvml_mock_image()
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
    # --compute-domain --dra composition: (1) layer the compute-domain
    # overlay values on top of dra-driver.values.yaml to flip
    # resources.computeDomains.enabled, and (2) route the daemon image
    # through image_deps + image_keys so Tilt actually builds it (a
    # docker_build with no manifest reference is pruned) and injects it
    # as the chart's image.repository/tag.
    dra_extra_values = []
    dra_image_deps   = []
    dra_image_keys   = []

    if with_compute_domain:
        dra_extra_values = ['local/compute-domain/dra-driver.values.yaml']
        dra_image_deps   = [compute_domain_daemon_image]
        dra_image_keys   = [('image.repository', 'image.tag')]

    dra_install(
      nvml_mock_releases,
      extra_values=dra_extra_values,
      image_deps=dra_image_deps,
      image_keys=dra_image_keys,
    )

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
