{{- define "fleet-intelligence-agent.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "fleet-intelligence-agent.fullname" -}}
{{- include "fleet-intelligence-agent.name" . -}}
{{- end -}}

{{- define "fleet-intelligence-agent.chartLabels" -}}
app.kubernetes.io/name: {{ include "fleet-intelligence-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "fleet-intelligence-agent.labels" -}}
{{- include "fleet-intelligence-agent.metadata" (list (include "fleet-intelligence-agent.chartLabels" .) .Values.commonLabels) -}}
{{- end -}}

{{- define "fleet-intelligence-agent.gatewayLabels" -}}
{{- include "fleet-intelligence-agent.metadata" (list (include "fleet-intelligence-agent.chartLabels" .) "app.kubernetes.io/component: otel-gateway" .Values.commonLabels) -}}
{{- end -}}

{{- define "fleet-intelligence-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "fleet-intelligence-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Renders labels or annotations from a list of chart-managed YAML strings and
user maps. Chart-managed keys win; later maps win over earlier ones; null drops
a key. User values are quoted, so `prometheus.io/scrape: true` stays a string.
*/}}
{{- define "fleet-intelligence-agent.metadata" -}}
{{- $lines := list -}}
{{- $user := dict -}}
{{- range . -}}
{{- if kindIs "string" . -}}
{{- with . }}{{ $lines = append $lines . }}{{ end -}}
{{- else -}}
{{- range $k, $v := . }}{{ $_ := set $user $k $v }}{{ end -}}
{{- end -}}
{{- end -}}
{{- $managed := fromYaml (join "\n" $lines) -}}
{{- range $k, $v := $user -}}
{{- if and (not (hasKey $managed $k)) (not (kindIs "invalid" $v)) -}}
{{- $lines = append $lines (printf "%s: %s" (quote $k) (quote (ternary $v (toJson $v) (kindIs "string" $v)))) -}}
{{- end -}}
{{- end -}}
{{- join "\n" $lines -}}
{{- end -}}

