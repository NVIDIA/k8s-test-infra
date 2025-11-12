{{/*
Expand the name of the chart.
*/}}
{{- define "gpu-mock.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
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
Mock driver labels
*/}}
{{- define "gpu-mock.mockDriverLabels" -}}
{{ include "gpu-mock.labels" . }}
app.kubernetes.io/component: mock-driver
{{- end }}

{{/*
Mock driver selector labels
*/}}
{{- define "gpu-mock.mockDriverSelectorLabels" -}}
{{ include "gpu-mock.selectorLabels" . }}
app.kubernetes.io/component: mock-driver
{{- end }}

{{/*
Container toolkit labels
*/}}
{{- define "gpu-mock.toolkitLabels" -}}
{{ include "gpu-mock.labels" . }}
app.kubernetes.io/component: container-toolkit
{{- end }}

{{/*
Container toolkit selector labels
*/}}
{{- define "gpu-mock.toolkitSelectorLabels" -}}
{{ include "gpu-mock.selectorLabels" . }}
app.kubernetes.io/component: container-toolkit
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "gpu-mock.serviceAccountName" -}}
{{- if .Values.global.serviceAccount.create }}
{{- default (include "gpu-mock.fullname" .) .Values.global.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.global.serviceAccount.name }}
{{- end }}
{{- end }}
