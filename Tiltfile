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
#   2. Add config.define_bool('with-<name>', ...) below.
#   3. Add a load(...) for the new tiltfile.
#   4. Add `if with_<name>: active_consumers.append('<name>')`.
#   5. Add `if with_<name>: <name>_install(nvml_mock_releases)`.
# The nvml-mock stack itself needs no changes to add a consumer.
# Optional: drop local/<name>/nvml-mock.values.yaml if the consumer
# needs mock-side tweaks (MOCK_* env vars, gpu.count overrides, etc.).
# The install helpers detect the file's presence via os.path.exists.
#
# Adding a new scenario (e.g. compute-domain):
#   Scenarios reshape the nvml-mock stack itself — different image, forced
#   profile, dedicated cluster topology — so they don't fit the additive
#   consumer contract above. See local/compute-domain/compute_domain.tiltfile
#   for the pattern: exports build_images() + install() the way
#   local/nvml_mock.tiltfile does, and the orchestrator calls them as the
#   base install path instead of build_nvml_mock_image() + install_single().

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
     observability_gpu_operator_values='GPU_OPERATOR_VALUES')

# --- Flags ---------------------------------------------------------------
config.define_string('gpu-profile', args=False,
    usage='GPU profile to simulate (used unless --multi-gpu-profile is set): a100 | h100 | b200 | gb200 | gb300 | l40s | t4')
config.define_string('k8s-context', args=False,
    usage='kubectl context to deploy into (must be a local cluster)')
# Boolean toggles use config.define_bool so `--multi-gpu-profile --gpu-operator
# --dra` parses as three separate flags without positional-value
# ambiguity that Tilt's string-flag parser can exhibit for `=X` forms.
config.define_bool('multi-gpu-profile', args=False,
    usage='Install one nvml-mock release per GPU profile, node-pinned via nodeSelector.kubernetes.io/hostname=<worker>. Simulates a heterogeneous fleet on the default cluster (a100 on worker-0 + t4 on worker-1). Without this flag, a single nvml-mock release covers all nodes with the profile from --gpu-profile.')
config.define_bool('compute-domain', args=False,
    usage='ComputeDomain scenario: 4-worker cluster with GB200 profile + NVLink topology overlay (requires PROFILE=compute-domain cluster)')
config.define_bool('gpu-operator', args=False,
    usage='Also deploy NVIDIA GPU Operator on top of nvml-mock')
config.define_bool('mig', args=False,
    usage='Boot every board MIG-partitioned and serve the partitions through the GPU Operator. Implies --gpu-operator. Needs a MIG-capable --gpu-profile: a100 | h100 | b200 | gb200 | gb300.')
config.define_string('mig-strategy', args=False,
    usage='With --mig: single (default) carves each board into seven of its smallest slice, published as nvidia.com/gpu; mixed carves one 1g, 2g and 3g slice each, published as nvidia.com/mig-<profile>.')
config.define_bool('dra', args=False,
    usage='Also deploy NVIDIA DRA driver on top of nvml-mock')
config.define_bool('fgo', args=False,
    usage='Also deploy Run:ai Fake GPU Operator (combine with --multi-gpu-profile to exercise both integration and scale pools)')
config.define_bool('topograph', args=False,
    usage='Also deploy NVIDIA topograph. Implies --compute-domain (topograph reads the static nvidia.com/gpu.clique labels). Still requires the compute-domain Kind cluster: make cluster-create PROFILE=compute-domain.')
config.define_bool('observability', args=False,
    usage='Also deploy kube-prometheus-stack + a Grafana dashboard over the mock GPUs, and expose two manual fault-injection triggers (inject-thermal, inject-xid) that assert the fault lands in Prometheus. Implies --gpu-operator (dcgm-exporter is the Operator\'s operand). Grafana on http://localhost:3000/d/mokka-gpu (admin/mokka).')
config.define_bool('control-plane', args=False,
    usage='Also deploy the Mokka Control Plane (MEP-0001) alongside nvml-mock. Off by default. Composes with --multi-gpu-profile (the first profile release owns the single CP), --compute-domain, and --nvmlmock-image.')
# CI hook: hand Tilt a pre-built image (in CI, loaded from the workflow's image
# artifact) instead of running docker_build. When set, docker_build is skipped
# and the nvml-mock chart's image.repository / image.tag are pinned via --set
# to the parsed <repo>/<tag>. Ref must be in `repo:tag` or `repo@digest` form.
config.define_string('nvmlmock-image', args=False,
    usage='Pre-built nvml-mock image ref (repo:tag). Skips docker_build and pins the chart image.repository/image.tag via --set. Used by CI to consume the image the build-nvmlmock-image job uploads as an artifact.')

cfg = config.parse()

nvmlmock_image      = cfg.get('nvmlmock-image', '')

multi_gpu_profile   = cfg.get('multi-gpu-profile', False)
with_compute_domain = cfg.get('compute-domain', False)
with_gpu_operator   = cfg.get('gpu-operator', False)
with_mig            = cfg.get('mig', False)
with_dra            = cfg.get('dra', False)
with_fgo            = cfg.get('fgo', False)
with_topograph      = cfg.get('topograph', False)
with_observability  = cfg.get('observability', False)
with_control_plane  = cfg.get('control-plane', False)

# --- Implicit flags ------------------------------------------------------
# --topograph implies --compute-domain: cliques only exist in the
# compute-domain cluster (local/kind/compute-domain.kind.yaml carries
# nvidia.com/gpu.clique labels as static node labels; topograph reads
# them without requiring the DRA driver). The compute-domain Kind
# cluster is still required (make cluster-create PROFILE=compute-domain).
if with_topograph:
    with_compute_domain = True

# --observability implies --gpu-operator: the thing Prometheus scrapes is
# dcgm-exporter, which is a GPU Operator operand. Without the Operator the
# stack installs cleanly and every GPU panel stays empty, so implying the
# flag is friendlier than failing on it.
if with_observability:
    with_gpu_operator = True

# --mig implies --gpu-operator: the partitions are served by the Operator's
# device plugin, and a partitioned node with no plugin advertises nothing.
if with_mig:
    with_gpu_operator = True

# --- Guardrails ----------------------------------------------------------
# compute-domain forces its own cluster shape (4 workers with clique
# labels, hardcoded worker names in topology.yaml) and its own profile
# (gb200 for NVLink5 fabric APIs), so it cannot compose with --multi-gpu-profile
# or with any --gpu-profile the user might pass. --gpu-operator is
# allowed but experimental — the Operator's RuntimeClass path with the
# compute-domain-imex layered image is untested.
if with_compute_domain and multi_gpu_profile:
    fail('--compute-domain is mutually exclusive with --multi-gpu-profile ' +
         '(compute-domain uses its own 4-worker cluster shape)')

if with_fgo and with_gpu_operator:
    fail('--fgo is mutually exclusive with --gpu-operator (FGO replaces the GPU Operator)')
if with_fgo and with_compute_domain:
    fail('--fgo is mutually exclusive with --compute-domain')

# --nvmlmock-image only wires the standard nvml-mock build/install path. The
# compute-domain scenario builds three layered images (base + imex + optional
# daemon) and cannot consume a single pre-built ref.
if nvmlmock_image and with_compute_domain:
    fail('--nvmlmock-image is not supported with --compute-domain (scenario builds its own layered images)')

gpu_profile_raw = cfg.get('gpu-profile', None)

if with_compute_domain and gpu_profile_raw != None:
    fail('--compute-domain forces gpu.profile=gb200; do not pass --gpu-profile explicitly')

gpu_profile = gpu_profile_raw or 'a100'

# Per-board layout for each device plugin MIG strategy, as (profile, count).
# single fills the board seven times over with its smallest slice, since that
# strategy rejects a node whose partitions do not all share one profile. mixed
# takes one each of the 1g, 2g and 3g slices — six of the seven compute slices,
# placed without overlap — so every partition is published under a resource
# name of its own.
_MIG_LAYOUTS = {
    'a100':  {'single': [('1g.5gb', 7)],  'mixed': [('1g.5gb', 1),  ('2g.10gb', 1), ('3g.20gb', 1)]},
    'h100':  {'single': [('1g.10gb', 7)], 'mixed': [('1g.10gb', 1), ('2g.20gb', 1), ('3g.40gb', 1)]},
    'b200':  {'single': [('1g.23gb', 7)], 'mixed': [('1g.23gb', 1), ('2g.45gb', 1), ('3g.90gb', 1)]},
    'gb200': {'single': [('1g.23gb', 7)], 'mixed': [('1g.23gb', 1), ('2g.47gb', 1), ('3g.93gb', 1)]},
    'gb300': {'single': [('1g.35gb', 7)], 'mixed': [('1g.35gb', 1), ('2g.70gb', 1), ('3g.139gb', 1)]},
}

mig_strategy = cfg.get('mig-strategy', 'single')

nvml_mock_mig_set = []
if with_mig:
    # The fleet pins t4, which cannot partition, and compute-domain brings its
    # own mock stack and cluster shape.
    if multi_gpu_profile or with_compute_domain:
        fail('--mig is mutually exclusive with --multi-gpu-profile and --compute-domain')
    if gpu_profile not in _MIG_LAYOUTS:
        fail('--mig needs a MIG-capable --gpu-profile (%s); %s cannot partition' %
             (' | '.join(_MIG_LAYOUTS.keys()), gpu_profile))
    if mig_strategy not in _MIG_LAYOUTS[gpu_profile]:
        fail('--mig-strategy must be one of: single | mixed (got %s)' % mig_strategy)
    nvml_mock_mig_set = ['gpu.mig.enabled=true']
    for i, (profile, count) in enumerate(_MIG_LAYOUTS[gpu_profile][mig_strategy]):
        nvml_mock_mig_set += [
            'gpu.mig.gpuInstances[%d].profile=%s' % (i, profile),
            'gpu.mig.gpuInstances[%d].count=%d' % (i, count),
        ]
elif cfg.get('mig-strategy', None) != None:
    fail('--mig-strategy has no effect without --mig')

k8s_context_default = 'kind-mokka-compute-domain' if with_compute_domain else 'kind-mokka'
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

if with_fgo:
    active_consumers.append('fgo')

if with_topograph:
    active_consumers.append('topograph')

# Appended so local/observability/nvml-mock.values.yaml is picked up by the
# install helpers — it turns on gpu.dynamicMetrics, without which every
# dashboard panel is a flat line of profile constants.
if with_observability:
    active_consumers.append('observability')

# --- Safety guard --------------------------------------------------------
allow_k8s_contexts(k8s_context)

# --- Base install: nvml-mock stack --------------------------------------
# Compute-domain owns image build and helm install itself (see
# local/compute-domain/compute_domain.tiltfile). In the non-scenario
# path, nvml_mock.tiltfile owns them.
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
        extra_set=nvml_mock_mig_set,
    )

# --- Shared NVIDIA Helm repo --------------------------------------------
# Both consumer subfiles pull from nvidia/... — register the repo once here so
# each subfile can stay agnostic about who else uses it. Labels are attached
# per active consumer so the repo groups next to whichever consumers are on.
if active_consumers:
    helm_repo('nvidia', 'https://helm.ngc.nvidia.com/nvidia', labels=active_consumers)

if with_topograph:
    helm_repo('topograph-repo', 'https://dsx-ai-factory.github.io/topograph', labels=['topograph'])

if with_observability:
    helm_repo('prometheus-community', 'https://prometheus-community.github.io/helm-charts',
              labels=['observability'])

# --- Consumers -----------------------------------------------------------
# Monitoring goes in BEFORE the GPU Operator: kube-prometheus-stack ships the
# ServiceMonitor CRD, and the Operator's chart creates a ServiceMonitor for
# dcgm-exporter once the observability overlay re-enables it. The ordering is
# carried by resource_deps below rather than by these call sites, so moving
# either block cannot silently break it.
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
        mig_strategy=mig_strategy if with_mig else '',
    )

if with_dra:
    # DRA overlay chain (order matters — later --values files win):
    # - --compute-domain: (1) layer the compute-domain overlay values on
    #   top of dra-driver.values.yaml to flip resources.computeDomains.
    #   enabled, and (2) route the daemon image through image_deps +
    #   image_keys so Tilt actually builds it (a docker_build with no
    #   manifest reference is pruned) and injects it as the chart's
    #   image.repository/tag.
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

# --- Test workload -------------------------------------------------------
# GPU validator pod, disabled by default (enable from the Tilt UI). Requests
# one mock GPU, so the device plugin must have registered nvidia.com/gpu
# before the pod can start. Pod spec lives in local/gpu-validator.k8s.yaml so
# it can be kubectl-applied standalone or edited without touching Starlark.
if with_gpu_operator:
    k8s_yaml('local/gpu-validator.k8s.yaml')
    k8s_resource('gpu-validator',
        auto_init=False,
        resource_deps=nvml_mock_releases,
        labels=['test'],
    )
