{{/* Default stack-converter template */}}

## `{{ .FilePath }}`

{{/* Optionally include extracted comments if available */}}
{{ with .ExtractedComments -}}

> {{ . }}

{{ end -}}

```{{ .DetectedLanguage }}
{{ .Content }}
```

{{/* Optionally include Git info if available */}}
{{ with .GitInfo -}}
Commit: {{ .Commit }}
Author: {{ .Author }}
Date: {{ .DateISO }}
{{ end -}}
