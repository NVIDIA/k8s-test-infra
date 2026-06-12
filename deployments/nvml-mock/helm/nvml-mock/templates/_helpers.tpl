{{/* Copyright 2026 NVIDIA CORPORATION */}}
{{/* SPDX-License-Identifier: Apache-2.0 */}}

{{/*
Expand the name of the chart.
*/}}
{{- define "nvml-mock.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "nvml-mock.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "nvml-mock.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "nvml-mock.labels" -}}
helm.sh/chart: {{ include "nvml-mock.chart" . }}
{{ include "nvml-mock.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "nvml-mock.selectorLabels" -}}
app.kubernetes.io/name: {{ include "nvml-mock.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
GPU configuration helper.
Returns the GPU profile configuration YAML content.
Priority: customConfig > profile file lookup > fail with error.

When .Values.gpu.dynamicMetrics.enabled is true, a `dynamic_metrics:`
block is injected under `device_defaults:` in the resulting YAML so that
the mock returns time-varying temperature/power/utilization readings.

When .Values.gpu.failureInjection.enabled is true, a `failure:` block is
injected under `device_defaults:` so consumers can test how device-plugin,
GPU operator and monitoring stacks behave when GPUs go lost / fall off
the bus / accumulate uncorrectable ECC errors.

If neither overlay is enabled the base YAML is returned verbatim, which
preserves comments and key order from the profile file.
*/}}
{{- define "nvml-mock.gpuConfig" -}}
{{- $base := include "nvml-mock.gpuConfigBase" . -}}
{{- $dynEnabled := and .Values.gpu.dynamicMetrics .Values.gpu.dynamicMetrics.enabled -}}
{{- $failEnabled := and .Values.gpu.failureInjection .Values.gpu.failureInjection.enabled -}}
{{- if or $dynEnabled $failEnabled -}}
{{- $cfg := fromYaml $base -}}
{{- if hasKey $cfg "Error" -}}
{{- fail (printf "nvml-mock.gpuConfig: failed to parse base YAML for overlay injection: %s" (get $cfg "Error")) -}}
{{- end -}}
{{- $defaults := get $cfg "device_defaults" | default (dict) -}}
{{- if $dynEnabled -}}
{{- $_ := set $defaults "dynamic_metrics" (omit .Values.gpu.dynamicMetrics "enabled") -}}
{{- end -}}
{{- if $failEnabled -}}
{{- $_ := set $defaults "failure" (omit .Values.gpu.failureInjection "enabled") -}}
{{- end -}}
{{- $_ := set $cfg "device_defaults" $defaults -}}
{{- toYaml $cfg -}}
{{- else -}}
{{- $base -}}
{{- end -}}
{{- end }}

{{/*
Internal: return the raw base GPU config (customConfig or profile file),
without any dynamic-metrics overlay. Kept as its own helper so the main
gpuConfig template can either return this verbatim (preserving comments
and key order) or round-trip it through fromYaml/toYaml for overlays.
*/}}
{{- define "nvml-mock.gpuConfigBase" -}}
{{- if .Values.gpu.customConfig }}
{{- .Values.gpu.customConfig }}
{{- else if eq .Values.gpu.profile "a100" }}
{{- .Files.Get "profiles/a100.yaml" }}
{{- else if eq .Values.gpu.profile "h100" }}
{{- .Files.Get "profiles/h100.yaml" }}
{{- else if eq .Values.gpu.profile "b200" }}
{{- .Files.Get "profiles/b200.yaml" }}
{{- else if eq .Values.gpu.profile "gb200" }}
{{- .Files.Get "profiles/gb200.yaml" }}
{{- else if eq .Values.gpu.profile "gb300" }}
{{- .Files.Get "profiles/gb300.yaml" }}
{{- else if eq .Values.gpu.profile "l40s" }}
{{- .Files.Get "profiles/l40s.yaml" }}
{{- else if eq .Values.gpu.profile "t4" }}
{{- .Files.Get "profiles/t4.yaml" }}
{{- else }}
{{- fail (printf "Unknown GPU profile %q. Supported profiles: a100, h100, b200, gb200, gb300, l40s, t4. Or set gpu.customConfig with inline YAML." .Values.gpu.profile) }}
{{- end }}
{{- end }}

{{/*
Return "true" when the rendered GPU config enables InfiniBand.
The chart uses the same config content that is mounted into the pod, so built-in
profiles and gpu.customConfig follow one source of truth.
*/}}
{{- define "nvml-mock.infinibandEnabled" -}}
{{- $cfg := fromYaml (include "nvml-mock.gpuConfig" .) -}}
{{- if hasKey $cfg "Error" -}}
{{- fail (printf "nvml-mock.infinibandEnabled: failed to parse GPU config: %s" (get $cfg "Error")) -}}
{{- end -}}
{{- $ib := get $cfg "infiniband" | default (dict) -}}
{{- if and (kindIs "map" $ib) (eq (toString (get $ib "enabled")) "true") -}}
true
{{- else -}}
false
{{- end -}}
{{- end }}

{{/*
Return "true" when the fake nvidia-fabricmanager daemon should run.
Single source of truth is the GPU profile. Every NVSwitch-based platform runs
a fabric manager on real hardware (A100/H100/B200 8-GPU baseboards via the
legacy nv-fabricmanager daemon; GB200/GB300 NVL racks via NMX-C/NMX-T), so the
daemon must run whenever the profile declares NVSwitches (nvlink.switches).
Hopper+/NVL profiles additionally set device_defaults.fabric.state: auto to
couple each GPU's GpuFabricInfo registration to the readiness marker; A100
(Ampere) has NVSwitches but no GpuFabricInfo API, so it runs the daemon
without an auto state. Standalone B200 and L40S/T4 declare no NVSwitches and
do not start the daemon (negative controls).

fabricmanager.enabled is a tri-state override: leave it empty/unset to derive
from the profile (the normal path, so CI/users never restate per-profile
facts); set it to true/false to force the daemon on or off regardless of
profile (e.g. a custom config, or disabling it on an auto profile).
*/}}
{{- define "nvml-mock.fabricmanagerEnabled" -}}
{{- $fm := .Values.fabricmanager | default dict -}}
{{- $override := get $fm "enabled" -}}
{{- if and (not (kindIs "invalid" $override)) (ne (toString $override) "") -}}
{{- ternary "true" "false" (eq (toString $override) "true") -}}
{{- else -}}
{{- $cfg := fromYaml (include "nvml-mock.gpuConfig" .) -}}
{{- if hasKey $cfg "Error" -}}
{{- fail (printf "nvml-mock.fabricmanagerEnabled: failed to parse GPU config: %s" (get $cfg "Error")) -}}
{{- end -}}
{{- $dd := get $cfg "device_defaults" | default (dict) -}}
{{- $fabric := dict -}}
{{- if kindIs "map" $dd -}}{{- $fabric = get $dd "fabric" | default (dict) -}}{{- end -}}
{{- $state := "" -}}
{{- if kindIs "map" $fabric -}}{{- $state = get $fabric "state" | default "" | toString -}}{{- end -}}
{{- $autoState := eq (lower $state) "auto" -}}
{{- $nvlink := dict -}}
{{- if kindIs "map" $cfg -}}{{- $nvlink = get $cfg "nvlink" | default (dict) -}}{{- end -}}
{{- $switches := list -}}
{{- if kindIs "map" $nvlink -}}{{- $switches = get $nvlink "switches" | default (list) -}}{{- end -}}
{{- $hasSwitches := gt (len $switches) 0 -}}
{{- ternary "true" "false" (or $autoState $hasSwitches) -}}
{{- end -}}
{{- end }}

{{/*
Resolve the effective GPU count.
Single source of truth is the GPU profile: the length of its `devices:` list
(t4 -> 4, the NVSwitch baseboards/NVL slices -> 8). The engine mirrors this
(config.go defaults NumDevices to len(devices)). gpu.count is an override:
leave it empty/unset to use the profile's device count (the normal path, so
CI/users never restate a per-profile fact); set it to a positive integer to
force a smaller (or larger) slice regardless of profile. setup.sh still caps
the effective count to the profile device count at runtime.
*/}}
{{- define "nvml-mock.gpuCount" -}}
{{- $count := .Values.gpu.count -}}
{{- if and (not (kindIs "invalid" $count)) (ne (toString $count) "") -}}
{{- $count -}}
{{- else -}}
{{- $cfg := fromYaml (include "nvml-mock.gpuConfig" .) -}}
{{- if hasKey $cfg "Error" -}}
{{- fail (printf "nvml-mock.gpuCount: failed to parse GPU config: %s" (get $cfg "Error")) -}}
{{- end -}}
{{- $devices := get $cfg "devices" | default (list) -}}
{{- if gt (len $devices) 0 -}}
{{- len $devices -}}
{{- else -}}
8
{{- end -}}
{{- end -}}
{{- end }}

{{/*
Resolve the effective MOCK_IB tier: one of "off", "sysfs", or "full".
Honors an explicit .Values.infiniband.mockTier override (validated here so a
typo fails the render, not silently disables IB); when empty/unset it derives
from the profile — "full" when InfiniBand is enabled, "sysfs" otherwise.
Non-IB profiles use "sysfs" (not "off") so the libibmocksys redirect stays
active and masks any real InfiniBand the host exposes (e.g. a CI runner with
mlx5 hardware), matching the behavior expected by validate-ibstat (0 HCAs).
*/}}
{{- define "nvml-mock.mockIBTier" -}}
{{- $ib := .Values.infiniband | default dict -}}
{{- $override := get $ib "mockTier" | default "" | toString -}}
{{- if $override -}}
{{- if not (has $override (list "off" "sysfs" "full")) -}}
{{- fail (printf "infiniband.mockTier must be one of off, sysfs, full (got %q)" $override) -}}
{{- end -}}
{{- $override -}}
{{- else -}}
{{- ternary "full" "sysfs" (eq (include "nvml-mock.infinibandEnabled" .) "true") -}}
{{- end -}}
{{- end }}

{{/*
Driver version helper.
Returns the user-provided driverVersion, otherwise reads system.driver_version
from the resolved GPU config (customConfig or the selected profile file) so the
profile stays the single source of truth — the DRIVER_VERSION env (which names
the on-disk libnvidia-ml.so.<ver> in setup.sh) never drifts from the
driver_version the engine reports via NVML. Fails if neither is set.
*/}}
{{- define "nvml-mock.driverVersion" -}}
{{- if .Values.driverVersion -}}
{{- .Values.driverVersion -}}
{{- else -}}
{{- $cfg := fromYaml (include "nvml-mock.gpuConfig" .) -}}
{{- $dv := dig "system" "driver_version" "" $cfg -}}
{{- if $dv -}}
{{- $dv -}}
{{- else -}}
{{- fail (printf "GPU config for profile %q has no system.driver_version; set .Values.driverVersion explicitly." .Values.gpu.profile) -}}
{{- end -}}
{{- end -}}
{{- end }}
