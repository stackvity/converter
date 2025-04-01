// --- START OF FINAL REVISED FILE internal/cli/runner/runner_test.go ---
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec" // Keep os/exec for checking LookPath
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter"
	"github.com/stackvity/stack-converter/pkg/converter/plugin" // Use constants/types from here
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	// "github.com/xeipuuv/gojsonschema" // Placeholder for potential schema validation recommendation
)

// --- Test Helpers ---

// pythonInPath checks if python or python3 is likely in the PATH.
var pythonInPath bool = false

func init() {
	pythonExe := "python"
	if runtime.GOOS == "windows" {
		pythonExe = "python.exe"
		_, errPy := exec.LookPath(pythonExe)
		if errPy != nil {
			pythonExe = "python3.exe" // Try python3
			_, errPy3 := exec.LookPath(pythonExe)
			if errPy3 == nil {
				pythonInPath = true
			}
		} else {
			pythonInPath = true
		}
	} else {
		_, errPy := exec.LookPath("python3") // Prefer python3 on Unix-like
		if errPy == nil {
			pythonInPath = true
		} else {
			_, errPy2 := exec.LookPath("python")
			if errPy2 == nil {
				pythonInPath = true
			}
		}
	}
}

// skipIfNoPython skips tests requiring python if it's not found.
func skipIfNoPython(t *testing.T) {
	t.Helper()
	if !pythonInPath {
		t.Skip("Skipping Python plugin test: python/python3 not found in PATH")
	}
}

// createMockPluginScript creates a temporary script file.
func createMockPluginScript(t *testing.T, content string, filename string) string {
	t.Helper()
	// Enhanced Windows handling
	isWindows := runtime.GOOS == "windows"
	isPython := strings.HasSuffix(filename, ".py")
	isBatch := strings.HasSuffix(filename, ".bat") || strings.HasSuffix(filename, ".cmd")
	isPowershell := strings.HasSuffix(filename, ".ps1")
	isShell := !isPython && !isBatch && !isPowershell // Assume shell otherwise

	if isWindows && isShell {
		t.Skipf("Skipping standard shell script test '%s' on Windows", filename)
		return "" // Return empty to satisfy caller, test is skipped
	}
	if isPython {
		skipIfNoPython(t) // Skip if Python isn't available
	}

	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, filename)
	scriptContent := content

	// Adjust content based on OS/interpreter
	if isPython {
		// No adjustments needed assuming python is in PATH
	} else if !isWindows && isShell { // Assume Unix shell script
		if !strings.HasPrefix(content, "#!") { // Add shebang if missing
			scriptContent = "#!/bin/sh\n" + content
		}
	} else if isBatch {
		scriptContent = "@echo off\r\n" + strings.ReplaceAll(content, "\n", "\r\n") // Basic prefix and line endings
	} else if isPowershell {
		// PowerShell specific setup if needed
	}

	err := os.WriteFile(filePath, []byte(scriptContent), 0755) // Make executable
	require.NoError(t, err)
	return filePath
}

// getTestPluginCommand returns the command slice for executing the script.
func getTestPluginCommand(scriptPath string) []string {
	if strings.HasSuffix(scriptPath, ".py") {
		pythonExe := "python"
		if runtime.GOOS == "windows" {
			pythonExe = "python.exe"
			_, errPy := exec.LookPath(pythonExe)
			if errPy != nil {
				pythonExe = "python3.exe" // Try python3
				_, errPy3 := exec.LookPath(pythonExe)
				if errPy3 != nil {
					// Panic here because the test should have been skipped if neither was found
					panic("Python executable check failed despite skipIfNoPython passing")
				}
			}
		} else {
			_, errPy3 := exec.LookPath("python3") // Prefer python3
			if errPy3 == nil {
				pythonExe = "python3"
			}
			// else stick with "python" and hope it works
		}
		return []string{pythonExe, scriptPath}
	}
	if strings.HasSuffix(scriptPath, ".ps1") && runtime.GOOS == "windows" {
		return []string{"powershell", "-ExecutionPolicy", "Bypass", "-File", scriptPath}
	}
	// For shell/batch, just return the path directly
	return []string{scriptPath}
}

// --- Test Cases ---

func TestExecPluginRunner_Run_Success_ModifyContent(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import sys, json
try:
    input_data = json.load(sys.stdin)
    # Prepare output strictly matching the schema
    output_data = {
        "$schemaVersion": "` + plugin.PluginSchemaVersion + `",
        "error": "",
        "content": "PREFIX:" + input_data.get('content', ''), # Handle potential None
        "metadata": input_data.get('metadata'), # Pass metadata through
        "output": None
    }
    json.dump(output_data, sys.stdout)
except Exception as e:
    print(f"Plugin Error: {e}", file=sys.stderr)
    sys.exit(1)
`
	scriptPath := createMockPluginScript(t, scriptContent, "modify_content.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "Modifier", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{
		// Runner sets SchemaVersion internally now
		Stage:    plugin.PluginStagePreprocessor,
		FilePath: "test.txt",
		Content:  "Original",
		Metadata: map[string]interface{}{"key": "value"},
		Config:   map[string]interface{}{},
	}

	output, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	// TODO: Implement automated JSON schema validation here (Recommendation #7)

	require.NoError(t, err)
	assert.Equal(t, "PREFIX:Original", output.Content)
	assert.Equal(t, plugin.PluginSchemaVersion, output.SchemaVersion)
	assert.Equal(t, "", output.Error)
	assert.NotNil(t, output.Metadata)
	assert.Equal(t, "value", output.Metadata["key"])
	assert.Equal(t, "", output.Output)
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Success_ModifyMetadata(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import sys, json
try:
    input_data = json.load(sys.stdin)
    current_metadata = input_data.get('metadata', {})
    if current_metadata is None: # Handle case where input metadata is explicitly null
        current_metadata = {}
    current_metadata['new_key'] = "new_value"
    current_metadata['existing_key'] = "overwritten"
    output_data = {
        "$schemaVersion": "` + plugin.PluginSchemaVersion + `",
        "error": "",
        "content": None,
        "metadata": current_metadata,
        "output": None
    }
    json.dump(output_data, sys.stdout)
except Exception as e:
    print(f"Plugin Error: {e}", file=sys.stderr)
    sys.exit(1)
`
	scriptPath := createMockPluginScript(t, scriptContent, "modify_meta.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "MetaAdder", Stage: plugin.PluginStagePostprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{
		Stage:    plugin.PluginStagePostprocessor,
		FilePath: "test.md",
		Content:  "Markdown Content",
		Metadata: map[string]interface{}{"existing_key": "original"},
		Config:   map[string]interface{}{},
	}

	output, err := runner.Run(context.Background(), plugin.PluginStagePostprocessor, pluginCfg, input)

	// TODO: Implement automated JSON schema validation here (Recommendation #7)

	require.NoError(t, err)
	assert.Equal(t, "", output.Content)
	assert.Equal(t, plugin.PluginSchemaVersion, output.SchemaVersion)
	assert.Equal(t, "", output.Error)
	require.NotNil(t, output.Metadata)
	assert.Equal(t, "new_value", output.Metadata["new_key"])
	assert.Equal(t, "overwritten", output.Metadata["existing_key"])
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Success_FormatterOutput(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import sys, json
try:
    input_data = json.load(sys.stdin)
    output_data = {
        "$schemaVersion": "` + plugin.PluginSchemaVersion + `",
        "output": "<html>" + input_data.get('filePath', '') + "</html>", # Use get for safety
        "error": ""
    }
    json.dump(output_data, sys.stdout)
except Exception as e:
    print(f"Plugin Error: {e}", file=sys.stderr)
    sys.exit(1)
`
	scriptPath := createMockPluginScript(t, scriptContent, "formatter.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "HTMLFormatter", Stage: plugin.PluginStageFormatter, Enabled: true, Command: cmd}
	input := converter.PluginInput{
		Stage:    plugin.PluginStageFormatter,
		FilePath: "doc.rst",
		Content:  "RST Content",
		Metadata: map[string]interface{}{},
		Config:   map[string]interface{}{},
	}

	output, err := runner.Run(context.Background(), plugin.PluginStageFormatter, pluginCfg, input)

	// TODO: Implement automated JSON schema validation here (Recommendation #7)

	require.NoError(t, err)
	assert.Equal(t, "", output.Content)
	assert.Nil(t, output.Metadata)
	assert.Equal(t, "<html>doc.rst</html>", output.Output)
	assert.Equal(t, plugin.PluginSchemaVersion, output.SchemaVersion)
	assert.Equal(t, "", output.Error)
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Error_NonZeroExit(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import sys
print("Error message on stderr", file=sys.stderr)
sys.exit(5)
`
	scriptPath := createMockPluginScript(t, scriptContent, "fail_exit.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "Failer", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.True(t, errors.Is(err, plugin.ErrPluginNonZeroExit))
	assert.Contains(t, err.Error(), "execution failed with exit code 5")
	assert.Contains(t, logBuf.String(), "plugin_stderr=\"Error message on stderr\"")
	assert.Contains(t, logBuf.String(), "exitCode=5")
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Error_Timeout(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import time, sys
print("Starting sleep", file=sys.stderr)
sys.stderr.flush()
time.sleep(2)
# This part should not be reached if timeout works
print("Finished sleep", file=sys.stderr)
sys.stderr.flush()
# Try to output valid JSON just in case timeout fails somehow
output = {"$schemaVersion": "` + plugin.PluginSchemaVersion + `", "error": "Timeout failed?", "content":"Timeout"}
import json
json.dump(output, sys.stdout)
sys.exit(0)
`
	scriptPath := createMockPluginScript(t, scriptContent, "timeout.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "Sleeper", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond) // Adjusted timeout
	defer cancel()

	_, err := runner.Run(ctx, plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.True(t, errors.Is(err, plugin.ErrPluginTimeout))
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Plugin execution cancelled or timed out")
	assert.Contains(t, logOutput, "Starting sleep")
	assert.NotContains(t, logOutput, "Finished sleep")
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Error_BadJSONOutput(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import sys
print("This is not JSON")
sys.exit(0)
`
	scriptPath := createMockPluginScript(t, scriptContent, "bad_json.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "BadJSON", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.True(t, errors.Is(err, plugin.ErrPluginBadOutput))
	assert.Contains(t, err.Error(), "failed to unmarshal JSON output")
	assert.Contains(t, logBuf.String(), "stdout_prefix=This is not JSON") // Check prefix of captured stdout
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Error_EmptyStdout(t *testing.T) { // minimal comment
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `#!/bin/sh
# Shell script that exits 0 but prints nothing to stdout
exit 0
`
	scriptPath := createMockPluginScript(t, scriptContent, "empty_stdout.sh")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "EmptyStdout", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.True(t, errors.Is(err, plugin.ErrPluginBadOutput))
	assert.Contains(t, err.Error(), "plugin 'EmptyStdout' returned empty stdout")
	assert.Contains(t, logBuf.String(), "Plugin returned empty output")
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Error_PluginReportedError(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import sys, json
output = {"$schemaVersion": "` + plugin.PluginSchemaVersion + `", "error": "Plugin encountered internal error"}
json.dump(output, sys.stdout)
sys.exit(0)
`
	scriptPath := createMockPluginScript(t, scriptContent, "report_error.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "Reporter", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.True(t, errors.Is(err, plugin.ErrPluginBadOutput))
	assert.Contains(t, err.Error(), "plugin 'Reporter' reported error: Plugin encountered internal error")
	assert.Contains(t, logBuf.String(), "plugin_error=\"Plugin encountered internal error\"")
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Error_SchemaMismatch(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import sys, json
output = {"$schemaVersion": "0.9", "error": ""} # Wrong schema version
json.dump(output, sys.stdout)
sys.exit(0)
`
	scriptPath := createMockPluginScript(t, scriptContent, "wrong_schema.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "WrongSchema", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.True(t, errors.Is(err, plugin.ErrPluginBadOutput))
	assert.Contains(t, err.Error(), "uses incompatible schema version '0.9'")
	assert.Contains(t, err.Error(), fmt.Sprintf("expected '%s'", plugin.PluginSchemaVersion))
	assert.Contains(t, logBuf.String(), "Plugin schema version mismatch")
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Error_CommandNotFound(t *testing.T) { // minimal comment
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	cmd := []string{"command-that-does-not-exist-hopefully-12345", "arg"}

	pluginCfg := converter.PluginConfig{Name: "NotFound", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.Contains(t, err.Error(), "failed to start plugin 'NotFound'")
	assert.Contains(t, err.Error(), cmd[0]) // Check command name is in error
	assert.Contains(t, logBuf.String(), "Failed to start plugin process")
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Error_EmptyCommand(t *testing.T) { // minimal comment
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	pluginCfg := converter.PluginConfig{Name: "EmptyCmd", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: []string{}}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.Contains(t, err.Error(), "plugin command cannot be empty")
	assert.Contains(t, logBuf.String(), "Plugin configuration error")
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Security_NoCommandInjection(t *testing.T) { // minimal comment
	if runtime.GOOS == "windows" {
		t.Skip("Shell command injection test format may differ on Windows")
	}
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	injectionAttempt := "; touch /tmp/injection_test_file_runner; echo"
	testFile := "/tmp/injection_test_file_runner"
	_ = os.Remove(testFile)

	cmd := []string{"echo", "arg1", injectionAttempt, "arg2"}

	pluginCfg := converter.PluginConfig{Name: "InjectTest", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "secure.txt", Content: "secure content"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginBadOutput))
	assert.False(t, errors.Is(err, plugin.ErrPluginNonZeroExit))

	_, statErr := os.Stat(testFile)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "File should NOT have been created by injected command")

	_ = os.Remove(testFile)
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Error_ExcessiveStdout(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := fmt.Sprintf(`
import sys
try:
    _ = sys.stdin.read()
    # Write slightly more than the limit
    large_string = "A" * (%d + 1)
    print(large_string)
    sys.exit(0)
except Exception as e:
    print(f"Plugin Error: {e}", file=sys.stderr)
    sys.exit(1)
`, maxPluginReadBytes) // Use constant in script generation
	scriptPath := createMockPluginScript(t, scriptContent, "big_stdout.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "BigStdout", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	// Expect it to fail specifically because the stdout limit was exceeded
	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.True(t, errors.Is(err, plugin.ErrPluginBadOutput))
	assert.Contains(t, err.Error(), "stdout exceeded read limit")
	assert.Contains(t, err.Error(), fmt.Sprintf("(%d bytes)", maxPluginReadBytes))
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Plugin stdout truncated")
	t.Log(logBuf.String())
}

func TestExecPluginRunner_Run_Error_ExcessiveStderr(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := fmt.Sprintf(`
import sys, json
try:
    input_data = json.load(sys.stdin)
    # Write slightly more than limit to stderr
    large_stderr = "E" * (%d + 1)
    print(large_stderr, file=sys.stderr)
    # Write valid output
    output_data = {
        "$schemaVersion": "`+plugin.PluginSchemaVersion+`",
        "error": "",
        "content": input_data.get('content'),
        "metadata": input_data.get('metadata')
    }
    json.dump(output_data, sys.stdout)
    sys.exit(0)
except Exception as e:
    print(f"Plugin Error: {e}", file=sys.stderr)
    sys.exit(1)
`, maxPluginReadBytes) // Use constant
	scriptPath := createMockPluginScript(t, scriptContent, "big_stderr.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "BigStderr", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt", Content: "Test"}

	output, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.NoError(t, err) // Functional success despite large stderr
	assert.Equal(t, "Test", output.Content)
	assert.Equal(t, "", output.Error)

	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Plugin stderr truncated") // Check warning log
	assert.Contains(t, logOutput, "Plugin stderr output (on success)")
	assert.Contains(t, logOutput, "plugin_stderr=EEEEE")
	assert.Contains(t, logOutput, "... (truncated)") // Check that logged stderr is truncated
	assert.Less(t, len(logOutput), 3*1024*1024, "Log output should not contain the full stderr")
	t.Log(logBuf.String())
}

// --- END OF FINAL REVISED FILE internal/cli/runner/runner_test.go ---
