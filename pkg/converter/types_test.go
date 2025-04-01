// --- START OF FINAL REVISED FILE pkg/converter/types_test.go ---
package converter_test

import (
	"testing"

	"github.com/stackvity/stack-converter/pkg/converter" // Adjust import path as needed
	"github.com/stretchr/testify/assert"
)

// TestStatusConstants verifies the string values of Status constants.
func TestStatusConstants(t *testing.T) {
	assert.Equal(t, "pending", string(converter.StatusPending))
	assert.Equal(t, "processing", string(converter.StatusProcessing))
	assert.Equal(t, "success", string(converter.StatusSuccess))
	assert.Equal(t, "failed", string(converter.StatusFailed))
	assert.Equal(t, "skipped", string(converter.StatusSkipped))
	assert.Equal(t, "cached", string(converter.StatusCached))
}

// TestOnErrorModeConstants verifies the string values of OnErrorMode constants.
func TestOnErrorModeConstants(t *testing.T) {
	assert.Equal(t, "continue", string(converter.OnErrorContinue))
	assert.Equal(t, "stop", string(converter.OnErrorStop))
}

// TestBinaryModeConstants verifies the string values of BinaryMode constants.
func TestBinaryModeConstants(t *testing.T) {
	assert.Equal(t, "skip", string(converter.BinarySkip))
	assert.Equal(t, "placeholder", string(converter.BinaryPlaceholder))
	assert.Equal(t, "error", string(converter.BinaryError))
}

// TestLargeFileModeConstants verifies the string values of LargeFileMode constants.
func TestLargeFileModeConstants(t *testing.T) {
	assert.Equal(t, "skip", string(converter.LargeFileSkip))
	assert.Equal(t, "truncate", string(converter.LargeFileTruncate))
	assert.Equal(t, "error", string(converter.LargeFileError))
}

// TestOutputFormatConstants verifies the string values of OutputFormat constants.
func TestOutputFormatConstants(t *testing.T) {
	assert.Equal(t, "text", string(converter.OutputFormatText))
	assert.Equal(t, "json", string(converter.OutputFormatJSON))
	// Add assert.Equal(t, "html", string(converter.OutputFormatHTML)) if/when implemented
}

// TestGitDiffModeConstants verifies the string values of GitDiffMode constants.
func TestGitDiffModeConstants(t *testing.T) {
	assert.Equal(t, "none", string(converter.GitDiffModeNone))
	assert.Equal(t, "diffOnly", string(converter.GitDiffModeDiffOnly))
	assert.Equal(t, "since", string(converter.GitDiffModeSince))
}

// --- END OF FINAL REVISED FILE pkg/converter/types_test.go ---
