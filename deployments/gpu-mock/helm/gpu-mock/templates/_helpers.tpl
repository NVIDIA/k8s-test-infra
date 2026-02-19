{{/* Copyright 2026 NVIDIA CORPORATION */}}
{{/* SPDX-License-Identifier: Apache-2.0 */}}

{{/*
Expand the name of the chart.
*/}}
{{- define "gpu-mock.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "gpu-mock.fullname" -}}
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
{{- define "gpu-mock.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "gpu-mock.labels" -}}
helm.sh/chart: {{ include "gpu-mock.chart" . }}
{{ include "gpu-mock.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "gpu-mock.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gpu-mock.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
GPU configuration helper.
Returns the GPU profile configuration YAML content.
Priority: customConfig > profile file lookup > fail with error.
*/}}
{{- define "gpu-mock.gpuConfig" -}}
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
{{- else }}
{{- fail (printf "Unknown GPU profile %q. Supported profiles: a100, h100, b200, gb200. Or set gpu.customConfig with inline YAML." .Values.gpu.profile) }}
{{- end }}
{{- end }}
