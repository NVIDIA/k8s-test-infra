# nvml-mock

Mock NVIDIA driver infrastructure for Kubernetes testing

This page is generated from `values.yaml`. For installation guides and
operational behaviour, see the [Helm chart guide](helm-chart.md).

## Requirements

Kubernetes: `>= 1.28.0-0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules for scheduling node-agent pods. |
| allocationWatcher.enabled | bool | `false` | Enable allocation-based synthetic memory metrics. |
| allocationWatcher.interval | string | `"2s"` | Poll period for the kubelet pod-resources socket. |
| allocationWatcher.podResourcesSocket | string | `"/var/lib/kubelet/pod-resources/kubelet.sock"` | Kubelet pod-resources socket path. |
| allocationWatcher.resources.limits.cpu | string | `"100m"` | CPU limit for the allocation watcher. |
| allocationWatcher.resources.limits.memory | string | `"64Mi"` | Memory limit for the allocation watcher. |
| allocationWatcher.resources.requests.cpu | string | `"10m"` | CPU request for the allocation watcher. |
| allocationWatcher.resources.requests.memory | string | `"32Mi"` | Memory request for the allocation watcher. |
| allocationWatcher.usedFractionPerClaim | float | `0.5` | Fraction of usable GPU memory attributed to each claim. |
| controlPlane.enabled | bool | `false` | Enable the Mokka control-plane service. |
| controlPlane.image.digest | string | `""` | Immutable digest; when set, it takes precedence over the tag. |
| controlPlane.image.pullPolicy | string | `"IfNotPresent"` | Kubernetes image pull policy for the control plane. |
| controlPlane.image.repository | string | `"ghcr.io/nvidia/mokka-control-plane"` | Control-plane image repository. |
| controlPlane.image.tag | string | `""` | Control-plane image tag; defaults to the chart appVersion. |
| controlPlane.kubeAPIBurst | int | `100` | Kubernetes API burst allowance for the control plane. |
| controlPlane.kubeAPIQPS | int | `50` | Kubernetes API request rate for the control plane. |
| controlPlane.leaderElection.name | string | `"control-plane.mokka.nvidia.com"` | Lease name used for control-plane leader election. |
| controlPlane.logging.format | string | `"json"` | Control-plane log encoding. |
| controlPlane.logging.level | string | `"info"` | Control-plane log level. |
| controlPlane.nodeSelector | object | `{}` | Node selector for control-plane pods. |
| controlPlane.replicas | int | `1` | Number of control-plane replicas. |
| controlPlane.resources.limits.cpu | string | `"500m"` | CPU limit for the control plane. |
| controlPlane.resources.limits.memory | string | `"1Gi"` | Memory limit for the control plane. |
| controlPlane.resources.requests.cpu | string | `"50m"` | CPU requested by the control plane. |
| controlPlane.resources.requests.memory | string | `"64Mi"` | Memory requested by the control plane. |
| controlPlane.service.port | int | `8080` | Kubernetes Service port for the control plane. |
| controlPlane.service.type | string | `"ClusterIP"` | Kubernetes Service type for the control plane. |
| controlPlane.shutdownTimeout | string | `"5s"` | Graceful control-plane shutdown timeout. |
| controlPlane.terminationGracePeriodSeconds | int | `30` | Pod termination grace period for the control plane. |
| controlPlane.tolerations | list | `[]` | Tolerations for control-plane pods. |
| controlPlane.workers | int | `2` | Number of control-plane worker goroutines. |
| driverVersion | string | `""` | Driver version reported by the simulated driver; empty derives it from the profile. |
| extraObjects | list | `[]` | Additional Kubernetes objects rendered with the release after tpl evaluation. |
| fabricmanager.enabled | string | `""` | Enable or disable the fake fabric manager; empty derives it from the profile. |
| fabricmanager.initDelay | string | `""` | Delay before publishing fabric readiness. |
| fabricmanager.stateDir | string | `"/var/lib/nvml-mock/fabric-state"` | Host directory for fabric-manager readiness state. |
| global.imagePullSecrets | list | `[]` | Pull secrets added to every pod created by this chart. |
| global.imageRegistry | string | `""` | Registry host override applied to every image, such as an internal mirror. |
| gpu.count | string | `""` | Number of simulated GPUs; empty derives the count from the selected profile. |
| gpu.customConfig | string | `""` | Complete YAML configuration that replaces the selected GPU profile. |
| gpu.dynamicMetrics.enabled | bool | `false` | Enable time-varying synthetic temperature, power, and utilization values. |
| gpu.failureInjection.after_calls | int | `0` | Trigger the selected failure after this many guarded calls; zero disables it. |
| gpu.failureInjection.enabled | bool | `false` | Enable deterministic or probabilistic simulated GPU failures. |
| gpu.failureInjection.mode | string | `"healthy"` | Failure mode to expose when failure injection is enabled. |
| gpu.failureInjection.probability | float | `0` | Probability that each guarded NVML call triggers the selected failure. |
| gpu.failureInjection.seed | int | `0` | Random seed; zero uses a time-based seed. |
| gpu.failureInjection.xid.code | int | `0` | Xid code reported when the failure mode emits an Xid event. |
| gpu.mig.enabled | bool | `false` | Enable MIG partitioning for profiles that support it. |
| gpu.mig.gpuInstances | list | `[]` | GPU instance layout to create on each simulated device. |
| gpu.profile | string | `"gb300"` | GPU profile and per-device simulation settings. |
| image.digest | string | `""` | Immutable digest; when set, it takes precedence over the tag. |
| image.pullPolicy | string | `"IfNotPresent"` | Kubernetes image pull policy. |
| image.repository | string | `"ghcr.io/nvidia/nvml-mock"` | Container image repository for the node agent. |
| image.tag | string | `""` | Container image tag; defaults to the chart appVersion. |
| imagePullSecrets | list | `[]` | Pull secrets for nvml-mock, NRI, and control-plane pods. |
| imex.mockChannels.capsMajor | int | `236` | Major number used for IMEX capability devices. |
| imex.mockChannels.channelCount | int | `2048` | Number of IMEX channel device nodes to create per node. |
| imex.mockChannels.channelMajor | int | `235` | Major number used for IMEX channel devices. |
| imex.mockChannels.enabled | bool | `false` | Enable simulation of IMEX channel devices. |
| infiniband.mockTier | string | `""` | InfiniBand mock tier; empty derives the tier from the selected profile. mockTier sets the container's MOCK_IB value: off | sysfs | full. Defaults to "full" for InfiniBand-enabled profiles and "sysfs" for non-IB profiles (sysfs keeps the libibmocksys redirect active so any real host IB is masked, i.e. ibstat reports 0 HCAs). Leaving this empty auto-derives that per profile. Set it explicitly to override (an invalid value fails `helm template`). |
| infiniband.ping.networkPolicy.enabled | bool | `true` | Restrict mock InfiniBand traffic to peer nvml-mock pods. |
| infiniband.ping.port | int | `18515` | TCP port used by the mock InfiniBand ping service. |
| integrations.fakeGpuOperator.enabled | bool | `false` | Enable fake-gpu-operator profile integration. |
| integrations.fakeGpuOperator.profileLabels."run.ai/gpu-profile" | string | `"true"` | Additional label applied to generated fake-gpu-operator profiles. |
| integrations.fakeGpuOperator.targetNamespace | string | `""` | Namespace where fake-gpu-operator reads profile ConfigMaps. |
| nodeAgent.kernelLog.enabled | bool | `false` | Announce injected Xids through the host kernel log. TODO: remove the flag and make it on by default. |
| nodeAgent.livenessProbe | object | `{"httpGet":{"path":"/healthz","port":"health"}}` | Liveness probe; /healthz fails when the last Stage wave failed. Set to null to disable. |
| nodeAgent.livenessProbe.httpGet.path | string | `"/healthz"` | Liveness probe endpoint path. |
| nodeAgent.livenessProbe.httpGet.port | string | `"health"` | Liveness probe endpoint port name or number. |
| nodeAgent.logging | object | `{"format":"json","level":"info"}` | Node-agent logging settings and lifecycle behavior. |
| nodeAgent.logging.format | string | `"json"` | Log encoding for the node agent. |
| nodeAgent.logging.level | string | `"info"` | Log level for the node agent. |
| nodeAgent.readinessProbe | object | `{"httpGet":{"path":"/readyz","port":"health"}}` | Readiness probe; /readyz reports whether every simulator is serving. Set to null to disable. |
| nodeAgent.readinessProbe.httpGet.path | string | `"/readyz"` | Readiness probe endpoint path. |
| nodeAgent.readinessProbe.httpGet.port | string | `"health"` | Readiness probe endpoint port name or number. |
| nodeAgent.resources | object | `{"requests":{"cpu":"10m","memory":"32Mi"}}` | Resource requests for the node agent. |
| nodeAgent.resources.requests.cpu | string | `"10m"` | CPU requested by the node agent. |
| nodeAgent.resources.requests.memory | string | `"32Mi"` | Memory requested by the node agent. |
| nodeAgent.shutdownTimeout | string | `"5s"` | Graceful shutdown timeout for the node agent. |
| nodeLabels.featuresDir | string | `"/etc/kubernetes/node-feature-discovery/features.d"` | Host directory from which NFD reads generated feature files. |
| nodeSelector | object | `{}` | Existing node labels required to schedule the mock DaemonSet. |
| nri.cdiSpecDir | string | `"/var/run/cdi"` | Host directory containing staged CDI specifications. |
| nri.deviceAnnotation | string | `"nvml-mock.nvidia.com/devices"` | Pod annotation enabling mock GPU device injection. |
| nri.deviceInjectionMode | string | `"raw"` | Device injection mode: raw nodes or CDI references. |
| nri.enabled | bool | `false` | Enable node-wide container injection through the NRI plugin. |
| nri.excludedNamespaces | list | `[]` | Namespaces excluded from NRI injection. |
| nri.healthPort | int | `8080` | NRI plugin health and readiness port. |
| nri.image | object | `{}` | Optional container image overrides for the NRI plugin. |
| nri.imexChannelAnnotation | string | `"nvml-mock.nvidia.com/imex-channels"` | Pod annotation enabling IMEX channel injection. |
| nri.livenessProbe.failureThreshold | int | `3` | Consecutive failures before the plugin is restarted. |
| nri.livenessProbe.httpGet.path | string | `"/healthz"` | Liveness endpoint path. |
| nri.livenessProbe.httpGet.port | string | `"nri-health"` | Liveness endpoint port name. |
| nri.livenessProbe.periodSeconds | int | `10` | Liveness probe interval. |
| nri.livenessProbe.timeoutSeconds | int | `2` | Liveness probe timeout. |
| nri.logging.format | string | `"json"` | Log encoding for the NRI plugin. |
| nri.logging.level | string | `"info"` | Log level for the NRI plugin. |
| nri.optOutAnnotation | string | `"nvml-mock.nvidia.com/inject"` | Pod annotation that disables ambient mock injection when set to "false". |
| nri.overlay.hostPath | string | `"/var/lib/nvml-mock"` | Host path containing the staged mock driver overlay. |
| nri.overlay.mountPath | string | `"/opt/nvml-mock"` | Container mount path for the mock driver overlay. |
| nri.pluginIndex | string | `"10"` | NRI plugin ordering index. |
| nri.pluginName | string | `"nvml-mock"` | NRI plugin name. |
| nri.readinessProbe.failureThreshold | int | `2` | Consecutive failures before the plugin is marked unready. |
| nri.readinessProbe.httpGet.path | string | `"/readyz"` | Readiness endpoint path. |
| nri.readinessProbe.httpGet.port | string | `"nri-health"` | Readiness endpoint port name. |
| nri.readinessProbe.periodSeconds | int | `10` | Readiness probe interval. |
| nri.readinessProbe.timeoutSeconds | int | `2` | Readiness probe timeout. |
| nri.resources | object | `{}` | Additional NRI plugin resources. |
| nri.socketPath | string | `"/var/run/nri/nri.sock"` | Runtime NRI socket path. |
| podAnnotations | object | `{}` | Additional annotations applied to node-agent pods. |
| podLabels | object | `{}` | Additional labels for node-agent pods; do not set app.kubernetes.io selector labels. |
| priorityClassName | string | `""` | Priority class for node-agent DaemonSet pods. |
| terminationGracePeriodSeconds | int | `10` | Pod termination grace period for the node-agent DaemonSet. |
| tolerations | list | `[{"operator":"Exists"}]` | Pod tolerations for the mock DaemonSet. |
| topology.domains | list | `[]` | Synthetic topology domains and clique membership. |
| topology.enabled | bool | `false` | Enable simulation of multi-node GPU fabric topology. |
| updateStrategy.rollingUpdate.maxUnavailable | string | `"25%"` | Maximum number of DaemonSet pods unavailable during an update. |
| updateStrategy.type | string | `"RollingUpdate"` | DaemonSet update strategy. |
