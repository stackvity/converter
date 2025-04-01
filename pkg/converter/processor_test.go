// --- START OF FINAL REVISED FILE pkg/converter/processor_test.go ---
package converter_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter" // Use corrected import path
	// Import template package types explicitly if needed
	tpl "github.com/stackvity/stack-converter/pkg/converter/template"

	// Mocks defined locally in this file for testing processor logic
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Mock Implementations (Defined locally for processor tests) ---

// MockCacheManager provides a mock implementation for converter.CacheManager.
type MockCacheManager struct {
	mock.Mock
}

func (m *MockCacheManager) Load(cachePath string) error {
	args := m.Called(cachePath)
	return args.Error(0)
}
func (m *MockCacheManager) Check(filePath string, modTime time.Time, contentHash string, configHash string) (bool, string) {
	args := m.Called(filePath, modTime, contentHash, configHash)
	isHit, _ := args.Get(0).(bool)
	outputHash, _ := args.Get(1).(string)
	return isHit, outputHash
}
func (m *MockCacheManager) Update(filePath string, modTime time.Time, sourceHash string, configHash string, outputHash string) error {
	args := m.Called(filePath, modTime, sourceHash, configHash, outputHash)
	return args.Error(0)
}
func (m *MockCacheManager) Persist(cachePath string) error {
	args := m.Called(cachePath)
	return args.Error(0)
}

// MockLanguageDetector provides a mock implementation for language.LanguageDetector.
type MockLanguageDetector struct {
	mock.Mock
}

func (m *MockLanguageDetector) Detect(content []byte, filePath string) (string, float64, error) {
	args := m.Called(content, filePath)
	lang, _ := args.Get(0).(string)
	confidence, _ := args.Get(1).(float64)
	err := args.Error(2)
	return lang, confidence, err
}

// MockEncodingHandler provides a mock implementation for encoding.EncodingHandler.
type MockEncodingHandler struct {
	mock.Mock
}

// Corrected mock method signature based on encoding/handler.go
func (m *MockEncodingHandler) DetectAndDecode(content []byte) ([]byte, string, bool, error) {
	args := m.Called(content)
	retBytes, _ := args.Get(0).([]byte)
	detectedEncoding, _ := args.Get(1).(string)
	certainty, _ := args.Get(2).(bool)
	err := args.Error(3)
	return retBytes, detectedEncoding, certainty, err
}
func (m *MockEncodingHandler) IsBinary(content []byte) bool {
	args := m.Called(content)
	isBinary, _ := args.Get(0).(bool)
	return isBinary
}

// MockAnalysisEngine provides a mock implementation for analysis.AnalysisEngine.
type MockAnalysisEngine struct {
	mock.Mock
}

func (m *MockAnalysisEngine) ExtractDocComments(content []byte, language string, styles []string) (string, error) {
	args := m.Called(content, language, styles)
	comments, _ := args.Get(0).(string)
	err := args.Error(1)
	return comments, err
}

// MockGitClient provides a mock implementation for converter.GitClient.
type MockGitClient struct {
	mock.Mock
}

func (m *MockGitClient) GetFileMetadata(repoPath, filePath string) (map[string]string, error) {
	args := m.Called(repoPath, filePath)
	retMap, _ := args.Get(0).(map[string]string)
	err := args.Error(1)
	return retMap, err
}
func (m *MockGitClient) GetChangedFiles(repoPath, mode string, ref string) ([]string, error) {
	args := m.Called(repoPath, mode, ref)
	retSlice, _ := args.Get(0).([]string)
	err := args.Error(1)
	return retSlice, err
}

// MockPluginRunner provides a mock implementation for converter.PluginRunner.
type MockPluginRunner struct {
	mock.Mock
}

// Use the PluginInput/PluginOutput types now defined in the converter package
func (m *MockPluginRunner) Run(ctx context.Context, stage string, pluginConfig converter.PluginConfig, input converter.PluginInput) (converter.PluginOutput, error) {
	args := m.Called(ctx, stage, pluginConfig, input)
	output, _ := args.Get(0).(converter.PluginOutput)
	err := args.Error(1)
	return output, err
}

// MockTemplateExecutor provides a mock implementation for template.TemplateExecutor.
type MockTemplateExecutor struct {
	mock.Mock
}

// Use the TemplateMetadata type from the template package
func (m *MockTemplateExecutor) Execute(writer io.Writer, template *template.Template, metadata *tpl.TemplateMetadata) error {
	args := m.Called(writer, template, metadata)
	if args.Error(0) == nil && metadata != nil {
		// Basic mock implementation - can be customized per test if needed
		content := fmt.Sprintf("## Mock Template Output for %s\nLang: %s\n", metadata.FilePath, metadata.DetectedLanguage)
		if metadata.GitInfo != nil {
			content += fmt.Sprintf("Commit: %s\n", metadata.GitInfo.Commit)
		}
		if metadata.ExtractedComments != nil {
			content += fmt.Sprintf("Comments: %s\n", *metadata.ExtractedComments)
		}
		if fm, ok := metadata.FrontMatter["layout"]; ok {
			content += fmt.Sprintf("Layout: %v\n", fm) // Example accessing front matter
		}
		_, _ = writer.Write([]byte(content))
	}
	return args.Error(0)
}

// MockEventHooks provides a mock implementation for converter.Hooks.
type MockEventHooks struct {
	mock.Mock
}

func (m *MockEventHooks) OnFileDiscovered(path string) error {
	args := m.Called(path)
	return args.Error(0)
}
func (m *MockEventHooks) OnFileStatusUpdate(path string, status converter.Status, message string, duration time.Duration) error {
	args := m.Called(path, status, message, duration)
	return args.Error(0)
}
func (m *MockEventHooks) OnRunComplete(report converter.Report) error {
	args := m.Called(report)
	return args.Error(0)
}

// --- Test Suite Setup ---

type ProcessorTestSuite struct {
	proc            *converter.FileProcessor // Use type from converter package
	mockCacheMgr    *MockCacheManager
	mockLangDet     *MockLanguageDetector
	mockEncHandler  *MockEncodingHandler
	mockAnalysisEng *MockAnalysisEngine
	mockGitClient   *MockGitClient
	mockPluginRun   *MockPluginRunner
	mockTplExec     *MockTemplateExecutor
	mockHooks       *MockEventHooks
	opts            *converter.Options // Use type from converter package
	tempDir         string
	logBuf          *strings.Builder
	loggerHandler   slog.Handler
}

func setupProcessorTestSuite(t *testing.T) *ProcessorTestSuite {
	s := &ProcessorTestSuite{}
	s.tempDir = t.TempDir()

	s.logBuf = &strings.Builder{}
	s.loggerHandler = slog.NewTextHandler(s.logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})

	// Use locally defined mocks
	s.mockCacheMgr = new(MockCacheManager)
	s.mockLangDet = new(MockLanguageDetector)
	s.mockEncHandler = new(MockEncodingHandler)
	s.mockAnalysisEng = new(MockAnalysisEngine)
	s.mockGitClient = new(MockGitClient)
	s.mockPluginRun = new(MockPluginRunner)
	s.mockTplExec = new(MockTemplateExecutor)
	s.mockHooks = new(MockEventHooks)

	absInputPath, _ := filepath.Abs(s.tempDir)
	absOutputPath := filepath.Join(absInputPath, "out")

	// Use the default template loading function from the template package
	// Assuming default.tmpl exists now. If not, this will error.
	defaultTpl, err := tpl.LoadDefaultTemplate()
	require.NoError(t, err, "Ensure default.tmpl exists in pkg/converter/template/")

	// Use converter from pkg/converter
	// Assume types like OnErrorMode, BinaryMode, LargeFileMode, GitDiffMode,
	// ReportSummary, FileInfo, SkippedInfo, ErrorInfo are defined in converter package
	s.opts = &converter.Options{
		InputPath:            absInputPath,
		OutputPath:           absOutputPath,
		CacheEnabled:         true,
		IgnoreCacheRead:      false,
		OnErrorMode:          converter.OnErrorContinue, // Assuming defined in converter/types.go
		BinaryMode:           converter.BinarySkip,      // Assuming defined in converter/types.go
		LargeFileMode:        converter.LargeFileSkip,   // Assuming defined in converter/types.go
		LargeFileThreshold:   100 * 1024 * 1024,         // Example large threshold
		LargeFileTruncateCfg: "1MB",
		GitMetadataEnabled:   false,
		AnalysisOptions:      converter.AnalysisConfig{ExtractComments: false},
		FrontMatterConfig:    converter.FrontMatterOptions{Enabled: false, Format: "yaml"},
		PluginConfigs:        []converter.PluginConfig{},
		EventHooks:           s.mockHooks,     // Use mock hooks
		Logger:               s.loggerHandler, // Use test logger handler
		Template:             defaultTpl,      // Use loaded default template
		// Explicitly inject mocks for optional interfaces used by processor
		CacheManager:     s.mockCacheMgr,
		GitClient:        s.mockGitClient,
		PluginRunner:     s.mockPluginRun,
		LanguageDetector: s.mockLangDet,
		EncodingHandler:  s.mockEncHandler,
		AnalysisEngine:   s.mockAnalysisEng,
		TemplateExecutor: s.mockTplExec,
	}

	err = os.MkdirAll(s.opts.OutputPath, 0755)
	require.NoError(t, err)

	// Instantiate processor using type from pkg/converter
	// Pass all injected interfaces explicitly
	s.proc = converter.NewFileProcessor(
		s.opts,
		s.loggerHandler,
		s.opts.CacheManager, // Pass interface from opts
		s.opts.LanguageDetector,
		s.opts.EncodingHandler,
		s.opts.AnalysisEngine,
		s.opts.GitClient,
		s.opts.PluginRunner,
		s.opts.TemplateExecutor,
	)
	require.NotNil(t, s.proc)

	return s
}

// Helper to create a dummy file
func createDummyFile(t *testing.T, path string, content string) {
	t.Helper()
	fullPath := filepath.Join(path)
	dir := filepath.Dir(fullPath)
	err := os.MkdirAll(dir, 0755)
	require.NoError(t, err)
	err = os.WriteFile(fullPath, []byte(content), 0644)
	require.NoError(t, err)
}

// Helper to calculate SHA256 hash
func calculateSHA256(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}

// Helper function within processor_test.go to access calculateConfigHash
// FIX: This helper now calls the *unexported* method calculateConfigHash.
// This is allowed because this test file is in the converter_test package.
func calculateConfigHashForTest(p *converter.FileProcessor) (string, error) {
	// Call the exported method
	return p.CalculateConfigHash() // CORRECT: Calls the exported method
}

// --- Test Cases ---

func TestProcessor_ProcessFile_HappyPath_CacheMiss(t *testing.T) {
	s := setupProcessorTestSuite(t)
	filePath := filepath.Join(s.tempDir, "code.go")
	relPath := "code.go"
	fileContent := "package main\n\n// My comment\nfunc main(){}"
	createDummyFile(t, filePath, fileContent)
	fileInfo, _ := os.Stat(filePath)
	modTime := fileInfo.ModTime()
	contentBytes := []byte(fileContent)
	sourceHash := calculateSHA256(contentBytes)
	configHash, cfgHashErr := calculateConfigHashForTest(s.proc) // Use helper/direct call
	require.NoError(t, cfgHashErr)
	expectedTemplateOutput := fmt.Sprintf("## `code.go`\n\n```go\n%s\n```\n", fileContent) // Expected base output from default template
	outputHash := calculateSHA256([]byte(expectedTemplateOutput))
	outputPath := "code.md"

	// --- Mock Expectations ---
	s.mockHooks.On("OnFileStatusUpdate", relPath, converter.StatusProcessing, "", mock.AnythingOfType("time.Duration")).Return(nil).Once()
	s.mockCacheMgr.On("Check", relPath, modTime, sourceHash, configHash).Return(false, "").Once() // Miss
	s.mockEncHandler.On("DetectAndDecode", contentBytes).Return(contentBytes, "utf-8", true, nil).Once()
	s.mockEncHandler.On("IsBinary", contentBytes).Return(false).Once()
	s.mockLangDet.On("Detect", contentBytes, relPath).Return("go", 0.99, nil).Once()
	// Mock Template Execution (Simulate default template behavior)
	s.mockTplExec.On("Execute", mock.Anything, s.opts.Template, mock.MatchedBy(func(m *tpl.TemplateMetadata) bool {
		return m.FilePath == relPath && m.DetectedLanguage == "go" && m.Content == fileContent && !m.IsBinary && !m.IsLarge && !m.Truncated && m.ExtractedComments == nil && m.GitInfo == nil
	})).Run(func(args mock.Arguments) {
		writer := args.Get(0).(io.Writer)
		_, _ = writer.Write([]byte(expectedTemplateOutput))
	}).Return(nil).Once()
	s.mockCacheMgr.On("Update", relPath, modTime, sourceHash, configHash, outputHash).Return(nil).Once()
	s.mockHooks.On("OnFileStatusUpdate", relPath, converter.StatusSuccess, "Successfully processed", mock.AnythingOfType("time.Duration")).Return(nil).Once()
	// --- End Mock Expectations ---

	result, status, err := s.proc.ProcessFile(context.Background(), filePath)

	// --- Assertions ---
	require.NoError(t, err)
	assert.Equal(t, converter.StatusSuccess, status)
	require.IsType(t, converter.FileInfo{}, result)
	fi := result.(converter.FileInfo)
	assert.Equal(t, relPath, fi.Path)
	assert.Equal(t, outputPath, fi.OutputPath)
	assert.Equal(t, "go", fi.Language)
	assert.Equal(t, converter.CacheStatusMiss, fi.CacheStatus)
	// Verify file content
	outputFilePath := filepath.Join(s.opts.OutputPath, outputPath)
	outputBytes, readErr := os.ReadFile(outputFilePath)
	require.NoError(t, readErr)
	assert.Equal(t, expectedTemplateOutput, string(outputBytes))
	// Verify mocks
	s.mockCacheMgr.AssertExpectations(t)
	s.mockLangDet.AssertExpectations(t)
	s.mockEncHandler.AssertExpectations(t)
	s.mockTplExec.AssertExpectations(t)
	s.mockHooks.AssertExpectations(t)
	s.mockAnalysisEng.AssertNotCalled(t, "ExtractDocComments", mock.Anything, mock.Anything, mock.Anything)
	s.mockGitClient.AssertNotCalled(t, "GetFileMetadata", mock.Anything, mock.Anything)
	s.mockPluginRun.AssertNotCalled(t, "Run", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	// --- End Assertions ---
}

func TestProcessor_ProcessFile_CacheHit(t *testing.T) {
	s := setupProcessorTestSuite(t)
	filePath := filepath.Join(s.tempDir, "cached.txt")
	relPath := "cached.txt"
	fileContent := "cached content"
	createDummyFile(t, filePath, fileContent)
	fileInfo, _ := os.Stat(filePath)
	modTime := fileInfo.ModTime()
	contentBytes := []byte(fileContent)
	sourceHash := calculateSHA256(contentBytes)
	configHash, _ := calculateConfigHashForTest(s.proc)

	// Mocks
	s.mockHooks.On("OnFileStatusUpdate", relPath, converter.StatusProcessing, "", mock.AnythingOfType("time.Duration")).Return(nil).Once()
	s.mockCacheMgr.On("Check", relPath, modTime, sourceHash, configHash).Return(true, "cachedOutputHash").Once() // Cache Hit
	s.mockHooks.On("OnFileStatusUpdate", relPath, converter.StatusCached, "Retrieved from cache", mock.AnythingOfType("time.Duration")).Return(nil).Once()

	result, status, err := s.proc.ProcessFile(context.Background(), filePath)

	// Assertions
	require.NoError(t, err)
	assert.Equal(t, converter.StatusCached, status)
	require.IsType(t, converter.FileInfo{}, result)
	fi := result.(converter.FileInfo)
	assert.Equal(t, relPath, fi.Path)
	assert.Equal(t, converter.CacheStatusHit, fi.CacheStatus)
	assert.Equal(t, int64(len(fileContent)), fi.SizeBytes)
	assert.Equal(t, modTime, fi.ModTime)
	// Verify mocks
	s.mockCacheMgr.AssertExpectations(t)
	s.mockHooks.AssertExpectations(t)
	s.mockEncHandler.AssertNotCalled(t, "DetectAndDecode", mock.Anything)
	s.mockLangDet.AssertNotCalled(t, "Detect", mock.Anything, mock.Anything)
	s.mockTplExec.AssertNotCalled(t, "Execute", mock.Anything, mock.Anything, mock.Anything)
	s.mockCacheMgr.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// Test specifically for plugin interaction (Example)
func TestProcessor_ProcessFile_WithPreprocessorPlugin(t *testing.T) {
	s := setupProcessorTestSuite(t)
	// Enable a mock preprocessor plugin in options
	pluginCmd := []string{"python", "scripts/plugins/test/add_prefix.py"} // Example command
	s.opts.PluginConfigs = []converter.PluginConfig{
		{Name: "TestPrefixer", Stage: converter.PluginStagePreprocessor, Enabled: true, Command: pluginCmd, AppliesTo: []string{"*.go"}},
	}
	// Recalculate config hash if proc creation doesn't do it based on modified opts
	configHash, cfgHashErr := calculateConfigHashForTest(s.proc)
	require.NoError(t, cfgHashErr)

	filePath := filepath.Join(s.tempDir, "plugin_test.go")
	relPath := "plugin_test.go"
	fileContent := "package main"
	modifiedContent := "PREFIX:" + fileContent // What the mock plugin will return
	createDummyFile(t, filePath, fileContent)
	fileInfo, _ := os.Stat(filePath)
	modTime := fileInfo.ModTime()
	contentBytes := []byte(fileContent)
	sourceHash := calculateSHA256(contentBytes)
	// Assume default template will just wrap the modified content
	expectedTemplateOutput := fmt.Sprintf("## `plugin_test.go`\n\n```go\n%s\n```\n", modifiedContent)
	outputHash := calculateSHA256([]byte(expectedTemplateOutput))
	outputPath := "plugin_test.md"

	// --- Mock Expectations ---
	s.mockHooks.On("OnFileStatusUpdate", relPath, converter.StatusProcessing, "", mock.AnythingOfType("time.Duration")).Return(nil).Once()
	s.mockCacheMgr.On("Check", relPath, modTime, sourceHash, configHash).Return(false, "").Once() // Miss
	s.mockEncHandler.On("DetectAndDecode", contentBytes).Return(contentBytes, "utf-8", true, nil).Once()
	s.mockEncHandler.On("IsBinary", contentBytes).Return(false).Once()
	s.mockLangDet.On("Detect", contentBytes, relPath).Return("go", 0.9, nil).Once()
	// Preprocessor Plugin Execution
	s.mockPluginRun.On("Run", mock.Anything, converter.PluginStagePreprocessor, mock.AnythingOfType("converter.PluginConfig"), mock.MatchedBy(func(input converter.PluginInput) bool {
		return input.FilePath == relPath && input.Content == fileContent && input.Stage == converter.PluginStagePreprocessor
	})).Return(converter.PluginOutput{SchemaVersion: converter.PluginSchemaVersion, Content: modifiedContent, Error: ""}, nil).Once()
	// Template Execution (should receive MODIFIED content)
	s.mockTplExec.On("Execute", mock.Anything, s.opts.Template, mock.MatchedBy(func(m *tpl.TemplateMetadata) bool {
		return m.FilePath == relPath && m.Content == modifiedContent // Verify plugin output used
	})).Run(func(args mock.Arguments) {
		writer := args.Get(0).(io.Writer)
		_, _ = writer.Write([]byte(expectedTemplateOutput))
	}).Return(nil).Once()
	s.mockCacheMgr.On("Update", relPath, modTime, sourceHash, configHash, outputHash).Return(nil).Once()
	s.mockHooks.On("OnFileStatusUpdate", relPath, converter.StatusSuccess, "Successfully processed", mock.AnythingOfType("time.Duration")).Return(nil).Once()
	// --- End Mock Expectations ---

	result, status, err := s.proc.ProcessFile(context.Background(), filePath)

	// --- Assertions ---
	require.NoError(t, err)
	assert.Equal(t, converter.StatusSuccess, status)
	require.IsType(t, converter.FileInfo{}, result)
	fi := result.(converter.FileInfo)
	assert.Contains(t, fi.PluginsRun, "TestPrefixer")
	outputFilePath := filepath.Join(s.opts.OutputPath, outputPath)
	outputBytes, readErr := os.ReadFile(outputFilePath)
	require.NoError(t, readErr)
	assert.Equal(t, expectedTemplateOutput, string(outputBytes))
	s.mockPluginRun.AssertExpectations(t)
	s.mockCacheMgr.AssertExpectations(t)
	s.mockTplExec.AssertExpectations(t)
	s.mockHooks.AssertExpectations(t)
	// --- End Assertions ---
}

// --- Test calculateConfigHash ---
func TestCalculateConfigHash_Stability(t *testing.T) {
	// Re-use setup, but focus only on opts and hash calculation
	s1 := setupProcessorTestSuite(t)
	s1.opts.FrontMatterConfig.Include = []string{"A", "B"}
	s1.opts.AnalysisOptions.CommentStyles = []string{"pydoc", "godoc"}
	s1.opts.PluginConfigs = []converter.PluginConfig{
		{Name: "P2", Stage: converter.PluginStagePostprocessor, Enabled: true},
		{Name: "P1", Stage: converter.PluginStagePreprocessor, Enabled: true},
	}
	hash1, err1 := calculateConfigHashForTest(s1.proc)
	require.NoError(t, err1)

	s2 := setupProcessorTestSuite(t)
	// Same settings, different slice/map order initially
	s2.opts.FrontMatterConfig.Include = []string{"B", "A"}             // Different order
	s2.opts.AnalysisOptions.CommentStyles = []string{"godoc", "pydoc"} // Different order
	s2.opts.PluginConfigs = []converter.PluginConfig{                  // Different order but same enabled plugins
		{Name: "P1", Stage: converter.PluginStagePreprocessor, Enabled: true},
		{Name: "P2", Stage: converter.PluginStagePostprocessor, Enabled: true},
	}
	hash2, err2 := calculateConfigHashForTest(s2.proc)
	require.NoError(t, err2)

	assert.Equal(t, hash1, hash2, "Config hash should be stable regardless of slice/map order")

	// Change a relevant field
	s2.opts.LargeFileMode = converter.LargeFileTruncate // Change a hashed field
	hash3, err3 := calculateConfigHashForTest(s2.proc)
	require.NoError(t, err3)
	assert.NotEqual(t, hash1, hash3, "Config hash should change when relevant fields change")

	// Test template change (simulate file content change)
	s1.opts.Template = template.Must(template.New("tpl1").Parse("{{.FilePath}}"))
	s1.opts.TemplatePath = filepath.Join(s1.tempDir, "tpl1.tmpl") // Use temp dir path
	createDummyFile(t, s1.opts.TemplatePath, "{{.FilePath}}")

	s2.opts.Template = template.Must(template.New("tpl2").Parse("Different {{.FilePath}}"))
	s2.opts.TemplatePath = filepath.Join(s2.tempDir, "tpl2.tmpl") // Use temp dir path
	createDummyFile(t, s2.opts.TemplatePath, "Different {{.FilePath}}")

	hashTpl1, _ := calculateConfigHashForTest(s1.proc)
	hashTpl2, _ := calculateConfigHashForTest(s2.proc)
	assert.NotEqual(t, hashTpl1, hashTpl2, "Config hash should change when TemplatePath/content changes")

	// Test disabled plugin doesn't affect hash
	s1.opts.PluginConfigs = []converter.PluginConfig{
		{Name: "P1", Stage: converter.PluginStagePreprocessor, Enabled: true},
		{Name: "P2", Stage: converter.PluginStagePostprocessor, Enabled: false}, // Disabled
	}
	hashDisabledP1, _ := calculateConfigHashForTest(s1.proc)

	s1.opts.PluginConfigs = []converter.PluginConfig{
		{Name: "P1", Stage: converter.PluginStagePreprocessor, Enabled: true},
		// P2 completely removed
	}
	hashDisabledP2, _ := calculateConfigHashForTest(s1.proc)
	assert.Equal(t, hashDisabledP1, hashDisabledP2, "Disabled plugins should not affect config hash")
}

// Add more tests for other scenarios like BinarySkip, BinaryError, LargeFileSkip, LargeFileTruncate, etc.
// Ensure mocks are configured correctly for each scenario.

// Example Test for Binary Skip
func TestProcessor_ProcessFile_BinarySkip(t *testing.T) {
	s := setupProcessorTestSuite(t)
	s.opts.BinaryMode = converter.BinarySkip // Ensure skip mode
	filePath := filepath.Join(s.tempDir, "logo.png")
	relPath := "logo.png"
	// Create some minimal binary-like content
	// FIX: Use contentBytes consistently
	contentBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00}
	createDummyFile(t, filePath, string(contentBytes)) // WriteFile takes string
	fileInfo, _ := os.Stat(filePath)
	modTime := fileInfo.ModTime()
	sourceHash := calculateSHA256(contentBytes)
	configHash, _ := calculateConfigHashForTest(s.proc)

	// --- Mock Expectations ---
	s.mockHooks.On("OnFileStatusUpdate", relPath, converter.StatusProcessing, "", mock.AnythingOfType("time.Duration")).Return(nil).Once()
	// Assume cache miss for simplicity, or mock hit if testing that interaction
	s.mockCacheMgr.On("Check", relPath, modTime, sourceHash, configHash).Return(false, "").Once()
	// Encoding handler will be called
	// FIX: Use contentBytes in mock expectations
	s.mockEncHandler.On("DetectAndDecode", contentBytes).Return(contentBytes, "application/octet-stream", false, nil).Once()
	// Binary detection returns true
	// FIX: Use contentBytes in mock expectations
	s.mockEncHandler.On("IsBinary", contentBytes).Return(true).Once()
	// Final hook for skipped status
	s.mockHooks.On("OnFileStatusUpdate", relPath, converter.StatusSkipped, mock.AnythingOfType("string"), mock.AnythingOfType("time.Duration")).Return(nil).Once()
	// --- End Mock Expectations ---

	result, status, err := s.proc.ProcessFile(context.Background(), filePath)

	// --- Assertions ---
	require.NoError(t, err)
	assert.Equal(t, converter.StatusSkipped, status)
	require.IsType(t, converter.SkippedInfo{}, result)
	si := result.(converter.SkippedInfo)
	assert.Equal(t, relPath, si.Path)
	assert.Equal(t, converter.SkipReasonBinary, si.Reason) // Assuming defined constant
	assert.Equal(t, "Binary file detected", si.Details)

	// Verify mocks
	s.mockHooks.AssertExpectations(t)
	s.mockCacheMgr.AssertExpectations(t)
	s.mockEncHandler.AssertExpectations(t)
	// Ensure later pipeline stages were NOT called
	s.mockLangDet.AssertNotCalled(t, "Detect", mock.Anything, mock.Anything)
	s.mockTplExec.AssertNotCalled(t, "Execute", mock.Anything, mock.Anything, mock.Anything)
	s.mockCacheMgr.AssertNotCalled(t, "Update", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	// --- End Assertions ---
}

// --- (Keep tests for generateOutputPath, convertMetaToMap, updateMetadataFromMap, truncateContent, generateFrontMatter) ---
// --- (Ensure they use types from pkg/converter or pkg/converter/template correctly) ---

// --- END OF FINAL REVISED FILE pkg/converter/processor_test.go ---
