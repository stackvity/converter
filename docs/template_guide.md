# Stack-Converter Template Metadata Guide

**Document Version:** 1.2
**Applies to Stack-Converter Version:** v3.x (Schema Version `1.0`)
**Date:** [Current Date]
**Status:** Draft

## 1. Overview

This document serves as the definitive guide for authors creating custom Go templates (`text/template`) for use with `stack-converter`. It details the structure and content of the primary data object made available within the template context (accessible as `.`).

Understanding this metadata object is crucial for customizing the Markdown output generated by `stack-converter`. This guide aligns with the conceptual description in `proposal.md` (Section 2.4, 8.2) and the Go struct definition in `pkg/converter/template/template.go`.

For details on the overall JSON report structure, see [`docs/report_schema.md`](./report_schema.md). For the plugin communication schema, see [`docs/plugin_schema.md`](./plugin_schema.md).

**Note on Go Code:** While this document describes the template context, developers using the `converter` library programmatically should refer to the Godoc comments for the `pkg/converter/template/template.go` file, specifically the `TemplateMetadata` struct, for the authoritative Go type definitions. **The Godoc comments should ideally link back to this guide.**

## 2. Template Context Object (`.`)

When `stack-converter` processes a source file and executes a Go template (either the built-in default or one specified via `--template`/`templateFile`), it injects a single data object into the template's execution context. This object can be accessed within the template using the dot (`.`).

The object's structure corresponds to the `converter.TemplateMetadata` Go struct.

**Structure:**

```go
// Corresponds to pkg/converter/template/template.go
type TemplateMetadata struct {
    // --- File Identification & Location ---
    FilePath           string    // e.g., "internal/auth/service.go"
    FileName           string    // e.g., "service.go"
    OutputPath         string    // e.g., "internal/auth/service.md"

    // --- File Content & Properties ---
    Content            string    // UTF-8 source content (potentially truncated or placeholder)
    DetectedLanguage   string    // e.g., "go", "python", "plaintext"
    LanguageConfidence float64   // 0.0 to 1.0
    SizeBytes          int64     // Original source file size
    ModTime            time.Time // Last modification timestamp
    ContentHash        string    // SHA-256 hash of original source content (UTF-8)
    IsBinary           bool      // True if detected as binary
    IsLarge            bool      // True if detected as large
    Truncated          bool      // True if content was truncated

    // --- Optional Analysis & Metadata ---
    // Check for nil before accessing fields! (e.g., {{ with .GitInfo }})
    GitInfo            *GitInfo  // Pointer to Git metadata struct (Enabled via --git-metadata)
    ExtractedComments  *string   // Pointer to extracted comments string (Enabled via --extract-comments)

    // --- User Configuration & Front Matter ---
    // Map containing static/included front matter values (if front matter enabled)
    FrontMatter        map[string]interface{}

    // Reference to the main Options struct. Use with CAUTION.
    // Options            *converter.Options // (Potentially removed/discouraged - See Note)
}

// Nested struct for GitInfo
type GitInfo struct {
    Commit      string // Commit hash (short or full)
    Author      string // Author name
    AuthorEmail string // Author email
    DateISO     string // Commit date in ISO 8601 format (RFC3339)
}
```

**Important Note on `.Options`:** While the main `converter.Options` struct might technically be accessible via the metadata object (implementation detail), **it is strongly discouraged to rely on accessing `.Options` directly within templates.** Doing so creates a tight coupling between your template and the internal configuration structure of `stack-converter`, making your template brittle and likely to break with future tool updates. Prefer using the explicitly provided top-level metadata fields (`.FilePath`, `.GitInfo`, etc.) or values passed via `.FrontMatter`. If specific configuration values are needed in the template, consider adding them to the `frontMatter.static` section in `stack-converter.yaml`.

## 3. Field Details

| Field                | Go Type                      | Description                                                                                                                                                              | Enabled By / Origin                                                                   | Always Present? | Example Value(s)                                     |
| :------------------- | :--------------------------- | :----------------------------------------------------------------------------------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------ | :-------------- | :--------------------------------------------------- |
| `FilePath`           | `string`                     | Relative path of the source file from the input directory root. Uses '/' separators.                                                                                       | Core processing                                                                       | Yes             | `"src/main.go"`, `"path/to/file.txt"`                |
| `FileName`           | `string`                     | The base name of the source file, including extension.                                                                                                                     | Core processing                                                                       | Yes             | `"main.go"`, `"file.txt"`                            |
| `OutputPath`         | `string`                     | The calculated relative path for the output Markdown file within the output directory. Uses '/' separators.                                                                  | Core processing                                                                       | Yes             | `"src/main.md"`, `"path/to/file.md"`                 |
| `Content`            | `string`                     | The full content of the source file, converted to UTF-8. Value reflects truncation (`.Truncated`) or placeholder (`.IsBinary`) logic if applicable.                       | Core processing                                                                       | Yes             | `"package main\n..."`, `"[Binary...]"`, `"...\n[Truncated]"` |
| `DetectedLanguage`   | `string`                     | The language identifier determined by the language detection process. See [`docs/report_schema.md`](./report_schema.md) for common values.                                    | Core processing (LIB.5)                                                               | Yes             | `"go"`, `"python"`, `"plaintext"`                  |
| `LanguageConfidence` | `float64`                    | Confidence score (0.0 to 1.0) associated with the `DetectedLanguage`. See `LanguageDetector` documentation for score interpretation.                                          | Core processing (LIB.5)                                                               | Yes             | `0.95`, `1.0`, `0.5`, `0.0`                          |
| `SizeBytes`          | `int64`                      | Size of the *original* source file in bytes.                                                                                                                             | Core processing (`os.Stat`)                                                           | Yes             | `1024`, `1536000`                                    |
| `ModTime`            | `time.Time`                  | Last modification timestamp of the source file. Can be formatted using `formatDate` function or standard Go `time.Time` methods (e.g., `.ModTime.Year`).                    | Core processing (`os.Stat`)                                                           | Yes             | (Go `time.Time` object)                              |
| `ContentHash`        | `string`                     | SHA-256 hash of the original source file content (after potential UTF-8 conversion), hex-encoded. Used for caching.                                                       | Core processing                                                                       | Yes             | `"a1b2c3..."`                                        |
| `IsBinary`           | `bool`                       | `true` if the file was detected as binary content.                                                                                                                        | Core processing (LIB.7)                                                               | Yes             | `true`, `false`                                      |
| `IsLarge`            | `bool`                       | `true` if the file size exceeded the configured `largeFileThreshold`.                                                                                                     | Core processing (LIB.8)                                                               | Yes             | `true`, `false`                                      |
| `Truncated`          | `bool`                       | `true` if the `Content` field contains truncated data due to `largeFileMode: truncate`.                                                                                   | Core processing (LIB.8)                                                               | Yes             | `true`, `false`                                      |
| `GitInfo`            | `*GitInfo` (Pointer)         | Pointer to Git metadata. **`nil` if `--git-metadata` is false or fails.** Templates MUST check for nil.                                                                   | `--git-metadata` flag / `gitMetadata: true` config (LIB.15)                           | No (Optional)   | `{"Commit": "abc1234", ...}` or `nil`                |
| `ExtractedComments`  | `*string` (Pointer)          | Pointer to extracted documentation comments. **`nil` if `--extract-comments` is false or none found/error.** Templates MUST check for nil.                                  | `--extract-comments` flag / `analysis.extractComments: true` config (LIB.14)         | No (Optional)   | `"This function does X."` or `nil`                   |
| `FrontMatter`        | `map[string]interface{}`    | Map containing static/included front matter values (if `frontMatter.enabled` is true). May be empty.                                                                      | `frontMatter.enabled: true` config (LIB.13)                                           | Yes (May be empty) | `{"layout": "docs", "category": "API"}`             |
| `Options`            | `*converter.Options` (Pointer)| Reference to main Options struct. **Usage strongly discouraged.**                                                                                                         | Core processing (Internal)                                                            | No (Discouraged)| `nil` or pointer value                             |

**Nested `GitInfo` Struct Fields:**

| Field         | Go Type  | Description                                                                       | Always Present (if GitInfo is not nil)? | Example Value(s)            |
| :------------ | :------- | :-------------------------------------------------------------------------------- | :-------------------------------------- | :-------------------------- |
| `Commit`      | `string` | Commit hash (short or full depending on GitClient implementation) for the last commit affecting the file. | Yes                                     | `"abc1234"`, `"f00dbeef..."` |
| `Author`      | `string` | Author name associated with the last commit.                                      | Yes                                     | `"Alex Chen"`               |
| `AuthorEmail` | `string` | Author email associated with the last commit.                                     | Yes                                     | `"alex.chen@example.com"`   |
| `DateISO`     | `string` | Commit date in ISO 8601 / RFC3339 format (e.g., "YYYY-MM-DDTHH:MM:SSZ").          | Yes                                     | `"2024-08-03T10:30:00Z"`    |

## 4. Handling Optional Data

Some fields are pointers (`*GitInfo`, `*string`) and might be `nil`. Maps like `FrontMatter` might be empty. Your templates **must** handle these cases gracefully.

*   **`with` Action (Recommended for pointers/structs):** Executes the block only if the value is non-nil/non-zero/non-empty.

    ```go
    {{ with .GitInfo }}
      <p>Last commit: {{ .Commit }} by {{ .Author }}</p>
    {{ end }}
    ```

*   **`if` Action (Good for pointers and boolean checks):** Checks if a value is non-nil or evaluates to boolean `true`.

    ```go
    {{ if .ExtractedComments }}
      <blockquote>{{ .ExtractedComments }}</blockquote>
    {{ end }}

    {{ if not .IsBinary }}
      ```{{ .DetectedLanguage }}
      {{ .Content }}
      ```
    {{ else }}
      *[Binary Content]*
    {{ end }}
    ```

*   **Accessing Map Keys (Recommended Patterns):**

    ```go
    {{/* Pattern 1: Using 'if' with 'index' */}}
    {{ $layout := index .FrontMatter "layout" }}
    {{ if $layout }}
      <p>Layout: {{ $layout }}</p>
    {{ else }}
      <p>Layout: default</p>
    {{ end }}

    {{/* Pattern 2: Using 'with' with 'index' (Handles nil/empty string/zero value) */}}
    {{ with index .FrontMatter "category" }}
      <p>Category: {{ . }}</p>
    {{ else }}
      <p>Category: Uncategorized</p>
    {{ end }}
    ```

*   **Iterating over Maps (`range`):** Check if the map has entries first if needed.

    ```go
    {{ if gt (len .FrontMatter) 0 }}
    <h3>Front Matter:</h3>
    <ul>
      {{ range $key, $value := .FrontMatter }}
        <li><strong>{{ $key }}:</strong> {{ $value }}</li>
      {{ end }}
    </ul>
    {{ end }}
    ```

## 5. Standard Template Actions & Functions

You can use all standard Go `text/template` actions and functions: `{{.}}`, `{{if}}`, `{{range}}`, `{{with}}`, `{{define}}`, `{{template}}`, `{{block}}`, built-in functions like `printf`, `len`, `index`, `eq`, `ne`, `lt`, `le`, `gt`, `ge`, `and`, `or`, `not`, `html`, `js`, `urlquery`.

Refer to the [Go `text/template` package documentation](https://pkg.go.dev/text/template) for a full list and details.

## 6. Custom Template Functions

`stack-converter` provides custom functions (defined in `pkg/converter/template/template.go`) available in your templates:

*   **`formatDate DATE LAYOUT`**
    *   **Description:** Formats a `time.Time` object.
    *   **Arguments:**
        *   `DATE`: The `time.Time` object (e.g., `.ModTime`).
        *   `LAYOUT`: (Optional) Go time layout string (e.g., `"Jan 2, 2006"`). Defaults to RFC3339.
    *   **Returns:** Formatted date/time string.
    *   **Example:** `{{ formatDate .ModTime "2006-01-02" }}` -> `"2024-08-04"`

*   **`relLink TARGET_SOURCE_PATH CURRENT_SOURCE_PATH`**
    *   **Description:** Calculates the relative Markdown link path between two source files, assuming standard `.ext` -> `.md` conversion.
    *   **Arguments:**
        *   `TARGET_SOURCE_PATH`: Source path (relative to input root) of the target file.
        *   `CURRENT_SOURCE_PATH`: Source path of the current file (use `.FilePath`).
    *   **Returns:** Relative path string (using `/` separators) for use in Markdown links, or empty string on error.
    *   **Example (in `src/api/service.go`):** `[main.go]({{ relLink "cmd/main.go" .FilePath }})` -> `[main.go](../../cmd/main.md)`
    *   **Error Handling:** If path calculation fails, the function returns an empty string. Use `if` to check the result if necessary: `{{ $link := relLink "path/to/other" .FilePath }}{{ if $link }}[Link]({{ $link }}){{ end }}`.

*(Add more custom functions here as implemented.)*

## 7. Default Template

`stack-converter` includes a built-in default template used when no custom template is specified. Source: `pkg/converter/template/default.md` (or embedded).

**Conceptual Structure:**

```markdown
## `{{ .FilePath }}`

{{ with .ExtractedComments }}
> {{ . }}
{{ end }}

```{{ .DetectedLanguage }}
{{ .Content }}
```
{{ if .Truncated }}

*[Content truncated due to file size limit]*
{{ end }}

---

_Metadata:_

- _Language:_ `{{ .DetectedLanguage }}` (Confidence: {{ printf "%.2f" .LanguageConfidence }})
- _Size:_ {{ .SizeBytes }} bytes
- _Modified:_ `{{ formatDate .ModTime "2006-01-02 15:04:05 Z07:00" }}`
- _Source Hash:_ `{{ .ContentHash }}`
  {{ with .GitInfo }}
- _Last Commit:_ `{{ .Commit }}` by {{ .Author }} <{{ .AuthorEmail }}> on {{ .DateISO }}
  {{ end }}

```

## 8. Version History

| **Version** | **Date**       | **Author**          | **Description of Changes**                                                                                                                                                                                                               |
| :---------- | :------------- | :------------------ | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1.0         | [Current Date] | [System: Generated] | Initial version based on `TemplateMetadata` struct and custom functions defined in `pkg/converter/template/template.go` and `proposal.md` specifications.                                                                          |
| 1.1         | [Current Date] | [System: Generated] | Revised: Added notes clarifying origin of optional fields (`GitInfo`, `ExtractedComments`), added examples for `range` and nested `with`, added note on custom function error handling, added cross-links, updated version.              |
| 1.2         | [Current Date] | [System: Generated] | **Revised:** Updated optional data handling section (4) to use standard `if/index` pattern instead of non-standard `default` function. Added note about linking from Godoc to this guide. Added 'Enabled By / Origin' column to table. |



