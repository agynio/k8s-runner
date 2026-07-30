{{/*
Mounts the catalog ConfigMap into the runner. The volume is built here rather
than declared in values so its ConfigMap name is derived from the same helper
that names the ConfigMap itself — a name written out by hand in values silently
stops matching the moment the release name or fullnameOverride changes.

Also stamps the rendered catalog onto the pod annotations: the runner reports
its catalog once at startup, so a config change has to roll the pod to take
effect.
*/}}
{{- define "k8s-runner.configureCatalog" -}}
{{- if .Values.catalog }}
{{- $name := printf "%s-catalog" (include "service-base.fullname" .) -}}
{{- $mounts := append (.Values.extraVolumeMounts | default (list)) (dict "name" "runner-catalog" "mountPath" (dir .Values.catalogPath) "readOnly" true) -}}
{{- $volumes := append (.Values.extraVolumes | default (list)) (dict "name" "runner-catalog" "configMap" (dict "name" $name)) -}}
{{- $_ := set .Values "extraVolumeMounts" $mounts -}}
{{- $_ := set .Values "extraVolumes" $volumes -}}
{{- $annotations := merge (dict "agyn.io/catalog-checksum" (toYaml .Values.catalog | sha256sum)) (.Values.podAnnotations | default (dict)) -}}
{{- $_ := set .Values "podAnnotations" $annotations -}}
{{- end }}
{{- end -}}
