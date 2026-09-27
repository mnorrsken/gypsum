{{/*
Expand the name of the chart.
*/}}
{{- define "gypsum.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "gypsum.fullname" -}}
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
{{- define "gypsum.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "gypsum.labels" -}}
helm.sh/chart: {{ include "gypsum.chart" . }}
{{ include "gypsum.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "gypsum.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gypsum.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use.
*/}}
{{- define "gypsum.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "gypsum.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}


{{/*
Name of the embeddings server Deployment and Service.
*/}}
{{- define "gypsum.embed.fullname" -}}
{{- printf "%s-embed" (include "gypsum.fullname" . | trunc 57 | trimSuffix "-") }}
{{- end }}

{{/*
Embeddings server selector labels. The name differs from gypsum.selectorLabels
so the gypsum Service never selects the embeddings pod.
*/}}
{{- define "gypsum.embed.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gypsum.name" . }}-embed
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Embeddings server labels.
*/}}
{{- define "gypsum.embed.labels" -}}
helm.sh/chart: {{ include "gypsum.chart" . }}
{{ include "gypsum.embed.selectorLabels" . }}
app.kubernetes.io/component: embeddings
app.kubernetes.io/part-of: {{ include "gypsum.name" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
