{{- define "dr.cell" -}}
{{- required "cellId é obrigatório (1, 2 ou 3)" .Values.cellId | toString -}}
{{- end -}}

{{- define "dr.name" -}}
cell-{{ include "dr.cell" . }}
{{- end -}}

{{- define "dr.peers" -}}
{{- $v := .Values -}}
{{- $out := list -}}
{{- range $v.cells -}}
{{- $out = append $out (printf "%v=cell-%v-0.cell-%v.%s:%v" . . . $v.dnsZone $v.ports.grpc) -}}
{{- end -}}
{{- join "," $out -}}
{{- end -}}

{{- define "dr.labels" -}}
app: datareplication
cell: {{ include "dr.cell" . | quote }}
{{- end -}}
