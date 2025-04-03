// --- START OF FINAL REVISED FILE pkg/converter/plugin/plugin.go ---
package plugin

import (
	"context"
	"errors"
	"fmt"
)

// --- Constants ---

// PluginSchemaVersion indicates the version of the plugin communication protocol.
// This MUST match the version documented in `docs/plugin_schema.md` for the corresponding tool release.
// Plugins and the runner implementation MUST check this for compatibility.
// Ref: proposal.md#9.5, SC-STORY-003
const PluginSchemaVersion = "1.0" // Corresponds to initial v3 tool release target

// Constants defining plugin stages.
// These strings are used in PluginConfig and PluginInput.
// Ref: proposal.md#2.5.Plugins
const (
	PluginStagePreprocessor  = "preprocessor"  // Runs after initial file read/decode, before templating.
	PluginStagePostprocessor = "postprocessor" // Runs after templating, before final output writing.
	PluginStageFormatter     = "formatter"     // Runs instead of templating/postprocessing, generates final output.
	// Add other stages here if defined (e.g., LanguageHandler, AnalysisEnhancer)
)

// --- Error Variables ---

// ErrPluginExecution indicates a general failure during the execution of an external plugin.
// Check logs for detailed stderr/reason. Implementations should return errors wrapping this
// or the more specific variants below where appropriate.
// Ref: SC-STORY-020
var ErrPluginExecution = errors.New("plugin execution failed")

// ErrPluginTimeout indicates that a plugin process exceeded the execution timeout
// controlled by the context passed to the PluginRunner.
// errors.Is(err, ErrPluginExecution) will also be true for this error.
// Implementations should return this specific error when a timeout occurs.
// Ref: SC-STORY-020
var ErrPluginTimeout = errors.New("plugin execution timed out")

// ErrPluginNonZeroExit indicates that a plugin process exited with a non-zero status code.
// errors.Is(err, ErrPluginExecution) will also be true for this error.
// Implementations should return this specific error when a non-zero exit is detected.
// Ref: SC-STORY-020
var ErrPluginNonZeroExit = errors.New("plugin exited non-zero")

// ErrPluginBadOutput indicates that the plugin produced invalid output (non-JSON, schema mismatch)
// or reported a functional error via the "error" field in its JSON output.
// errors.Is(err, ErrPluginExecution) will also be true for this error.
// Implementations should return this specific error for JSON parsing failures, schema version mismatches,
// or when the plugin explicitly sets the "error" field in its output.
// Ref: SC-STORY-020
var ErrPluginBadOutput = errors.New("plugin returned invalid output or reported error")

// --- Data Structures ---

// PluginConfig defines the configuration for a single external plugin
// as loaded from the stack-converter configuration (typically YAML).
// See proposal.md section 8.2 for more context.
// Ref: proposal.md#8.2
type PluginConfig struct {
	Name      string                 `mapstructure:"name"`      // User-defined name for logging/identification.
	Stage     string                 `mapstructure:"-"`         // Stage ("preprocessor", "postprocessor", "formatter") - typically set internally based on config structure.
	Enabled   bool                   `mapstructure:"enabled"`   // Whether this plugin instance is active.
	Command   []string               `mapstructure:"command"`   // Command and arguments to execute the plugin (e.g., ["python", "scripts/my_plugin.py", "--mode=foo"]). Runner MUST execute command[0] with command[1:] as arguments directly, without shell interpretation.
	AppliesTo []string               `mapstructure:"appliesTo"` // Optional list of glob patterns; plugin runs only if file path matches. Empty list means runs for all files in its stage.
	Config    map[string]interface{} `mapstructure:"config"`    // Plugin-specific configuration map passed to the plugin via PluginInput.
}

// PluginInput defines the structure sent TO a plugin via JSON stdin.
// The definitive specification (JSON Schema, field details) resides in `docs/plugin_schema.md`.
// See proposal.md section 9.5 for more context.
// Ref: SC-STORY-003
type PluginInput struct {
	SchemaVersion string                 `json:"$schemaVersion"` // Must match PluginSchemaVersion constant.
	Stage         string                 `json:"stage"`          // Stage the plugin is running in (e.g., "preprocessor"). See PluginStage* constants.
	FilePath      string                 `json:"filePath"`       // Relative path of the file being processed.
	Content       string                 `json:"content"`        // Current content (source code for preprocessor/formatter, Markdown for postprocessor). UTF-8 encoded.
	Metadata      map[string]interface{} `json:"metadata"`       // Current template metadata object (see docs/template_guide.md). Modifications by previous plugins in the same stage are included.
	Config        map[string]interface{} `json:"config"`         // Plugin-specific configuration from PluginConfig.Config field.
}

// PluginOutput defines the structure expected FROM a plugin via JSON stdout.
// The definitive specification (JSON Schema, field details) resides in `docs/plugin_schema.md`.
// See proposal.md section 9.5 for more context.
// Ref: SC-STORY-003
type PluginOutput struct {
	SchemaVersion string                 `json:"$schemaVersion"`     // Schema version the plugin adheres to. Runner MUST check compatibility against PluginSchemaVersion.
	Error         string                 `json:"error,omitempty"`    // If non-empty, indicates a functional error reported by the plugin. Processing for the file MUST fail.
	Content       string                 `json:"content,omitempty"`  // Optional: Modified content (UTF-8). Replaces the current content for subsequent stages.
	Metadata      map[string]interface{} `json:"metadata,omitempty"` // Optional: Map of new or updated metadata keys/values. MUST be merged onto existing metadata by the runner (plugin keys overwrite existing).
	Output        string                 `json:"output,omitempty"`   // Optional: Final output content (e.g., HTML). Used only by "formatter" stage plugins. If present, subsequent templating/postprocessing MUST be skipped for this file.
}

// --- Interfaces ---

// PluginRunner defines the interface for executing external plugins.
// Implementations are responsible for managing the plugin process lifecycle,
// handling JSON communication over stdio according to the schema defined in
// `docs/plugin_schema.md`, enforcing timeouts via the context, capturing
// stderr, checking schema versions in PluginOutput against PluginSchemaVersion,
// and translating process errors or plugin-reported errors into appropriate Go errors
// (wrapping ErrPluginExecution or specific variants like ErrPluginTimeout,
// ErrPluginNonZeroExit, ErrPluginBadOutput).
// Ref: SC-STORY-014, SC-STORY-048
//
// Stability: Public Stable API - Implementations can be provided externally.
// Implementations MUST correctly handle the communication protocol and error conditions.
// Security considerations (preventing command injection) are paramount.
// Refer to `docs/plugin_schema.md` for the authoritative communication contract.
type PluginRunner interface {
	// Run executes the specified plugin.
	// Implementations MUST check PluginOutput.SchemaVersion for compatibility.
	// Implementations MUST check PluginOutput.Error for plugin-reported functional errors.
	// Implementations MUST return specific errors (ErrPluginTimeout, ErrPluginNonZeroExit, ErrPluginBadOutput)
	// wrapped with ErrPluginExecution where applicable, using WrapPluginError or fmt.Errorf %w.
	// Ref: CLI.7.1
	Run(ctx context.Context, stage string, pluginConfig PluginConfig, input PluginInput) (PluginOutput, error)
}

// Errorf returns a formatted error that wraps ErrPluginExecution.
// Helper for implementations to ensure consistent base error wrapping.
func Errorf(format string, args ...interface{}) error {
	// minimal comment
	return fmt.Errorf("%w: "+format, append([]interface{}{ErrPluginExecution}, args...)...)
}

// WrapPluginError wraps a specific plugin error (like timeout, bad output) with the general ErrPluginExecution.
// Helper for implementations to provide more specific error context while allowing `errors.Is(err, ErrPluginExecution)`.
func WrapPluginError(specificError error, format string, args ...interface{}) error {
	// minimal comment
	baseMsg := fmt.Sprintf(format, args...)
	return fmt.Errorf("%w: %s: %w", ErrPluginExecution, baseMsg, specificError)
}

// --- END OF FINAL REVISED FILE pkg/converter/plugin/plugin.go ---
