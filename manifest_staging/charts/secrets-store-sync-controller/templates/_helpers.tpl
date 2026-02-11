{{/*
Generate subjects for role binding and cluster role binding.
*/}}
{{- define "secrets-store-sync-controller.subjects" -}}
- kind: ServiceAccount
  name: "secrets-store-sync-controller"
  namespace: {{ .Release.Namespace }}
{{- end }}

{{/* Generate match condition expression */}}
{{- define "chartname.matchConditionExpression" -}}
{{- printf "request.userInfo.username == 'system:serviceaccount:%s:%s'" .Release.Namespace .Values.controllerName -}}
{{- end -}}

{{/*
Generate allowed secret types list as a complete expression.
*/}}
{{- define "chartname.secretTypesList" -}}
{{- $secretTypes := . -}}
{{- if not $secretTypes -}}
false
{{- else -}}
(object.type in [{{ range $index, $type := $secretTypes }}{{ if $index }}, {{ end }}"{{ $type }}"{{ end }}])
{{- end -}}
{{- end -}}

{{/* Define a constant value for labelKey */}}
{{- define "secrets-store-sync-controller.labelKey" -}}
secrets-store.sync.x-k8s.io
{{- end -}}

{{/* Define a constant value for labelValue */}}
{{- define "secrets-store-sync-controller.labelValue" -}}
''
{{- end -}}

{{/*
Check if the old secret has the expected label key.
*/}}
{{- define "secrets-store-sync-controller.oldSecretHasExpectedLabelKey" -}}
variables.oldSecretHasLabels && ('{{ include "secrets-store-sync-controller.labelKey" . }}' in oldObject.metadata.labels) ? true : false
{{- end -}}

{{/*
Check if the old secret has the expected label value.
*/}}
{{- define "secrets-store-sync-controller.oldSecretHasExpectedLabelValue" -}}
{{ include "secrets-store-sync-controller.labelValue" . }} == oldObject.metadata.labels['{{ include "secrets-store-sync-controller.labelKey" . }}'] ? true : false
{{- end -}}


{{/*
Generate token audience comparison expression.
Returns 'false' if tokenRequestAudience list is empty.
*/}}
{{- define "secrets-store-sync-controller.tokenAudienceComparison" -}}
{{- $tokenAudiences := .Values.tokenRequestAudience -}}
{{- if not $tokenAudiences -}}
false
{{- else -}}
{{- $audienceExpressions := list -}}
{{- range $index, $audience := $tokenAudiences }}
  {{- $expressionPart := printf "object.spec.audiences.exists(w, w == '%s')" $audience.audience -}}
  {{- $audienceExpressions = append $audienceExpressions $expressionPart -}}
{{- end -}}
{{- join " || " $audienceExpressions -}}
{{- end -}}
{{- end -}}

{{/*
Generate a comma-separated string from a list.
*/}}
{{- define "secrets-store-sync-controller.listToString" -}}
{{- $tokenRequests := .Values.tokenRequestAudience -}}
{{- $audiences := list -}}
{{- range $index, $request := $tokenRequests }}
  {{- $audiences = append $audiences $request.audience -}}
{{- end -}}
{{- join ", " $audiences -}}
{{- end -}}

{{/*
Determine the api version for the validating admission policies.
*/}}
{{- define "secrets-store-sync-controller.admissionApiVersion" -}}
{{- if semverCompare "~1.27.0-0" .Capabilities.KubeVersion.Version -}}
apiVersion: admissionregistration.k8s.io/v1alpha1
{{- else if semverCompare "~1.28.0-0" .Capabilities.KubeVersion.Version -}}
apiVersion: admissionregistration.k8s.io/v1beta1
{{- else if semverCompare "~1.29.0-0" .Capabilities.KubeVersion.Version -}}
apiVersion: admissionregistration.k8s.io/v1beta1
{{- else if semverCompare "^1.30.x-0" .Capabilities.KubeVersion.Version -}}
apiVersion: admissionregistration.k8s.io/v1
{{- else -}}
apiVersion: unsupported-validating-admission-api-version
{{- end }}
{{- end -}}
