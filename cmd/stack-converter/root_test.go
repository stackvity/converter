package main

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag" // Import pflag for VisitAll
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// executeCommand is a helper function to execute cobra command and capture output
func executeCommand(root *cobra.Command, args ...string) (stdout string, stderr string, err error) {
	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)
	root.SetOut(stdoutBuf)
	root.SetErr(stderrBuf)
	root.SetArgs(args)

	err = root.Execute()

	return stdoutBuf.String(), stderrBuf.String(), err
}

// TestRootCmdHelp tests the basic --help flag output structure
func TestRootCmdHelp(t *testing.T) {
	// Use the actual rootCmd as help generation is generally stateless
	stdout, stderr, err := executeCommand(rootCmd, "--help")

	require.NoError(t, err, "Executing --help should not produce an error")
	assert.Empty(t, stderr, "Executing --help should not produce stderr output")
	assert.Contains(t, stdout, "Usage:", "Help output should contain Usage section")
	assert.Contains(t, stdout, "stack-converter -i <inputDir> -o <outputDir>", "Help output should contain basic syntax")
	assert.Contains(t, stdout, "--input", "Help output should contain --input flag")
	assert.Contains(t, stdout, "--output", "Help output should contain --output flag")
	assert.Contains(t, stdout, "--version", "Help output should contain --version flag")
	assert.Contains(t, stdout, "--help", "Help output should contain --help flag")
}

// TestRootCmdHelp_AllFlagsPresent verifies all defined flags appear in help output
func TestRootCmdHelp_AllFlagsPresent(t *testing.T) {
	// Use the actual rootCmd
	stdout, stderr, err := executeCommand(rootCmd, "--help")
	require.NoError(t, err)
	assert.Empty(t, stderr)

	// Check local flags registered in init()
	rootCmd.Flags().VisitAll(func(f *pflag.Flag) {
		expectedFlagText := "--" + f.Name
		assert.Contains(t, stdout, expectedFlagText, "Help output should contain flag --%s", f.Name)
		if f.Shorthand != "" && f.ShorthandDeprecated == "" {
			expectedShorthandText := "-" + f.Shorthand + ","
			assert.Contains(t, stdout, expectedShorthandText, "Help output should contain shorthand -%s for flag --%s", f.Shorthand, f.Name)
		}
	})

	// Check persistent flags registered in init()
	rootCmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" { // Skip built-in help flag if needed
			return
		}
		expectedFlagText := "--" + f.Name
		assert.Contains(t, stdout, expectedFlagText, "Help output should contain persistent flag --%s", f.Name)
		if f.Shorthand != "" && f.ShorthandDeprecated == "" {
			expectedShorthandText := "-" + f.Shorthand + ","
			assert.Contains(t, stdout, expectedShorthandText, "Help output should contain persistent shorthand -%s for flag --%s", f.Shorthand, f.Name)
		}
	})
}

// TestRootCmdVersion tests the --version flag output format
func TestRootCmdVersion(t *testing.T) {
	originalVersion, originalCommit, originalDate := version, commit, date
	version = "test-1.2.3"
	commit = "testcommit123"
	date = "2024-01-01T10:00:00Z"
	defer func() {
		version, commit, date = originalVersion, originalCommit, originalDate
	}()

	// It's safer to test against a new instance if modifying global state,
	// but for version which is set directly on the command, using a test instance is cleaner.
	testCmd := &cobra.Command{Use: "stack-converter"}
	// Populate the version string for the test command instance
	testCmd.Version = fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date)
	testCmd.SetVersionTemplate(`{{.Use}} version {{.Version}}` + "\n")
	// Need to ensure the --version flag itself is added (Cobra does this by default if Version field is set)
	// testCmd.PersistentFlags().Bool("version", false, "Show application version") // Usually not needed

	stdout, stderr, err := executeCommand(testCmd, "--version")

	require.NoError(t, err, "Executing --version should not produce an error")
	assert.Empty(t, stderr, "Executing --version should not produce stderr output")

	expectedVersionString := fmt.Sprintf("stack-converter version %s (commit: %s, built: %s)\n", version, commit, date)
	assert.Equal(t, expectedVersionString, stdout, "Version output format or content mismatch")
}

// TestRootCmdFlagParsingErrors tests basic flag parsing errors handled by Cobra
func TestRootCmdFlagParsingErrors(t *testing.T) {
	// Create a fresh command instance for these tests to ensure isolation
	var testCmd *cobra.Command

	// Helper to reset command before each test case
	resetCmd := func() {
		// Create a new command instance mirroring rootCmd's structure
		testCmd = &cobra.Command{
			Use: "stack-converter -i <inputDir> -o <outputDir>",
			// Minimal RunE for parsing tests - prevents execution of main logic
			RunE: func(cmd *cobra.Command, args []string) error {
				return nil
			},
		}
		// Re-register necessary flags (especially required ones) for parsing checks
		testCmd.PersistentFlags().StringP("input", "i", "", "Required. Source code directory path.")
		testCmd.PersistentFlags().StringP("output", "o", "", "Required. Output directory path for Markdown files.")
		_ = testCmd.MarkPersistentFlagRequired("input")
		_ = testCmd.MarkPersistentFlagRequired("output")
		testCmd.Flags().Int("concurrency", 0, "Number of parallel workers") // Example other flag
	}

	testCases := []struct {
		name        string
		args        []string
		expectError bool
		errorMsg    string // Substring to look for in stderr
	}{
		{
			name:        "Unknown flag",
			args:        []string{"-i", ".", "-o", ".", "--unknown-flag"}, // Provide required flags
			expectError: true,
			errorMsg:    "unknown flag: --unknown-flag",
		},
		{
			name:        "Missing required input flag",
			args:        []string{"-o", "./out"},
			expectError: true,
			errorMsg:    "required flag(s) \"input\" not set",
		},
		{
			name:        "Missing required output flag",
			args:        []string{"-i", "./in"},
			expectError: true,
			errorMsg:    "required flag(s) \"output\" not set",
		},
		{
			name:        "Invalid value type for int flag",
			args:        []string{"-i", ".", "-o", ".", "--concurrency", "abc"},
			expectError: true,
			errorMsg:    "invalid argument \"abc\" for \"--concurrency\" flag",
		},
		{
			name:        "Valid flags (required only)",
			args:        []string{"-i", ".", "-o", "."},
			expectError: false, // No flag parsing error expected
			errorMsg:    "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resetCmd() // Reset command state for each test
			_, stderr, err := executeCommand(testCmd, tc.args...)

			if tc.expectError {
				require.Error(t, err, "Expected an error for args: %v", tc.args)
				if tc.errorMsg != "" {
					assert.Contains(t, stderr, tc.errorMsg, "Stderr should contain specific error message for args: %v", tc.args)
				}
			} else {
				require.NoError(t, err, "Expected no flag parsing error for args: %v", tc.args)
				assert.NotContains(t, stderr, "Error:", "Stderr should not contain 'Error:' for valid flags: %v", tc.args)
				assert.NotContains(t, stderr, "unknown flag:", "Stderr should not contain 'unknown flag:' for valid flags: %v", tc.args)
			}
		})
	}
}

// TestMain is needed to run tests
func TestMain(m *testing.M) {
	// Optional: Setup global test state if necessary
	code := m.Run()
	// Optional: Teardown global test state if necessary
	os.Exit(code)
}
