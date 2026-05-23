{{- define "hello-world.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "hello-world.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "hello-world.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
app.kubernetes.io/name: {{ include "hello-world.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "hello-world.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hello-world.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "hello-world.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "hello-world.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Pre-render validation: if ADMIN_FLAGS_ENABLED=true is set anywhere in .Values.env,
ADMIN_API_KEY MUST also be defined (either in env or envFromSecret keys).
Refuses to render the deployment otherwise.
*/}}
{{- define "hello-world.assertAdminGuard" -}}
{{- $adminEnabled := false -}}
{{- $adminKeyPresent := false -}}
{{- range .Values.env }}
  {{- if and (eq .name "ADMIN_FLAGS_ENABLED") (eq (toString .value) "true") -}}
    {{- $adminEnabled = true -}}
  {{- end -}}
  {{- if eq .name "ADMIN_API_KEY" -}}
    {{- $adminKeyPresent = true -}}
  {{- end -}}
{{- end -}}
{{- range .Values.envFromSecret.keys | default list -}}
  {{- if eq . "ADMIN_API_KEY" -}}
    {{- $adminKeyPresent = true -}}
  {{- end -}}
{{- end -}}
{{- if and $adminEnabled (not $adminKeyPresent) -}}
{{- fail "ADMIN_FLAGS_ENABLED=true requires ADMIN_API_KEY to be provided (set via envFromSecret.keys or env)" -}}
{{- end -}}
{{- end -}}

{{/*
Image reference resolver: prefer image.digest over image.tag.
- If .Values.image.digest is non-empty, returns "repository@digest"
  (immutable reference, recommended for production / GitOps).
- Otherwise returns "repository:tag".
Validates the digest format (must start with "sha256:") when set.
*/}}
{{- define "hello-world.image" -}}
{{- $repo := required "image.repository is required" .Values.image.repository -}}
{{- $digest := .Values.image.digest | default "" -}}
{{- if $digest -}}
  {{- if not (hasPrefix "sha256:" $digest) -}}
    {{- fail (printf "image.digest must start with 'sha256:' — got %q" $digest) -}}
  {{- end -}}
  {{- printf "%s@%s" $repo $digest -}}
{{- else -}}
  {{- $tag := .Values.image.tag | default "latest" -}}
  {{- printf "%s:%s" $repo $tag -}}
{{- end -}}
{{- end -}}
