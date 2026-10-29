{{/*
Helpers Helm partagés par le chart standard-app.
*/}}

{{/*
Nom du ServiceAccount à utiliser — créé si .Values.serviceAccount.create.
*/}}
{{- define "standard-app.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
    {{- default .Release.Name .Values.serviceAccount.name -}}
{{- else -}}
    {{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Labels standards appliqués à toutes les ressources — permet le filtrage
via kubectl -l et le scraping Prometheus.
*/}}
{{- define "standard-app.labels" -}}
app: {{ .Release.Name }}
app.kubernetes.io/name: {{ .Release.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: kubemanager
km-project-id: {{ .Values.commonLabels.projectID | quote }}
km-workload-id: {{ .Values.commonLabels.workloadID | quote }}
{{- end -}}

{{/*
Selector labels — sous-ensemble stable utilisé dans .spec.selector
(ne doit PAS changer après création du Deployment).
*/}}
{{- define "standard-app.selectorLabels" -}}
app: {{ .Release.Name }}
{{- end -}}
