// --- START OF FINAL REVISED FILE internal/cli/runner/runner.go ---
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall" // Import syscall for checking specific errors like EPIPE

	// FIX: Remove direct import of parent converter package
	// "github.com/stackvity/stack-converter/pkg/converter"
	"github.com/stackvity/stack-converter/pkg/converter/plugin" // Use constants/types from here
)

const (
	// maxLogOutputBytes limits the size of stdout/stderr captured in logs on JSON errors.
	maxLogOutputBytes = 1024
	// maxPluginReadBytes sets a limit on stdout/stderr read to prevent OOM from rogue plugins.
	maxPluginReadBytes = 10 * 1024 * 1024 // 10 MiB limit for stdout/stderr capture
)

// execPluginRunner implements the plugin.PluginRunner interface using os/exec.
type execPluginRunner struct {
	logger *slog.Logger
}

// NewExecPluginRunner creates a new plugin runner that executes plugins as external processes.
// FIX: Change return type from converter.PluginRunner to plugin.PluginRunner
func NewExecPluginRunner(loggerHandler slog.Handler) plugin.PluginRunner { // minimal comment
	if loggerHandler == nil {
		// Discard logs if no handler provided, though typically the CLI provides one.
		loggerHandler = slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})
	}
	logger := slog.New(loggerHandler).With(slog.String("component", "pluginRunner"))
	return &execPluginRunner{logger: logger}
}

// Run executes the specified plugin process according to the defined protocol.
// FIX: Use plugin.* types for parameters and return value
func (r *execPluginRunner) Run(ctx context.Context, stage string, pluginCfg plugin.PluginConfig, input plugin.PluginInput) (plugin.PluginOutput, error) { // minimal comment
	logArgs := []any{
		slog.String("plugin", pluginCfg.Name),
		slog.String("stage", stage),
		slog.String("path", input.FilePath),
	}

	// Configuration Validation
	if len(pluginCfg.Command) == 0 {
		err := fmt.Errorf("plugin command cannot be empty")
		r.logger.Error("Plugin configuration error", append(logArgs, slog.Any("error", err))...)
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.Errorf("plugin configuration error for '%s': %w", pluginCfg.Name, err)
	}

	// Prepare Input
	input.SchemaVersion = plugin.PluginSchemaVersion // Ensure correct schema version is sent
	inputJSON, marshalErr := json.Marshal(input)
	if marshalErr != nil {
		r.logger.Error("Failed to marshal plugin input JSON", append(logArgs, slog.Any("error", marshalErr))...)
		// Wrap specific error type ErrPluginBadOutput
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, "failed to marshal input for plugin '%s': %v", pluginCfg.Name, marshalErr)
	}

	// Setup Command Execution
	// Use CommandContext for timeout/cancellation handling.
	// Pass command and args separately to prevent command injection.
	cmd := exec.CommandContext(ctx, pluginCfg.Command[0], pluginCfg.Command[1:]...)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		r.logger.Error("Failed to create stdin pipe for plugin", append(logArgs, slog.Any("error", err))...)
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.Errorf("failed to create stdin pipe for plugin '%s': %w", pluginCfg.Name, err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		r.logger.Error("Failed to create stdout pipe for plugin", append(logArgs, slog.Any("error", err))...)
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.Errorf("failed to create stdout pipe for plugin '%s': %w", pluginCfg.Name, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		r.logger.Error("Failed to create stderr pipe for plugin", append(logArgs, slog.Any("error", err))...)
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.Errorf("failed to create stderr pipe for plugin '%s': %w", pluginCfg.Name, err)
	}

	// Start Plugin Process
	if startErr := cmd.Start(); startErr != nil {
		r.logger.Error("Failed to start plugin process", append(logArgs, slog.String("command", strings.Join(pluginCfg.Command, " ")), slog.Any("error", startErr))...)
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.Errorf("failed to start plugin '%s' command '%s': %w", pluginCfg.Name, pluginCfg.Command[0], startErr)
	}
	r.logger.Debug("Plugin process started", logArgs...)

	// Manage I/O asynchronously using Goroutines
	var wg sync.WaitGroup
	var writeErr error
	var stdoutData, stderrData []byte
	var readStdoutErr, readStderrErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			// Close stdin pipe to signal EOF to plugin after writing.
			// Ignore common pipe errors that occur if plugin exits early.
			if closeErr := stdinPipe.Close(); closeErr != nil && !errors.Is(closeErr, syscall.EPIPE) && !errors.Is(closeErr, os.ErrClosed) && !strings.Contains(closeErr.Error(), "file already closed") {
				r.logger.Warn("Error closing plugin stdin pipe", append(logArgs, slog.Any("error", closeErr))...)
			}
		}()
		_, writeErr = stdinPipe.Write(inputJSON)
		if writeErr != nil && !errors.Is(writeErr, syscall.EPIPE) && !errors.Is(writeErr, os.ErrClosed) {
			// Log only significant write errors, not expected pipe errors on early exit.
			r.logger.Warn("Error writing to plugin stdin", append(logArgs, slog.Any("error", writeErr))...)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		var stdoutBuf bytes.Buffer
		// Limit reading to prevent OOM attacks via excessive stdout.
		lr := io.LimitReader(stdoutPipe, maxPluginReadBytes)
		bytesRead, err := io.Copy(&stdoutBuf, lr)
		readStdoutErr = err // Capture potential io.Copy error
		stdoutData = stdoutBuf.Bytes()

		// Check if limit was hit, indicating truncation.
		if readStdoutErr == nil && bytesRead >= maxPluginReadBytes {
			readStdoutErr = fmt.Errorf("plugin stdout exceeded limit of %d bytes", maxPluginReadBytes)
			r.logger.Warn("Plugin stdout truncated", append(logArgs, slog.Int64("limit_bytes", maxPluginReadBytes))...)
			_, _ = io.Copy(io.Discard, stdoutPipe) // Consume remaining data to avoid blocking plugin.
		} else if readStdoutErr != nil && !errors.Is(readStdoutErr, io.ErrClosedPipe) && !errors.Is(readStdoutErr, io.EOF) {
			r.logger.Warn("Error reading plugin stdout", append(logArgs, slog.Any("error", readStdoutErr))...)
		} else {
			readStdoutErr = nil // Ignore expected EOF or ClosedPipe errors.
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		var stderrBuf bytes.Buffer
		lr := io.LimitReader(stderrPipe, maxPluginReadBytes)
		bytesRead, err := io.Copy(&stderrBuf, lr)
		readStderrErr = err
		stderrData = stderrBuf.Bytes()

		if readStderrErr == nil && bytesRead >= maxPluginReadBytes {
			r.logger.Warn("Plugin stderr truncated", append(logArgs, slog.Int64("limit_bytes", maxPluginReadBytes))...)
			_, _ = io.Copy(io.Discard, stderrPipe)
			readStderrErr = nil // Consider truncation a warning, not a fatal read error itself.
		} else if readStderrErr != nil && !errors.Is(readStderrErr, io.ErrClosedPipe) && !errors.Is(readStderrErr, io.EOF) {
			r.logger.Warn("Error reading plugin stderr", append(logArgs, slog.Any("error", readStderrErr))...)
		} else {
			readStderrErr = nil // Ignore expected EOF or ClosedPipe errors.
		}
	}()

	// Wait for command completion and capture error.
	waitErr := cmd.Wait()
	// Wait for all I/O goroutines to finish.
	wg.Wait()
	stderrString := strings.TrimSpace(string(stderrData))

	// --- Process Results and Errors (Order is important) ---

	// 1. Check for Context Cancellation (Timeout or external cancellation)
	if ctx.Err() != nil {
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin execution cancelled or timed out", append(logArgs, slog.Any("error", ctx.Err()))...)
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginTimeout, "plugin '%s' execution cancelled or timed out: %v", pluginCfg.Name, ctx.Err())
	}

	// 2. Check for errors writing to plugin's stdin (excluding pipe errors)
	if writeErr != nil && !errors.Is(writeErr, syscall.EPIPE) && !errors.Is(writeErr, os.ErrClosed) {
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin failed due to stdin write error", append(logArgs, slog.Any("error", writeErr))...)
		// A stdin write error likely prevents the plugin from working correctly.
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginExecution, "failed writing input to plugin '%s': %v", pluginCfg.Name, writeErr)
	}

	// 3. Check for process exit errors (Non-zero exit code or other Wait error)
	if waitErr != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		logArgs = append(logArgs, slog.Int("exitCode", exitCode), slog.Any("error", waitErr))
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin execution failed (non-zero exit or other wait error)", logArgs...)
		// Use specific error for non-zero exit.
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginNonZeroExit, "plugin '%s' execution failed with exit code %d: %v", pluginCfg.Name, exitCode, waitErr)
	}

	// ---- Process exited successfully (code 0) ----

	// 4. Check for errors reading plugin's stdout (e.g., truncation)
	if readStdoutErr != nil {
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin execution succeeded but failed to read stdout", append(logArgs, slog.Any("error", readStdoutErr))...)
		// Failure to read stdout means we can't get the result.
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, "error reading stdout from plugin '%s': %v", pluginCfg.Name, readStdoutErr)
	}

	// 5. Log stderr read errors as warnings if they occurred, but don't fail the run.
	if readStderrErr != nil {
		r.logger.Warn("Error occurred while reading plugin stderr after process exit", append(logArgs, slog.Any("error", readStderrErr))...)
	}

	// 6. Check if stdout is empty (valid exit 0, but no output JSON)
	if len(stdoutData) == 0 {
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin returned empty output", logArgs...)
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, "plugin '%s' returned empty stdout", pluginCfg.Name)
	}

	// 7. Attempt to unmarshal the JSON output
	// FIX: Unmarshal into plugin.PluginOutput type
	var output plugin.PluginOutput
	if unmarshalErr := json.Unmarshal(stdoutData, &output); unmarshalErr != nil {
		logStdout := string(stdoutData)
		if len(logStdout) > maxLogOutputBytes { // Log truncated stdout on error
			logStdout = logStdout[:maxLogOutputBytes] + "... (truncated)"
		}
		logArgs = append(logArgs, slog.Any("error", unmarshalErr), slog.String("stdout_prefix", logStdout))
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Failed to unmarshal plugin output JSON", logArgs...)
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, "failed to unmarshal JSON output from plugin '%s': %v", pluginCfg.Name, unmarshalErr)
	}

	// 8. Check schema version compatibility
	if output.SchemaVersion != plugin.PluginSchemaVersion {
		schemaErr := fmt.Errorf("plugin '%s' uses incompatible schema version '%s', expected '%s'", pluginCfg.Name, output.SchemaVersion, plugin.PluginSchemaVersion)
		logArgs = append(logArgs, slog.String("expected_schema", plugin.PluginSchemaVersion), slog.String("plugin_schema", output.SchemaVersion))
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin schema version mismatch", logArgs...)
		// FIX: Return plugin.PluginOutput zero value
		// FIX: Pass schemaErr as argument, not format string
		return plugin.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, "schema version mismatch for plugin '%s': %v", pluginCfg.Name, schemaErr)
	}

	// 9. Check if the plugin reported a functional error in its JSON output
	if output.Error != "" {
		pluginReportedErr := errors.New(output.Error)
		logArgs = append(logArgs, slog.String("plugin_error", output.Error))
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin reported functional error", logArgs...)
		// Return ErrPluginBadOutput, wrapping the plugin's specific error message.
		// FIX: Return plugin.PluginOutput zero value
		return plugin.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, "plugin '%s' reported error: %v", pluginCfg.Name, pluginReportedErr)
	}

	// --- Success Case ---
	// Log stderr output at Debug level if present on successful run.
	if len(stderrString) > 0 {
		r.logger.Debug("Plugin stderr output (on success)", append(logArgs, slog.String("plugin_stderr", stderrString))...)
	}
	r.logger.Debug("Plugin finished successfully", logArgs...)
	return output, nil // Return the parsed output and nil error
}

// --- END OF FINAL REVISED FILE internal/cli/runner/runner.go ---
