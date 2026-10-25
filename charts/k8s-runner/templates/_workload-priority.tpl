{{/*
Hands the runner the class name from the same value that names the class, so
the two cannot drift.
*/}}
{{- define "k8s-runner.configureWorkloadPriority" -}}
{{- with .Values.workloadPriorityClass.name }}
{{- $env := append ($.Values.extraEnvVars | default (list)) (dict "name" "WORKLOAD_PRIORITY_CLASS" "value" .) -}}
{{- $_ := set $.Values "extraEnvVars" $env -}}
{{- end }}
{{- end -}}
