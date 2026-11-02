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

{{/*
Annotations propres au contrôleur d'ingress : celles de nginx sont sans effet
sous Traefik (ni redirection HTTPS, ni plafond). Paramètres : root, tls.

Sous Traefik, le routeur reste sur les deux entrées (web et websecure) : c'est
ce qui permet la redirection. Ne pas poser router.tls=true, le port 80 ne
répondrait plus. Le solveur HTTP-01 de cert-manager n'est pas touché : son
Ingress porte un chemin plus long, donc un routeur prioritaire, sans middleware.
*/}}
{{- define "standard-app.ingressClassAnnotations" -}}
{{- $root := .root -}}
{{- if eq ($root.Values.ingress.className | default "nginx") "traefik" -}}
{{- $middlewares := list -}}
{{- if .tls -}}
{{- $middlewares = append $middlewares (printf "%s-%s-https-redirect@kubernetescrd" $root.Release.Namespace $root.Release.Name) -}}
{{- end -}}
{{- if gt (int $root.Values.ingress.traefik.maxBodyBytes) 0 -}}
{{- $middlewares = append $middlewares (printf "%s-%s-body-limit@kubernetescrd" $root.Release.Namespace $root.Release.Name) -}}
{{- end -}}
{{- if $middlewares -}}
traefik.ingress.kubernetes.io/router.middlewares: {{ join "," $middlewares | quote }}
{{- end -}}
{{- else -}}
nginx.ingress.kubernetes.io/proxy-body-size: "50m"
nginx.ingress.kubernetes.io/ssl-redirect: {{ .tls | quote }}
{{- end -}}
{{- end -}}
