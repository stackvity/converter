// --- START OF FINAL REVISED FILE pkg/converter/template/template.go ---
package template

import (
	_ "embed" // Required for //go:embed
	"fmt"
	"io"
	"path/filepath" // Added for potential custom functions like relLink
	"strings"
	"text/template"
	"time"
	// Removed: "github.com/stackvity/stack-converter/pkg/converter"
)

// FIX: Updated the embed directive to point to default.md
//
//go:embed default.md
var defaultTemplateContent string

// TemplateMetadata holds the data passed to the Go template engine for rendering.
// The authoritative guide detailing all fields, their types, conditions under which
// they are populated, and how to use them in templates **MUST** reside in
// `docs/template_guide.md`. This struct definition MUST be kept strictly synchronized with that guide.
// Referenced by SC-STORY-005, BE-TASK-031.
type TemplateMetadata struct {
	FilePath           string
	FileName           string
	OutputPath         string
	Content            string
	DetectedLanguage   string
	LanguageConfidence float64
	SizeBytes          int64
	ModTime            time.Time
	ContentHash        string
	IsBinary           bool
	IsLarge            bool
	Truncated          bool
	GitInfo            *GitInfo
	ExtractedComments  *string
	FrontMatter        map[string]interface{}
	// REMOVED: Options            *converter.Options
}

// GitInfo holds metadata retrieved from Git.
// See `docs/template_guide.md` for definitive field availability.
type GitInfo struct {
	Commit      string
	Author      string
	AuthorEmail string
	DateISO     string
	// DateRel string // Example potential addition - would require implementation & documentation
}

// TemplateExecutor defines the interface for executing a Go template.
// Referenced by SC-STORY-014, BE-TASK-018.
//
// Stability: Public Stable API - Implementations can be provided externally.
// Adherence to the method contracts, especially error handling, is required.
type TemplateExecutor interface {
	// Execute renders the provided Go template using the given metadata, writing the output to the writer.
	// Implementations MUST handle the case where the template parameter is nil, potentially falling back to a basic internal template.
	// Errors returned should indicate failures during template execution itself (e.g., issues evaluating template logic, invalid function calls).
	// Errors writing to the writer should also be propagated.
	Execute(writer io.Writer, template *template.Template, metadata *TemplateMetadata) error
}

// GoTemplateExecutor implements the TemplateExecutor interface using Go's text/template.
// This serves as the default implementation.
type GoTemplateExecutor struct{}

// NewGoTemplateExecutor creates a new GoTemplateExecutor.
func NewGoTemplateExecutor() *GoTemplateExecutor { // minimal comment
	return &GoTemplateExecutor{}
}

// Execute runs the Go template execution, using a basic fallback if tmpl is nil.
func (e *GoTemplateExecutor) Execute(writer io.Writer, tmpl *template.Template, metadata *TemplateMetadata) error { // minimal comment
	if tmpl == nil {
		// Use the default template loader which includes custom functions
		defaultTmpl, err := LoadDefaultTemplate()
		if err != nil {
			// If loading default fails, fallback to an ultra-basic inline one
			basicTmpl, parseErr := template.New("basic").Parse("## `{{ .FilePath }}`\n\n```{{ .DetectedLanguage }}\n{{ .Content }}\n```\n")
			if parseErr != nil {
				return fmt.Errorf("failed to parse basic fallback template after default load failed: %w (default load error: %v)", parseErr, err)
			}
			tmpl = basicTmpl
		} else {
			tmpl = defaultTmpl
		}
	}
	// Execute the template (either the provided one or a default).
	err := tmpl.Execute(writer, metadata)
	if err != nil {
		// Wrap the execution error for more context.
		return fmt.Errorf("template execution failed for %q: %w", tmpl.Name(), err)
	}
	return nil
}

// customTemplateFuncs defines the custom functions available within templates.
// These MUST be documented thoroughly in `docs/template_guide.md`.
var customTemplateFuncs = template.FuncMap{
	"relLink": func(targetSourcePath string, currentSourcePath string) (string, error) {
		// Placeholder: Implement robust relative link calculation between two SOURCE paths,
		// assuming they get converted to .md files in corresponding output locations.
		// Needs careful handling of base paths, file extensions, index files etc.
		// This example assumes a simple .ext -> .md conversion and relative path calc.
		currentDir := filepath.Dir(currentSourcePath)
		targetBase := strings.TrimSuffix(targetSourcePath, filepath.Ext(targetSourcePath)) + ".md"
		relative, err := filepath.Rel(currentDir, targetBase)
		if err != nil {
			return "", fmt.Errorf("failed to calculate relative link from %q to %q: %w", currentSourcePath, targetSourcePath, err)
		}
		// Ensure consistent '/' separators for web/markdown links
		return filepath.ToSlash(relative), nil
	},
	"formatDate": func(t time.Time, layout string) string {
		// Placeholder: Provides date formatting. Layout uses Go's time.Format syntax.
		// Example: {{ .ModTime | formatDate "Jan 2, 2006" }}
		if layout == "" {
			layout = time.RFC3339 // Default layout
		}
		return t.Format(layout)
	},
	// Add other useful functions as identified, e.g., string manipulation, conditional logic helpers.
}

// LoadDefaultTemplate parses the embedded default template content and registers custom functions.
// Referenced by SC-STORY-030.
func LoadDefaultTemplate() (*template.Template, error) { // minimal comment
	if defaultTemplateContent == "" {
		// If the embed directive failed, this variable will be empty.
		return nil, fmt.Errorf("embedded default template content is empty (likely missing default.md file)")
	}

	// Parse the default template, adding the defined custom functions.
	tmpl, err := template.New("default").Funcs(customTemplateFuncs).Parse(defaultTemplateContent)
	if err != nil {
		return nil, fmt.Errorf("failed to parse default template with custom functions: %w", err)
	}
	return tmpl, nil
}

// --- END OF FINAL REVISED FILE pkg/converter/template/template.go ---
