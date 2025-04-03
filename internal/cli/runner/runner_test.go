// --- START OF FINAL REVISED FILE internal/cli/runner/runner_test.go ---
package runner

import (
	"bytes"
	"context" // Required for PluginOutput comparison and potential schema validation
	"errors"
	"fmt" // Required for io.Discard
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
	// Example: Add import for a JSON schema validator library
	// "github.com/xeipuuv/gojsonschema"
)

// --- Test Helpers ---

// pythonInPath checks if python or python3 is likely in the PATH.
var pythonInPath bool = false
var pythonExecutable string = ""

// init finds a python executable, preferring python3.
func init() {
	// Use OS-specific executable names
	executables := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		// Check common Windows Python executable names
		executables = []string{"python.exe", "py.exe", "python3.exe"}
	}

	for _, exe := range executables {
		path, err := exec.LookPath(exe)
		if err == nil {
			pythonInPath = true
			pythonExecutable = path // Store the full path found
			break
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

// createMockPluginScript creates a temporary script file with appropriate line endings and shebang.
func createMockPluginScript(t *testing.T, content string, filename string) string { // minimal comment
	t.Helper()
	isWindows := runtime.GOOS == "windows"
	isPython := strings.HasSuffix(filename, ".py")
	isBatch := strings.HasSuffix(filename, ".bat") || strings.HasSuffix(filename, ".cmd")
	isPowershell := strings.HasSuffix(filename, ".ps1")
	isShell := !isPython && !isBatch && !isPowershell

	if isWindows && isShell {
		t.Skipf("Skipping standard shell script test '%s' on Windows", filename)
		return ""
	}
	if isPython {
		skipIfNoPython(t)
	}

	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, filename)
	scriptContent := content

	// Adjust line endings and shebangs for script types
	if isPython {
		// Python handles line endings generally, no shebang needed for direct execution via executable path
	} else if !isWindows && isShell {
		if !strings.HasPrefix(content, "#!") {
			scriptContent = "#!/bin/sh\n" + content // Add standard POSIX shell shebang
		}
		scriptContent = strings.ReplaceAll(scriptContent, "\r\n", "\n") // Ensure Unix line endings
	} else if isBatch {
		scriptContent = "@echo off\r\n" + strings.ReplaceAll(content, "\n", "\r\n") // Ensure Windows line endings
	} else if isPowershell {
		scriptContent = strings.ReplaceAll(content, "\n", "\r\n") // Ensure Windows line endings
	}

	// Write file and make it executable
	err := os.WriteFile(filePath, []byte(scriptContent), 0755)
	require.NoError(t, err)
	return filePath
}

// getTestPluginCommand returns the command slice for executing the script.
func getTestPluginCommand(scriptPath string) []string { // minimal comment
	if strings.HasSuffix(scriptPath, ".py") {
		if !pythonInPath {
			panic("Python executable check failed despite skipIfNoPython passing")
		}
		// Pass the script path as an argument to the python executable
		return []string{pythonExecutable, scriptPath}
	}
	if strings.HasSuffix(scriptPath, ".ps1") && runtime.GOOS == "windows" {
		psExe := "powershell.exe"              // Default
		path, err := exec.LookPath("pwsh.exe") // Prefer modern PowerShell Core if available
		if err == nil {
			psExe = path
		} else {
			path, err = exec.LookPath("powershell.exe") // Fallback to Windows PowerShell
			if err != nil {
				panic("powershell.exe/pwsh.exe not found in PATH for .ps1 tests on Windows")
			}
			psExe = path
		}
		// Use -NoProfile to speed up execution slightly and avoid profile interference
		return []string{psExe, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath}
	}
	// For shell scripts (.sh) on Unix or batch files (.bat/.cmd) on Windows,
	// the OS can execute them directly if the path is provided as the command.
	return []string{scriptPath}
}

// --- Test Cases ---

// TestExecPluginRunner_Run_Success_ModifyContent verifies basic success scenario.
func TestExecPluginRunner_Run_Success_ModifyContent(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	// Plugin script ensures a fully valid PluginOutput JSON
	scriptContent := `
import sys, json
try:
    input_data = json.load(sys.stdin)
    output_data = {
        "$schemaVersion": "` + plugin.PluginSchemaVersion + `",
        "error": "",
        "content": "PREFIX:" + input_data.get('content', ''),
        "metadata": input_data.get('metadata'),
        "output": None # Explicitly null for non-formatter
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
		Stage:    plugin.PluginStagePreprocessor,
		FilePath: "test.txt",
		Content:  "Original",
		Metadata: map[string]interface{}{"key": "value"},
		Config:   map[string]interface{}{},
	}

	output, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.NoError(t, err)
	assert.Equal(t, plugin.PluginSchemaVersion, output.SchemaVersion)
	assert.Equal(t, "", output.Error)
	assert.Equal(t, "PREFIX:Original", output.Content)
	require.NotNil(t, output.Metadata)
	assert.Equal(t, "value", output.Metadata["key"])
	assert.Nil(t, output.Output) // Check output field is nil as expected
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Success_ModifyMetadata verifies metadata modification.
func TestExecPluginRunner_Run_Success_ModifyMetadata(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import sys, json
try:
    input_data = json.load(sys.stdin)
    current_metadata = input_data.get('metadata', {}) or {}
    current_metadata['new_key'] = "new_value"
    current_metadata['existing_key'] = "overwritten"
    output_data = {
        "$schemaVersion": "` + plugin.PluginSchemaVersion + `",
        "error": "",
        "content": None, # Indicate no content change
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

	require.NoError(t, err)
	assert.Equal(t, plugin.PluginSchemaVersion, output.SchemaVersion)
	assert.Equal(t, "", output.Error)
	assert.Nil(t, output.Content) // Verify content is nil
	require.NotNil(t, output.Metadata)
	assert.Equal(t, "new_value", output.Metadata["new_key"])
	assert.Equal(t, "overwritten", output.Metadata["existing_key"])
	assert.Nil(t, output.Output)
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Success_FormatterOutput verifies formatter stage returning output.
func TestExecPluginRunner_Run_Success_FormatterOutput(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import sys, json
try:
    input_data = json.load(sys.stdin)
    output_data = {
        "$schemaVersion": "` + plugin.PluginSchemaVersion + `",
        "error": "",
        # Content and metadata omitted (implicitly null)
        "output": "<html>OUTPUT:" + input_data.get('filePath', '') + "</html>"
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

	require.NoError(t, err)
	assert.Equal(t, plugin.PluginSchemaVersion, output.SchemaVersion)
	assert.Equal(t, "", output.Error)
	assert.Nil(t, output.Content)  // Check content is nil
	assert.Nil(t, output.Metadata) // Check metadata is nil
	assert.Equal(t, "<html>OUTPUT:doc.rst</html>", output.Output)
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Error_NonZeroExit verifies handling of plugin non-zero exit.
func TestExecPluginRunner_Run_Error_NonZeroExit(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import sys
print("Error: Something went wrong internally!", file=sys.stderr)
sys.exit(15) # Specific non-zero exit code
`
	scriptPath := createMockPluginScript(t, scriptContent, "fail_exit.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "Failer", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution), "Error should wrap ErrPluginExecution")
	assert.True(t, errors.Is(err, plugin.ErrPluginNonZeroExit), "Error should wrap ErrPluginNonZeroExit")
	assert.Contains(t, err.Error(), "execution failed with exit code 15")
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, `plugin=Failer`)
	assert.Contains(t, logOutput, `exitCode=15`)
	assert.Contains(t, logOutput, `plugin_stderr="Error: Something went wrong internally!"`)
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Error_Timeout verifies handling of plugin timeout.
func TestExecPluginRunner_Run_Error_Timeout(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import time, sys
print("Starting long sleep...", file=sys.stderr)
sys.stderr.flush() # Ensure message is sent before sleep
time.sleep(5)
print("Finished sleep (should not happen)", file=sys.stderr)
# Try to output valid JSON just in case timeout fails somehow
output = {"$schemaVersion": "` + plugin.PluginSchemaVersion + `", "error": "Timeout failed?", "content":"Timeout"}
import json
try:
	json.dump(output, sys.stdout)
except Exception:
	pass # Ignore stdout errors if pipe closed due to timeout
sys.exit(0)
`
	scriptPath := createMockPluginScript(t, scriptContent, "timeout.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "Sleeper", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond) // Short timeout
	defer cancel()

	_, err := runner.Run(ctx, plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution), "Error should wrap ErrPluginExecution")
	assert.True(t, errors.Is(err, plugin.ErrPluginTimeout), "Error should wrap ErrPluginTimeout")
	assert.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled), "Underlying error should be context deadline/canceled")
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Plugin execution cancelled or timed out")
	assert.Contains(t, logOutput, `plugin=Sleeper`)
	assert.Contains(t, logOutput, `plugin_stderr="Starting long sleep..."`) // Check captured stderr
	assert.NotContains(t, logOutput, "Finished sleep")                      // Verify plugin was killed
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Error_BadJSONOutput verifies handling of non-JSON output.
func TestExecPluginRunner_Run_Error_BadJSONOutput(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `
import sys
# Output invalid JSON (missing closing brace)
sys.stdout.write('{"$schemaVersion": "` + plugin.PluginSchemaVersion + `", "error": ""')
print("Stderr message", file=sys.stderr)
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
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Failed to unmarshal plugin output JSON")
	assert.Contains(t, logOutput, `plugin=BadJSON`)
	// Check that the truncated invalid JSON is logged
	assert.Contains(t, logOutput, `stdout_prefix="{\"$schemaVersion\": \"`+plugin.PluginSchemaVersion+`\", \"error\": \"\""`)
	assert.Contains(t, logOutput, `plugin_stderr="Stderr message"`)
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Error_EmptyStdout verifies handling of empty stdout.
func TestExecPluginRunner_Run_Error_EmptyStdout(t *testing.T) { // minimal comment
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	scriptContent := `#!/bin/sh
# No stdout output, exit cleanly
echo "stderr message" >&2
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
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Plugin returned empty output")
	assert.Contains(t, logOutput, `plugin=EmptyStdout`)
	assert.Contains(t, logOutput, `plugin_stderr="stderr message"`)
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Error_PluginReportedError verifies handling of error field in JSON.
func TestExecPluginRunner_Run_Error_PluginReportedError(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	pluginErrMsg := "Input file format not supported by plugin"
	scriptContent := fmt.Sprintf(`
import sys, json
# Output valid JSON, but with the error field populated
output = {"$schemaVersion": "%s", "error": "%s"}
json.dump(output, sys.stdout)
print("stderr: Check input format.", file=sys.stderr)
sys.exit(0) # Exit 0 because the error is functional, not executional
`, plugin.PluginSchemaVersion, pluginErrMsg)
	scriptPath := createMockPluginScript(t, scriptContent, "report_error.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "Reporter", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.True(t, errors.Is(err, plugin.ErrPluginBadOutput), "Error should wrap ErrPluginBadOutput")
	assert.Contains(t, err.Error(), "plugin 'Reporter' reported error")
	assert.Contains(t, err.Error(), pluginErrMsg) // Ensure plugin's error message is included
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Plugin reported functional error")
	assert.Contains(t, logOutput, `plugin=Reporter`)
	assert.Contains(t, logOutput, fmt.Sprintf(`plugin_error="%s"`, pluginErrMsg))
	assert.Contains(t, logOutput, `plugin_stderr="stderr: Check input format."`)
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Error_SchemaMismatch verifies handling of schema version mismatch.
func TestExecPluginRunner_Run_Error_SchemaMismatch(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	wrongSchema := "2.0-alpha" // Plugin claims a newer, incompatible version
	scriptContent := fmt.Sprintf(`
import sys, json
# Output JSON with an incorrect schema version
output = {"$schemaVersion": "%s", "error": ""}
json.dump(output, sys.stdout)
sys.exit(0)
`, wrongSchema)
	scriptPath := createMockPluginScript(t, scriptContent, "wrong_schema.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "WrongSchema", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.True(t, errors.Is(err, plugin.ErrPluginBadOutput))
	assert.Contains(t, err.Error(), "uses incompatible schema version")
	assert.Contains(t, err.Error(), fmt.Sprintf("'%s'", wrongSchema))
	assert.Contains(t, err.Error(), fmt.Sprintf("expected '%s'", plugin.PluginSchemaVersion))
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Plugin schema version mismatch")
	assert.Contains(t, logOutput, `plugin=WrongSchema`)
	assert.Contains(t, logOutput, fmt.Sprintf(`expected_schema=%s`, plugin.PluginSchemaVersion))
	assert.Contains(t, logOutput, fmt.Sprintf(`plugin_schema=%s`, wrongSchema))
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Error_CommandNotFound verifies handling of non-existent command.
func TestExecPluginRunner_Run_Error_CommandNotFound(t *testing.T) { // minimal comment
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	nonExistentCommand := "/path/to/totally/fake/command-" + time.Now().Format("20060102150405")
	cmd := []string{nonExistentCommand}

	pluginCfg := converter.PluginConfig{Name: "NotFound", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.False(t, errors.Is(err, plugin.ErrPluginNonZeroExit))
	assert.False(t, errors.Is(err, plugin.ErrPluginBadOutput))
	assert.Contains(t, err.Error(), "failed to start plugin 'NotFound'")
	assert.Contains(t, err.Error(), nonExistentCommand)
	assert.True(t, errors.Is(err, exec.ErrNotFound) || strings.Contains(err.Error(), "no such file or directory"), "Underlying error should indicate command not found")
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Failed to start plugin process")
	assert.Contains(t, logOutput, `plugin=NotFound`)
	assert.Contains(t, logOutput, nonExistentCommand)
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Error_EmptyCommand verifies handling of empty command slice.
func TestExecPluginRunner_Run_Error_EmptyCommand(t *testing.T) { // minimal comment
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	pluginCfg := converter.PluginConfig{Name: "EmptyCmd", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: []string{}} // Empty command
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.False(t, errors.Is(err, plugin.ErrPluginNonZeroExit))
	assert.False(t, errors.Is(err, plugin.ErrPluginBadOutput))
	assert.Contains(t, err.Error(), "plugin command cannot be empty")
	assert.Contains(t, err.Error(), "plugin configuration error for 'EmptyCmd'")
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Plugin configuration error")
	assert.Contains(t, logOutput, `plugin=EmptyCmd`)
	assert.Contains(t, logOutput, "plugin command cannot be empty")
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Security_NoCommandInjection verifies secure command execution.
func TestExecPluginRunner_Run_Security_NoCommandInjection(t *testing.T) { // minimal comment
	if runtime.GOOS == "windows" {
		t.Skip("Command injection test uses Unix shell metacharacters")
	}
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	// Simple script that echoes its arguments safely, one per line, in JSON content field
	scriptContent := `#!/bin/sh
echo -n '{"$schemaVersion":"` + plugin.PluginSchemaVersion + `","error":"","content":"'
# Loop through args, escaping potentially problematic characters for JSON string
first=1
for arg in "$@"; do
  if [ "$first" -ne 1 ]; then
    printf '\\n' # Add newline between args in JSON string
  fi
  printf '%s' "$arg" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/\n/\\n/g'
  first=0
done
echo -n '"}'
exit 0
`
	scriptPath := createMockPluginScript(t, scriptContent, "safe_echo_args.sh")
	cmd := getTestPluginCommand(scriptPath)

	// Attempt injection as an argument
	injectionAttempt := "; id > /tmp/injection_test_runner.txt; echo 'pwned'"
	testFile := "/tmp/injection_test_runner.txt"
	_ = os.Remove(testFile)

	// The injection attempt is passed as a single argument element.
	pluginCfg := converter.PluginConfig{Name: "InjectTest", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: append(cmd, "safe_arg1", injectionAttempt, "safe_arg2")}
	input := converter.PluginInput{FilePath: "secure.txt", Content: "ignored"}

	output, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.NoError(t, err) // Echo script should succeed
	// Verify the output content literally contains the injection attempt as an argument string
	expectedContent := "safe_arg1\\n; id > /tmp/injection_test_runner.txt; echo 'pwned'\\nsafe_arg2"
	assert.Equal(t, expectedContent, output.Content)

	// Verify the injected command did NOT execute
	_, statErr := os.Stat(testFile)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "File should NOT have been created by injected command")

	_ = os.Remove(testFile)
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Error_ExcessiveStdout verifies handling of stdout exceeding limit.
func TestExecPluginRunner_Run_Error_ExcessiveStdout(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	// Writes slightly more than maxPluginReadBytes to stdout, then exits 0
	scriptContent := fmt.Sprintf(`
import sys
try:
    _ = sys.stdin.read() # Consume stdin
    # Write slightly more than the limit
    large_string = "S" * (%d + 50) # Ensure it's clearly over
    sys.stdout.write(large_string)
    sys.stdout.flush()
    print("stderr after stdout spam", file=sys.stderr) # Still write to stderr
    sys.exit(0)
except Exception as e:
    print(f"Plugin Error: {e}", file=sys.stderr)
    sys.exit(1)
`, maxPluginReadBytes)
	scriptPath := createMockPluginScript(t, scriptContent, "big_stdout.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "BigStdout", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt"}

	_, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.Error(t, err)
	assert.True(t, errors.Is(err, plugin.ErrPluginExecution))
	assert.True(t, errors.Is(err, plugin.ErrPluginBadOutput), "Exceeding stdout limit should be ErrPluginBadOutput")
	assert.Contains(t, err.Error(), "stdout exceeded read limit")
	assert.Contains(t, err.Error(), fmt.Sprintf("%d bytes", maxPluginReadBytes))
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Plugin stdout truncated")
	assert.Contains(t, logOutput, `plugin=BigStdout`)
	assert.Contains(t, logOutput, "stderr after stdout spam") // Verify stderr was still captured
	t.Log(logBuf.String())
}

// TestExecPluginRunner_Run_Error_ExcessiveStderr verifies handling of stderr exceeding limit.
func TestExecPluginRunner_Run_Error_ExcessiveStderr(t *testing.T) { // minimal comment
	skipIfNoPython(t)
	logBuf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	runner := NewExecPluginRunner(logger.Handler())

	// Writes excessive stderr, but valid JSON stdout
	scriptContent := fmt.Sprintf(`
import sys, json
try:
    input_data = json.load(sys.stdin)
    # Write slightly more than limit to stderr
    large_stderr = "E" * (%d + 50)
    sys.stderr.write(large_stderr)
    sys.stderr.flush()
    # Write valid output to stdout
    output_data = {
        "$schemaVersion": input_data.get('$schemaVersion', 'unknown'),
        "error": "",
        "content": "Success Content",
        "metadata": {"status": "ok"}
    }
    json.dump(output_data, sys.stdout)
    sys.exit(0)
except Exception as e:
    print(f"Plugin Error: {e}", file=sys.stderr)
    sys.exit(1)
`, maxPluginReadBytes)
	scriptPath := createMockPluginScript(t, scriptContent, "big_stderr.py")
	cmd := getTestPluginCommand(scriptPath)

	pluginCfg := converter.PluginConfig{Name: "BigStderr", Stage: plugin.PluginStagePreprocessor, Enabled: true, Command: cmd}
	input := converter.PluginInput{FilePath: "a.txt", Content: "Input"}

	output, err := runner.Run(context.Background(), plugin.PluginStagePreprocessor, pluginCfg, input)

	require.NoError(t, err) // Expect success because stderr limit doesn't cause failure
	assert.Equal(t, "Success Content", output.Content)
	assert.Equal(t, "", output.Error)
	require.NotNil(t, output.Metadata)
	assert.Equal(t, "ok", output.Metadata["status"])

	// Check logs for truncation warning and truncated stderr log
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Plugin stderr truncated")
	assert.Contains(t, logOutput, "Plugin stderr output (on success)") // Debug message on success
	assert.Contains(t, logOutput, `plugin=BigStderr`)
	loggedStderr := getLogAttr(t, logOutput, "plugin_stderr")
	require.NotEmpty(t, loggedStderr, "Logged stderr should not be empty")
	assert.True(t, strings.HasPrefix(loggedStderr, strings.Repeat("E", 100)), "Logged stderr should have expected prefix") // Check prefix
	assert.True(t, strings.HasSuffix(loggedStderr, "... (truncated)"), "Logged stderr should indicate truncation")         // Check suffix
	assert.NotContains(t, logOutput, strings.Repeat("E", maxPluginReadBytes+1))                                            // Ensure full large stderr isn't logged
	t.Log(logBuf.String())
}

// Helper to extract attribute value from structured log line (basic parsing)
func getLogAttr(t *testing.T, logOutput, key string) string { // minimal comment
	t.Helper()
	// Simple search for key="value", handles potential quotes within value poorly
	// but sufficient for these tests.
	searchKey := key + `="`
	startIdx := strings.Index(logOutput, searchKey)
	if startIdx == -1 {
		return ""
	}
	startIdx += len(searchKey)
	// Find the closing quote, respecting escaped quotes
	endIdx := -1
	escaped := false
	for i := startIdx; i < len(logOutput); i++ {
		if logOutput[i] == '\\' && !escaped {
			escaped = true
			continue
		}
		if logOutput[i] == '"' && !escaped {
			endIdx = i
			break
		}
		escaped = false
	}

	if endIdx == -1 {
		return "" // Closing quote not found
	}

	// Crude unescaping for basic comparison needs
	val := logOutput[startIdx:endIdx]
	val = strings.ReplaceAll(val, `\"`, `"`)
	val = strings.ReplaceAll(val, `\\`, `\`)
	return val
}

// TestPluginRunner_JSONSchemaValidation (Example Placeholder)
func TestPluginRunner_JSONSchemaValidation(t *testing.T) { // minimal comment
	t.Skip("Skipping JSON Schema validation test - requires implementing schema loading and validation library integration.")
	// Placeholder steps:
	// 1. Load the actual `docs/plugin_schema.md` (or derived JSON schema file) for PluginOutput.
	// 2. Configure a mock plugin to return a *valid* JSON string matching the success case.
	// 3. Run the plugin runner.
	// 4. Capture the raw stdoutData []byte from the runner (might require modifying runner or using test hooks).
	// 5. Use a JSON schema validation library (e.g., xeipuuv/gojsonschema) to validate stdoutData against the loaded schema.
	// 6. Assert validation passes.
	// 7. Configure another mock plugin to return *invalid* JSON (e.g., missing required field "$schemaVersion").
	// 8. Run runner, capture stdoutData.
	// 9. Validate against schema.
	// 10. Assert validation fails with specific schema violation errors.
}

// --- END OF FINAL REVISED FILE internal/cli/runner/runner_test.go ---
