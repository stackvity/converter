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

	"github.com/stackvity/stack-converter/pkg/converter"
	"github.com/stackvity/stack-converter/pkg/converter/plugin" // Use constants/types from here
)

const (
	// maxLogOutputBytes limits the size of stdout/stderr captured in logs on JSON errors.
	maxLogOutputBytes = 1024
	// maxPluginReadBytes sets a limit on stdout/stderr read to prevent OOM from rogue plugins.
	maxPluginReadBytes = 10 * 1024 * 1024 // 10 MiB limit for stdout/stderr capture
)

// execPluginRunner implements the converter.PluginRunner interface using os/exec.
type execPluginRunner struct {
	logger *slog.Logger
}

// NewExecPluginRunner creates a new plugin runner that executes plugins as external processes.
func NewExecPluginRunner(loggerHandler slog.Handler) converter.PluginRunner { // minimal comment
	if loggerHandler == nil {
		loggerHandler = slog.NewTextHandler(io.Discard, nil)
	}
	logger := slog.New(loggerHandler).With(slog.String("component", "pluginRunner"))
	return &execPluginRunner{logger: logger}
}

// Run executes the specified plugin process.
func (r *execPluginRunner) Run(ctx context.Context, stage string, pluginCfg converter.PluginConfig, input converter.PluginInput) (converter.PluginOutput, error) { // minimal comment
	logArgs := []any{
		slog.String("plugin", pluginCfg.Name),
		slog.String("stage", stage),
		slog.String("path", input.FilePath),
	}

	if len(pluginCfg.Command) == 0 {
		err := fmt.Errorf("plugin command cannot be empty")
		r.logger.Error("Plugin configuration error", append(logArgs, slog.Any("error", err))...)
		return converter.PluginOutput{}, plugin.Errorf("plugin configuration error for '%s': %w", pluginCfg.Name, err)
	}

	input.SchemaVersion = plugin.PluginSchemaVersion

	inputJSON, marshalErr := json.Marshal(input)
	if marshalErr != nil {
		r.logger.Error("Failed to marshal plugin input JSON", append(logArgs, slog.Any("error", marshalErr))...)
		return converter.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, "failed to marshal input for plugin '%s': %v", pluginCfg.Name, marshalErr)
	}

	cmd := exec.CommandContext(ctx, pluginCfg.Command[0], pluginCfg.Command[1:]...)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		r.logger.Error("Failed to create stdin pipe for plugin", append(logArgs, slog.Any("error", err))...)
		return converter.PluginOutput{}, plugin.Errorf("failed to create stdin pipe for plugin '%s': %w", pluginCfg.Name, err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		r.logger.Error("Failed to create stdout pipe for plugin", append(logArgs, slog.Any("error", err))...)
		return converter.PluginOutput{}, plugin.Errorf("failed to create stdout pipe for plugin '%s': %w", pluginCfg.Name, err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		r.logger.Error("Failed to create stderr pipe for plugin", append(logArgs, slog.Any("error", err))...)
		return converter.PluginOutput{}, plugin.Errorf("failed to create stderr pipe for plugin '%s': %w", pluginCfg.Name, err)
	}

	if startErr := cmd.Start(); startErr != nil {
		r.logger.Error("Failed to start plugin process", append(logArgs, slog.String("command", strings.Join(pluginCfg.Command, " ")), slog.Any("error", startErr))...)
		return converter.PluginOutput{}, plugin.Errorf("failed to start plugin '%s' command '%s': %w", pluginCfg.Name, pluginCfg.Command[0], startErr)
	}

	r.logger.Debug("Plugin process started", logArgs...)

	var wg sync.WaitGroup
	var writeErr error
	var stdoutData, stderrData []byte
	var readStdoutErr, readStderrErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if closeErr := stdinPipe.Close(); closeErr != nil && !errors.Is(closeErr, syscall.EPIPE) && !errors.Is(closeErr, os.ErrClosed) && !strings.Contains(closeErr.Error(), "file already closed") {
				r.logger.Warn("Error closing plugin stdin pipe", append(logArgs, slog.Any("error", closeErr))...)
			}
		}()
		_, writeErr = stdinPipe.Write(inputJSON)
		if writeErr != nil {
			r.logger.Warn("Error writing to plugin stdin", append(logArgs, slog.Any("error", writeErr))...)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		var stdoutBuf bytes.Buffer
		lr := io.LimitReader(stdoutPipe, maxPluginReadBytes)
		// Use io.Copy instead of io.ReadAll to handle the limit correctly
		bytesRead, err := io.Copy(&stdoutBuf, lr)
		readStdoutErr = err // Store the error from io.Copy
		stdoutData = stdoutBuf.Bytes()

		// Check if the limit was reached by checking the number of bytes read
		if readStdoutErr == nil && bytesRead >= maxPluginReadBytes {
			// If Copy finished without error but read the limit, it means there was more data
			readStdoutErr = fmt.Errorf("plugin stdout exceeded limit of %d bytes", maxPluginReadBytes)
			r.logger.Warn("Plugin stdout truncated", append(logArgs, slog.Int64("limit_bytes", maxPluginReadBytes))...)
			// Drain the rest of the pipe to allow process to exit cleanly, discard data
			_, _ = io.Copy(io.Discard, stdoutPipe)
		} else if readStdoutErr != nil && !errors.Is(readStdoutErr, io.ErrClosedPipe) && !errors.Is(readStdoutErr, io.EOF) {
			r.logger.Warn("Error reading plugin stdout", append(logArgs, slog.Any("error", readStdoutErr))...)
		} else {
			// No error or expected EOF/ClosedPipe, clear readStdoutErr
			readStdoutErr = nil
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
			readStderrErr = nil // Treat truncation itself not as a fatal read error
		} else if readStderrErr != nil && !errors.Is(readStderrErr, io.ErrClosedPipe) && !errors.Is(readStderrErr, io.EOF) {
			r.logger.Warn("Error reading plugin stderr", append(logArgs, slog.Any("error", readStderrErr))...)
		} else {
			readStderrErr = nil // Clear expected EOF/ClosedPipe
		}
	}()

	waitErr := cmd.Wait()
	wg.Wait()
	stderrString := strings.TrimSpace(string(stderrData))

	if ctx.Err() != nil {
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin execution cancelled or timed out", append(logArgs, slog.Any("error", ctx.Err()))...)
		return converter.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginTimeout, "plugin '%s' execution cancelled or timed out: %v", pluginCfg.Name, ctx.Err())
	}

	if writeErr != nil && !errors.Is(writeErr, syscall.EPIPE) && !errors.Is(writeErr, os.ErrClosed) {
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin failed due to stdin write error", append(logArgs, slog.Any("error", writeErr))...)
		return converter.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginExecution, "failed writing input to plugin '%s': %v", pluginCfg.Name, writeErr)
	}

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
		r.logger.Error("Plugin execution failed (non-zero exit or other error)", logArgs...)
		return converter.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginNonZeroExit, "plugin '%s' execution failed with exit code %d: %v", pluginCfg.Name, exitCode, waitErr)
	}

	// Check for read errors *after* confirming successful process exit
	if readStdoutErr != nil {
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin execution succeeded but failed to read stdout", append(logArgs, slog.Any("error", readStdoutErr))...)
		// Report stdout read limit error specifically
		if strings.Contains(readStdoutErr.Error(), "stdout exceeded limit") {
			return converter.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, "plugin '%s' stdout exceeded read limit (%d bytes)", pluginCfg.Name, maxPluginReadBytes)
		}
		return converter.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, "error reading stdout from plugin '%s': %v", pluginCfg.Name, readStdoutErr)
	}
	if readStderrErr != nil {
		r.logger.Warn("Error occurred while reading plugin stderr after process exit", append(logArgs, slog.Any("error", readStderrErr))...)
		// Don't fail the run for stderr read errors if process succeeded otherwise
	}

	if len(stdoutData) == 0 {
		// FIX: Removed unused variable 'emptyOutputErr'
		// emptyOutputErr := errors.New("plugin returned empty stdout")
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin returned empty output", logArgs...)
		return converter.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, "plugin '%s' returned empty stdout", pluginCfg.Name)
	}

	var output converter.PluginOutput
	if unmarshalErr := json.Unmarshal(stdoutData, &output); unmarshalErr != nil {
		logStdout := string(stdoutData)
		if len(logStdout) > maxLogOutputBytes {
			logStdout = logStdout[:maxLogOutputBytes] + "... (truncated)"
		}

		logArgs = append(logArgs, slog.Any("error", unmarshalErr), slog.String("stdout_prefix", logStdout))
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Failed to unmarshal plugin output JSON", logArgs...)
		return converter.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, "failed to unmarshal JSON output from plugin '%s': %v", pluginCfg.Name, unmarshalErr)
	}

	if output.SchemaVersion != plugin.PluginSchemaVersion {
		schemaErr := fmt.Errorf("plugin '%s' uses incompatible schema version '%s', expected '%s'", pluginCfg.Name, output.SchemaVersion, plugin.PluginSchemaVersion)
		logArgs = append(logArgs, slog.String("expected_schema", plugin.PluginSchemaVersion), slog.String("plugin_schema", output.SchemaVersion))
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin schema version mismatch", logArgs...)
		return converter.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, schemaErr.Error())
	}

	if output.Error != "" {
		pluginReportedErr := errors.New(output.Error)
		logArgs = append(logArgs, slog.String("plugin_error", output.Error))
		if len(stderrString) > 0 {
			logArgs = append(logArgs, slog.String("plugin_stderr", stderrString))
		}
		r.logger.Error("Plugin reported functional error", logArgs...)
		return converter.PluginOutput{}, plugin.WrapPluginError(plugin.ErrPluginBadOutput, "plugin '%s' reported error: %v", pluginCfg.Name, pluginReportedErr)
	}

	if len(stderrString) > 0 {
		r.logger.Debug("Plugin stderr output (on success)", append(logArgs, slog.String("plugin_stderr", stderrString))...)
	}
	r.logger.Debug("Plugin finished successfully", logArgs...)
	return output, nil
}

// --- END OF FINAL REVISED FILE internal/cli/runner/runner.go ---
