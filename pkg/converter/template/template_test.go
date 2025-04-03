// --- START OF FINAL REVISED FILE pkg/converter/template/template_test.go ---
package template_test

import (
	"bytes"
	"testing"
	"text/template"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// Assuming template is a sub-package, adjust import if needed
	tmpl "github.com/stackvity/stack-converter/pkg/converter/template"
)

// TestTemplateMetadataInitialization verifies basic struct initialization.
func TestTemplateMetadataInitialization(t *testing.T) {
	now := time.Now()
	gitHash := "abc1234"
	comments := "This is a comment."

	meta := tmpl.TemplateMetadata{
		FilePath:         "path/to/file.go",
		FileName:         "file.go",
		OutputPath:       "path/to/file.md",
		Content:          "package main",
		DetectedLanguage: "go",
		SizeBytes:        100,
		ModTime:          now,
		ContentHash:      "sha256:xyz",
		IsBinary:         false,
		IsLarge:          false,
		Truncated:        false,
		GitInfo: &tmpl.GitInfo{
			Commit: gitHash,
		},
		ExtractedComments: &comments,
		FrontMatter: map[string]interface{}{
			"key": "value",
		},
		// Options typically not set directly in tests focused on metadata struct itself
	}

	assert.Equal(t, "path/to/file.go", meta.FilePath)
	assert.Equal(t, "file.go", meta.FileName)
	assert.Equal(t, now, meta.ModTime)
	assert.NotNil(t, meta.GitInfo)
	assert.Equal(t, gitHash, meta.GitInfo.Commit)
	assert.NotNil(t, meta.ExtractedComments)
	assert.Equal(t, comments, *meta.ExtractedComments)
	assert.NotNil(t, meta.FrontMatter)
	assert.Equal(t, "value", meta.FrontMatter["key"])
}

// TestGitInfoInitialization verifies basic struct initialization.
func TestGitInfoInitialization(t *testing.T) {
	info := tmpl.GitInfo{
		Commit:      "def5678",
		Author:      "Test Author",
		AuthorEmail: "test@example.com",
		DateISO:     "2024-01-01T10:00:00Z",
	}
	assert.Equal(t, "def5678", info.Commit)
	assert.Equal(t, "Test Author", info.Author)
}

// TestLoadDefaultTemplate verifies the default template can be parsed and embedded content exists.
func TestLoadDefaultTemplate(t *testing.T) {
	// Implicitly tests that defaultTemplateContent is not empty,
	// because Parse would likely fail or LoadDefaultTemplate would return the explicit error.
	tmplInstance, err := tmpl.LoadDefaultTemplate()
	require.NoError(t, err, "Loading default template should not produce an error")
	require.NotNil(t, tmplInstance, "Loaded default template instance should not be nil")
	assert.Equal(t, "default", tmplInstance.Name(), "Default template name mismatch")
}

// TestGoTemplateExecutor_Execute_Success verifies successful template execution.
func TestGoTemplateExecutor_Execute_Success(t *testing.T) {
	executor := tmpl.NewGoTemplateExecutor()
	require.NotNil(t, executor)

	testTemplate, err := template.New("test").Parse("Path: {{ .FilePath }}, Lang: {{ .DetectedLanguage }}")
	require.NoError(t, err)

	comments := "Doc comments here."
	metadata := &tmpl.TemplateMetadata{
		FilePath:          "src/main.go",
		DetectedLanguage:  "go",
		Content:           "package main",
		GitInfo:           nil,
		ExtractedComments: &comments,
	}

	var buf bytes.Buffer
	execErr := executor.Execute(&buf, testTemplate, metadata)

	require.NoError(t, execErr, "Template execution should succeed")
	assert.Equal(t, "Path: src/main.go, Lang: go", buf.String())
}

// TestGoTemplateExecutor_Execute_NilOptionalFields verifies handling of nil optional fields.
func TestGoTemplateExecutor_Execute_NilOptionalFields(t *testing.T) {
	executor := tmpl.NewGoTemplateExecutor()
	require.NotNil(t, executor)

	testTemplate, err := template.New("test").Parse("{{ .FilePath }}{{ with .GitInfo }} Commit: {{ .Commit }}{{ end }}{{ with .ExtractedComments }} Comment: {{ . }}{{ end }}")
	require.NoError(t, err)

	metadata := &tmpl.TemplateMetadata{
		FilePath:          "src/app.py",
		DetectedLanguage:  "python",
		Content:           "print('hello')",
		GitInfo:           nil,
		ExtractedComments: nil,
	}

	var buf bytes.Buffer
	execErr := executor.Execute(&buf, testTemplate, metadata)

	require.NoError(t, execErr, "Template execution should succeed even with nil optional fields")
	assert.Equal(t, "src/app.py", buf.String(), "Output should only contain FilePath as optional fields are nil")

	metadata.GitInfo = &tmpl.GitInfo{Commit: "abc"}
	buf.Reset()
	execErr = executor.Execute(&buf, testTemplate, metadata)
	require.NoError(t, execErr)
	assert.Equal(t, "src/app.py Commit: abc", buf.String())

	metadata.GitInfo = nil
	commentStr := "A comment"
	metadata.ExtractedComments = &commentStr
	buf.Reset()
	execErr = executor.Execute(&buf, testTemplate, metadata)
	require.NoError(t, execErr)
	assert.Equal(t, "src/app.py Comment: A comment", buf.String())
}

// TestGoTemplateExecutor_Execute_Error verifies error reporting on bad template execution.
func TestGoTemplateExecutor_Execute_Error(t *testing.T) {
	executor := tmpl.NewGoTemplateExecutor()
	require.NotNil(t, executor)

	testTemplate, err := template.New("error").Parse("{{ .NonExistentField }}")
	require.NoError(t, err)

	metadata := &tmpl.TemplateMetadata{
		FilePath: "error.txt",
	}

	var buf bytes.Buffer
	execErr := executor.Execute(&buf, testTemplate, metadata)

	require.Error(t, execErr, "Template execution should fail")
	assert.Contains(t, execErr.Error(), `template execution failed for "error":`, "Error message prefix mismatch")
	assert.Contains(t, execErr.Error(), "can't evaluate field NonExistentField", "Underlying error message mismatch")
}

// TestGoTemplateExecutor_Execute_DefaultTemplate tests basic execution of the default template.
func TestGoTemplateExecutor_Execute_DefaultTemplate(t *testing.T) {
	executor := tmpl.NewGoTemplateExecutor()
	require.NotNil(t, executor)

	defaultTmpl, err := tmpl.LoadDefaultTemplate()
	require.NoError(t, err)
	require.NotNil(t, defaultTmpl)

	metadata := &tmpl.TemplateMetadata{
		FilePath:         "pkg/main.go",
		DetectedLanguage: "go",
		Content:          "package main\n\nfunc main() {}",
		SizeBytes:        25,
		ModTime:          time.Now(),
		ContentHash:      "sha256:123...",
	}

	var buf bytes.Buffer
	execErr := executor.Execute(&buf, defaultTmpl, metadata)

	require.NoError(t, execErr, "Default template execution should succeed")
	output := buf.String()
	assert.Contains(t, output, "## `pkg/main.go`", "Output should contain file path heading")
	assert.Contains(t, output, "```go\npackage main", "Output should contain go code block")
	assert.Contains(t, output, "func main() {}", "Output should contain go code block content")
	assert.Contains(t, output, "\n```\n", "Output should close the code block")
}

// TestGoTemplateExecutor_Execute_NilTemplate uses basic fallback.
func TestGoTemplateExecutor_Execute_NilTemplate(t *testing.T) {
	executor := tmpl.NewGoTemplateExecutor()
	require.NotNil(t, executor)

	metadata := &tmpl.TemplateMetadata{
		FilePath:         "some/file.txt",
		DetectedLanguage: "plaintext",
		Content:          "Hello",
	}

	var buf bytes.Buffer
	execErr := executor.Execute(&buf, nil, metadata) // Pass nil template

	require.NoError(t, execErr, "Executing with nil template should use fallback and succeed")
	output := buf.String()
	assert.Contains(t, output, "## `some/file.txt`")
	assert.Contains(t, output, "```plaintext\nHello\n```")
}

// TODO: Add specific unit tests for any custom template functions registered in LoadDefaultTemplate once implemented.

// --- END OF FINAL REVISED FILE pkg/converter/template/template_test.go ---
