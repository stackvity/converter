// --- START OF FINAL REVISED FILE pkg/converter/types.go ---
package converter

// Status defines the possible processing states of a file during conversion.
type Status string

// Constants representing the defined file processing statuses.
const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusSuccess    Status = "success"
	StatusFailed     Status = "failed"
	StatusSkipped    Status = "skipped"
	StatusCached     Status = "cached"
)

// OnErrorMode defines the behavior when a non-fatal error occurs during file processing.
type OnErrorMode string

const (
	OnErrorContinue OnErrorMode = "continue"
	OnErrorStop     OnErrorMode = "stop"
)

// BinaryMode defines how detected binary files should be handled during processing.
type BinaryMode string

// Constants representing the defined Binary handling modes.
const (
	BinarySkip        BinaryMode = "skip"
	BinaryPlaceholder BinaryMode = "placeholder"
	BinaryError       BinaryMode = "error"
)

// LargeFileMode defines how files exceeding the configured size threshold should be handled.
type LargeFileMode string

// Constants representing the defined Large File handling modes.
const (
	LargeFileSkip     LargeFileMode = "skip"
	LargeFileTruncate LargeFileMode = "truncate"
	LargeFileError    LargeFileMode = "error"
)

// OutputFormat defines the format for the final summary report printed to standard output when TUI is disabled.
type OutputFormat string

const (
	OutputFormatText OutputFormat = "text"
	OutputFormatJSON OutputFormat = "json"
)

// GitDiffMode defines the strategy for using Git differences to filter processed files.
type GitDiffMode string

const (
	GitDiffModeNone     GitDiffMode = "none"
	GitDiffModeDiffOnly GitDiffMode = "diffOnly"
	GitDiffModeSince    GitDiffMode = "since"
)

// --- END OF FINAL REVISED FILE pkg/converter/types.go ---
