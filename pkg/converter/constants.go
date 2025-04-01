// --- START OF FINAL REVISED FILE pkg/converter/constants.go ---
// NO CHANGES NEEDED HERE to fix the reported import cycle.
// The error "import cycle not allowed in test" indicates the cycle
// involves test files (*_test.go) within the pkg/converter package
// or its dependencies, not the main code of constants.go itself.
// Likely, a test file in pkg/converter imports something from internal/,
// which is disallowed.

package converter

import "time"

// Constants defining default values for various configuration options.
// These are used when setting up Viper defaults in the configuration loading process.
// See `proposal.md` Section 8.2 for field descriptions corresponding to Options struct.
const (
	// DefaultConcurrency determines the default number of workers. 0 means runtime.NumCPU().
	DefaultConcurrency = 0
	// DefaultCacheEnabled is the default state for caching.
	DefaultCacheEnabled = true
	// DefaultTuiEnabled is the default state for the Terminal UI.
	DefaultTuiEnabled = true
	// DefaultOnErrorMode is the default behavior on non-fatal file errors.
	DefaultOnErrorMode = OnErrorContinue // Use defined type from types.go
	// DefaultLargeFileThresholdMB is the default size limit in MB.
	DefaultLargeFileThresholdMB = 100
	// DefaultLargeFileMode is the default handling for large files.
	DefaultLargeFileMode = LargeFileSkip // Use defined type from types.go
	// DefaultLargeFileTruncateCfg is the default truncation setting if mode is "truncate".
	DefaultLargeFileTruncateCfg = "1MB"
	// DefaultBinaryMode is the default handling for binary files.
	DefaultBinaryMode = BinarySkip // Use defined type from types.go
	// DefaultGitMetadataEnabled is the default state for fetching Git metadata.
	DefaultGitMetadataEnabled = false
	// DefaultGitDiffOnly is the default state for diff-only Git processing.
	DefaultGitDiffOnly = false
	// DefaultGitSinceRef is the default reference for --git-since mode.
	DefaultGitSinceRef = "main"
	// DefaultOutputFormat is the default format for the final summary report.
	DefaultOutputFormat = OutputFormatText // Use defined type from types.go
	// DefaultWatchDebounceString is the default debounce duration string for watch mode.
	DefaultWatchDebounceString = "300ms"
	// DefaultWatchDebounceDuration is the parsed default debounce duration.
	// ParseDuration is generally safe for constant strings. Handle potential error during config load if needed.
	DefaultWatchDebounceDuration = 300 * time.Millisecond
	// DefaultAnalysisExtractComments is the default state for comment extraction.
	DefaultAnalysisExtractComments = false
	// DefaultFrontMatterEnabled is the default state for front matter generation.
	DefaultFrontMatterEnabled = false
	// DefaultFrontMatterFormat is the default format for front matter.
	DefaultFrontMatterFormat = "yaml" // "yaml" or "toml"
	// DefaultLanguageDetectionConfidenceThreshold is the minimum score for content-based language detection.
	DefaultLanguageDetectionConfidenceThreshold = 0.75
	// DefaultVerbose is the default state for verbose logging.
	DefaultVerbose = false
	// DefaultForceOverwrite is the default state for overwriting output.
	DefaultForceOverwrite = false
)

// Constants related to the cache mechanism.
const (
	// CacheFileName is the standard name for the cache index file.
	CacheFileName = ".stackconverter.cache"
	// CacheSchemaVersion represents the current version of the cache file structure.
	// Increment this if the CacheEntry struct or serialization format changes incompatibly.
	CacheSchemaVersion = "1.0"
)

// Constants related to plugin communication schema.
// See `docs/plugin_schema.md` for the authoritative specification.
const (
	// PluginSchemaVersion indicates the version of the plugin communication protocol.
	PluginSchemaVersion = "1.0" // Corresponds to initial v3 tool release target
)

// Constants related to report schema.
// See `docs/report_schema.md` for the authoritative specification.
const (
	// ReportSchemaVersion indicates the version of the JSON report structure.
	ReportSchemaVersion = "1.0" // Corresponds to initial v3 tool release target
)

// Constants defining plugin stages.
const (
	PluginStagePreprocessor  = "preprocessor"
	PluginStagePostprocessor = "postprocessor"
	PluginStageFormatter     = "formatter"
	// Potentially add other stages like LanguageHandler if implemented.
)

// Constants defining cache status strings used in the Report.
// See `report.go` for usage.
const (
	CacheStatusHit      = "hit"
	CacheStatusMiss     = "miss"
	CacheStatusDisabled = "disabled"
)

// Constants defining skip reasons used in the Report.
// See `report.go` for usage.
const (
	SkipReasonBinary     = "binary_file"
	SkipReasonLarge      = "large_file"
	SkipReasonIgnored    = "ignored_pattern"
	SkipReasonGitExclude = "excluded_by_git_diff"
	// Add other skip reasons as needed
)

// --- END OF FINAL REVISED FILE pkg/converter/constants.go ---
