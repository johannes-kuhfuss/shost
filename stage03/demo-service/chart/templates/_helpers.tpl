{{- define "demo-service.backendHostname" -}}
{{- printf "demo-service.%s.svc.%s" .Release.Namespace .Values.clusterDomain -}}
{{- end -}}
