# nvml-mock

Mock NVIDIA driver infrastructure for Kubernetes testing

This page is generated from `values.yaml`. For installation guides and
operational behaviour, see the [Helm chart guide](helm-chart.md).

## Requirements

Kubernetes: `>= 1.28.0-0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| allocationWatcher.enabled | bool | `false` |  |
| allocationWatcher.interval | string | `"2s"` |  |
| allocationWatcher.podResourcesSocket | string | `"/var/lib/kubelet/pod-resources/kubelet.sock"` |  |
| allocationWatcher.resources.limits.cpu | string | `"100m"` |  |
| allocationWatcher.resources.limits.memory | string | `"64Mi"` |  |
| allocationWatcher.resources.requests.cpu | string | `"10m"` |  |
| allocationWatcher.resources.requests.memory | string | `"32Mi"` |  |
| allocationWatcher.usedFractionPerClaim | float | `0.5` |  |
| controlPlane.enabled | bool | `false` |  |
| controlPlane.image.allowMutableTag | bool | `false` |  |
| controlPlane.image.digest | string | `""` |  |
| controlPlane.image.pullPolicy | string | `"IfNotPresent"` |  |
| controlPlane.image.repository | string | `"ghcr.io/nvidia/mokka-control-plane"` |  |
| controlPlane.image.tag | string | `""` |  |
| controlPlane.kubeAPIBurst | int | `100` |  |
| controlPlane.kubeAPIQPS | int | `50` |  |
| controlPlane.leaderElection.name | string | `"control-plane.mokka.nvidia.com"` |  |
| controlPlane.logging.format | string | `"json"` |  |
| controlPlane.logging.level | string | `"info"` |  |
| controlPlane.nodeSelector | object | `{}` |  |
| controlPlane.replicas | int | `1` |  |
| controlPlane.resources.limits.cpu | string | `"500m"` |  |
| controlPlane.resources.limits.memory | string | `"1Gi"` |  |
| controlPlane.resources.requests.cpu | string | `"50m"` |  |
| controlPlane.resources.requests.memory | string | `"64Mi"` |  |
| controlPlane.service.port | int | `8080` |  |
| controlPlane.service.type | string | `"ClusterIP"` |  |
| controlPlane.shutdownTimeout | string | `"5s"` |  |
| controlPlane.terminationGracePeriodSeconds | int | `30` |  |
| controlPlane.tolerations | list | `[]` |  |
| controlPlane.workers | int | `2` |  |
| driverVersion | string | `""` |  |
| fabricmanager.enabled | string | `""` |  |
| fabricmanager.initDelay | string | `""` |  |
| fabricmanager.stateDir | string | `"/var/lib/nvml-mock/fabric-state"` |  |
| gpu.count | string | `""` |  |
| gpu.customConfig | string | `""` |  |
| gpu.dynamicMetrics.enabled | bool | `false` |  |
| gpu.failureInjection.after_calls | int | `0` |  |
| gpu.failureInjection.enabled | bool | `false` |  |
| gpu.failureInjection.mode | string | `"healthy"` |  |
| gpu.failureInjection.probability | float | `0` |  |
| gpu.failureInjection.seed | int | `0` |  |
| gpu.failureInjection.xid.code | int | `0` |  |
| gpu.profile | string | `"gb300"` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.repository | string | `"ghcr.io/nvidia/nvml-mock"` |  |
| image.tag | string | `"latest"` |  |
| imex.mockChannels.capsMajor | int | `236` |  |
| imex.mockChannels.channelCount | int | `2048` |  |
| imex.mockChannels.channelMajor | int | `235` |  |
| imex.mockChannels.enabled | bool | `false` |  |
| infiniband.mockTier | string | `""` |  |
| infiniband.ping.networkPolicy.enabled | bool | `true` |  |
| infiniband.ping.port | int | `18515` |  |
| integrations.fakeGpuOperator.enabled | bool | `false` |  |
| integrations.fakeGpuOperator.profileLabels."run.ai/gpu-profile" | string | `"true"` |  |
| integrations.fakeGpuOperator.targetNamespace | string | `""` |  |
| nodeAgent.kernelLog.enabled | bool | `false` |  |
| nodeAgent.logging.format | string | `"json"` |  |
| nodeAgent.logging.level | string | `"info"` |  |
| nodeAgent.resources.requests.cpu | string | `"10m"` |  |
| nodeAgent.resources.requests.memory | string | `"32Mi"` |  |
| nodeAgent.shutdownTimeout | string | `"5s"` |  |
| nodeLabels.featuresDir | string | `"/etc/kubernetes/node-feature-discovery/features.d"` |  |
| nodeSelector | object | `{}` |  |
| nri.cdiSpecDir | string | `"/var/run/cdi"` |  |
| nri.deviceAnnotation | string | `"nvml-mock.nvidia.com/devices"` |  |
| nri.deviceInjectionMode | string | `"raw"` |  |
| nri.enabled | bool | `false` |  |
| nri.excludedNamespaces | list | `[]` |  |
| nri.healthPort | int | `8080` |  |
| nri.image | object | `{}` |  |
| nri.imexChannelAnnotation | string | `"nvml-mock.nvidia.com/imex-channels"` |  |
| nri.livenessProbe.failureThreshold | int | `3` |  |
| nri.livenessProbe.httpGet.path | string | `"/healthz"` |  |
| nri.livenessProbe.httpGet.port | string | `"health"` |  |
| nri.livenessProbe.periodSeconds | int | `10` |  |
| nri.livenessProbe.timeoutSeconds | int | `2` |  |
| nri.logging.format | string | `"json"` |  |
| nri.logging.level | string | `"info"` |  |
| nri.optOutAnnotation | string | `"nvml-mock.nvidia.com/inject"` |  |
| nri.overlay.hostPath | string | `"/var/lib/nvml-mock"` |  |
| nri.overlay.mountPath | string | `"/opt/nvml-mock"` |  |
| nri.pluginIndex | string | `"10"` |  |
| nri.pluginName | string | `"nvml-mock"` |  |
| nri.readinessProbe.failureThreshold | int | `2` |  |
| nri.readinessProbe.httpGet.path | string | `"/readyz"` |  |
| nri.readinessProbe.httpGet.port | string | `"health"` |  |
| nri.readinessProbe.periodSeconds | int | `10` |  |
| nri.readinessProbe.timeoutSeconds | int | `2` |  |
| nri.resources | object | `{}` |  |
| nri.socketPath | string | `"/var/run/nri/nri.sock"` |  |
| terminationGracePeriodSeconds | int | `10` |  |
| tolerations[0].operator | string | `"Exists"` |  |
| topology.domains | list | `[]` |  |
| topology.enabled | bool | `false` |  |
| updateStrategy.rollingUpdate.maxUnavailable | string | `"25%"` |  |
| updateStrategy.type | string | `"RollingUpdate"` |  |
