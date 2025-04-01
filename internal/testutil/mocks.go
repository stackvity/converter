// --- START OF FINAL REVISED FILE internal/testutil/mocks.go ---
// Package testutil provides mock implementations for interfaces defined in the
// stack-converter core library (pkg/converter and subpackages). These mocks
// facilitate unit testing by isolating components.
//
// For guidance on how to effectively use these mocks in tests, refer to the
// main testing strategy document (e.g., /docs/TESTING_GUIDE.md or test-case.md).
package testutil

import (
	"context"
	"io"
	"log/slog"
	"text/template"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter"
	tpl "github.com/stackvity/stack-converter/pkg/converter/template" // Alias to avoid collision
	"github.com/stretchr/testify/mock"
)

// MockCacheManager provides a mock implementation of the converter.CacheManager interface.
// Configure expectations using testify/mock methods (e.g., .On("Check", ...).Return(...)).
// Test implementations using this mock MUST handle thread-safety if the mock state is modified
// concurrently (e.g., tracking Update calls). See converter.CacheManager for the interface contract.
type MockCacheManager struct {
	mock.Mock
}

// Load mocks the Load method.
func (m *MockCacheManager) Load(cachePath string) error {
	args := m.Called(cachePath)
	return args.Error(0)
}

// Check mocks the Check method.
func (m *MockCacheManager) Check(filePath string, modTime time.Time, contentHash string, configHash string) (isHit bool, outputHash string) {
	args := m.Called(filePath, modTime, contentHash, configHash)
	isHit, _ = args.Get(0).(bool)        // Use zero value if assertion fails (implies test setup issue)
	outputHash, _ = args.Get(1).(string) // Use zero value if assertion fails
	return
}

// Update mocks the Update method.
func (m *MockCacheManager) Update(filePath string, modTime time.Time, sourceHash string, configHash string, outputHash string) error {
	args := m.Called(filePath, modTime, sourceHash, configHash, outputHash)
	return args.Error(0)
}

// Persist mocks the Persist method.
func (m *MockCacheManager) Persist(cachePath string) error {
	args := m.Called(cachePath)
	return args.Error(0)
}

// MockLanguageDetector provides a mock implementation of the language.LanguageDetector interface.
// Configure expectations using testify/mock methods (e.g., .On("Detect", ...).Return(...)).
// See language.LanguageDetector for the interface contract.
type MockLanguageDetector struct {
	mock.Mock
}

// Detect mocks the Detect method.
func (m *MockLanguageDetector) Detect(content []byte, filePath string) (lang string, confidence float64, err error) {
	args := m.Called(content, filePath)
	lang, _ = args.Get(0).(string)
	confidence, _ = args.Get(1).(float64)
	err = args.Error(2)
	return
}

// MockEncodingHandler provides a mock implementation of the encoding.EncodingHandler interface.
// Configure expectations using testify/mock methods (e.g., .On("DetectAndDecode", ...).Return(...)).
// See encoding.EncodingHandler for the interface contract.
type MockEncodingHandler struct {
	mock.Mock
}

// DetectAndDecode mocks the DetectAndDecode method.
func (m *MockEncodingHandler) DetectAndDecode(content []byte) (utf8Content []byte, detectedEncoding string, certainty bool, err error) {
	args := m.Called(content)
	utf8Content, _ = args.Get(0).([]byte)
	detectedEncoding, _ = args.Get(1).(string)
	certainty, _ = args.Get(2).(bool)
	err = args.Error(3)
	return
}

// IsBinary mocks the IsBinary method.
func (m *MockEncodingHandler) IsBinary(content []byte) bool {
	args := m.Called(content)
	isBinary, _ := args.Get(0).(bool)
	return isBinary
}

// MockAnalysisEngine provides a mock implementation of the analysis.AnalysisEngine interface.
// Configure expectations using testify/mock methods (e.g., .On("ExtractDocComments", ...).Return(...)).
// See analysis.AnalysisEngine for the interface contract.
type MockAnalysisEngine struct {
	mock.Mock
}

// ExtractDocComments mocks the ExtractDocComments method.
func (m *MockAnalysisEngine) ExtractDocComments(content []byte, language string, styles []string) (comments string, err error) {
	args := m.Called(content, language, styles)
	comments, _ = args.Get(0).(string)
	err = args.Error(1)
	return
}

// MockGitClient provides a mock implementation of the converter.GitClient interface.
// Configure expectations using testify/mock methods (e.g., .On("GetFileMetadata", ...).Return(...)).
// See converter.GitClient for the interface contract.
type MockGitClient struct {
	mock.Mock
}

// GetFileMetadata mocks the GetFileMetadata method.
func (m *MockGitClient) GetFileMetadata(repoPath, filePath string) (metadata map[string]string, err error) {
	args := m.Called(repoPath, filePath)
	metadata, _ = args.Get(0).(map[string]string)
	err = args.Error(1)
	return
}

// GetChangedFiles mocks the GetChangedFiles method.
func (m *MockGitClient) GetChangedFiles(repoPath, mode string, ref string) (files []string, err error) {
	args := m.Called(repoPath, mode, ref)
	files, _ = args.Get(0).([]string)
	err = args.Error(1)
	return
}

// MockPluginRunner provides a mock implementation of the converter.PluginRunner interface.
// Configure expectations using testify/mock methods (e.g., .On("Run", ...).Return(...)).
// See converter.PluginRunner for the interface contract.
type MockPluginRunner struct {
	mock.Mock
}

// Run mocks the Run method.
func (m *MockPluginRunner) Run(ctx context.Context, stage string, pluginConfig converter.PluginConfig, input converter.PluginInput) (output converter.PluginOutput, err error) {
	args := m.Called(ctx, stage, pluginConfig, input)
	output, _ = args.Get(0).(converter.PluginOutput)
	err = args.Error(1)
	return
}

// MockTemplateExecutor provides a mock implementation of the template.TemplateExecutor interface.
// Configure expectations using testify/mock methods (e.g., .On("Execute", ...).Return(...)).
// See template.TemplateExecutor for the interface contract.
type MockTemplateExecutor struct {
	mock.Mock
}

// Execute mocks the Execute method.
func (m *MockTemplateExecutor) Execute(writer io.Writer, template *template.Template, metadata *tpl.TemplateMetadata) error {
	args := m.Called(writer, template, metadata)
	return args.Error(0)
}

// MockHooks provides a mock implementation of the converter.Hooks interface.
// Configure expectations using testify/mock methods (e.g., .On("OnFileStatusUpdate", ...).Return(...)).
// IMPORTANT: If test logic adds state to this mock (e.g., recording calls), the test itself MUST ensure thread-safety
// for concurrent hook invocations (e.g., using mutexes or channels).
// See converter.Hooks for the interface contract and thread-safety requirements.
type MockHooks struct {
	mock.Mock
}

// OnFileDiscovered mocks the OnFileDiscovered method.
func (m *MockHooks) OnFileDiscovered(path string) error {
	args := m.Called(path)
	return args.Error(0)
}

// OnFileStatusUpdate mocks the OnFileStatusUpdate method.
func (m *MockHooks) OnFileStatusUpdate(path string, status converter.Status, message string, duration time.Duration) error {
	args := m.Called(path, status, message, duration)
	return args.Error(0)
}

// OnRunComplete mocks the OnRunComplete method.
func (m *MockHooks) OnRunComplete(report converter.Report) error {
	args := m.Called(report)
	return args.Error(0)
}

// MockLoggerHandler provides a mock implementation for slog.Handler.
// Generally, using slog.NewTextHandler with a bytes.Buffer is preferred for testing log output.
// Use this full mock only if complex handler interaction logic needs verification.
// See slog.Handler for the interface contract.
type MockLoggerHandler struct {
	mock.Mock
}

// Enabled mocks the Enabled method.
func (m *MockLoggerHandler) Enabled(ctx context.Context, level slog.Level) bool {
	args := m.Called(ctx, level)
	enabled, _ := args.Get(0).(bool)
	return enabled
}

// Handle mocks the Handle method.
func (m *MockLoggerHandler) Handle(ctx context.Context, r slog.Record) error {
	args := m.Called(ctx, r)
	return args.Error(0)
}

// WithAttrs mocks the WithAttrs method.
func (m *MockLoggerHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	args := m.Called(attrs)
	retHandler, ok := args.Get(0).(slog.Handler)
	if !ok || retHandler == nil {
		return m // Return self if no specific handler configured or type assertion fails
	}
	return retHandler
}

// WithGroup mocks the WithGroup method.
func (m *MockLoggerHandler) WithGroup(name string) slog.Handler {
	args := m.Called(name)
	retHandler, ok := args.Get(0).(slog.Handler)
	if !ok || retHandler == nil {
		return m // Return self if no specific handler configured or type assertion fails
	}
	return retHandler
}

// --- END OF FINAL REVISED FILE internal/testutil/mocks.go ---
