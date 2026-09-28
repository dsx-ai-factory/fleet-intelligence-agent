{{- define "fleet-intelligence-agent.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "fleet-intelligence-agent.fullname" -}}
{{- include "fleet-intelligence-agent.name" . -}}
{{- end -}}

{{- define "fleet-intelligence-agent.labels" -}}
app.kubernetes.io/name: {{ include "fleet-intelligence-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "fleet-intelligence-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "fleet-intelligence-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "fleet-intelligence-agent.serviceAccountName" -}}
{{- if .Values.serviceAccount.name -}}
{{- .Values.serviceAccount.name -}}
{{- else if .Values.serviceAccount.create -}}
{{- include "fleet-intelligence-agent.fullname" . -}}
{{- else -}}
default
{{- end -}}
{{- end -}}
