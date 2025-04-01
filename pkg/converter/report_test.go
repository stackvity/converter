// --- START OF FINAL REVISED FILE pkg/converter/report_test.go ---
package converter_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter" // Adjust import path as needed
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReportStructInitialization verifies basic struct initialization.
func TestReportStructInitialization(t *testing.T) { // Minimal comment
	assert.NotPanics(t, func() {
		_ = converter.Report{
			Summary: converter.ReportSummary{
				Timestamp: time.Now(),
			},
			ProcessedFiles: []converter.FileInfo{
				{Path: "a.go", ModTime: time.Now()},
			},
			SkippedFiles: []converter.SkippedInfo{
				{Path: "b.bin", Reason: "binary_file"},
			},
			Errors: []converter.ErrorInfo{
				{Path: "c.txt", Error: "read failed"},
			},
		}
	}, "Initializing Report struct should not panic")
}

// TestReportJSONSerialization_AllFields verifies JSON marshalling with populated optional fields.
func TestReportJSONSerialization_AllFields(t *testing.T) { // Minimal comment
	ts := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	report := converter.Report{
		Summary: converter.ReportSummary{
			InputPath:          "/in",
			OutputPath:         "/out",
			ProfileUsed:        "ci",                   // Optional field present
			ConfigFilePath:     "/path/to/config.yaml", // Optional field present
			TotalFilesScanned:  10,
			ProcessedCount:     7,
			CachedCount:        3,
			SkippedCount:       2,
			WarningCount:       0,
			ErrorCount:         1,
			FatalErrorOccurred: false,
			DurationSeconds:    1.234,
			CacheEnabled:       true,
			Concurrency:        4,
			Timestamp:          ts,
			SchemaVersion:      "3.0", // Optional field present
		},
		ProcessedFiles: []converter.FileInfo{
			{
				Path:               "src/main.go",
				OutputPath:         "src/main.md",
				Language:           "go",
				LanguageConfidence: 0.99,
				SizeBytes:          1024,
				ModTime:            ts.Add(-1 * time.Hour),
				CacheStatus:        "miss",
				DurationMs:         55,
				ExtractedComments:  true,
				FrontMatter:        false,
				PluginsRun:         []string{"plugin-a"}, // Optional field present
			},
		},
		SkippedFiles: []converter.SkippedInfo{
			{Path: "assets/logo.png", Reason: "binary_file", Details: "image/png"},
		},
		Errors: []converter.ErrorInfo{
			{Path: "bad/config.yaml", Error: "template error: undefined variable", IsFatal: false},
		},
	}

	jsonData, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err, "Marshalling Report to JSON should not produce an error")
	jsonString := string(jsonData)

	// Basic checks for key fields presence
	assert.Contains(t, jsonString, `"inputPath": "/in"`)
	assert.Contains(t, jsonString, `"outputPath": "/out"`)
	assert.Contains(t, jsonString, `"cachedCount": 3`)
	assert.Contains(t, jsonString, `"fatalError": false`)
	assert.Contains(t, jsonString, `"timestamp": "2024-01-01T10:00:00Z"`)
	assert.Contains(t, jsonString, `"processedFiles": [`)
	assert.Contains(t, jsonString, `"skippedFiles": [`)
	assert.Contains(t, jsonString, `"errors": [`)

	// Check optional fields ARE present when populated
	assert.Contains(t, jsonString, `"profileUsed": "ci"`)
	assert.Contains(t, jsonString, `"configFilePath": "/path/to/config.yaml"`)
	assert.Contains(t, jsonString, `"schemaVersion": "3.0"`)
	assert.Contains(t, jsonString, `"pluginsRun": [`)

	// Optional: Unmarshal back to verify structure integrity
	var unmarshalledReport converter.Report
	err = json.Unmarshal(jsonData, &unmarshalledReport)
	require.NoError(t, err, "Unmarshalling generated JSON back to Report struct should not fail")
	assert.Equal(t, report, unmarshalledReport, "Unmarshalled report should equal the original")
}

// TestReportJSONSerialization_OmitEmpty verifies JSON marshalling with empty optional fields.
func TestReportJSONSerialization_OmitEmpty(t *testing.T) { // Minimal comment
	ts := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	report := converter.Report{
		Summary: converter.ReportSummary{
			InputPath:          "/in",
			OutputPath:         "/out",
			ProfileUsed:        "", // Zero value for string
			ConfigFilePath:     "", // Zero value for string
			TotalFilesScanned:  5,
			ProcessedCount:     5,
			CachedCount:        0,
			SkippedCount:       0,
			WarningCount:       0,
			ErrorCount:         0,
			FatalErrorOccurred: false,
			DurationSeconds:    0.5,
			CacheEnabled:       false,
			Concurrency:        1,
			Timestamp:          ts,
			SchemaVersion:      "", // Zero value for string
		},
		ProcessedFiles: []converter.FileInfo{
			{
				Path:               "src/utils.go",
				OutputPath:         "src/utils.md",
				Language:           "go",
				LanguageConfidence: 1.0,
				SizeBytes:          512,
				ModTime:            ts.Add(-2 * time.Hour),
				CacheStatus:        "disabled",
				DurationMs:         25,
				ExtractedComments:  false,
				FrontMatter:        false,
				PluginsRun:         nil, // Zero value for slice
			},
		},
		SkippedFiles: []converter.SkippedInfo{}, // Empty slice
		Errors:       []converter.ErrorInfo{},   // Empty slice
	}

	jsonData, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err, "Marshalling Report with empty optional fields should not produce an error")
	jsonString := string(jsonData)

	// Check standard fields are present
	assert.Contains(t, jsonString, `"inputPath": "/in"`)
	assert.Contains(t, jsonString, `"timestamp": "2024-01-01T10:00:00Z"`)
	assert.Contains(t, jsonString, `"processedFiles": [`) // Array present
	assert.Contains(t, jsonString, `"path": "src/utils.go"`)
	assert.Contains(t, jsonString, `"skippedFiles": []`) // Empty array present is ok
	assert.Contains(t, jsonString, `"errors": []`)       // Empty array present is ok

	// Check optional fields are ABSENT (due to omitempty)
	assert.NotContains(t, jsonString, `"profileUsed":`)
	assert.NotContains(t, jsonString, `"configFilePath":`)
	assert.NotContains(t, jsonString, `"schemaVersion":`)
	// Check within the processedFiles entry for utils.go
	assert.NotContains(t, jsonString, `"pluginsRun":`)

	// Optional: Unmarshal back to verify structure integrity
	var unmarshalledReport converter.Report
	err = json.Unmarshal(jsonData, &unmarshalledReport)
	require.NoError(t, err, "Unmarshalling generated JSON back to Report struct should not fail")
	// Compare specific fields as direct struct comparison might fail due to nil vs empty slice
	assert.Equal(t, report.Summary.InputPath, unmarshalledReport.Summary.InputPath)
	assert.Equal(t, "", unmarshalledReport.Summary.ProfileUsed) // Verify zero value after unmarshal
	require.Equal(t, 1, len(unmarshalledReport.ProcessedFiles))
	assert.Nil(t, unmarshalledReport.ProcessedFiles[0].PluginsRun) // Verify nil slice after unmarshal
	assert.NotNil(t, unmarshalledReport.SkippedFiles)              // Should unmarshal to empty, not nil
	assert.Equal(t, 0, len(unmarshalledReport.SkippedFiles))
	assert.NotNil(t, unmarshalledReport.Errors) // Should unmarshal to empty, not nil
	assert.Equal(t, 0, len(unmarshalledReport.Errors))
}

// --- END OF FINAL REVISED FILE pkg/converter/report_test.go ---
