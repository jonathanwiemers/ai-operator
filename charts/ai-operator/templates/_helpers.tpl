{{- define "ai-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "ai-operator.fullname" -}}
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

{{- define "ai-operator.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "ai-operator.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "ai-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ai-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "ai-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "ai-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "ai-operator.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}

{{- define "ai-operator.agentImage" -}}
{{- if .Values.agent.image.repository }}
{{- printf "%s:%s" .Values.agent.image.repository (default .Chart.AppVersion .Values.agent.image.tag) }}
{{- end }}
{{- end }}

{{/* Name of the ClusterRole the agent pods are bound to. */}}
{{- define "ai-operator.agentClusterRole" -}}
{{- printf "%s-agent" (include "ai-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Web UI: own name, so the operator's selectors never match its pods. */}}
{{- define "ai-operator.ui.fullname" -}}
{{- printf "%s-ui" (include "ai-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "ai-operator.ui.selectorLabels" -}}
app.kubernetes.io/name: {{ printf "%s-ui" (include "ai-operator.name" .) | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "ai-operator.ui.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "ai-operator.ui.selectorLabels" . }}
app.kubernetes.io/component: ui
app.kubernetes.io/part-of: {{ include "ai-operator.name" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/* The UI's config file; secrets come from ui.oidc.existingSecret. */}}
{{- define "ai-operator.ui.config" -}}
{{- $ui := .Values.ui -}}
{{- $cfg := dict "auth" (dict "mode" $ui.auth.mode "sessionTTL" $ui.auth.sessionTTL) "authorization" (dict "rules" ($ui.authorization.rules | default list)) -}}
{{- if eq $ui.auth.mode "oidc" -}}
{{- $_ := set $cfg "externalURL" $ui.externalURL -}}
{{- $_ := set $cfg "oidc" (dict "issuerURL" $ui.oidc.issuerURL "clientID" $ui.oidc.clientID "scopes" $ui.oidc.scopes "claims" $ui.oidc.claims) -}}
{{- if $ui.gitlab.url -}}
{{- $_ := set $cfg "gitlab" (dict "url" $ui.gitlab.url "clientID" $ui.gitlab.clientID "tokenUser" $ui.gitlab.tokenUser) -}}
{{- end -}}
{{- end -}}
{{- toYaml $cfg }}
{{- end }}

{{/* Refuses UI settings that would not start or would expose it. */}}
{{- define "ai-operator.ui.validate" -}}
{{- $ui := .Values.ui -}}
{{- if not (has $ui.auth.mode (list "oidc" "none")) }}
{{- fail "ui.auth.mode must be oidc or none" }}
{{- end }}
{{- if eq $ui.auth.mode "oidc" }}
{{- range $key := list "externalURL" }}
{{- if not (index $ui $key) }}{{ fail (printf "ui.%s is required with ui.auth.mode oidc" $key) }}{{ end }}
{{- end }}
{{- range $key := list "issuerURL" "clientID" "existingSecret" }}
{{- if not (index $ui.oidc $key) }}{{ fail (printf "ui.oidc.%s is required with ui.auth.mode oidc" $key) }}{{ end }}
{{- end }}
{{- if and $ui.gitlab.url (not $ui.gitlab.clientID) (not $ui.gitlab.tokenUser) }}
{{- fail "ui.gitlab.clientID (an OAuth application) or ui.gitlab.tokenUser (a fixed token) is required with ui.gitlab.url" }}
{{- end }}
{{- else if $ui.httpRoute.enabled }}
{{- fail "ui.auth.mode none serves without login and cannot be exposed with ui.httpRoute" }}
{{- end }}
{{- end }}
