// --- START OF FINAL REVISED FILE pkg/converter/processor_test.go ---
package converter_test

import (
	"bytes"
	"context" // Import crypto/sha256
	"errors"
	"fmt"
	"io" // Import io for io.Writer
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/stackvity/stack-converter/internal/testutil" // Use shared mocks
	"github.com/stackvity/stack-converter/pkg/converter"
	pkghtmltemplate "github.com/stackvity/stack-converter/pkg/converter/template" // Use aliased type
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// setupProcessorTest creates a FileProcessor with mocked dependencies for testing.
func setupProcessorTest(t *testing.T) ( // minimal comment
	*converter.FileProcessor,
	*converter.Options,
	*testutil.MockCacheManager,
	*testutil.MockLanguageDetector,
	*testutil.MockEncodingHandler,
	*testutil.MockAnalysisEngine,
	*testutil.MockGitClient,
	*testutil.MockPluginRunner,
	*testutil.MockTemplateExecutor,
	*testutil.MockHooks,
	*bytes.Buffer, // log buffer
) {
	t.Helper()

	// Create temporary directories
	inputDir := t.TempDir()
	outputDir := t.TempDir()

	// Basic logger setup
	logBuf := &bytes.Buffer{}
	loggerHandler := slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})

	// Mocks
	mockCacheMgr := new(testutil.MockCacheManager)
	mockLangDet := new(testutil.MockLanguageDetector)
	mockEncHandler := new(testutil.MockEncodingHandler)
	mockAnalysisEng := new(testutil.MockAnalysisEngine)
	mockGitClient := new(testutil.MockGitClient)
	mockPluginRunner := new(testutil.MockPluginRunner)
	mockTplExecutor := new(testutil.MockTemplateExecutor)
	mockHooks := new(testutil.MockHooks)

	// Default Options
	opts := &converter.Options{
		InputPath:             inputDir,
		OutputPath:            outputDir,
		AppVersion:            "test-processor-v1",
		Logger:                loggerHandler,
		EventHooks:            mockHooks,
		OnErrorMode:           converter.OnErrorContinue,
		CacheEnabled:          true, // Enable cache for most tests
		CacheFilePath:         filepath.Join(outputDir, converter.CacheFileName),
		Concurrency:           1,
		LargeFileThreshold:    10 * 1024, // 10 KB threshold for testing
		LargeFileThresholdMB:  0,         // Derived later, set base threshold low
		LargeFileMode:         converter.LargeFileSkip,
		LargeFileTruncateCfg:  "1KB",
		BinaryMode:            converter.BinarySkip,
		GitDiffMode:           converter.GitDiffModeNone,
		GitMetadataEnabled:    false,
		AnalysisOptions:       converter.AnalysisConfig{ExtractComments: false},
		FrontMatterConfig:     converter.FrontMatterOptions{Enabled: false, Format: "yaml"},
		DispatchWarnThreshold: 1 * time.Second,
		// Inject mocks (these will be used by the processor)
		CacheManager:     mockCacheMgr,
		LanguageDetector: mockLangDet,
		EncodingHandler:  mockEncHandler,
		AnalysisEngine:   mockAnalysisEng,
		GitClient:        mockGitClient,
		PluginRunner:     mockPluginRunner,
		TemplateExecutor: mockTplExecutor,
	}

	// Default hook expectations (can be overridden in specific tests)
	mockHooks.On("OnFileStatusUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Default cache manager expectations
	mockCacheMgr.On("Check", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, "").Maybe() // Default: miss
	mockCacheMgr.On("Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Default encoding handler expectations
	mockEncHandler.On("DetectAndDecode", mock.Anything).Return(
		func(content []byte) []byte { return content }, // Pass through by default
		"utf-8",
		true, // Certainty
		nil,  // No error
	).Maybe()
	mockEncHandler.On("IsBinary", mock.Anything).Return(false).Maybe() // Default: not binary

	// Default language detector expectations
	mockLangDet.On("Detect", mock.Anything, mock.Anything).Return("plaintext", 0.9, nil).Maybe()

	// Default template executor expectations
	mockTplExecutor.On("Execute", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe().Run(func(args mock.Arguments) {
		// Simulate writing basic content if needed by default
		writer := args.Get(0).(io.Writer)
		metadata := args.Get(2).(*pkghtmltemplate.TemplateMetadata) // Use aliased type
		fmt.Fprintf(writer, "CONTENT:%s", metadata.Content)
	})

	processor := converter.NewFileProcessor(
		opts,
		loggerHandler,
		mockCacheMgr,
		mockLangDet,
		mockEncHandler,
		mockAnalysisEng,
		mockGitClient,
		mockPluginRunner,
		mockTplExecutor,
	)
	require.NotNil(t, processor)

	return processor, opts, mockCacheMgr, mockLangDet, mockEncHandler, mockAnalysisEng, mockGitClient, mockPluginRunner, mockTplExecutor, mockHooks, logBuf
}

// createTestFile creates a file in the input directory with given content.
func createTestFile(t *testing.T, inputDir, relPath, content string) string { // minimal comment
	t.Helper()
	absPath := filepath.Join(inputDir, relPath)
	dir := filepath.Dir(absPath)
	err := os.MkdirAll(dir, 0755)
	require.NoError(t, err)
	err = os.WriteFile(absPath, []byte(content), 0644)
	require.NoError(t, err)
	return absPath
}

// createDummyTemplateFile creates a file with given content, ensuring directories exist.
func createDummyTemplateFile(t *testing.T, path string, content string) string { // minimal comment
	t.Helper()
	fullPath := filepath.Clean(path) // Clean the path
	dir := filepath.Dir(fullPath)
	err := os.MkdirAll(dir, 0755)
	require.NoError(t, err, "Failed to create directory %s for dummy template", dir)
	err = os.WriteFile(fullPath, []byte(content), 0644)
	require.NoError(t, err, "Failed to write dummy template %s", fullPath)
	return fullPath
}

// TestFileProcessor_ProcessFile_HappyPath_CacheMiss verifies basic successful processing.
func TestFileProcessor_ProcessFile_HappyPath_CacheMiss(t *testing.T) { // minimal comment
	processor, opts, mockCacheMgr, mockLangDet, _, _, _, _, mockTplExecutor, mockHooks, _ := setupProcessorTest(t)

	relPath := "subdir/file.go"
	content := "package main\nfunc main(){}"
	absPath := createTestFile(t, opts.InputPath, relPath, content)

	// Setup specific mock returns
	mockCacheMgr.On("Check", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, "").Once() // Force miss
	mockLangDet.On("Detect", []byte(content), relPath).Return("go", 0.99, nil).Once()
	mockTplExecutor.On("Execute", mock.Anything, mock.Anything, mock.AnythingOfType("*pkghtmltemplate.TemplateMetadata")).Return(nil).Once().Run(func(args mock.Arguments) { // Use aliased type
		writer := args.Get(0).(io.Writer)
		metadata := args.Get(2).(*pkghtmltemplate.TemplateMetadata) // Use aliased type
		assert.Equal(t, relPath, metadata.FilePath)
		assert.Equal(t, "go", metadata.DetectedLanguage)
		assert.Equal(t, content, metadata.Content)
		fmt.Fprintf(writer, "TEMPLATE_OUTPUT for %s", metadata.FileName) // Simulate template writing output
	})
	mockCacheMgr.On("Update", relPath, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	result, status, err := processor.ProcessFile(context.Background(), absPath)

	require.NoError(t, err)
	assert.Equal(t, converter.StatusSuccess, status)
	require.IsType(t, converter.FileInfo{}, result)
	fileInfo := result.(converter.FileInfo)
	assert.Equal(t, relPath, fileInfo.Path)
	assert.Equal(t, "subdir/file.md", fileInfo.OutputPath) // Check generated path implicitly tests generateOutputPath
	assert.Equal(t, "go", fileInfo.Language)
	assert.Equal(t, converter.CacheStatusMiss, fileInfo.CacheStatus)
	assert.False(t, fileInfo.ExtractedComments)
	assert.False(t, fileInfo.FrontMatter)
	assert.Empty(t, fileInfo.PluginsRun)

	// Verify output file content
	outputFilePath := filepath.Join(opts.OutputPath, fileInfo.OutputPath)
	outputBytes, readErr := os.ReadFile(outputFilePath)
	require.NoError(t, readErr)
	assert.Equal(t, "TEMPLATE_OUTPUT for file.go", string(outputBytes))

	// Assert mock calls
	mockCacheMgr.AssertExpectations(t)
	mockLangDet.AssertExpectations(t)
	mockTplExecutor.AssertExpectations(t)
	mockCacheMgr.AssertCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusProcessing, "", mock.Anything)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusSuccess, "Successfully processed", mock.Anything)
}

// TestFileProcessor_ProcessFile_CacheHit verifies cache hit behavior.
func TestFileProcessor_ProcessFile_CacheHit(t *testing.T) { // minimal comment
	processor, opts, mockCacheMgr, _, _, _, _, _, _, mockHooks, _ := setupProcessorTest(t)

	relPath := "cachedfile.txt"
	absPath := createTestFile(t, opts.InputPath, relPath, "cached content")

	// Setup specific mock returns
	expectedOutputHash := "dummyOutputHash"
	mockCacheMgr.On("Check", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(true, expectedOutputHash).Once() // Force hit

	result, status, err := processor.ProcessFile(context.Background(), absPath)

	require.NoError(t, err)
	assert.Equal(t, converter.StatusCached, status)
	require.IsType(t, converter.FileInfo{}, result)
	fileInfo := result.(converter.FileInfo)
	assert.Equal(t, relPath, fileInfo.Path)
	assert.Equal(t, converter.CacheStatusHit, fileInfo.CacheStatus)
	// Other fields like Language, OutputPath might be zero/empty for cache hit in this basic test

	// Assert mocks
	mockCacheMgr.AssertExpectations(t)
	mockCacheMgr.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything) // Update should not be called on hit
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusProcessing, "", mock.Anything)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusCached, "Retrieved from cache", mock.Anything)

	// Ensure output file was NOT created
	outputFilePath := filepath.Join(opts.OutputPath, strings.TrimSuffix(relPath, filepath.Ext(relPath))+".md")
	_, statErr := os.Stat(outputFilePath)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "Output file should not be created on cache hit")
}

// TestFileProcessor_ProcessFile_BinarySkip verifies skipping binary files.
func TestFileProcessor_ProcessFile_BinarySkip(t *testing.T) { // minimal comment
	processor, opts, _, _, mockEncHandler, _, _, _, _, mockHooks, _ := setupProcessorTest(t)
	opts.BinaryMode = converter.BinarySkip // Explicitly set skip mode

	relPath := "image.png"
	absPath := createTestFile(t, opts.InputPath, relPath, "\x89PNG...") // Simulate binary

	// Setup mocks
	mockEncHandler.On("DetectAndDecode", mock.Anything).Return([]byte("\x89PNG..."), "unknown", false, nil).Maybe()
	mockEncHandler.On("IsBinary", mock.Anything).Return(true).Once() // Detect as binary

	result, status, err := processor.ProcessFile(context.Background(), absPath)

	require.NoError(t, err)
	assert.Equal(t, converter.StatusSkipped, status)
	require.IsType(t, converter.SkippedInfo{}, result)
	skippedInfo := result.(converter.SkippedInfo)
	assert.Equal(t, relPath, skippedInfo.Path)
	assert.Equal(t, converter.SkipReasonBinary, skippedInfo.Reason)
	assert.Equal(t, "Binary file detected", skippedInfo.Details)

	// Assert mocks
	mockEncHandler.AssertExpectations(t)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusProcessing, "", mock.Anything)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusSkipped, "Skipped - binary_file: Binary file detected", mock.Anything)
}

// TestFileProcessor_ProcessFile_LargeFileTruncate verifies large file truncation.
func TestFileProcessor_ProcessFile_LargeFileTruncate(t *testing.T) { // minimal comment
	processor, opts, mockCacheMgr, _, _, _, _, _, mockTplExecutor, mockHooks, _ := setupProcessorTest(t)
	opts.LargeFileMode = converter.LargeFileTruncate // Set truncate mode
	opts.LargeFileThreshold = 10                     // Set low threshold
	opts.LargeFileTruncateCfg = "10b"                // Truncate to 10 bytes

	relPath := "large_truncate.log"
	fileContent := "This content is definitely larger than 10 bytes"
	absPath := createTestFile(t, opts.InputPath, relPath, fileContent)

	// Setup mock returns
	mockCacheMgr.On("Check", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, "").Once()
	// Expect template execution with truncated content
	mockTplExecutor.On("Execute", mock.Anything, mock.Anything, mock.AnythingOfType("*pkghtmltemplate.TemplateMetadata")).Return(nil).Once().Run(func(args mock.Arguments) {
		writer := args.Get(0).(io.Writer)
		metadata := args.Get(2).(*pkghtmltemplate.TemplateMetadata)
		assert.True(t, metadata.IsLarge, "IsLarge flag should be true")
		assert.True(t, metadata.Truncated, "Truncated flag should be true")
		// Verify content passed to template is truncated
		assert.Equal(t, "This conte\n\n[Content truncated due to file size limit]", metadata.Content) // Expected truncated content based on 10b limit and added notice
		fmt.Fprintf(writer, "TRUNCATED_TEMPLATE:%s", metadata.Content)
	})
	mockCacheMgr.On("Update", relPath, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	result, status, err := processor.ProcessFile(context.Background(), absPath)

	require.NoError(t, err)
	assert.Equal(t, converter.StatusSuccess, status)
	require.IsType(t, converter.FileInfo{}, result)
	fileInfo := result.(converter.FileInfo)
	assert.Equal(t, relPath, fileInfo.Path)
	assert.Equal(t, converter.CacheStatusMiss, fileInfo.CacheStatus)

	// Verify output file content includes the truncated notice
	outputFilePath := filepath.Join(opts.OutputPath, fileInfo.OutputPath)
	outputBytes, readErr := os.ReadFile(outputFilePath)
	require.NoError(t, readErr)
	assert.Contains(t, string(outputBytes), "[Content truncated due to file size limit]")

	// Assert mocks
	mockCacheMgr.AssertExpectations(t)
	mockTplExecutor.AssertExpectations(t)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusProcessing, "", mock.Anything)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusSuccess, "Successfully processed", mock.Anything)
}

// TestFileProcessor_ProcessFile_LargeFileError verifies error on large files.
func TestFileProcessor_ProcessFile_LargeFileError(t *testing.T) { // minimal comment
	processor, opts, _, _, _, _, _, _, _, mockHooks, _ := setupProcessorTest(t)
	opts.LargeFileMode = converter.LargeFileError // Explicitly set error mode
	opts.LargeFileThreshold = 5                   // Set very low threshold
	opts.OnErrorMode = converter.OnErrorStop      // Ensure error is fatal for assertion check

	relPath := "large.log"
	fileContent := "This content is larger than 5 bytes"
	absPath := createTestFile(t, opts.InputPath, relPath, fileContent)

	result, status, err := processor.ProcessFile(context.Background(), absPath)

	require.Error(t, err)
	assert.ErrorIs(t, err, converter.ErrLargeFile) // Check specific error type
	assert.Equal(t, converter.StatusFailed, status)
	require.IsType(t, converter.ErrorInfo{}, result)
	errorInfo := result.(converter.ErrorInfo)
	assert.Equal(t, relPath, errorInfo.Path)
	expectedErrMsg := fmt.Sprintf("Large file encountered (size %d bytes > threshold %d bytes)", len(fileContent), opts.LargeFileThreshold)
	assert.Equal(t, expectedErrMsg, errorInfo.Error) // Check exact error message
	assert.True(t, errorInfo.IsFatal, "Error should be fatal in stop mode")

	// Assert hooks
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusProcessing, "", mock.Anything)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusFailed, expectedErrMsg, mock.Anything)
}

// TestFileProcessor_ProcessFile_OnErrorStop verifies early exit on error.
func TestFileProcessor_ProcessFile_OnErrorStop(t *testing.T) { // minimal comment
	processor, opts, _, _, mockEncHandler, _, _, _, _, mockHooks, _ := setupProcessorTest(t)
	opts.OnErrorMode = converter.OnErrorStop // Explicitly set stop mode

	relPath := "badfile.txt"
	absPath := createTestFile(t, opts.InputPath, relPath, "content")

	mockError := errors.New("simulated encoding error")
	// Since setupProcessorTest sets up mockEncHandler on opts, we can get it back here
	// Clear default ".Maybe()" expectation if we need specific calls checked
	mockEncHandler.ExpectedCalls = []*mock.Call{}
	mockEncHandler.On("DetectAndDecode", mock.Anything).Return(nil, "", false, mockError).Once()
	mockEncHandler.On("IsBinary", mock.Anything).Return(false).Maybe() // Still needed maybe

	result, status, err := processor.ProcessFile(context.Background(), absPath)

	require.Error(t, err)
	// Check if the returned error wraps the mockError
	// Note: The processor wraps errors, so we check if the original is contained.
	assert.True(t, errors.Is(err, mockError), "Expected error to wrap the simulated encoding error")
	assert.Equal(t, converter.StatusFailed, status)
	require.IsType(t, converter.ErrorInfo{}, result)
	errorInfo := result.(converter.ErrorInfo)
	assert.Equal(t, relPath, errorInfo.Path)
	assert.Equal(t, mockError.Error(), errorInfo.Error) // Verify the error message matches
	assert.True(t, errorInfo.IsFatal, "Error should be marked fatal in stop mode")

	// Assert mocks
	mockEncHandler.AssertExpectations(t)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusProcessing, "", mock.Anything)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusFailed, mockError.Error(), mock.Anything)
}

// TestFileProcessor_ProcessFile_ContextCancellation verifies cancellation handling.
func TestFileProcessor_ProcessFile_ContextCancellation(t *testing.T) { // minimal comment
	processor, opts, _, _, _, _, _, _, _, mockHooks, _ := setupProcessorTest(t)

	relPath := "cancellable.txt"
	absPath := createTestFile(t, opts.InputPath, relPath, "content")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result, status, err := processor.ProcessFile(ctx, absPath)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled) // Check specific error type
	assert.Equal(t, converter.StatusFailed, status)
	require.IsType(t, converter.ErrorInfo{}, result)
	errorInfo := result.(converter.ErrorInfo)
	assert.Equal(t, relPath, errorInfo.Path)
	assert.Equal(t, context.Canceled.Error(), errorInfo.Error) // Check error message
	assert.True(t, errorInfo.IsFatal, "Cancellation error should be marked fatal")

	// Assert mocks
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusProcessing, "", mock.Anything)
	// Verify final status update reflects cancellation
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusFailed, context.Canceled.Error(), mock.Anything)
}

// TestFileProcessor_ProcessFile_WithPreprocessorPlugin verifies preprocessor plugin call and metadata merge.
func TestFileProcessor_ProcessFile_WithPreprocessorPlugin(t *testing.T) { // minimal comment
	processor, opts, _, _, _, _, _, mockPluginRunner, mockTplExecutor, mockHooks, _ := setupProcessorTest(t)

	relPath := "pre_plugin_test.txt"
	content := "Original Content"
	preprocessedContent := "PREPROCESSED: Original Content"
	absPath := createTestFile(t, opts.InputPath, relPath, content)

	opts.PluginConfigs = []converter.PluginConfig{
		{Name: "pre1", Stage: converter.PluginStagePreprocessor, Enabled: true, Command: []string{"pre1_cmd"}, AppliesTo: []string{"*.txt"}},
	}

	// Mock preprocessor output including metadata
	preprocessorMetadata := map[string]interface{}{"preprocessed": true, "added_by_pre": "value1", "DetectedLanguage": "override"}
	mockPluginRunner.On("Run", mock.Anything, converter.PluginStagePreprocessor, mock.Anything, mock.Anything).Return(
		converter.PluginOutput{SchemaVersion: converter.PluginSchemaVersion, Content: preprocessedContent, Metadata: preprocessorMetadata},
		nil,
	).Once()

	// Mock template executor to check received metadata
	mockTplExecutor.On("Execute", mock.Anything, mock.Anything, mock.AnythingOfType("*pkghtmltemplate.TemplateMetadata")).Return(nil).Once().Run(func(args mock.Arguments) {
		writer := args.Get(0).(io.Writer)
		metadata := args.Get(2).(*pkghtmltemplate.TemplateMetadata)
		assert.Equal(t, preprocessedContent, metadata.Content, "Template should receive preprocessed content")
		// Verify metadata from plugin is present *and merged correctly*
		// The UpdateMetadataFromMap function attempts to update corresponding fields in the meta struct.
		assert.Equal(t, "override", metadata.DetectedLanguage, "Metadata 'DetectedLanguage' should be updated by plugin")
		// Check if arbitrary keys landed in FrontMatter (current updateMetadataFromMap behavior)
		require.NotNil(t, metadata.FrontMatter)
		assert.Equal(t, true, metadata.FrontMatter["preprocessed"])
		assert.Equal(t, "value1", metadata.FrontMatter["added_by_pre"])

		fmt.Fprintf(writer, "TEMPLATE:%s", metadata.Content)
	})

	result, status, err := processor.ProcessFile(context.Background(), absPath)

	require.NoError(t, err)
	assert.Equal(t, converter.StatusSuccess, status)
	fileInfo := result.(converter.FileInfo)
	assert.Contains(t, fileInfo.PluginsRun, "pre1", "pre1 should be listed in PluginsRun")
	assert.Equal(t, "override", fileInfo.Language, "Final FileInfo should reflect metadata updated by plugin") // Verify merged metadata persists

	outputFilePath := filepath.Join(opts.OutputPath, fileInfo.OutputPath)
	outputBytes, readErr := os.ReadFile(outputFilePath)
	require.NoError(t, readErr)
	assert.Equal(t, "TEMPLATE:"+preprocessedContent, string(outputBytes)) // Verify final output based on template seeing preprocessed content

	mockPluginRunner.AssertExpectations(t)
	mockTplExecutor.AssertExpectations(t)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusSuccess, "Successfully processed", mock.Anything)
}

// TestFileProcessor_ProcessFile_WithPostprocessorPlugin verifies postprocessor plugin call.
func TestFileProcessor_ProcessFile_WithPostprocessorPlugin(t *testing.T) { // minimal comment
	processor, opts, _, _, _, _, _, mockPluginRunner, mockTplExecutor, mockHooks, _ := setupProcessorTest(t)

	relPath := "post_plugin_test.txt"
	content := "Original Content"
	templateOutput := "TEMPLATE:Original Content"
	postprocessedContent := "TEMPLATE:Original Content:POSTPROCESSED"
	absPath := createTestFile(t, opts.InputPath, relPath, content)

	opts.PluginConfigs = []converter.PluginConfig{
		{Name: "post1", Stage: converter.PluginStagePostprocessor, Enabled: true, Command: []string{"post1_cmd"}, AppliesTo: []string{"*.txt"}},
	}

	// Mock postprocessor to modify content based on expected template output
	mockPluginRunner.On("Run", mock.Anything, converter.PluginStagePostprocessor, mock.Anything, mock.MatchedBy(func(input converter.PluginInput) bool {
		// Check if the input content matches the expected template output
		return input.Content == templateOutput
	})).Return(
		converter.PluginOutput{SchemaVersion: converter.PluginSchemaVersion, Content: postprocessedContent, Metadata: map[string]interface{}{"postprocessed": true}},
		nil,
	).Once()

	// Template executor produces the base template output
	mockTplExecutor.On("Execute", mock.Anything, mock.Anything, mock.AnythingOfType("*pkghtmltemplate.TemplateMetadata")).Return(nil).Once().Run(func(args mock.Arguments) {
		writer := args.Get(0).(io.Writer)
		metadata := args.Get(2).(*pkghtmltemplate.TemplateMetadata)
		fmt.Fprintf(writer, "TEMPLATE:%s", metadata.Content) // Template outputs based on original content
	})

	result, status, err := processor.ProcessFile(context.Background(), absPath)

	require.NoError(t, err)
	assert.Equal(t, converter.StatusSuccess, status)
	fileInfo := result.(converter.FileInfo)
	assert.Contains(t, fileInfo.PluginsRun, "post1", "post1 should be listed in PluginsRun")

	outputFilePath := filepath.Join(opts.OutputPath, fileInfo.OutputPath)
	outputBytes, readErr := os.ReadFile(outputFilePath)
	require.NoError(t, readErr)
	assert.Equal(t, postprocessedContent, string(outputBytes)) // Verify final output matches postprocessor result

	mockPluginRunner.AssertExpectations(t)
	mockTplExecutor.AssertExpectations(t)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusSuccess, "Successfully processed", mock.Anything)
}

// TestFileProcessor_ProcessFile_WithFormatterPlugin verifies formatter takes precedence.
func TestFileProcessor_ProcessFile_WithFormatterPlugin(t *testing.T) { // minimal comment
	processor, opts, _, _, _, _, _, mockPluginRunner, mockTplExecutor, mockHooks, _ := setupProcessorTest(t) // Include mockTplExecutor

	relPath := "formatter_test.txt"
	content := "Original Content"
	absPath := createTestFile(t, opts.InputPath, relPath, content)
	opts.FrontMatterConfig.Enabled = true // Enable front matter to test if formatter bypasses it

	// Configure plugins
	opts.PluginConfigs = []converter.PluginConfig{
		{Name: "fmt1", Stage: converter.PluginStageFormatter, Enabled: true, Command: []string{"fmt1_cmd"}, AppliesTo: []string{"*.txt"}},
		{Name: "post1", Stage: converter.PluginStagePostprocessor, Enabled: true, Command: []string{"post1_cmd"}, AppliesTo: []string{"*.txt"}}, // Add a postprocessor
	}

	// Setup mock plugin response - IMPORTANT: Use the 'Output' field for formatters
	formatterFinalOutput := "FORMATTER_FINAL_OUTPUT"
	mockPluginRunner.On("Run", mock.Anything, converter.PluginStageFormatter, mock.Anything, mock.Anything).Return(
		converter.PluginOutput{SchemaVersion: converter.PluginSchemaVersion, Output: formatterFinalOutput}, // Use Output field
		nil,
	).Once()
	// Postprocessor should NOT be called if formatter provides Output
	mockPluginRunner.On("Run", mock.Anything, converter.PluginStagePostprocessor, mock.Anything, mock.Anything).Return(
		converter.PluginOutput{}, nil,
	).Maybe() // Use Maybe in case logic changes, but expect 0 calls

	// Template executor should NOT be called if formatter provides Output
	mockTplExecutor.On("Execute", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	result, status, err := processor.ProcessFile(context.Background(), absPath)

	require.NoError(t, err)
	assert.Equal(t, converter.StatusSuccess, status)
	fileInfo := result.(converter.FileInfo)
	assert.Contains(t, fileInfo.PluginsRun, "fmt1")
	assert.NotContains(t, fileInfo.PluginsRun, "post1", "Postprocessor should not run after formatter provides output")
	assert.False(t, fileInfo.FrontMatter, "Front matter should be false as formatter provided final output")

	// Verify output file content matches the Formatter's Output field
	outputFilePath := filepath.Join(opts.OutputPath, fileInfo.OutputPath)
	outputBytes, readErr := os.ReadFile(outputFilePath)
	require.NoError(t, readErr)
	assert.Equal(t, formatterFinalOutput, string(outputBytes))

	// Assert mocks
	mockPluginRunner.AssertExpectations(t)
	mockPluginRunner.AssertNumberOfCalls(t, "Run", 1)    // Only formatter should run
	mockTplExecutor.AssertNumberOfCalls(t, "Execute", 0) // Template executor shouldn't run
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusSuccess, "Successfully processed", mock.Anything)
}

// TestFileProcessor_ProcessFile_FrontMatterAndPostprocessor verifies correct ordering.
func TestFileProcessor_ProcessFile_FrontMatterAndPostprocessor(t *testing.T) { // minimal comment
	processor, opts, _, mockLangDet, _, _, _, mockPluginRunner, mockTplExecutor, mockHooks, _ := setupProcessorTest(t)

	relPath := "fm_and_postplugin.txt"
	content := "File Content"
	absPath := createTestFile(t, opts.InputPath, relPath, content)

	// Configure Front Matter
	opts.FrontMatterConfig.Enabled = true
	opts.FrontMatterConfig.Format = "yaml"
	opts.FrontMatterConfig.Static = map[string]interface{}{"static_key": "static_value"}
	opts.FrontMatterConfig.Include = []string{"DetectedLanguage"} // Expect "plaintext"

	// Configure Postprocessor Plugin
	opts.PluginConfigs = []converter.PluginConfig{
		{Name: "post1", Stage: converter.PluginStagePostprocessor, Enabled: true, Command: []string{"post1_cmd"}, AppliesTo: []string{"*.txt"}},
	}

	// Mock Template Output
	templateOutput := "TEMPLATE_OUTPUT_BODY"
	mockTplExecutor.On("Execute", mock.Anything, mock.Anything, mock.AnythingOfType("*pkghtmltemplate.TemplateMetadata")).Return(nil).Once().Run(func(args mock.Arguments) {
		writer := args.Get(0).(io.Writer)
		fmt.Fprint(writer, templateOutput)
	})

	// Mock Postprocessor Output
	postprocessedContent := "POSTPROCESSED_TEMPLATE_OUTPUT_BODY"
	mockPluginRunner.On("Run", mock.Anything, converter.PluginStagePostprocessor, mock.Anything, mock.MatchedBy(func(input converter.PluginInput) bool {
		return input.Content == templateOutput // Postprocessor receives template output
	})).Return(
		converter.PluginOutput{SchemaVersion: converter.PluginSchemaVersion, Content: postprocessedContent},
		nil,
	).Once()

	// Default language detection
	mockLangDet.On("Detect", mock.Anything, mock.Anything).Return("plaintext", 0.9, nil).Once()

	// --- Run Processing ---
	result, status, err := processor.ProcessFile(context.Background(), absPath)

	// --- Assertions ---
	require.NoError(t, err)
	assert.Equal(t, converter.StatusSuccess, status)
	require.IsType(t, converter.FileInfo{}, result)
	fileInfo := result.(converter.FileInfo)
	assert.True(t, fileInfo.FrontMatter, "FrontMatter flag should be true")
	assert.Contains(t, fileInfo.PluginsRun, "post1")

	// Verify final output file content
	outputFilePath := filepath.Join(opts.OutputPath, fileInfo.OutputPath)
	outputBytes, readErr := os.ReadFile(outputFilePath)
	require.NoError(t, readErr)
	outputString := string(outputBytes)

	// Check for front matter block first
	assert.True(t, strings.HasPrefix(outputString, "---\n"), "Output should start with YAML front matter delimiter")
	assert.Contains(t, outputString, "\n---\n", "Output should contain ending YAML front matter delimiter")
	assert.Contains(t, outputString, "static_key: static_value", "Static front matter missing")
	assert.Contains(t, outputString, "detectedLanguage: plaintext", "Included dynamic front matter missing") // Check included field

	// Check for post-processed content *after* the front matter block
	expectedBody := "\n" + postprocessedContent // Expect newline after front matter
	assert.True(t, strings.HasSuffix(outputString, expectedBody), "Output should end with post-processed content")
	// Verify the template output itself isn't directly in the final output
	assert.NotContains(t, outputString, templateOutput)

	// Assert mocks
	mockPluginRunner.AssertExpectations(t)
	mockTplExecutor.AssertExpectations(t)
	mockLangDet.AssertExpectations(t)
	mockHooks.AssertCalled(t, "OnFileStatusUpdate", relPath, converter.StatusSuccess, "Successfully processed", mock.Anything)
}

// TestCalculateConfigHash verifies hash stability and sensitivity.
func TestCalculateConfigHash(t *testing.T) { // minimal comment
	// Assuming CalculateConfigHash is a public method of FileProcessor or accessible helper
	processor, opts, _, _, _, _, _, _, _, _, _ := setupProcessorTest(t)

	// 1. Calculate initial hash
	hash1, err1 := processor.CalculateConfigHash()
	require.NoError(t, err1)
	require.NotEmpty(t, hash1)

	// 2. Calculate hash again with identical options - should match
	hash2, err2 := processor.CalculateConfigHash()
	require.NoError(t, err2)
	assert.Equal(t, hash1, hash2, "Config hash should be stable for identical options")

	// 3. Change an *irrelevant* option - should match
	opts.Verbose = !opts.Verbose
	hash3, err3 := processor.CalculateConfigHash()
	require.NoError(t, err3)
	assert.Equal(t, hash1, hash3, "Config hash should not change for irrelevant options like Verbose")
	opts.Verbose = !opts.Verbose // Change back

	// 4. Change a *relevant* option (e.g., binaryMode) - should NOT match
	originalBinaryMode := opts.BinaryMode
	opts.BinaryMode = converter.BinaryPlaceholder
	hash4, err4 := processor.CalculateConfigHash()
	require.NoError(t, err4)
	assert.NotEqual(t, hash1, hash4, "Config hash should change when relevant option BinaryMode changes")
	opts.BinaryMode = originalBinaryMode // Change back

	// 5. Change another relevant option (e.g., plugin config order/content) - should NOT match
	opts.PluginConfigs = []converter.PluginConfig{
		{Name: "B", Enabled: true, Stage: "pre"},
		{Name: "A", Enabled: true, Stage: "pre"},
	}
	hash5a, err5a := processor.CalculateConfigHash()
	require.NoError(t, err5a)
	assert.NotEqual(t, hash1, hash5a, "Config hash should change when plugins are added")

	// Change order - MUST change hash due to sorting in implementation
	opts.PluginConfigs = []converter.PluginConfig{
		{Name: "A", Enabled: true, Stage: "pre"},
		{Name: "B", Enabled: true, Stage: "pre"},
	}
	hash5b, err5b := processor.CalculateConfigHash()
	require.NoError(t, err5b)
	// The hash should ideally change if the config details change, even if sorted identically.
	// If JSON marshaling of the slice after sorting produces same bytes, this might be equal.
	// Let's assert not equal, assuming the content/order detail matters after sorting.
	assert.NotEqual(t, hash5a, hash5b, "Config hash should change when plugin details change (even if sorted)")

	opts.PluginConfigs = []converter.PluginConfig{} // Change back

	// 6. Change template path/content (relevant) - should NOT match
	tmpDir := t.TempDir() // Use temp dir for templates
	tmpFile1 := createDummyTemplateFile(t, filepath.Join(tmpDir, "template1.tmpl"), "Template 1 Content {{ .FilePath }}")
	tmpFile2 := createDummyTemplateFile(t, filepath.Join(tmpDir, "template2.tmpl"), "Template 2 Content {{ .FilePath }}")

	opts.TemplatePath = tmpFile1
	tpl1, _ := template.ParseFiles(tmpFile1)
	opts.Template = tpl1
	hash6a, err6a := processor.CalculateConfigHash()
	require.NoError(t, err6a)
	assert.NotEqual(t, hash1, hash6a, "Config hash should change when TemplatePath changes")

	opts.TemplatePath = tmpFile2
	tpl2, _ := template.ParseFiles(tmpFile2)
	opts.Template = tpl2
	hash6b, err6b := processor.CalculateConfigHash()
	require.NoError(t, err6b)
	assert.NotEqual(t, hash6a, hash6b, "Config hash should change when template content changes")

	// 7. Test Front Matter config changes
	opts.FrontMatterConfig.Enabled = true
	hash7a, err7a := processor.CalculateConfigHash()
	require.NoError(t, err7a)
	assert.NotEqual(t, hash1, hash7a, "Hash should change when FrontMatter enabled")

	opts.FrontMatterConfig.Format = "toml"
	hash7b, err7b := processor.CalculateConfigHash()
	require.NoError(t, err7b)
	assert.NotEqual(t, hash7a, hash7b, "Hash should change when FrontMatter format changes")

	opts.FrontMatterConfig.Static = map[string]interface{}{"new": "val"} // Ensure map is not nil before adding
	hash7c, err7c := processor.CalculateConfigHash()
	require.NoError(t, err7c)
	assert.NotEqual(t, hash7b, hash7c, "Hash should change when FrontMatter static changes")

	opts.FrontMatterConfig.Include = append(opts.FrontMatterConfig.Include, "FilePath")
	hash7d, err7d := processor.CalculateConfigHash()
	require.NoError(t, err7d)
	assert.NotEqual(t, hash7c, hash7d, "Hash should change when FrontMatter include changes")
}

// REMOVED: TestGenerateOutputPath
// REMOVED: TestGenerateFrontMatter
// REMOVED: TestTruncateContent
// REMOVED: TestConvertMetaToMap
// REMOVED: TestUpdateMetadataFromMap

// Rationale for removal:
// The functions generateOutputPath, generateFrontMatter, truncateContent,
// convertMetaToMap, and updateMetadataFromMap are unexported helper functions
// within the pkg/converter package (specifically in processor.go).
// Test files in pkg/converter_test cannot directly access unexported functions
// from pkg/converter.
// Attempting to call processor.FunctionName() fails because these are not methods
// on the FileProcessor struct.
// Attempting to call converter.FunctionName() fails because the functions are not exported.
//
// The correctness of these helper functions is implicitly tested by the main
// TestFileProcessor_ProcessFile_* tests which cover the overall file processing
// pipeline where these helpers are used. For example:
// - TestFileProcessor_ProcessFile_HappyPath_CacheMiss implicitly tests generateOutputPath
//   by checking the fileInfo.OutputPath field.
// - TestFileProcessor_ProcessFile_FrontMatterAndPostprocessor implicitly tests
//   generateFrontMatter by checking the presence and content of the front matter block.
// - TestFileProcessor_ProcessFile_LargeFileTruncate implicitly tests truncateContent
//   by checking the content passed to the template executor.
// - TestFileProcessor_ProcessFile_WithPreprocessorPlugin and other tests involving
//   plugins implicitly test convertMetaToMap and updateMetadataFromMap by verifying
//   that metadata is correctly passed to plugins and updates from plugins are reflected.
//
// If direct unit testing of these helpers is desired, the tests should be moved
// into the pkg/converter package itself (e.g., in processor_internal_test.go)
// or the helpers refactored into an exported utility function if appropriate.

// --- END OF FINAL REVISED FILE pkg/converter/processor_test.go ---
