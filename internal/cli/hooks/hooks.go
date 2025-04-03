// --- START OF FINAL REVISED FILE internal/cli/hooks/hooks.go ---
package hooks

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter"
)

// --- TUI Message Structs ---

type FileDiscoveredMsg struct{ Path string }

type FileStatusUpdateMsg struct {
	Path     string
	Status   converter.Status
	Message  string
	Duration time.Duration
}

type RunCompleteMsg struct{ Report converter.Report }

// --- Hook Implementation ---

type CLIHooks struct {
	logger         *slog.Logger
	tuiEnabled     bool
	verboseEnabled bool
	tuiProgram     TUIProgram
	progressBar    ProgressBar
	mu             sync.Mutex
}

type TUIProgram interface {
	Send(msg interface{})
}

type ProgressBar interface {
	Add(num int) error
	Describe(description string) error
	Close() error
}

// --- No-Op Implementations for Decoupling ---

type NoOpTUIProgram struct{}

// Send implements TUIProgram.
func (n *NoOpTUIProgram) Send(msg interface{}) {}

type NoOpProgressBar struct{}

// Add implements ProgressBar.
func (n *NoOpProgressBar) Add(num int) error { return nil }

// Describe implements ProgressBar.
func (n *NoOpProgressBar) Describe(description string) error { return nil }

// Close implements ProgressBar.
func (n *NoOpProgressBar) Close() error { return nil }

// --- Constructor ---

// NewCLIHooks creates a new CLIHooks instance.
func NewCLIHooks(logger *slog.Logger, tuiEnabled, verboseEnabled bool, tuiProg TUIProgram, progBar ProgressBar) converter.Hooks { // minimal comment
	if tuiProg == nil {
		tuiProg = &NoOpTUIProgram{}
	}
	if progBar == nil {
		progBar = &NoOpProgressBar{}
	}
	return &CLIHooks{
		logger:         logger,
		tuiEnabled:     tuiEnabled,
		verboseEnabled: verboseEnabled,
		tuiProgram:     tuiProg,
		progressBar:    progBar,
	}
}

// --- Interface Method Implementations ---

// OnFileDiscovered handles the event when a file or directory is found by the walker.
func (h *CLIHooks) OnFileDiscovered(path string) error { // minimal comment
	if h.tuiEnabled {
		h.tuiProgram.Send(FileDiscoveredMsg{Path: path})
	} else if h.verboseEnabled {
		h.logger.Debug("File discovered", "path", path)
	}
	// Note: Progress bar is not incremented here, only on file *completion* statuses.
	return nil
}

// OnFileStatusUpdate handles events when a file's processing status changes.
func (h *CLIHooks) OnFileStatusUpdate(path string, status converter.Status, message string, duration time.Duration) error { // minimal comment
	if h.tuiEnabled {
		h.tuiProgram.Send(FileStatusUpdateMsg{
			Path:     path,
			Status:   status,
			Message:  message, // Pass the full message (e.g., "skipped: binary file")
			Duration: duration,
		})
		return nil
	}

	// Verbose logging path - already handles detailed message correctly
	if h.verboseEnabled {
		logLevel := slog.LevelDebug
		logMsg := "File status updated"
		attrs := []any{
			slog.String("path", path),
			slog.String("status", string(status)),
		}
		if duration > 0 {
			attrs = append(attrs, slog.Duration("duration", duration))
		}
		if message != "" {
			logKey := "message"
			if status == converter.StatusFailed {
				logKey = "error" // Use 'error' key for Failed status messages
			} else if status == converter.StatusSkipped {
				logKey = "reason" // Use 'reason' key for Skipped status messages
			}
			attrs = append(attrs, slog.String(logKey, message)) // Log the full message
		}

		switch status {
		case converter.StatusSuccess, converter.StatusCached, converter.StatusSkipped:
			logLevel = slog.LevelInfo
		case converter.StatusFailed:
			logLevel = slog.LevelError
			logMsg = "File processing failed"
		}
		h.logger.Log(nil, logLevel, logMsg, attrs...)
		return nil
	}

	// Progress bar path - correctly increments on final states
	if h.progressBar != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		isFinalState := status == converter.StatusSuccess ||
			status == converter.StatusFailed ||
			status == converter.StatusSkipped ||
			status == converter.StatusCached
		if isFinalState {
			_ = h.progressBar.Add(1)
		}
		// Log only errors separately if progress bar is active
		if status == converter.StatusFailed {
			h.logger.Error("File processing failed", "path", path, "error", message)
		}
		return nil
	}

	// Standard log mode (non-TTY, non-verbose) - log only errors
	if status == converter.StatusFailed {
		h.logger.Error("File processing failed", "path", path, "error", message)
	}

	return nil
}

// OnRunComplete handles the event when the entire conversion process finishes.
func (h *CLIHooks) OnRunComplete(report converter.Report) error { // minimal comment
	if h.tuiEnabled {
		h.tuiProgram.Send(RunCompleteMsg{Report: report})
	} else {
		if h.progressBar != nil {
			// Ensure progress bar closure happens safely after potential Add calls
			h.mu.Lock()
			_ = h.progressBar.Close()
			h.mu.Unlock()
			// Print a newline to avoid the final summary overwriting the progress bar line
			_, _ = fmt.Fprintln(os.Stderr)
		}
		// Always log the final summary in non-TUI mode (unless JSON output is handled elsewhere)
		// This assumes the final summary logic is outside the hook, perhaps in cli.Run after GenerateDocs returns.
		// If the hook *is* responsible for the final log summary, add it here:
		// logArgs := []any{
		//     slog.Int("processed", report.Summary.ProcessedCount),
		//     // ... other summary fields ...
		// }
		// h.logger.Info("Run Complete", logArgs...)
	}
	return nil
}

// --- END OF FINAL REVISED FILE internal/cli/hooks/hooks.go ---
