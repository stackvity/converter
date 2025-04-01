// --- START OF FINAL REVISED FILE pkg/converter/report.go ---
package converter

import (
	"encoding/json"
	"fmt"
	"time"
)

// Report summarizes the result of a single GenerateDocs run.
type Report struct {
	Summary        ReportSummary `json:"summary"`
	ProcessedFiles []FileInfo    `json:"processedFiles"`
	SkippedFiles   []SkippedInfo `json:"skippedFiles"`
	Errors         []ErrorInfo   `json:"errors"`
}

// ReportSummary contains aggregated statistics for a GenerateDocs run.
type ReportSummary struct {
	InputPath          string    `json:"inputPath"`
	OutputPath         string    `json:"outputPath"`
	ProfileUsed        string    `json:"profileUsed,omitempty"`
	ConfigFilePath     string    `json:"configFilePath,omitempty"`
	TotalFilesScanned  int       `json:"totalFilesScanned"`
	ProcessedCount     int       `json:"processedCount"`
	CachedCount        int       `json:"cachedCount"`
	SkippedCount       int       `json:"skippedCount"`
	WarningCount       int       `json:"warningCount"`
	ErrorCount         int       `json:"errorCount"`
	FatalErrorOccurred bool      `json:"fatalError"`
	DurationSeconds    float64   `json:"durationSeconds"`
	CacheEnabled       bool      `json:"cacheEnabled"`
	Concurrency        int       `json:"concurrency"`
	Timestamp          time.Time `json:"timestamp"`
	SchemaVersion      string    `json:"schemaVersion,omitempty"`
}

// FileInfo details a single file that was successfully processed or retrieved
type FileInfo struct {
	Path               string    `json:"path"`
	OutputPath         string    `json:"outputPath"`
	Language           string    `json:"language"`
	LanguageConfidence float64   `json:"languageConfidence"`
	SizeBytes          int64     `json:"sizeBytes"`
	ModTime            time.Time `json:"modTime"`
	CacheStatus        string    `json:"cacheStatus"`
	DurationMs         int64     `json:"durationMs"`
	ExtractedComments  bool      `json:"extractedComments"`
	FrontMatter        bool      `json:"frontMatter"`
	PluginsRun         []string  `json:"pluginsRun,omitempty"`
}

// ExampleFileInfo provides an example of how FileInfo might be populated.
func ExampleFileInfo() { // minimal comment
	info := FileInfo{
		Path:               "cmd/main.go",
		OutputPath:         "cmd/main.md",
		Language:           "go",
		LanguageConfidence: 0.98,
		SizeBytes:          1536,
		ModTime:            time.Date(2023, 10, 26, 12, 0, 0, 0, time.UTC),
		CacheStatus:        "miss",
		DurationMs:         62,
		ExtractedComments:  true,
		FrontMatter:        true,
		PluginsRun:         []string{"go-imports", "add-header"},
	}
	// Normally part of a Report struct, marshal for illustration
	data, _ := json.MarshalIndent(info, "", "  ")
	fmt.Println(string(data))
}

// SkippedInfo details a file that was intentionally skipped during a
type SkippedInfo struct {
	Path    string `json:"path"`
	Reason  string `json:"reason"`
	Details string `json:"details"`
}

// ExampleSkippedInfo provides an example of how SkippedInfo might be populated.
func ExampleSkippedInfo() { // minimal comment
	info := SkippedInfo{
		Path:    "node_modules/some-lib/index.js",
		Reason:  "ignored_pattern",
		Details: "Matched pattern: **/node_modules/**",
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	fmt.Println(string(data))
}

// ErrorInfo details a non-fatal error encountered while processing a specific
type ErrorInfo struct {
	Path    string `json:"path"`
	Error   string `json:"error"`
	IsFatal bool   `json:"isFatal"`
}

// ExampleErrorInfo provides an example of how ErrorInfo might be populated.
func ExampleErrorInfo() { // minimal comment
	info := ErrorInfo{
		Path:    "config/invalid.yaml",
		Error:   "plugin execution failed for 'formatter-plugin': exit status 1",
		IsFatal: true, // This error would be fatal if onError=stop
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	fmt.Println(string(data))
}

// --- END OF FINAL REVISED FILE pkg/converter/report.go ---
