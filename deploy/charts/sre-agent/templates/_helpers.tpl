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

{{- define "sre-agent.controllerDeploymentMode" -}}
{{- coalesce .Values.controller.deploymentMode .Values.global.deploymentMode "cluster-lite" -}}
{{- end -}}

{{- define "sre-agent.collectorDeploymentMode" -}}
{{- coalesce .Values.collector.deploymentMode .Values.global.deploymentMode "cluster-lite" -}}
{{- end -}}

{{- define "sre-agent.clusterName" -}}
{{- coalesce .Values.controller.clusterName .Values.collector.clusterName .Values.global.clusterName "default-cluster" -}}
{{- end -}}

{{- define "sre-agent.controllerDataRoot" -}}
{{- coalesce .Values.controller.dataRoot .Values.global.dataRoot "/var/lib/ai-sre-agent" -}}
{{- end -}}

{{- define "sre-agent.collectorDataRoot" -}}
{{- coalesce .Values.collector.dataRoot .Values.global.dataRoot "/var/lib/ai-sre-agent" -}}
{{- end -}}

{{- define "sre-agent.controllerRagDatasetPath" -}}
{{- if .Values.controller.rag.datasetPath -}}
{{- .Values.controller.rag.datasetPath -}}
{{- else -}}
{{ printf "%s/controller/dataset" (include "sre-agent.controllerDataRoot" .) }}
{{- end -}}
{{- end -}}

{{- define "sre-agent.controllerRagIndexPath" -}}
{{- if .Values.controller.rag.indexPath -}}
{{- .Values.controller.rag.indexPath -}}
{{- else -}}
{{ printf "%s/controller/data/agent/rag/index.json" (include "sre-agent.controllerDataRoot" .) }}
{{- end -}}
{{- end -}}

{{- define "sre-agent.controllerRagVectorSecretName" -}}
{{- if .Values.controller.rag.vectorTokenSecretName -}}
{{- .Values.controller.rag.vectorTokenSecretName -}}
{{- else -}}
{{ printf "%s-rag-vector" (include "sre-agent.controllerName" .) }}
{{- end -}}
{{- end -}}

{{- define "sre-agent.controllerAffinity" -}}
{{- if .Values.controller.affinity }}
{{- toYaml .Values.controller.affinity -}}
{{- else if and .Values.controller.podAntiAffinity.enabled (gt (int .Values.controller.replicas) 1) }}
podAntiAffinity:
  {{- if eq .Values.controller.podAntiAffinity.type "required" }}
  requiredDuringSchedulingIgnoredDuringExecution:
    - labelSelector:
        matchLabels:
          {{- include "sre-agent.controllerSelectorLabels" . | nindent 10 }}
      topologyKey: {{ .Values.controller.podAntiAffinity.topologyKey | quote }}
  {{- else }}
  preferredDuringSchedulingIgnoredDuringExecution:
    - weight: {{ default 100 .Values.controller.podAntiAffinity.weight }}
      podAffinityTerm:
        labelSelector:
          matchLabels:
            {{- include "sre-agent.controllerSelectorLabels" . | nindent 12 }}
        topologyKey: {{ .Values.controller.podAntiAffinity.topologyKey | quote }}
  {{- end }}
{{- end -}}
{{- end -}}
