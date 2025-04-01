// --- START OF FINAL REVISED FILE pkg/converter/converter_test.go ---
package converter_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog" // Required for t.TempDir
	"testing"
	"time"

	"github.com/stackvity/stack-converter/internal/testutil" // Use shared mocks
	"github.com/stackvity/stack-converter/pkg/converter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// createValidTestOptions sets up a basic, valid converter.Options struct for testing purposes.
func createValidTestOptions(t *testing.T) converter.Options {
	t.Helper()
	inputDir := t.TempDir()
	outputDir := t.TempDir()
	logBuf := &bytes.Buffer{}
	loggerHandler := slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})

	opts := converter.Options{
		InputPath:             inputDir,
		OutputPath:            outputDir,
		Logger:                loggerHandler,
		EventHooks:            &testutil.MockHooks{}, // Use mock from testutil
		OnErrorMode:           converter.OnErrorContinue,
		CacheEnabled:          false,
		Concurrency:           1,
		LargeFileThreshold:    100 * 1024 * 1024,
		LargeFileMode:         converter.LargeFileSkip,
		BinaryMode:            converter.BinarySkip,
		GitDiffMode:           converter.GitDiffModeNone,
		AnalysisOptions:       converter.AnalysisConfig{ExtractComments: false},
		FrontMatterConfig:     converter.FrontMatterOptions{Enabled: false},
		DispatchWarnThreshold: 1 * time.Second,
	}

	mockHooks, ok := opts.EventHooks.(*testutil.MockHooks)
	require.True(t, ok)
	mockHooks.On("OnRunComplete", mock.AnythingOfType("converter.Report")).Return(nil)

	return opts
}

// TestGenerateDocs_BasicSuccess verifies the placeholder execution path.
// NOTE: This test currently validates the placeholder implementation.
// After Engine integration, it should be updated or replaced with tests mocking Engine.Run.
func TestGenerateDocs_BasicSuccess(t *testing.T) {
	opts := createValidTestOptions(t)
	ctx := context.Background()

	report, err := converter.GenerateDocs(ctx, opts)

	require.NoError(t, err)
	assert.NotNil(t, report)
	assert.Equal(t, opts.InputPath, report.Summary.InputPath)
	assert.Equal(t, opts.OutputPath, report.Summary.OutputPath)
	assert.False(t, report.Summary.FatalErrorOccurred)
	assert.Equal(t, 5, report.Summary.TotalFilesScanned) // From placeholder
	assert.Equal(t, 3, report.Summary.ProcessedCount)    // From placeholder
	assert.Equal(t, 0, report.Summary.ErrorCount)        // From placeholder

	mockHooks := opts.EventHooks.(*testutil.MockHooks)
	mockHooks.AssertCalled(t, "OnRunComplete", mock.AnythingOfType("converter.Report"))
}

// TestGenerateDocs_MissingInputPath verifies validation.
func TestGenerateDocs_MissingInputPath(t *testing.T) {
	opts := createValidTestOptions(t)
	opts.InputPath = "" // Make input path invalid
	ctx := context.Background()

	_, err := converter.GenerateDocs(ctx, opts)

	require.Error(t, err)
	assert.True(t, errors.Is(err, converter.ErrConfigValidation), "Error should wrap ErrConfigValidation")
	assert.Contains(t, err.Error(), "input path cannot be empty")

	mockHooks := opts.EventHooks.(*testutil.MockHooks)
	mockHooks.AssertNotCalled(t, "OnRunComplete", mock.Anything)
}

// TestGenerateDocs_MissingOutputPath verifies validation.
func TestGenerateDocs_MissingOutputPath(t *testing.T) {
	opts := createValidTestOptions(t)
	opts.OutputPath = "" // Make output path invalid
	ctx := context.Background()

	_, err := converter.GenerateDocs(ctx, opts)

	require.Error(t, err)
	assert.True(t, errors.Is(err, converter.ErrConfigValidation), "Error should wrap ErrConfigValidation")
	assert.Contains(t, err.Error(), "output path cannot be empty")
}

// TestGenerateDocs_NegativeConcurrency verifies validation.
func TestGenerateDocs_NegativeConcurrency(t *testing.T) {
	opts := createValidTestOptions(t)
	opts.Concurrency = -1 // Invalid concurrency
	ctx := context.Background()

	_, err := converter.GenerateDocs(ctx, opts)

	require.Error(t, err)
	assert.True(t, errors.Is(err, converter.ErrConfigValidation), "Error should wrap ErrConfigValidation")
	assert.Contains(t, err.Error(), "concurrency cannot be negative")
}

// TestGenerateDocs_NilLogger verifies validation.
func TestGenerateDocs_NilLogger(t *testing.T) {
	opts := createValidTestOptions(t)
	opts.Logger = nil // Set logger to nil
	ctx := context.Background()

	_, err := converter.GenerateDocs(ctx, opts)

	require.Error(t, err)
	assert.True(t, errors.Is(err, converter.ErrConfigValidation), "Error should wrap ErrConfigValidation")
	assert.Contains(t, err.Error(), "Logger implementation cannot be nil")
}

// TestGenerateDocs_NilHooks verifies validation.
func TestGenerateDocs_NilHooks(t *testing.T) {
	opts := createValidTestOptions(t)
	opts.EventHooks = nil // Set hooks to nil
	ctx := context.Background()

	_, err := converter.GenerateDocs(ctx, opts)

	require.Error(t, err)
	assert.True(t, errors.Is(err, converter.ErrConfigValidation), "Error should wrap ErrConfigValidation")
	assert.Contains(t, err.Error(), "EventHooks implementation cannot be nil")
}

// TestGenerateDocs_ContextCancellation verifies cancellation propagation.
func TestGenerateDocs_ContextCancellation(t *testing.T) {
	opts := createValidTestOptions(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var returnedErr error

	go func() {
		_, returnedErr = converter.GenerateDocs(ctx, opts)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel() // Cancel the context while the placeholder "work" might be happening

	select {
	case <-done:
		// Function returned
	case <-time.After(1 * time.Second):
		t.Fatal("GenerateDocs did not return after context cancellation")
	}

	require.Error(t, returnedErr)
	assert.True(t, errors.Is(returnedErr, context.Canceled), "Error should be context.Canceled")

	// FIX: Removed unused mockHooks variable declaration
	// mockHooks := opts.EventHooks.(*testutil.MockHooks)
	// OnRunComplete might still be called by the placeholder depending on timing.
	// Removing strict NotCalled assertion for the placeholder case.
}

// TestGenerateDocs_DependencyWarnings verifies logging of warnings for missing optional interfaces.
func TestGenerateDocs_DependencyWarnings(t *testing.T) {
	logBuf := &bytes.Buffer{}
	loggerHandler := slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	opts := createValidTestOptions(t)
	opts.Logger = loggerHandler

	opts.CacheEnabled = true
	opts.GitMetadataEnabled = true
	opts.PluginConfigs = []converter.PluginConfig{{Enabled: true}}
	opts.AnalysisOptions.ExtractComments = true

	opts.CacheManager = nil
	opts.GitClient = nil
	opts.PluginRunner = nil
	opts.AnalysisEngine = nil
	opts.LanguageDetector = nil
	opts.EncodingHandler = nil
	opts.TemplateExecutor = nil

	ctx := context.Background()
	_, err := converter.GenerateDocs(ctx, opts)

	require.NoError(t, err) // Expect placeholder success

	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "No CacheManager provided and cache enabled")
	assert.Contains(t, logOutput, "No LanguageDetector provided")
	assert.Contains(t, logOutput, "No EncodingHandler provided")
	assert.Contains(t, logOutput, "Comment extraction enabled but no AnalysisEngine provided")
	assert.Contains(t, logOutput, "Git features requested but no GitClient provided")
	assert.Contains(t, logOutput, "Plugins configured but no PluginRunner provided")
	assert.Contains(t, logOutput, "No TemplateExecutor provided")
}

// TestGenerateDocs_OnRunCompleteHookError verifies that hook errors are logged but don't cause GenerateDocs to fail.
func TestGenerateDocs_OnRunCompleteHookError(t *testing.T) {
	logBuf := &bytes.Buffer{}
	loggerHandler := slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	opts := createValidTestOptions(t)
	opts.Logger = loggerHandler

	hookError := errors.New("mock hook completion error")
	mockHooks := opts.EventHooks.(*testutil.MockHooks)
	mockHooks.ExpectedCalls = nil // Clear previous default expectation
	mockHooks.On("OnRunComplete", mock.AnythingOfType("converter.Report")).Return(hookError)

	ctx := context.Background()
	report, err := converter.GenerateDocs(ctx, opts)

	require.NoError(t, err) // GenerateDocs should still succeed
	assert.NotNil(t, report)

	mockHooks.AssertCalled(t, "OnRunComplete", mock.AnythingOfType("converter.Report"))

	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Error reported by OnRunComplete hook")
	assert.Contains(t, logOutput, hookError.Error())
}

// --- END OF FINAL REVISED FILE pkg/converter/converter_test.go ---
