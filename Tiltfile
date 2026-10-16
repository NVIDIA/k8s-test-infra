# Copyright 2026 NVIDIA CORPORATION
# SPDX-License-Identifier: Apache-2.0
#
# Conventions for a consumer, local/<name>/<name>.tiltfile:
#   - install(nvml_mock_releases) does the work; load() has no side effects.
#   - local/<name>/nvml-mock.values.yaml, if present, is layered into the mock while
#     the consumer is active (MOCK_* env vars, gpu.count, a gpu.profile it cannot
#     work without).
#   - If its helm --wait outlasts Tilt's 30s apply timeout it exports
#     UPSERT_TIMEOUT_S, which upsert_timeouts below collects. It never calls
#     update_settings: that is session-wide and the last call wins.
#   - Chart versions are pinned, so an upstream release cannot reach CI without a
#     commit. Bump deliberately.

load('ext://helm_resource', 'helm_repo')
load('./local/nvml_mock.tiltfile',
     'build_nvml_mock_image',
     'build_control_plane_image',
     'install_control_plane_crds',
     'install_single',
     'install_fleet')
load('./local/compute-domain/compute_domain.tiltfile',
     compute_domain_build_images='build_images',
     compute_domain_install='install',
     compute_domain_daemon_image='DAEMON_IMAGE')
load('./local/gpu-operator/gpu_operator.tiltfile', gpu_operator_install='install')
load('./local/dra/dra.tiltfile', dra_install='install')
load('./local/fgo/fgo.tiltfile', fgo_install='install')
load('./local/topograph/topograph.tiltfile', topograph_install='install')
load('./local/observability/observability.tiltfile',
     observability_install='install',
     observability_gpu_operator_values='GPU_OPERATOR_VALUES',
     observability_upsert_timeout_s='UPSERT_TIMEOUT_S')
load('./local/dynamo/dynamo.tiltfile',
     dynamo_install='install',
     dynamo_upsert_timeout_s='UPSERT_TIMEOUT_S')
load('./local/slinky/slinky.tiltfile',
     slinky_install='install',
     slinky_upsert_timeout_s='UPSERT_TIMEOUT_S')

config.define_string('gpu-profile', args=False,
    usage='GPU profile to simulate (used unless --multi-gpu-profile is set): a100 | h100 | b200 | gb200 | gb300 | l40s | t4. Defaults to the chart\'s gpu.profile.')
config.define_string('k8s-context', args=False,
    usage='kubectl context to deploy into (must be a local cluster)')
# Boolean toggles use config.define_bool so `--multi-gpu-profile --gpu-operator
# --dra` parses as three separate flags without positional-value
# ambiguity that Tilt's string-flag parser can exhibit for `=X` forms.
config.define_bool('multi-gpu-profile', args=False,
    usage='One nvml-mock release per worker, each with a different GPU profile (a100 on worker-0, t4 on worker-1), instead of one release for all nodes. Ignores --gpu-profile.')
config.define_bool('compute-domain', args=False,
    usage='ComputeDomain scenario: GB200 profile with an NVLink topology overlay. Needs the compute-domain cluster (make cluster-create PROFILE=compute-domain).')
config.define_bool('gpu-operator', args=False,
    usage='Also deploy NVIDIA GPU Operator on top of nvml-mock')
config.define_bool('dra', args=False,
    usage='Also deploy NVIDIA DRA driver on top of nvml-mock')
config.define_bool('fgo', args=False,
    usage='Also deploy Run:ai Fake GPU Operator: nvml-mock backs the integration worker pool and FGO simulates the scale pool.')
config.define_bool('topograph', args=False,
    usage='Also deploy topograph (github.com/dsx-ai-factory/topograph). Implies --compute-domain (topograph reads the NVLink cliques the mock reports through nvidia-smi). Still requires the compute-domain Kind cluster: make cluster-create PROFILE=compute-domain.')
config.define_bool('observability', args=False,
    usage='Also deploy kube-prometheus-stack, a Grafana dashboard and two manual fault-injection triggers (inject-thermal, inject-xid) that assert the fault reaches Prometheus. Implies --gpu-operator (dcgm-exporter is one of its operands). Grafana on http://localhost:3000/d/mokka-gpu (admin/mokka).')
config.define_bool('dynamo', args=False,
    usage='Also deploy NVIDIA Dynamo (operator only) and an aggregated DynamoGraphDeployment whose worker runs the mocker engine on a mock GPU, plus a manual dynamo-smoke trigger. Implies --gpu-operator (the worker requests nvidia.com/gpu). OpenAI API on http://localhost:8000.')
config.define_bool('slinky', args=False,
    usage='Also deploy Slinky (cert-manager, slurm-operator, a Slurm cluster) with one slurmd per mock-GPU worker. Implies --gpu-operator (slurmd requests nvidia.com/gpu). Fits any cluster shape; with --compute-domain it gives a 4-node GB200 Slurm cluster.')
config.define_bool('control-plane', args=False,
    usage='Also deploy the Mokka Control Plane (MEP-0001) alongside nvml-mock. Composes with --multi-gpu-profile (the first profile release owns the single control plane), --compute-domain and --nvmlmock-image.')
# CI passes the image its build job produced instead of having Tilt rebuild it.
config.define_string('nvmlmock-image', args=False,
    usage='Pre-built nvml-mock image ref (repo:tag, repo@sha256:<hex> or repo:tag@sha256:<hex>). Skips docker_build and pins the chart image.repository, image.tag and image.digest via --set.')

cfg = config.parse()

nvmlmock_image      = cfg.get('nvmlmock-image', '')

multi_gpu_profile   = cfg.get('multi-gpu-profile', False)
with_compute_domain = cfg.get('compute-domain', False)
with_gpu_operator   = cfg.get('gpu-operator', False)
with_dra            = cfg.get('dra', False)
with_fgo            = cfg.get('fgo', False)
with_topograph      = cfg.get('topograph', False)
with_observability  = cfg.get('observability', False)
with_dynamo         = cfg.get('dynamo', False)
with_slinky         = cfg.get('slinky', False)
with_control_plane  = cfg.get('control-plane', False)

# --topograph needs cliques, which only the compute-domain cluster has. It asks
# the mock for them through nvidia-smi, so neither the DRA driver nor the GPU
# Operator is implied.
if with_topograph:
    with_compute_domain = True

# Observability, Dynamo and Slinky need dcgm-exporter or nvidia.com/gpu, which
# only the GPU Operator provides. Without it they install cleanly and then sit
# idle (empty panels, a Pending worker or slurmd), so the flag is implied rather
# than refused.
if with_observability:
    with_gpu_operator = True

if with_dynamo:
    with_gpu_operator = True

if with_slinky:
    with_gpu_operator = True

# --gpu-operator with --compute-domain is allowed but untested.
if with_compute_domain and multi_gpu_profile:
    fail('--compute-domain is mutually exclusive with --multi-gpu-profile ' +
         '(compute-domain uses its own 4-worker cluster shape)')

if with_fgo and with_gpu_operator:
    fail('--fgo is mutually exclusive with --gpu-operator (FGO replaces the GPU Operator)')
if with_fgo and with_compute_domain:
    fail('--fgo is mutually exclusive with --compute-domain')

if with_slinky and with_dynamo:
    fail('--slinky is mutually exclusive with --dynamo (slurmd claims every mock GPU)')

if nvmlmock_image and with_compute_domain:
    fail('--nvmlmock-image is not supported with --compute-domain (scenario builds its own layered images)')

# Empty unless --gpu-profile is passed: the chart owns the default profile, and a
# --set would also beat every values file, consumer overlays included.
gpu_profile = cfg.get('gpu-profile', '')

if with_compute_domain and gpu_profile:
    fail('--compute-domain forces gpu.profile=gb200; do not pass --gpu-profile explicitly')

k8s_context_default = 'kind-mokka-compute-domain' if with_compute_domain else 'kind-mokka'
k8s_context         = cfg.get('k8s-context', k8s_context_default)

active_consumers = []

if with_gpu_operator:
    active_consumers.append('gpu-operator')

if with_dra:
    active_consumers.append('dra')

if with_fgo:
    active_consumers.append('fgo')

if with_topograph:
    active_consumers.append('topograph')

if with_observability:
    active_consumers.append('observability')

if with_dynamo:
    active_consumers.append('dynamo')

if with_slinky:
    active_consumers.append('slinky')

allow_k8s_contexts(k8s_context)

if with_control_plane:
    build_control_plane_image()
    install_control_plane_crds()

if with_compute_domain:
    compute_domain_build_images(with_dra)
    nvml_mock_releases = compute_domain_install(active_consumers, control_plane=with_control_plane)
elif multi_gpu_profile:
    build_nvml_mock_image(nvmlmock_image=nvmlmock_image)
    nvml_mock_releases = install_fleet(
        active_consumers,
        nvmlmock_image=nvmlmock_image,
        control_plane=with_control_plane,
    )
else:
    build_nvml_mock_image(nvmlmock_image=nvmlmock_image)
    nvml_mock_releases = install_single(
        gpu_profile,
        active_consumers,
        nvmlmock_image=nvmlmock_image,
        control_plane=with_control_plane,
    )

if active_consumers:
    helm_repo('nvidia', 'https://helm.ngc.nvidia.com/nvidia', labels=active_consumers)

if with_topograph:
    helm_repo('topograph-repo', 'https://dsx-ai-factory.github.io/topograph', labels=['topograph'])

if with_observability:
    helm_repo('prometheus-community', 'https://prometheus-community.github.io/helm-charts',
              labels=['observability'])

# Tilt caps a k8s apply at 30s, and a consumer's helm --wait exceeds that: the build
# fails with a bare "apply command timed out" that says nothing about helm.
# update_settings is session-wide and the last call wins, so it is called once,
# with the largest timeout any active consumer needs.
upsert_timeouts = []
if with_observability:
    upsert_timeouts.append(observability_upsert_timeout_s)
if with_dynamo:
    upsert_timeouts.append(dynamo_upsert_timeout_s)
if with_slinky:
    upsert_timeouts.append(slinky_upsert_timeout_s)
if upsert_timeouts:
    update_settings(k8s_upsert_timeout_secs=max(upsert_timeouts))

if with_observability:
    observability_releases = observability_install(nvml_mock_releases)

if with_gpu_operator:
    gpu_operator_extra_values = []
    gpu_operator_extra_deps   = []

    if with_observability:
        gpu_operator_extra_values.append(observability_gpu_operator_values)
        gpu_operator_extra_deps += observability_releases

    gpu_operator_install(
        nvml_mock_releases,
        extra_values=gpu_operator_extra_values,
        extra_resource_deps=gpu_operator_extra_deps,
    )

if with_dra:
    # image_deps is what makes Tilt build the daemon image: a docker_build that
    # nothing references is pruned.
    dra_extra_values = []
    dra_image_deps   = []
    dra_image_keys   = []

    if with_compute_domain:
        dra_extra_values.append('local/compute-domain/dra-driver.values.yaml')
        dra_image_deps.append(compute_domain_daemon_image)
        dra_image_keys.append(('image.repository', 'image.tag'))

    dra_install(
      nvml_mock_releases,
      extra_values=dra_extra_values,
      image_deps=dra_image_deps,
      image_keys=dra_image_keys,
    )

if with_fgo:
    fgo_install(nvml_mock_releases)

if with_topograph:
    topograph_install(nvml_mock_releases)

if with_dynamo:
    dynamo_install(nvml_mock_releases)

if with_slinky:
    slinky_install(nvml_mock_releases)

# Manual: the pod cannot run before the device plugin advertises nvidia.com/gpu.
if with_gpu_operator:
    k8s_yaml('local/gpu-validator.k8s.yaml')
    k8s_resource('gpu-validator',
        auto_init=False,
        resource_deps=nvml_mock_releases,
        labels=['test'],
    )
