// --- START OF FINAL REVISED FILE pkg/converter/errors.go ---
package converter

import "errors"

// --- Exported Error Variables ---
// These errors represent specific categories of issues that might be returned
// directly by GenerateDocs or encountered during processing. Library users
// can check against these using errors.Is.

var (
	// ErrReadFailed indicates a failure to read a source file from the filesystem.
	// This might be due to permissions, the file being deleted after discovery, or other I/O issues.
	// May be returned wrapped by GenerateDocs if fatal, or included in Report.Errors if non-fatal.
	ErrReadFailed = errors.New("failed to read file")

	// ErrStatFailed indicates a failure to get file statistics (like ModTime or Size) using os.Stat.
	// This might be due to permissions or the file being deleted.
	// May be returned wrapped by GenerateDocs if fatal, or included in Report.Errors if non-fatal.
	ErrStatFailed = errors.New("failed to get file stats")

	// ErrBinaryFile indicates that a file was detected as binary and the configured
	// BinaryMode is "error".
	// May be returned wrapped by GenerateDocs if fatal, or included in Report.Errors if non-fatal.
	ErrBinaryFile = errors.New("binary file encountered")

	// ErrLargeFile indicates that a file exceeded the configured LargeFileThreshold
	// and the configured LargeFileMode is "error".
	// May be returned wrapped by GenerateDocs if fatal, or included in Report.Errors if non-fatal.
	ErrLargeFile = errors.New("large file encountered")

	// ErrPluginExecution indicates a general failure during the execution of an external plugin.
	// This could encompass various specific issues like timeouts, non-zero exits, invalid output, or
	// errors reported by the plugin itself. Use errors.Is to check for this general category,
	// or check for more specific errors like ErrPluginTimeout, ErrPluginNonZeroExit, etc.
	// Check logs for detailed stderr/reason.
	// May be returned wrapped by GenerateDocs if fatal, or included in Report.Errors if non-fatal.
	ErrPluginExecution = errors.New("plugin execution failed")

	// ErrPluginTimeout indicates that a plugin process exceeded the execution timeout
	// controlled by the context passed to the PluginRunner.
	// Allows differentiation from other plugin failures. errors.Is(err, ErrPluginExecution) will also be true.
	ErrPluginTimeout = errors.New("plugin execution timed out")

	// ErrPluginNonZeroExit indicates that a plugin process exited with a non-zero status code,
	// signifying an error according to the process convention.
	// Allows differentiation from other plugin failures. errors.Is(err, ErrPluginExecution) will also be true.
	ErrPluginNonZeroExit = errors.New("plugin exited non-zero")

	// ErrPluginBadOutput indicates that the plugin produced output (stdout) that could not be
	// successfully unmarshalled into the expected PluginOutput JSON structure, or the output
	// JSON contained a non-empty "error" field reported by the plugin itself.
	// Allows differentiation from other plugin failures. errors.Is(err, ErrPluginExecution) will also be true.
	ErrPluginBadOutput = errors.New("plugin returned invalid output or reported error")

	// ErrFrontMatterGen indicates a failure during the generation or marshalling of front matter.
	// This might be due to invalid data types in static/included fields or issues with the YAML/TOML encoder.
	// May be returned wrapped by GenerateDocs if fatal, or included in Report.Errors if non-fatal.
	ErrFrontMatterGen = errors.New("failed to generate front matter")

	// ErrTemplateExecution indicates an error occurred during the execution phase of the Go template.
	// This might be due to invalid template syntax (unlikely if parsing succeeded), undefined functions,
	// or errors accessing metadata fields within the template logic.
	// May be returned wrapped by GenerateDocs if fatal, or included in Report.Errors if non-fatal.
	ErrTemplateExecution = errors.New("template execution failed")

	// ErrMkdirFailed indicates a failure to create a necessary output subdirectory.
	// This is often due to filesystem permissions.
	// May be returned wrapped by GenerateDocs if fatal, or included in Report.Errors if non-fatal.
	ErrMkdirFailed = errors.New("failed to create output directory")

	// ErrWriteFailed indicates a failure to write the final generated content to an output file.
	// This might be due to permissions, disk space exhaustion, or other filesystem I/O errors.
	// May be returned wrapped by GenerateDocs if fatal, or included in Report.Errors if non-fatal.
	ErrWriteFailed = errors.New("failed to write output file")

	// ErrConfigValidation indicates that the provided Options struct failed validation checks
	// performed at the beginning of GenerateDocs (e.g., invalid paths, invalid modes).
	// This is typically returned directly as a fatal error by GenerateDocs.
	ErrConfigValidation = errors.New("invalid configuration options provided")

	// ErrConfigHashCalculation indicates an internal error occurred while trying to calculate
	// the configuration hash needed for cache validation.
	// This is typically returned directly as a fatal error by GenerateDocs.
	ErrConfigHashCalculation = errors.New("failed to calculate config hash")

	// ErrTruncationFailed indicates an error occurred while attempting to truncate
	// file content based on the LargeFileMode="truncate" configuration.
	// This might be due to invalid truncation parameters or internal processing errors.
	// May be returned wrapped by GenerateDocs if fatal, or included in Report.Errors if non-fatal.
	ErrTruncationFailed = errors.New("failed to truncate content")

	// ErrMetadataConversion indicates an internal error occurred during the conversion
	// between the internal TemplateMetadata struct and the map[string]interface{} representation
	// used for plugins or front matter.
	// This typically indicates a programming error and may be returned as a fatal error.
	ErrMetadataConversion = errors.New("failed metadata conversion")

	// ErrCacheLoad indicates an error occurred while loading or decoding the cache index file.
	// This is typically treated as a cache miss and logged as a warning, not returned as fatal.
	ErrCacheLoad = errors.New("failed to load cache index")

	// ErrCachePersist indicates an error occurred while persisting the cache index file.
	// This is typically logged as an error, but the main conversion run may still succeed.
	ErrCachePersist = errors.New("failed to persist cache index")

	// ErrGitOperation indicates a failure during a Git operation performed via the GitClient.
	// This might be due to the path not being a repository, an invalid reference, or errors
	// running the underlying Git command or using the Git library.
	// May be returned as fatal if Git data was essential for the run (e.g., diff filtering).
	ErrGitOperation = errors.New("git operation failed")
)

// --- END OF FINAL REVISED FILE pkg/converter/errors.go ---
