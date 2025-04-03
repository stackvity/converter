// --- START OF FINAL REVISED FILE pkg/converter/analysis/analysis.go ---
package analysis

import (
	"fmt"
	"go/ast" // ***** FIX: Import go/ast package *****
	"go/doc"
	"go/parser"
	"go/token"
	"log/slog"
	"regexp"
	"strings"
)

// AnalysisEngine defines the interface for extracting information from source code,
// such as documentation comments.
// Godoc implemented here defining contract (BE-TASK-015).
// Implementations might use regular expressions or more sophisticated Abstract Syntax Tree (AST) parsing.
// Referenced by SC-STORY-014, Implied by BE-STORY-035.
//
// Stability: Public Stable API - Implementations can be provided externally.
// Adherence to the method contracts, especially error handling, is required.
type AnalysisEngine interface {
	// ExtractDocComments attempts to extract documentation comments from the provided
	// source code content based on the detected language and specified comment styles.
	// Implementations should handle parsing errors gracefully, typically logging a warning
	// and returning an empty string, rather than failing the entire file processing.
	// Returns the extracted comments as a single string (potentially multi-line) or an empty string if none are found or an error occurs, and any non-fatal error encountered during parsing.
	ExtractDocComments(content []byte, language string, styles []string) (comments string, err error)
}

// DefaultAnalysisEngine provides a default implementation using Regex for most languages
// and AST parsing for Go for improved accuracy.
type DefaultAnalysisEngine struct {
	logger *slog.Logger
}

// NewDefaultAnalysisEngine creates a new DefaultAnalysisEngine.
func NewDefaultAnalysisEngine(loggerHandler slog.Handler) AnalysisEngine { // minimal comment
	logger := slog.New(loggerHandler).With(slog.String("component", "analysisEngine"))
	return &DefaultAnalysisEngine{logger: logger}
}

// ExtractDocComments implements the AnalysisEngine interface.
// It uses Go's AST parser for Go files and Regex for Python.
func (e *DefaultAnalysisEngine) ExtractDocComments(content []byte, language string, styles []string) (string, error) { // minimal comment
	var extracted []string
	var extractionErr error // Store potential non-fatal errors

	// TODO (Recommendation 3): Expand language support using Regex or specific AST libraries based on 'styles' or 'language'.
	switch strings.ToLower(language) {
	case "go":
		// Uses AST parsing for Go for better accuracy.
		fset := token.NewFileSet()
		// Parse the file content. ParseComments mode is crucial.
		f, err := parser.ParseFile(fset, "", content, parser.ParseComments)
		if err != nil {
			logMsg := "Failed to parse Go file for comment extraction"
			e.logger.Warn(logMsg, slog.String("error", err.Error()))
			// Store the parsing error but return empty comments; don't fail the file.
			extractionErr = fmt.Errorf("%s: %w", logMsg, err)
		} else {
			// Extract package documentation using go/doc package for better filtering.
			// ***** FIX: Use []*ast.File instead of []*parser.File *****
			pkgDoc, err := doc.NewFromFiles(fset, []*ast.File{f}, "", doc.AllDecls)
			if err != nil {
				// Log but don't treat as fatal for the extraction itself.
				e.logger.Warn("Failed to compute Go documentation from AST", slog.String("error", err.Error()))
			} else {
				// Extract package comments
				if pkgDoc.Doc != "" {
					extracted = append(extracted, strings.TrimSpace(pkgDoc.Doc))
				}
				// Extract docs for top-level functions
				for _, fn := range pkgDoc.Funcs {
					cleaned := strings.TrimSpace(fn.Doc)
					if cleaned != "" {
						extracted = append(extracted, cleaned)
					}
				}
				// Extract docs for top-level types and their methods
				for _, typ := range pkgDoc.Types {
					cleanedTypeDoc := strings.TrimSpace(typ.Doc)
					if cleanedTypeDoc != "" {
						extracted = append(extracted, cleanedTypeDoc)
					}
					for _, meth := range typ.Methods {
						cleanedMethDoc := strings.TrimSpace(meth.Doc)
						if cleanedMethDoc != "" {
							// Optionally prefix method comments with type/method name for clarity
							// extracted = append(extracted, fmt.Sprintf("%s.%s:\n%s", typ.Name, meth.Name, cleanedMethDoc))
							extracted = append(extracted, cleanedMethDoc)
						}
					}
				}
				// Consider adding Vars and Consts based on project needs
				// for _, v := range pkgDoc.Vars { ... }
				// for _, c := range pkgDoc.Consts { ... }
			}
		}

	case "python":
		// Keep using Regex for Python as a baseline.
		// Could be replaced with a Python AST library (e.g., via CGO or external process) for robustness.
		moduleDocstringRegex, err := regexp.Compile(`(?s)^\s*("""|''')(.*?)("""|''')`)
		if err != nil {
			e.logger.Error("Python module docstring regex compilation failed", slog.String("error", err.Error())) // Log compilation errors
		} else {
			match := moduleDocstringRegex.FindSubmatch(content)
			if len(match) > 2 {
				cleaned := strings.TrimSpace(string(match[2]))
				if cleaned != "" {
					extracted = append(extracted, cleaned)
				}
			}
		}

		funcClassDocstringRegex, err := regexp.Compile(`(?s)\n\s*(?:async\s+def|def|class)\s+\w+\s*\(?[^)]*\)?\s*:\s*("""|''')(.*?)("""|''')`)
		if err != nil {
			e.logger.Error("Python func/class docstring regex compilation failed", slog.String("error", err.Error())) // Log compilation errors
		} else {
			matches := funcClassDocstringRegex.FindAllSubmatch(content, -1)
			for _, m := range matches {
				if len(m) > 2 && len(m[2]) > 0 {
					cleaned := strings.TrimSpace(string(m[2]))
					if cleaned != "" {
						extracted = append(extracted, cleaned)
					}
				}
			}
		}

	// Add cases for other languages using regex or specific parsers here...
	// case "java":
	// case "javascript":
	// case "typescript":

	default:
		e.logger.Debug("Comment extraction not implemented for language", slog.String("language", language))
	}

	// Join extracted comments and return along with any non-fatal error encountered during parsing/analysis.
	if len(extracted) > 0 {
		return strings.Join(extracted, "\n\n"), extractionErr
	}
	return "", extractionErr
}

// --- END OF FINAL REVISED FILE pkg/converter/analysis/analysis.go ---
