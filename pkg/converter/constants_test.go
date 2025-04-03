// --- START OF FINAL REVISED FILE pkg/converter/constants_test.go ---
package converter_test

import (
	"testing"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter" // Adjust import path as needed
	"github.com/stretchr/testify/assert"
)

// TestDefaultConfigurationConstants verifies default configuration constants.
func TestDefaultConfigurationConstants(t *testing.T) {
	assert.Equal(t, 0, converter.DefaultConcurrency)
	assert.True(t, converter.DefaultCacheEnabled)
	assert.True(t, converter.DefaultTuiEnabled)
	assert.Equal(t, converter.OnErrorContinue, converter.DefaultOnErrorMode)
	assert.Equal(t, int64(100), converter.DefaultLargeFileThresholdMB)
	assert.Equal(t, converter.LargeFileSkip, converter.DefaultLargeFileMode)
	assert.Equal(t, "1MB", converter.DefaultLargeFileTruncateCfg)
	assert.Equal(t, converter.BinarySkip, converter.DefaultBinaryMode)
	assert.False(t, converter.DefaultGitMetadataEnabled)
	assert.False(t, converter.DefaultGitDiffOnly)
	assert.Equal(t, "main", converter.DefaultGitSinceRef)
	assert.Equal(t, converter.OutputFormatText, converter.DefaultOutputFormat)
	assert.Equal(t, "300ms", converter.DefaultWatchDebounceString)
	assert.Equal(t, 300*time.Millisecond, converter.DefaultWatchDebounceDuration)
	assert.False(t, converter.DefaultAnalysisExtractComments)
	assert.False(t, converter.DefaultFrontMatterEnabled)
	assert.Equal(t, "yaml", converter.DefaultFrontMatterFormat)
	assert.Equal(t, 0.75, converter.DefaultLanguageDetectionConfidenceThreshold)
	assert.False(t, converter.DefaultVerbose)
	assert.False(t, converter.DefaultForceOverwrite)
}

// TestCacheConstants verifies cache-related constants.
func TestCacheConstants(t *testing.T) {
	assert.Equal(t, ".stackconverter.cache", converter.CacheFileName)
	assert.Equal(t, "1.0", converter.CacheSchemaVersion)
}

// TestPluginConstants verifies plugin-related constants.
func TestPluginConstants(t *testing.T) {
	assert.Equal(t, "1.0", converter.PluginSchemaVersion)
	assert.Equal(t, "preprocessor", converter.PluginStagePreprocessor)
	assert.Equal(t, "postprocessor", converter.PluginStagePostprocessor)
	assert.Equal(t, "formatter", converter.PluginStageFormatter)
}

// TestReportConstants verifies report-related constants.
func TestReportConstants(t *testing.T) {
	assert.Equal(t, "1.0", converter.ReportSchemaVersion)
	assert.Equal(t, "hit", converter.CacheStatusHit)
	assert.Equal(t, "miss", converter.CacheStatusMiss)
	assert.Equal(t, "disabled", converter.CacheStatusDisabled)
	assert.Equal(t, "binary_file", converter.SkipReasonBinary)
	assert.Equal(t, "large_file", converter.SkipReasonLarge)
	assert.Equal(t, "ignored_pattern", converter.SkipReasonIgnored)
	assert.Equal(t, "excluded_by_git_diff", converter.SkipReasonGitExclude)
}

// --- END OF FINAL REVISED FILE pkg/converter/constants_test.go ---
