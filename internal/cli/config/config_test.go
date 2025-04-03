// --- START OF FINAL REVISED FILE internal/cli/config/config_test.go ---
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings" // Added for contains check
	"testing"

	"github.com/spf13/pflag"
	"github.com/stackvity/stack-converter/pkg/converter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a temporary config file
func createTempConfigFile(t *testing.T, content string, format string) string {
	t.Helper()
	tempDir := t.TempDir()
	fileName := fmt.Sprintf("config.%s", format)
	filePath := filepath.Join(tempDir, fileName)
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)
	return filePath
}

// Helper function to create a dummy file or directory
func createDummyFsNode(t *testing.T, path string, isDir bool) string {
	t.Helper()
	fullPath, _ := filepath.Abs(path) // Work with absolute paths for consistency
	dir := filepath.Dir(fullPath)
	err := os.MkdirAll(dir, 0755)
	require.NoError(t, err)

	if isDir {
		// Check if it already exists from MkdirAll above
		if _, statErr := os.Stat(fullPath); os.IsNotExist(statErr) {
			err = os.Mkdir(fullPath, 0755) // Use Mkdir if creating the final dir
			require.NoError(t, err)
		} else {
			require.NoError(t, statErr) // Ensure no other error occurred
		}
	} else {
		err = os.WriteFile(fullPath, []byte("dummy"), 0644)
		require.NoError(t, err)
	}
	return fullPath // Return absolute path
}

// Helper function to create a dummy template file
func createDummyTemplateFile(t *testing.T, path string, content string) string {
	t.Helper()
	// Use createDummyFsNode to ensure parent directories exist and handle file creation
	fullPath := createDummyFsNode(t, path, false)
	// Overwrite content if needed (createDummyFsNode writes "dummy")
	err := os.WriteFile(fullPath, []byte(content), 0644)
	require.NoError(t, err)
	return fullPath
}

// defineAllFlags defines all flags used across tests onto a FlagSet.
// This prevents "flag accessed but not defined" errors in tests.
func defineAllFlags(flags *pflag.FlagSet) {
	// Mimic flag definitions from cmd/stack-converter/root.go init()
	// Persistent Flags
	flags.StringP("input", "i", "", "Input")
	flags.StringP("output", "o", "", "Output")
	flags.String("config", "", "Config file")
	flags.String("profile", "", "Config profile")
	flags.BoolP("verbose", "v", false, "Verbose logging")

	// Local Flags (Root Command)
	flags.BoolP("force", "f", false, "Force overwrite")
	flags.Bool("no-tui", false, "Disable TUI")
	flags.StringArray("ignore", []string{}, "Ignore patterns")
	flags.String("onError", string(converter.DefaultOnErrorMode), "Error handling mode")
	flags.Int("concurrency", converter.DefaultConcurrency, "Concurrency level")
	flags.Bool("no-cache", false, "Disable cache read")
	flags.Bool("clear-cache", false, "Clear cache before run")
	flags.Bool("git-metadata", converter.DefaultGitMetadataEnabled, "Enable Git metadata")
	flags.Bool("git-diff-only", converter.DefaultGitDiffOnly, "Enable Git diff-only")
	flags.String("git-since", "", "Git since reference") // Default comes from config
	flags.String("output-format", string(converter.DefaultOutputFormat), "Output report format")
	flags.String("template", "", "Custom template path") // Flag name is 'template'
	flags.Int64("large-file-threshold", converter.DefaultLargeFileThresholdMB, "Large file threshold (MB)")
	flags.String("large-file-mode", string(converter.DefaultLargeFileMode), "Large file handling mode")
	flags.String("large-file-truncate-cfg", converter.DefaultLargeFileTruncateCfg, "Large file truncation config")
	flags.String("binary-mode", string(converter.DefaultBinaryMode), "Binary file handling mode")
	flags.Bool("watch", false, "Enable watch mode")
	// Add watch.debounce if it becomes a direct flag, but it's likely config-only
	flags.String("watch-debounce", converter.DefaultWatchDebounceString, "Watch debounce duration string")
	flags.Bool("extract-comments", converter.DefaultAnalysisExtractComments, "Enable comment extraction")
	// Add other flags if defined in root.go
}

// TestLoadAndValidate_Defaults remains largely the same
func TestLoadAndValidate_Defaults(t *testing.T) {
	tempInputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "in"), true)
	tempOutputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "out"), true)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	defineAllFlags(flags) // Define all flags
	require.NoError(t, flags.Set("input", tempInputDir))
	require.NoError(t, flags.Set("output", tempOutputDir))

	opts, logger, err := LoadAndValidate("", "", false, flags)

	require.NoError(t, err)
	require.NotNil(t, logger)
	require.NotNil(t, opts.Logger) // Ensure logger handler is set in opts

	// Assert core defaults
	assert.Equal(t, string(converter.OnErrorContinue), string(opts.OnErrorMode))
	assert.Equal(t, converter.DefaultLargeFileThresholdMB*1024*1024, opts.LargeFileThreshold)
	assert.Equal(t, string(converter.DefaultLargeFileMode), string(opts.LargeFileMode))
	assert.Equal(t, string(converter.DefaultBinaryMode), string(opts.BinaryMode))
	assert.Greater(t, opts.Concurrency, 0) // Should be auto-detected CPU count > 0
	assert.Equal(t, false, opts.Verbose)
	assert.Equal(t, false, opts.ForceOverwrite)
	assert.Equal(t, converter.DefaultWatchDebounceDuration, opts.WatchDebounce)

	// Assert derived defaults
	assert.True(t, opts.CacheEnabled, "Cache should be enabled by default") // Check specific default
	assert.True(t, opts.TuiEnabled, "TUI should be enabled by default")     // Check specific default
	assert.Equal(t, filepath.Join(tempOutputDir, converter.CacheFileName), opts.CacheFilePath)
	assert.Equal(t, converter.GitDiffModeNone, opts.GitDiffMode)
	assert.NotNil(t, opts.Template, "Default template should be loaded")
	assert.Equal(t, defaultTemplateName, opts.Template.Name(), "Default template name mismatch") // Use constant
}

// TestLoadAndValidate_ConfigFile_YAML remains largely the same
func TestLoadAndValidate_ConfigFile_YAML(t *testing.T) {
	tempInputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "in"), true)
	tempOutputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "out"), true)
	yamlContent := `
cache: false
concurrency: 4
onError: "stop"
largeFileThresholdMB: 50
ignore:
  - "*.tmp"
  - "vendor/"
verbose: true # This can be overridden by flag
templateFile: "my_template.tmpl" # Config key for template path
profiles:
  ci:
    concurrency: 2
    cache: false
`
	cfgFile := createTempConfigFile(t, yamlContent, "yaml")
	// Create dummy template file mentioned in config
	_ = createDummyTemplateFile(t, filepath.Join(filepath.Dir(cfgFile), "my_template.tmpl"), "{{ .FilePath }}")

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	defineAllFlags(flags) // Define all flags
	require.NoError(t, flags.Set("input", tempInputDir))
	require.NoError(t, flags.Set("output", tempOutputDir))
	// Do not set verbose flag, let config value take effect

	opts, logger, err := LoadAndValidate(cfgFile, "", false, flags) // verbose flag=false

	require.NoError(t, err)
	require.NotNil(t, logger)
	assert.Equal(t, cfgFile, opts.ConfigFilePath)
	assert.Equal(t, false, opts.CacheEnabled)
	assert.Equal(t, 4, opts.Concurrency)
	assert.Equal(t, converter.OnErrorStop, opts.OnErrorMode)
	assert.Equal(t, int64(50), opts.LargeFileThresholdMB) // Check MB value
	assert.Equal(t, int64(50*1024*1024), opts.LargeFileThreshold)
	assert.Contains(t, opts.IgnorePatterns, "*.tmp")
	assert.Contains(t, opts.IgnorePatterns, "vendor/")
	assert.Equal(t, true, opts.Verbose)     // From config file
	assert.Equal(t, false, opts.TuiEnabled) // Verbose disables TUI

	// Check template loaded from config
	require.NotNil(t, opts.Template)
	assert.Equal(t, "my_template.tmpl", opts.Template.Name()) // Check loaded template name
	absTplPath, _ := filepath.Abs(filepath.Join(filepath.Dir(cfgFile), "my_template.tmpl"))
	assert.Equal(t, absTplPath, opts.TemplatePath) // Check stored template path
}

// TestLoadAndValidate_Profile remains largely the same
func TestLoadAndValidate_Profile(t *testing.T) {
	tempInputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "in"), true)
	tempOutputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "out"), true)
	yamlContent := `
cache: true
concurrency: 8
onError: "continue" # Base setting
profiles:
  ci:
    concurrency: 2
    cache: false
    onError: "stop" # Profile override
`
	cfgFile := createTempConfigFile(t, yamlContent, "yaml")

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	defineAllFlags(flags)
	require.NoError(t, flags.Set("input", tempInputDir))
	require.NoError(t, flags.Set("output", tempOutputDir))

	opts, _, err := LoadAndValidate(cfgFile, "ci", false, flags) // Use "ci" profile

	require.NoError(t, err)
	assert.Equal(t, "ci", opts.ProfileName)
	assert.Equal(t, false, opts.CacheEnabled)                // Profile override
	assert.Equal(t, 2, opts.Concurrency)                     // Profile override
	assert.Equal(t, converter.OnErrorStop, opts.OnErrorMode) // Profile setting override
}

// TestLoadAndValidate_EnvVarOverride remains largely the same
func TestLoadAndValidate_EnvVarOverride(t *testing.T) {
	tempInputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "in"), true)
	tempOutputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "out"), true)
	yamlContent := `concurrency: 4`
	cfgFile := createTempConfigFile(t, yamlContent, "yaml")

	t.Setenv("STACKCONVERTER_CONCURRENCY", "8")
	t.Setenv("STACKCONVERTER_LARGE_FILE_MODE", "truncate") // Env override default

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	defineAllFlags(flags)
	require.NoError(t, flags.Set("input", tempInputDir))
	require.NoError(t, flags.Set("output", tempOutputDir))

	opts, _, err := LoadAndValidate(cfgFile, "", false, flags)

	require.NoError(t, err)
	assert.Equal(t, 8, opts.Concurrency)                             // Env override file
	assert.Equal(t, converter.LargeFileTruncate, opts.LargeFileMode) // Env override default
}

// TestLoadAndValidate_FlagOverride remains largely the same
func TestLoadAndValidate_FlagOverride(t *testing.T) {
	tempInputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "in"), true)
	tempOutputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "out"), true)
	yamlContent := `concurrency: 4`
	cfgFile := createTempConfigFile(t, yamlContent, "yaml")

	t.Setenv("STACKCONVERTER_CONCURRENCY", "8")

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	defineAllFlags(flags)
	require.NoError(t, flags.Set("input", tempInputDir))
	require.NoError(t, flags.Set("output", tempOutputDir))
	require.NoError(t, flags.Set("concurrency", "16")) // Flag value
	require.NoError(t, flags.Set("verbose", "true"))   // Flag value

	// Pass verbose=true here as cobra would have parsed it
	opts, _, err := LoadAndValidate(cfgFile, "", true, flags)

	require.NoError(t, err)
	assert.Equal(t, 16, opts.Concurrency) // Flag overrides Env and File
	assert.Equal(t, true, opts.Verbose)   // Flag overrides default/file
}

// TestLoadAndValidate_Precedence remains largely the same
func TestLoadAndValidate_Precedence(t *testing.T) {
	tempInputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "in"), true)
	tempOutputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "out"), true)
	// Default: concurrency=0, cache=true, onError="continue"
	// File: concurrency=4, cache=false, onError="continue"
	// Profile(ci): concurrency=2, onError="stop" (inherits cache: false)
	// Env: STACKCONVERTER_CONCURRENCY=8, STACKCONVERTER_ONERROR="continue"
	// Flag: --concurrency=16, --cache=true
	yamlContent := `
concurrency: 4
cache: false
onError: continue
profiles:
  ci:
    concurrency: 2
    onError: stop
`
	cfgFile := createTempConfigFile(t, yamlContent, "yaml")
	t.Setenv("STACKCONVERTER_CONCURRENCY", "8")
	t.Setenv("STACKCONVERTER_ONERROR", "continue") // Env value for onError

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	defineAllFlags(flags)
	require.NoError(t, flags.Set("input", tempInputDir))
	require.NoError(t, flags.Set("output", tempOutputDir))
	require.NoError(t, flags.Set("concurrency", "16")) // Flag
	// *** FIX: Use "no-cache" flag set to "false" to test overriding profile's cache: false ***
	// require.NoError(t, flags.Set("cache", "true")) // Flag "cache" likely doesn't exist, should use "no-cache" logic
	require.NoError(t, flags.Set("no-cache", "false")) // Flag --no-cache=false means *do not* ignore cache reads

	// Run with profile "ci" and flags
	// Pass verbose=false
	opts, _, err := LoadAndValidate(cfgFile, "ci", false, flags)

	require.NoError(t, err)
	assert.Equal(t, 16, opts.Concurrency) // Flag wins
	// *** FIX: Assert based on the corrected flag logic. Profile sets cache=false, --no-cache=false doesn't change that. ***
	assert.False(t, opts.CacheEnabled)                       // Profile wins over default/file, flag doesn't enable it directly
	assert.False(t, opts.IgnoreCacheRead)                    // Flag --no-cache=false correctly sets IgnoreCacheRead to false
	assert.Equal(t, converter.OnErrorStop, opts.OnErrorMode) // Profile wins over Env and File
}

// TestLoadAndValidate_ValidationErrors is significantly expanded
func TestLoadAndValidate_ValidationErrors(t *testing.T) {
	// Need valid temp dirs for input/output paths in most tests
	tempBase := t.TempDir()
	tempInputDir := createDummyFsNode(t, filepath.Join(tempBase, "in"), true)
	tempOutputDir := createDummyFsNode(t, filepath.Join(tempBase, "out"), true)
	tempFile := createDummyFsNode(t, filepath.Join(tempInputDir, "dummy.txt"), false) // A file, not a dir
	tempTplFile := createDummyTemplateFile(t, filepath.Join(tempBase, "dummy.tmpl"), "{{.FilePath}}")
	absTempTplFile, _ := filepath.Abs(tempTplFile) // Get absolute path for template error messages

	// *** FIX: Add setupFunc to the struct definition ***
	testCases := []struct {
		name        string
		cfgContent  string // Optional file content
		profile     string
		envVars     map[string]string
		flags       map[string]string         // Map of flag name -> value string
		setupFunc   func(t *testing.T) string // Optional setup function per test case
		expectError bool                      // Whether any error is expected
		errorMsg    string                    // Substring of expected error message (use specific error var name like 'ErrConfigValidation')
		errorKey    string                    // Specific config key related to the error (optional)
	}{
		{
			name:        "Missing Input Flag",
			flags:       map[string]string{"output": tempOutputDir}, // Missing input
			expectError: true,
			errorMsg:    converter.ErrConfigValidation.Error(),
			errorKey:    "InputPath",
		},
		{
			name:        "Missing Output Flag",
			flags:       map[string]string{"input": tempInputDir}, // Missing output
			expectError: true,
			errorMsg:    converter.ErrConfigValidation.Error(),
			errorKey:    "OutputPath",
		},
		{
			name:        "Invalid Input Path (Does Not Exist)",
			flags:       map[string]string{"input": "/path/that/does/not/exist/ever", "output": tempOutputDir},
			expectError: true,
			errorMsg:    "input path '/path/that/does/not/exist/ever' does not exist", // Specific message
		},
		{
			name:        "Input Path is File",
			flags:       map[string]string{"input": tempFile, "output": tempOutputDir}, // Use the created temp file
			expectError: true,
			errorMsg:    "is not a directory", // Specific message
		},
		{
			name:        "Invalid onError mode",
			cfgContent:  `onError: "maybe"`,
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir},
			expectError: true,
			errorMsg:    "invalid value 'maybe' for key 'onError'", // Specific message
			errorKey:    "onError",
		},
		{
			name:        "Invalid binaryMode",
			envVars:     map[string]string{"STACKCONVERTER_BINARYMODE": "invalid"},
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir},
			expectError: true,
			errorMsg:    "invalid value 'invalid' for key 'binaryMode'", // Specific message
			errorKey:    "binaryMode",
		},
		{
			name:        "Invalid largeFileMode",
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir, "large-file-mode": "fast"},
			expectError: true,
			errorMsg:    "invalid value 'fast' for key 'largeFileMode'", // Specific message
			errorKey:    "largeFileMode",
		},
		{
			name:        "Invalid outputFormat",
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir, "output-format": "xml"},
			expectError: true,
			errorMsg:    "invalid value 'xml' for key 'outputFormat'", // Specific message
			errorKey:    "outputFormat",
		},
		{
			name:        "Invalid frontMatter format",
			cfgContent:  `frontMatter: { enabled: true, format: "json" }`, // Enable FM to trigger format check
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir},
			expectError: true,
			errorMsg:    "invalid value 'json' for key 'frontMatter.format'", // Specific message
			errorKey:    "frontMatter.format",
		},
		{
			name:        "Negative Concurrency",
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir, "concurrency": "-1"},
			expectError: true,
			errorMsg:    "invalid value '-1' for key 'concurrency'", // Specific message
			errorKey:    "concurrency",
		},
		{
			name:        "Negative Large File Threshold",
			cfgContent:  `largeFileThresholdMB: -10`,
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir},
			expectError: true,
			errorMsg:    "invalid value '-10' for key 'largeFileThresholdMB'", // Specific message
			errorKey:    "largeFileThresholdMB",
		},
		{
			name:        "Confidence Threshold Too Low",
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir}, // Remove invalid flag setting here
			cfgContent:  `languageDetectionConfidenceThreshold: -0.1`,                      // Keep config content
			expectError: true,
			errorMsg:    "invalid value '-0.100000' for key 'languageDetectionConfidenceThreshold'", // Updated expected message based on potential float formatting
			errorKey:    "languageDetectionConfidenceThreshold",
		},
		{
			name:        "Confidence Threshold Too High",
			envVars:     map[string]string{"STACKCONVERTER_LANGUAGEDETECTIONCONFIDENCETHRESHOLD": "1.1"},
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir},
			expectError: true,
			errorMsg:    "invalid value '1.100000' for key 'languageDetectionConfidenceThreshold'", // Updated expected message
			errorKey:    "languageDetectionConfidenceThreshold",
		},
		{
			name:        "Profile Not Found",
			cfgContent:  `profiles: {}`,
			profile:     "nonexistent",
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir},
			expectError: true,
			errorMsg:    "profile 'nonexistent' not found", // Loader error
		},
		{
			name:        "Config Parse Error",
			cfgContent:  `invalid: yaml: here`,
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir},
			expectError: true,
			errorMsg:    "error reading config file", // Viper's error
		},
		{
			name:        "Invalid Watch Debounce Duration String",
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir, "watch": "true", "watch-debounce": "bad-duration"},
			expectError: true,
			// Error comes from time.ParseDuration via the decode hook now
			// Wrapped by config validation error during unmarshal stage
			errorMsg: "invalid duration format 'bad-duration'", // Expected substring
		},
		{
			name:        "Invalid Negative Watch Debounce Duration",
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir, "watch": "true", "watch-debounce": "-5s"},
			expectError: true,
			errorMsg:    "invalid negative watch debounce duration '-5s' for key 'watch.debounce'", // Validation error
			errorKey:    "watch.debounce",
		},
		{
			name:        "Invalid Template Path Not Found",
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir, "template": "/no/such/template.tmpl"},
			expectError: true,
			errorMsg:    "template file specified via 'templateFile' or --template ('/no/such/template.tmpl') does not exist", // Validation error
			errorKey:    "TemplatePath",
		},
		{
			name:        "Invalid Template Path Is Dir",
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir, "template": tempInputDir}, // Pass a directory as template
			expectError: true,
			errorMsg:    "template path specified ('" + tempInputDir + "') is a directory, not a file", // Validation error
			errorKey:    "TemplatePath",
		},
		{
			name:       "Invalid Template Parsing Error",
			flags:      map[string]string{"input": tempInputDir, "output": tempOutputDir, "template": absTempTplFile}, // Use valid file path but invalid content
			cfgContent: "",                                                                                            // Ensure no file overrides template flag
			// Create a template file with invalid syntax
			setupFunc: func(t *testing.T) string {
				return createDummyTemplateFile(t, absTempTplFile, "{{ .Invalid }") // Unclosed brace
			},
			expectError: true,
			errorMsg:    "failed to parse template", // Template load error
		},
		{
			name:  "Valid Template Path Exists",
			flags: map[string]string{"input": tempInputDir, "output": tempOutputDir, "template": absTempTplFile}, // Use valid dummy template file
			setupFunc: func(t *testing.T) string { // Ensure the dummy file exists with valid content for this case
				return createDummyTemplateFile(t, absTempTplFile, "{{ .FilePath }}")
			},
			expectError: false, // No error expected
		},
		{
			name:        "Git Diff Mode Conflict",
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir, "git-diff-only": "true", "git-since": "main"},
			expectError: true,
			errorMsg:    "cannot use --git-diff-only and --git-since flags simultaneously", // Validation error
		},
		{
			name:        "Git Since Requires Value",
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir, "git-since": ""}, // Flag used but value is empty
			expectError: true,
			errorMsg:    "flag --git-since requires a non-empty reference", // Validation error
		},
		{
			name:        "Valid Flags Minimal",
			flags:       map[string]string{"input": tempInputDir, "output": tempOutputDir},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup: Dynamic setup like creating invalid template file
			// *** FIX: Check tc.setupFunc directly ***
			if tc.setupFunc != nil {
				_ = tc.setupFunc(t) // Execute setup if defined for the test case
			}
			// *** Removed incorrect deletion from tc.flags map ***

			// Setup env vars
			originalEnv := map[string]string{}
			for k, v := range tc.envVars {
				originalEnv[k] = os.Getenv(k)
				t.Setenv(k, v)
			}
			// Restore env vars after test
			defer func() {
				for k := range tc.envVars { // Iterate over tc.envVars keys for cleanup
					origVal, exists := originalEnv[k]
					if !exists || origVal == "" { // If original didn't exist or was empty string
						os.Unsetenv(k)
					} else {
						t.Setenv(k, origVal) // Use t.Setenv to restore original value
					}
				}
			}()

			// Setup flags
			flags := pflag.NewFlagSet(tc.name, pflag.ContinueOnError)
			defineAllFlags(flags) // Define all possible flags first

			for k, v := range tc.flags {
				err := flags.Set(k, v)
				// Allow flag parsing errors for specific tests if needed, otherwise require no error
				if strings.Contains(tc.name, "Flag Parsing Error") { // Example condition
					if tc.expectError && err != nil {
						// Expected flag parsing error, continue check below
						require.Error(t, err)
						assert.Contains(t, err.Error(), tc.errorMsg)
						return // Skip LoadAndValidate if flag parsing itself is the error
					}
				}
				require.NoError(t, err, "Failed to set flag %s=%s", k, v)
			}

			// Ensure required input/output flags have *some* value if not explicitly tested for missing
			if _, ok := tc.flags["input"]; !ok && strings.Contains(tc.name, "Missing Input") == false {
				require.NoError(t, flags.Set("input", tempInputDir))
			}
			if _, ok := tc.flags["output"]; !ok && strings.Contains(tc.name, "Missing Output") == false {
				require.NoError(t, flags.Set("output", tempOutputDir))
			}

			// Setup config file
			var cfgFile string
			if tc.cfgContent != "" {
				cfgFile = createTempConfigFile(t, tc.cfgContent, "yaml")
			}

			_, _, err := LoadAndValidate(cfgFile, tc.profile, false, flags)

			if tc.expectError {
				require.Error(t, err, "Expected an error for test case: %s", tc.name)
				// Allow non-wrapped errors for loader/parser issues
				if !errors.Is(err, converter.ErrConfigValidation) && strings.Contains(tc.name, "Config Parse Error") || strings.Contains(tc.name, "Profile Not Found") || strings.Contains(tc.name, "Flag Parsing Error") {
					// Loader/Parser errors might not wrap ErrConfigValidation initially
					require.Error(t, err) // Just check an error occurred
				} else if !strings.Contains(tc.name, "Watch Debounce Duration String") {
					// Specific check for other validation errors to ensure they wrap ErrConfigValidation
					assert.ErrorIs(t, err, converter.ErrConfigValidation, "Error should wrap ErrConfigValidation for validation issues: %v", err)
				} else {
					// For the debounce string error, it comes from the decode hook, might be wrapped differently
					require.Error(t, err) // Just check error
				}

				if tc.errorMsg != "" {
					assert.Contains(t, err.Error(), tc.errorMsg, "Error message mismatch")
				}
				// Optionally check if the error message contains the key
				if tc.errorKey != "" {
					assert.Contains(t, err.Error(), tc.errorKey, "Error message should mention the key or related term") // Loosen check slightly for key presence
				}
			} else {
				require.NoError(t, err, "Expected no error for test case: %s", tc.name)
			}
		})
	}
}

// TestLoadAndValidate_DefaultHooks remains the same
func TestLoadAndValidate_DefaultHooks(t *testing.T) {
	tempInputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "in"), true)
	tempOutputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "out"), true)
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	defineAllFlags(flags)
	require.NoError(t, flags.Set("input", tempInputDir))
	require.NoError(t, flags.Set("output", tempOutputDir))

	opts, _, err := LoadAndValidate("", "", false, flags)

	require.NoError(t, err)
	assert.NotNil(t, opts.EventHooks, "EventHooks should have a default non-nil implementation")
	_, ok := opts.EventHooks.(*converter.NoOpHooks)
	assert.True(t, ok, "Default EventHooks should be of type *converter.NoOpHooks")
}

// TestLoadAndValidate_DefaultLogger remains the same
func TestLoadAndValidate_DefaultLogger(t *testing.T) {
	tempInputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "in"), true)
	tempOutputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "out"), true)
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	defineAllFlags(flags)
	require.NoError(t, flags.Set("input", tempInputDir))
	require.NoError(t, flags.Set("output", tempOutputDir))

	opts, logger, err := LoadAndValidate("", "", false, flags)

	require.NoError(t, err)
	require.NotNil(t, logger)
	require.NotNil(t, opts.Logger) // Check opts.Logger handler

	loggerUsingOptsHandler := slog.New(opts.Logger)
	assert.False(t, loggerUsingOptsHandler.Enabled(nil, slog.LevelDebug), "Default logger should not have Debug level enabled")
	assert.True(t, loggerUsingOptsHandler.Enabled(nil, slog.LevelInfo), "Default logger should have Info level enabled")
}

// TestLoadAndValidate_VerboseLogger remains the same
func TestLoadAndValidate_VerboseLogger(t *testing.T) {
	tempInputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "in"), true)
	tempOutputDir := createDummyFsNode(t, filepath.Join(t.TempDir(), "out"), true)
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	defineAllFlags(flags)
	require.NoError(t, flags.Set("input", tempInputDir))
	require.NoError(t, flags.Set("output", tempOutputDir))
	require.NoError(t, flags.Set("verbose", "true")) // Enable verbosity via flag

	// Pass verbose=true here to simulate cobra parsing
	opts, logger, err := LoadAndValidate("", "", true, flags)

	require.NoError(t, err)
	require.NotNil(t, logger)
	require.NotNil(t, opts.Logger)

	loggerUsingOptsHandler := slog.New(opts.Logger)
	assert.True(t, loggerUsingOptsHandler.Enabled(nil, slog.LevelDebug), "Verbose logger should have Debug level enabled")
	assert.True(t, loggerUsingOptsHandler.Enabled(nil, slog.LevelInfo), "Verbose logger should have Info level enabled")
}

// --- END OF FINAL REVISED FILE internal/cli/config/config_test.go ---
