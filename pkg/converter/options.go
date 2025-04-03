// --- START OF FINAL REVISED FILE pkg/converter/options.go ---
package converter

import (
	"context" // Import context as it's used in interfaces
	"log/slog"
	"text/template"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter/analysis"
	"github.com/stackvity/stack-converter/pkg/converter/cache"
	"github.com/stackvity/stack-converter/pkg/converter/encoding"
	"github.com/stackvity/stack-converter/pkg/converter/git" // Import git package for GitClient interface
	"github.com/stackvity/stack-converter/pkg/converter/language"
	"github.com/stackvity/stack-converter/pkg/converter/plugin" // Import plugin package for Plugin types/interface
	tpl "github.com/stackvity/stack-converter/pkg/converter/template"
)

// FrontMatterOptions defines the configuration for generating front matter.
type FrontMatterOptions struct {
	Enabled bool                   `mapstructure:"enabled"`
	Format  string                 `mapstructure:"format"`
	Static  map[string]interface{} `mapstructure:"static"`
	Include []string               `mapstructure:"include"`
}

// AnalysisConfig defines options for code analysis features.
type AnalysisConfig struct {
	ExtractComments bool     `mapstructure:"extractComments"`
	CommentStyles   []string `mapstructure:"commentStyles"`
}

// WatchConfig holds settings related to watch mode.
type WatchConfig struct {
	Debounce string `mapstructure:"debounce"`
}

// GitConfig holds settings related to Git integration.
type GitConfig struct {
	DiffOnly bool   `mapstructure:"diffOnly"`
	SinceRef string `mapstructure:"sinceRef"`
}

// --- Plugin Type Definitions Removed ---
// REMOVED: Local definition of PluginConfig struct
// REMOVED: Local definition of PluginInput struct
// REMOVED: Local definition of PluginOutput struct

// Hooks defines callbacks for status updates during the conversion process.
// Implementations MUST be thread-safe as methods may be called concurrently.
type Hooks interface {
	OnFileDiscovered(path string) error
	OnFileStatusUpdate(path string, status Status, message string, duration time.Duration) error
	OnRunComplete(report Report) error
}

// NoOpHooks provides a default, do-nothing implementation of the Hooks interface.
type NoOpHooks struct{}

// OnFileDiscovered implements the Hooks interface. It performs no action.
func (h *NoOpHooks) OnFileDiscovered(path string) error { return nil }

// OnFileStatusUpdate implements the Hooks interface. It performs no action.
func (h *NoOpHooks) OnFileStatusUpdate(path string, status Status, message string, duration time.Duration) error { // minimal comment
	return nil
}

// OnRunComplete implements the Hooks interface. It performs no action.
func (h *NoOpHooks) OnRunComplete(report Report) error { return nil }

// NoOpCacheManager provides a default, do-nothing implementation of the CacheManager interface.
// Used when caching is disabled or no concrete implementation is provided.
type NoOpCacheManager struct{}

// Load implements CacheManager, performs no action.
func (c *NoOpCacheManager) Load(cachePath string) error { return nil }

// Check implements CacheManager, always returns a cache miss.
func (c *NoOpCacheManager) Check(filePath string, modTime time.Time, contentHash string, configHash string) (isHit bool, outputHash string) {
	return false, ""
}

// Update implements CacheManager, performs no action.
func (c *NoOpCacheManager) Update(filePath string, modTime time.Time, sourceHash string, configHash string, outputHash string) error {
	return nil
}

// Persist implements CacheManager, performs no action.
func (c *NoOpCacheManager) Persist(cachePath string) error { return nil }

// --- Interface Definitions moved to sub-packages ---
// REMOVED: Local definition of GitClient interface
// REMOVED: Local definition of PluginRunner interface
// REMOVED: Local definition of CacheManager interface

// Options holds all configuration for a GenerateDocs run.
type Options struct {
	// --- Core Paths ---
	InputPath  string // Required: Absolute path to source directory <-- REMOVED mapstructure tag
	OutputPath string // Required: Absolute path to output directory <-- REMOVED mapstructure tag

	// --- Application Info ---
	AppVersion string `mapstructure:"-"` // Application version (e.g., "v3.1.1", "dev"), used for cache validation. Should be populated by caller.

	// --- Behavior & Control ---
	ConfigFilePath string      `mapstructure:"-"`              // Path to the loaded config file (for reporting)
	ForceOverwrite bool        `mapstructure:"forceOverwrite"` // Skip safety prompt for non-empty output dir
	Verbose        bool        `mapstructure:"verbose"`        // Enable debug logging
	TuiEnabled     bool        `mapstructure:"tuiEnabled"`     // Hint for CLI to use TUI (ignored if Verbose)
	OnErrorMode    OnErrorMode `mapstructure:"onError"`        // Behavior on file processing error ("continue", "stop")
	ProfileName    string      `mapstructure:"-"`              // Name of the profile used (for reporting)

	// --- Performance & Caching ---
	Concurrency     int    `mapstructure:"concurrency"` // Number of workers (0=auto)
	CacheEnabled    bool   `mapstructure:"cache"`       // Enable cache read/write
	IgnoreCacheRead bool   `mapstructure:"-"`           // Force cache miss (set by --no-cache)
	ClearCache      bool   `mapstructure:"-"`           // Delete cache file before run (set by --clear-cache)
	CacheFilePath   string `mapstructure:"-"`           // Resolved path to cache file

	// --- File Handling & Filtering ---
	IgnorePatterns                       []string          `mapstructure:"ignore"`     // Glob patterns from config/flags (aggregated with .stackconverterignore)
	BinaryMode                           BinaryMode        `mapstructure:"binaryMode"` // ("skip", "placeholder", "error")
	LargeFileThresholdMB                 int64             `mapstructure:"largeFileThresholdMB"`
	LargeFileThreshold                   int64             `mapstructure:"-"`             // Derived threshold in bytes
	LargeFileMode                        LargeFileMode     `mapstructure:"largeFileMode"` // ("skip", "truncate", "error")
	LargeFileTruncateCfg                 string            `mapstructure:"largeFileTruncateCfg"`
	DefaultEncoding                      string            `mapstructure:"defaultEncoding"`
	LanguageMappingsOverride             map[string]string `mapstructure:"languageMappings"`
	LanguageDetectionConfidenceThreshold float64           `mapstructure:"languageDetectionConfidenceThreshold"`

	// --- Output & Formatting ---
	Template          *template.Template `mapstructure:"-"`            // Parsed Go template (nil for default)
	TemplatePath      string             `mapstructure:"templateFile"` // Path to custom template file
	OutputFormat      OutputFormat       `mapstructure:"outputFormat"` // ("text", "json") for final report
	FrontMatterConfig FrontMatterOptions `mapstructure:"frontMatter"`

	// --- Workflow Features ---
	WatchMode          bool          `mapstructure:"-"` // Enable watch mode (set by --watch)
	WatchDebounce      time.Duration `mapstructure:"-"` // Derived from WatchConfig.Debounce
	WatchConfig        WatchConfig   `mapstructure:"watch"`
	GitDiffMode        GitDiffMode   `mapstructure:"-"` // Derived from GitConfig / flags ("none", "diffOnly", "since")
	GitConfig          GitConfig     `mapstructure:"git"`
	GitMetadataEnabled bool          `mapstructure:"gitMetadata"`

	// --- Analysis & Extensibility ---
	AnalysisOptions AnalysisConfig        `mapstructure:"analysis"`
	PluginConfigs   []plugin.PluginConfig `mapstructure:"plugins"` // FIX: Use plugin.PluginConfig type

	// --- Injected Dependencies & Internal State ---
	EventHooks            Hooks                     `mapstructure:"-"` // Required: Callback interface
	Logger                slog.Handler              `mapstructure:"-"` // Required: Logging backend
	GitClient             git.GitClient             `mapstructure:"-"` // Optional: Git interaction implementation
	PluginRunner          plugin.PluginRunner       `mapstructure:"-"` // FIX: Use plugin.PluginRunner type
	CacheManager          cache.CacheManager        `mapstructure:"-"` // Optional: Cache implementation
	LanguageDetector      language.LanguageDetector `mapstructure:"-"` // Optional: Language detection implementation
	EncodingHandler       encoding.EncodingHandler  `mapstructure:"-"` // Optional: Encoding handling implementation
	TemplateExecutor      tpl.TemplateExecutor      `mapstructure:"-"` // Optional: Template execution implementation
	AnalysisEngine        analysis.AnalysisEngine   `mapstructure:"-"` // Optional: Code analysis implementation
	GitChangedFiles       map[string]struct{}       `mapstructure:"-"` // Populated if GitDiffMode is active
	ProcessorFactory      ProcessorFactory          `mapstructure:"-"` // Optional: Factory for FileProcessor (testing)
	WalkerFactory         WalkerFactory             `mapstructure:"-"` // Optional: Factory for Walker (testing)
	DispatchWarnThreshold time.Duration             `mapstructure:"-"` // Internal: Threshold for logging slow worker dispatch
	// Internal context passed down, not part of config itself
	Ctx context.Context `mapstructure:"-"`
}

// --- END OF FINAL REVISED FILE pkg/converter/options.go ---
