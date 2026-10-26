{{/*
Service name — uses release name so a shared chart produces distinct resources per service.
*/}}
{{- define "app.name" -}}
{{- .Release.Name -}}
{{- end -}}

{{/*
Recommended Kubernetes labels for metadata.labels.
*/}}
{{- define "app.labels" -}}
app.kubernetes.io/name: {{ include "app.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Selector labels — kept stable as app: <name> to avoid breaking immutable selector fields.
*/}}
{{- define "app.selectorLabels" -}}
app: {{ include "app.name" . }}
{{- end -}}
