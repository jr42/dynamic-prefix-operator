{{/*
Expand the name of the chart.
*/}}
{{- define "dynamic-prefix-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "dynamic-prefix-operator.fullname" -}}
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
{{- define "dynamic-prefix-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "dynamic-prefix-operator.labels" -}}
helm.sh/chart: {{ include "dynamic-prefix-operator.chart" . }}
{{ include "dynamic-prefix-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "dynamic-prefix-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "dynamic-prefix-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "dynamic-prefix-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "dynamic-prefix-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the image reference
*/}}
{{- define "dynamic-prefix-operator.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}

{{/*
Generate watch configuration as JSON for ConfigMap
*/}}
{{- define "dynamic-prefix-operator.watchConfig" -}}
{{- $config := dict }}
{{- $_ := set $config "namespaces" .Values.watch.namespaces }}
{{- $_ := set $config "ciliumLoadBalancerIPPool" .Values.watch.ciliumLoadBalancerIPPool }}
{{- $_ := set $config "ciliumCIDRGroup" .Values.watch.ciliumCIDRGroup }}
{{- $_ := set $config "ingress" .Values.watch.ingress }}
{{- $_ := set $config "service" .Values.watch.service }}
{{- $config | toJson }}
{{- end }}

{{/*
Check if any Cilium resources are being watched
*/}}
{{- define "dynamic-prefix-operator.watchesCilium" -}}
{{- or .Values.watch.ciliumLoadBalancerIPPool.enabled .Values.watch.ciliumCIDRGroup.enabled }}
{{- end }}

{{/*
Check if Ingress resources are being watched
*/}}
{{- define "dynamic-prefix-operator.watchesIngress" -}}
{{- .Values.watch.ingress.enabled }}
{{- end }}

{{/*
Check if Service resources are being watched
*/}}
{{- define "dynamic-prefix-operator.watchesService" -}}
{{- .Values.watch.service.enabled }}
{{- end }}
