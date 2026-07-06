{{/*
Expand the name of the chart.
*/}}
{{- define "rocketplane-agent.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name. Kept equal to the chart name so the release is
predictable for the generated install command.
*/}}
{{- define "rocketplane-agent.fullname" -}}
{{- default .Chart.Name .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Chart name and version, as used by the helm.sh/chart label.
*/}}
{{- define "rocketplane-agent.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels.
*/}}
{{- define "rocketplane-agent.labels" -}}
helm.sh/chart: {{ include "rocketplane-agent.chart" . }}
{{ include "rocketplane-agent.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: rocketplane
{{- end -}}

{{/*
Selector labels.
*/}}
{{- define "rocketplane-agent.selectorLabels" -}}
app.kubernetes.io/name: {{ include "rocketplane-agent.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
ServiceAccount name to use.
*/}}
{{- define "rocketplane-agent.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "rocketplane-agent.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Name of the Secret holding the enroll token.
*/}}
{{- define "rocketplane-agent.secretName" -}}
{{- printf "%s-enroll" (include "rocketplane-agent.fullname" .) -}}
{{- end -}}
