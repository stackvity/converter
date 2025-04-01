// --- START OF FINAL REVISED FILE internal/cli/hooks/hooks.go ---
package hooks

import (
	"fmt"
	// "io" // io is no longer needed directly for Stderr
	"log/slog"
	"os" // Import the 'os' package for os.Stderr
	"sync"
	"time"

	// Use tea "github.com/charmbracelet/bubbletea" // Import if directly interacting with tea.Program
	// Use progressbar "github.com/schollz/progressbar/v3" // Import if directly interacting with progressbar
	"github.com/stackvity/stack-converter/pkg/converter"
)

// --- TUI Message Structs ---
// Note: Consider moving these definitions to the package containing the TUI
// implementation (e.g., internal/cli/ui/) for better organization.

// FileDiscoveredMsg signals that a file/directory was found by the walker.
type FileDiscoveredMsg struct{ Path string }

// FileStatusUpdateMsg signals a change in a file's processing status.
type FileStatusUpdateMsg struct {
	Path     string
	Status   converter.Status
	Message  string
	Duration time.Duration
}

// RunCompleteMsg signals the completion of the entire conversion run.
type RunCompleteMsg struct{ Report converter.Report }

// --- Hook Implementation ---

// CLIHooks implements the converter.Hooks interface, bridging library events
// to the CLI's UI layer (TUI, Logger, Progress Bar).
type CLIHooks struct {
	logger         *slog.Logger
	tuiEnabled     bool
	verboseEnabled bool
	tuiProgram     TUIProgram  // Decoupled TUI program interface
	progressBar    ProgressBar // Decoupled Progress Bar interface
	mu             sync.Mutex  // Protects concurrent access to progressBar
}

// TUIProgram defines the interface needed to interact with the Bubble Tea program.
type TUIProgram interface {
	Send(msg interface{})
}

// ProgressBar defines the interface needed to interact with the progress bar.
type ProgressBar interface {
	Add(num int) error
	Describe(description string) error
	Close() error
}

// --- No-Op Implementations for Decoupling ---

// NoOpTUIProgram provides a default null implementation.
type NoOpTUIProgram struct{}

// Send implements TUIProgram.
func (n *NoOpTUIProgram) Send(msg interface{}) {}

// NoOpProgressBar provides a default null implementation.
type NoOpProgressBar struct{}

// Add implements ProgressBar.
func (n *NoOpProgressBar) Add(num int) error { return nil }

// Describe implements ProgressBar.
func (n *NoOpProgressBar) Describe(description string) error { return nil }

// Close implements ProgressBar.
func (n *NoOpProgressBar) Close() error { return nil }

// --- Constructor ---

// NewCLIHooks creates a new CLIHooks instance.
// Pass nil for tuiProgram or progressBar if not applicable; NoOp versions will be used.
func NewCLIHooks(logger *slog.Logger, tuiEnabled, verboseEnabled bool, tuiProg TUIProgram, progBar ProgressBar) converter.Hooks {
	if tuiProg == nil {
		tuiProg = &NoOpTUIProgram{} // Ensure non-nil interface
	}
	if progBar == nil {
		progBar = &NoOpProgressBar{} // Ensure non-nil interface
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
func (h *CLIHooks) OnFileDiscovered(path string) error {
	if h.tuiEnabled {
		h.tuiProgram.Send(FileDiscoveredMsg{Path: path})
	} else if h.verboseEnabled {
		h.logger.Debug("File discovered", "path", path)
	}
	return nil // Library ignores hook errors
}

// OnFileStatusUpdate handles events when a file's processing status changes.
// This method MUST be thread-safe.
func (h *CLIHooks) OnFileStatusUpdate(path string, status converter.Status, message string, duration time.Duration) error {
	// TUI Mode: Send a message
	if h.tuiEnabled {
		h.tuiProgram.Send(FileStatusUpdateMsg{
			Path:     path,
			Status:   status,
			Message:  message,
			Duration: duration,
		})
		return nil
	}

	// Non-TUI Modes:
	// Verbose Logging Mode
	if h.verboseEnabled {
		logLevel := slog.LevelDebug // Default for most statuses
		logMsg := "File status updated"
		attrs := []any{
			slog.String("path", path),
			slog.String("status", string(status)),
		}
		if duration > 0 {
			attrs = append(attrs, slog.Duration("duration", duration))
		}
		if message != "" {
			// Use "error" key if status is Failed, otherwise "message"
			logKey := "message"
			if status == converter.StatusFailed {
				logKey = "error"
			}
			attrs = append(attrs, slog.String(logKey, message))
		}

		switch status {
		case converter.StatusSuccess, converter.StatusCached, converter.StatusSkipped:
			logLevel = slog.LevelInfo
		case converter.StatusFailed:
			logLevel = slog.LevelError
			logMsg = "File processing failed"
		}
		// Log using the handler passed to the logger in NewCLIHooks
		// Using logger.Log(nil, ...) indicates logging without a specific context.
		h.logger.Log(nil, logLevel, logMsg, attrs...)
		return nil
	}

	// Progress Bar Mode (Non-Verbose, TTY)
	if h.progressBar != nil {
		// Protect concurrent updates to progress bar
		h.mu.Lock()
		defer h.mu.Unlock()

		// Only increment progress bar on final states
		isFinalState := status == converter.StatusSuccess ||
			status == converter.StatusFailed ||
			status == converter.StatusSkipped ||
			status == converter.StatusCached

		if isFinalState {
			_ = h.progressBar.Add(1) // Ignore potential error
			// Optionally update description briefly
			// _ = h.progressBar.Describe(fmt.Sprintf("Processed: %s", filepath.Base(path)))
		}

		// Log errors even in progress bar mode
		if status == converter.StatusFailed {
			h.logger.Error("File processing failed", "path", path, "error", message)
		}
	}

	// Standard Log Mode (Non-Verbose, Non-TTY, Non-Progress)
	// Only log errors
	if !h.tuiEnabled && !h.verboseEnabled && h.progressBar == nil {
		if status == converter.StatusFailed {
			h.logger.Error("File processing failed", "path", path, "error", message)
		}
	}

	return nil // Library ignores hook errors
}

// OnRunComplete handles the event when the entire conversion process finishes.
// Sends the final report to the TUI or finalizes the progress bar.
func (h *CLIHooks) OnRunComplete(report converter.Report) error {
	if h.tuiEnabled {
		h.tuiProgram.Send(RunCompleteMsg{Report: report})
	} else {
		// Final text summary logging is now handled by the main CLI logic in root.go
		// This hook only needs to finalize the progress bar if it was used.
		if h.progressBar != nil {
			// Ensure mutex protection during close, although contention is less likely here.
			h.mu.Lock()
			_ = h.progressBar.Close() // Ignore error closing bar
			h.mu.Unlock()
			// Add a newline after the progress bar finishes to prevent prompt overlap.
			// Use os.Stderr instead of io.Stderr
			_, _ = fmt.Fprintf(os.Stderr, "\n") // FIX: Use os.Stderr
		}
	}
	return nil // Library ignores hook errors
}

// --- END OF FINAL REVISED FILE internal/cli/hooks/hooks.go ---
