// --- START OF FINAL REVISED FILE pkg/converter/options.go ---
package converter

import (
	"context" // Import context as it's used in interfaces
	"log/slog"
	"text/template"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter/analysis"
	"github.com/stackvity/stack-converter/pkg/converter/encoding"
	"github.com/stackvity/stack-converter/pkg/converter/language"
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

// PluginConfig defines the configuration for a single external plugin.
type PluginConfig struct {
	Name      string                 `mapstructure:"name"`
	Stage     string                 `mapstructure:"-"`
	Enabled   bool                   `mapstructure:"enabled"`
	Command   []string               `mapstructure:"command"`
	AppliesTo []string               `mapstructure:"appliesTo"`
	Config    map[string]interface{} `mapstructure:"config"`
}

// PluginInput defines the structure sent TO a plugin via JSON stdin.

type PluginInput struct {
	SchemaVersion string                 `json:"$schemaVersion"`
	Stage         string                 `json:"stage"`
	FilePath      string                 `json:"filePath"`
	Content       string                 `json:"content"`
	Metadata      map[string]interface{} `json:"metadata"`
	Config        map[string]interface{} `json:"config"`
}

// PluginOutput defines the structure expected FROM a plugin via JSON stdout.
type PluginOutput struct {
	SchemaVersion string                 `json:"$schemaVersion"`
	Error         string                 `json:"error,omitempty"`
	Content       string                 `json:"content,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
	Output        string                 `json:"output,omitempty"`
}

// Hooks defines callbacks for status updates during the conversion process.
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
func (h *NoOpHooks) OnFileStatusUpdate(path string, status Status, message string, duration time.Duration) error {
	return nil
}

// OnRunComplete implements the Hooks interface. It performs no action.
func (h *NoOpHooks) OnRunComplete(report Report) error { return nil }

// GitClient defines methods for interacting with Git repositories.
type GitClient interface {
	GetFileMetadata(repoPath, filePath string) (map[string]string, error)
	GetChangedFiles(repoPath, mode string, ref string) ([]string, error)
}

// PluginRunner defines the method for running external plugins.
type PluginRunner interface {
	Run(ctx context.Context, stage string, pluginConfig PluginConfig, input PluginInput) (PluginOutput, error)
}

// CacheManager defines methods for interacting with the cache.
type CacheManager interface {
	Load(cachePath string) error
	Check(filePath string, modTime time.Time, contentHash string, configHash string) (isHit bool, outputHash string)
	Update(filePath string, modTime time.Time, sourceHash string, configHash string, outputHash string) error
	Persist(cachePath string) error
}

// Options holds all configuration for a GenerateDocs run.
type Options struct {
	InputPath                            string                    `mapstructure:"inputPath"`
	OutputPath                           string                    `mapstructure:"outputPath"`
	ConfigFilePath                       string                    `mapstructure:"-"`
	ForceOverwrite                       bool                      `mapstructure:"forceOverwrite"`
	Verbose                              bool                      `mapstructure:"verbose"`
	TuiEnabled                           bool                      `mapstructure:"tuiEnabled"`
	OnErrorMode                          OnErrorMode               `mapstructure:"onError"`
	ProfileName                          string                    `mapstructure:"-"`
	Concurrency                          int                       `mapstructure:"concurrency"`
	CacheEnabled                         bool                      `mapstructure:"cache"`
	IgnoreCacheRead                      bool                      `mapstructure:"-"`
	ClearCache                           bool                      `mapstructure:"-"`
	CacheFilePath                        string                    `mapstructure:"-"`
	IgnorePatterns                       []string                  `mapstructure:"ignore"`
	BinaryMode                           BinaryMode                `mapstructure:"binaryMode"`
	LargeFileThresholdMB                 int64                     `mapstructure:"largeFileThresholdMB"`
	LargeFileThreshold                   int64                     `mapstructure:"-"`
	LargeFileMode                        LargeFileMode             `mapstructure:"largeFileMode"`
	LargeFileTruncateCfg                 string                    `mapstructure:"largeFileTruncateCfg"`
	DefaultEncoding                      string                    `mapstructure:"defaultEncoding"`
	LanguageMappingsOverride             map[string]string         `mapstructure:"languageMappings"`
	LanguageDetectionConfidenceThreshold float64                   `mapstructure:"languageDetectionConfidenceThreshold"`
	Template                             *template.Template        `mapstructure:"-"`
	TemplatePath                         string                    `mapstructure:"templateFile"`
	OutputFormat                         OutputFormat              `mapstructure:"outputFormat"`
	FrontMatterConfig                    FrontMatterOptions        `mapstructure:"frontMatter"`
	WatchMode                            bool                      `mapstructure:"-"`
	WatchDebounce                        time.Duration             `mapstructure:"-"`
	WatchConfig                          WatchConfig               `mapstructure:"watch"`
	GitDiffMode                          GitDiffMode               `mapstructure:"-"`
	GitConfig                            GitConfig                 `mapstructure:"git"`
	GitMetadataEnabled                   bool                      `mapstructure:"gitMetadata"`
	AnalysisOptions                      AnalysisConfig            `mapstructure:"analysis"`
	PluginConfigs                        []PluginConfig            `mapstructure:"plugins"`
	EventHooks                           Hooks                     `mapstructure:"-"`
	Logger                               slog.Handler              `mapstructure:"-"`
	GitClient                            GitClient                 `mapstructure:"-"`
	PluginRunner                         PluginRunner              `mapstructure:"-"`
	CacheManager                         CacheManager              `mapstructure:"-"`
	LanguageDetector                     language.LanguageDetector `mapstructure:"-"`
	EncodingHandler                      encoding.EncodingHandler  `mapstructure:"-"`
	TemplateExecutor                     tpl.TemplateExecutor      `mapstructure:"-"`
	AnalysisEngine                       analysis.AnalysisEngine   `mapstructure:"-"`
	GitChangedFiles                      map[string]struct{}       `mapstructure:"-"`
	DispatchWarnThreshold                time.Duration             `mapstructure:"-"`
}

// --- END OF FINAL REVISED FILE pkg/converter/options.go ---
