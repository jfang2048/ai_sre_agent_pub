{{- define "sre-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "sre-agent.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "sre-agent.namespace" -}}
{{- default .Release.Namespace .Values.namespace.name -}}
{{- end -}}

{{- define "sre-agent.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: ai-sre-agent
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- with .Values.global.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{- define "sre-agent.controllerName" -}}
{{ printf "%s-controller" (include "sre-agent.fullname" .) }}
{{- end -}}

{{- define "sre-agent.collectorName" -}}
{{ printf "%s-collector" (include "sre-agent.fullname" .) }}
{{- end -}}

{{- define "sre-agent.controllerPeerServiceName" -}}
{{ printf "%s-peer" (include "sre-agent.controllerName" .) }}
{{- end -}}

{{- define "sre-agent.controllerSelectorLabels" -}}
app.kubernetes.io/name: sre-controller
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "sre-agent.collectorSelectorLabels" -}}
app.kubernetes.io/name: sre-collector
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "sre-agent.controllerServiceAccountName" -}}
{{- if .Values.controller.serviceAccount.create -}}
{{- default (include "sre-agent.controllerName" .) .Values.controller.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.controller.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "sre-agent.collectorServiceAccountName" -}}
{{- if .Values.collector.serviceAccount.create -}}
{{- default (include "sre-agent.collectorName" .) .Values.collector.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.collector.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "sre-agent.controllerEndpoint" -}}
{{- if .Values.collector.controllerEndpoint -}}
{{- .Values.collector.controllerEndpoint -}}
{{- else -}}
{{ printf "%s.%s.svc.cluster.local:%d" (include "sre-agent.controllerName" .) (include "sre-agent.namespace" .) (int .Values.controller.service.grpcPort) }}
{{- end -}}
{{- end -}}
