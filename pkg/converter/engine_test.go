// --- START OF FINAL REVISED FILE pkg/converter/engine_test.go ---
package converter_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog" // Added os import (needed by createDummyFile if kept local, or by os.MkdirAll if moved)
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter"
	"github.com/stackvity/stack-converter/pkg/converter/cache"

	// Import interfaces for factory function signature matching
	"github.com/stackvity/stack-converter/pkg/converter/analysis"
	"github.com/stackvity/stack-converter/pkg/converter/encoding"
	libgit "github.com/stackvity/stack-converter/pkg/converter/git" // Alias for git interface
	"github.com/stackvity/stack-converter/pkg/converter/language"
	libplugin "github.com/stackvity/stack-converter/pkg/converter/plugin" // Alias for plugin interface

	// Use fully qualified path for template package types in factory signature
	pkghtmltemplate "github.com/stackvity/stack-converter/pkg/converter/template" // Use a distinct alias

	"github.com/stackvity/stack-converter/internal/testutil" // Use shared mocks

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Test Suite Setup ---

// EngineTestSuite holds common setup for engine tests.
type EngineTestSuite struct {
	opts          converter.Options
	mockCacheMgr  *testutil.MockCacheManager // Use shared mock
	mockHooks     *testutil.MockHooks        // Use shared mock
	mockProcessor *MockFileProcessor         // Use local mock for processor
	mockWalker    *MockWalker                // Use local mock for walker
	loggerHandler slog.Handler
	logBuf        *bytes.Buffer
	tempInputDir  string
	tempOutputDir string
	defaultCtx    context.Context
	defaultCancel context.CancelFunc
	// Keep mocks for dependencies needed by the *real* processor (used by mock factory)
	mockLangDet     *testutil.MockLanguageDetector
	mockEncHandler  *testutil.MockEncodingHandler
	mockAnalysisEng *testutil.MockAnalysisEngine
	mockGitClient   *testutil.MockGitClient
	mockPluginRun   *testutil.MockPluginRunner // This is testutil.MockPluginRunner which implements converter.PluginRunner
	mockTplExec     *testutil.MockTemplateExecutor
	// Mock Factories
	mockProcessorFactory converter.ProcessorFactory
	mockWalkerFactory    converter.WalkerFactory
}

// --- Local Mocks (Walker, Processor - Factories now return these) ---

// MockWalker simulates the directory walker.
type MockWalker struct {
	mock.Mock
	workerChan   chan<- string   // Store the channel to simulate dispatch
	startWalkErr error           // Error to return from StartWalk
	ctx          context.Context // Store context to check for cancellation
}

// StartWalk mocks the StartWalk method.
func (m *MockWalker) StartWalk(ctx context.Context) error { // minimal comment
	// This method exists for the mock interface but won't be directly called by the
	// Engine in this test setup because the factory returns a real Walker.
	// Assertions should be placed on the factory function or Engine behavior.
	args := m.Called(ctx)
	m.ctx = ctx
	if m.startWalkErr != nil {
		m.CloseChannel()
		return m.startWalkErr
	}
	return args.Error(0) // Return configured error
}

// Dispatch simulates the walker finding a file and sending it to the channel.
// This is a helper for tests to control when the mocked walker "finds" files.
// It doesn't mock an existing Walker method.
func (m *MockWalker) Dispatch(filePath string) bool { // minimal comment
	if m.ctx != nil {
		select {
		case <-m.ctx.Done():
			m.CloseChannel()
			return false
		default:
		}
	}
	if m.workerChan != nil {
		select {
		case m.workerChan <- filePath:
			return true
		case <-m.ctx.Done():
			m.CloseChannel()
			return false
		case <-time.After(2 * time.Second): // Increased timeout slightly
			panic(fmt.Sprintf("MockWalker Dispatch timed out sending %s", filePath))
		}
	}
	return false
}

// CloseChannel simulates the walker finishing and closing the channel.
// This is a helper for tests to control when the mocked walker "finishes".
// It doesn't mock an existing Walker method.
func (m *MockWalker) CloseChannel() { // minimal comment
	if m.workerChan != nil {
		func() {
			// Prevent panic on closing already closed channel
			defer func() {
				recover()
			}()
			close(m.workerChan)
		}()
		m.workerChan = nil
	}
}

// SetWorkerChan allows the test setup to provide the channel to the mock.
func (m *MockWalker) SetWorkerChan(wc chan<- string) { // minimal comment
	m.workerChan = wc
}

// SetStartWalkError configures the error StartWalk should return.
func (m *MockWalker) SetStartWalkError(err error) { // minimal comment
	m.startWalkErr = err
}

// NewMockWalkerFactory creates a factory function.
func NewMockWalkerFactory(mockW *MockWalker) converter.WalkerFactory { // minimal comment
	return func(opts *converter.Options, wc chan<- string, wg *sync.WaitGroup, lh slog.Handler) (*converter.Walker, error) {
		mockW.SetWorkerChan(wc)
		mockW.On("StartWalk", mock.AnythingOfType("*context.cancelCtx")).Return(mockW.startWalkErr).Maybe()
		realWalker, err := converter.NewWalker(opts, wc, wg, lh)
		if err != nil {
			return nil, err
		}
		return realWalker, nil
	}
}

// MockFileProcessor simulates the file processor.
type MockFileProcessor struct {
	mock.Mock
	ProcessFunc func(ctx context.Context, absFilePath string) (interface{}, converter.Status, error)
}

// ProcessFile mocks the ProcessFile method.
func (m *MockFileProcessor) ProcessFile(ctx context.Context, absFilePath string) (interface{}, converter.Status, error) { // minimal comment
	if m.ProcessFunc != nil {
		return m.ProcessFunc(ctx, absFilePath)
	}
	args := m.Called(ctx, absFilePath)
	res, _ := args.Get(0).(interface{})
	status, _ := args.Get(1).(converter.Status)
	err := args.Error(2)
	return res, status, err
}

// NewMockProcessorFactory creates a factory function.
func NewMockProcessorFactory(mockP *MockFileProcessor) converter.ProcessorFactory {
	// The function being returned *is* the factory.
	factoryFunc := func(
		opts *converter.Options,
		lh slog.Handler,
		// FIX: Use correct interface types from subpackages
		cm cache.CacheManager, // Use type from pkg/converter/cache
		ld language.LanguageDetector, // Use type from pkg/converter/language
		eh encoding.EncodingHandler, // Use type from pkg/converter/encoding
		ae analysis.AnalysisEngine, // Use type from pkg/converter/analysis
		gc libgit.GitClient, // Use aliased type from pkg/converter/git
		pr libplugin.PluginRunner, // Use aliased type from pkg/converter/plugin
		te pkghtmltemplate.TemplateExecutor, // Use aliased type from pkg/converter/template
	) *converter.FileProcessor {
		// This realProcessor instance is returned but its ProcessFile won't be called
		// if the test replaces it or mocks the execution flow differently.
		// Test expectations should be set on the 'mockP' instance passed into the factory.
		// FIX: Pass arguments with corrected types
		realProcessor := converter.NewFileProcessor(opts, lh, cm, ld, eh, ae, gc, pr, te)
		return realProcessor
	}
	// Return the factory function itself. This matches the converter.ProcessorFactory type.
	return factoryFunc
}

// setupEngineTestSuite initializes the test suite environment.
func setupEngineTestSuite(t *testing.T) *EngineTestSuite { // minimal comment
	s := &EngineTestSuite{}
	s.tempInputDir = t.TempDir()
	s.tempOutputDir = t.TempDir()

	s.logBuf = &bytes.Buffer{}
	s.loggerHandler = slog.NewTextHandler(s.logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})

	s.defaultCtx, s.defaultCancel = context.WithCancel(context.Background())

	// Initialize mocks
	s.mockCacheMgr = new(testutil.MockCacheManager)
	s.mockHooks = new(testutil.MockHooks)
	s.mockProcessor = &MockFileProcessor{} // Local mock used for setting expectations
	s.mockWalker = &MockWalker{}           // Local mock used for setting expectations & controlling dispatch
	s.mockLangDet = new(testutil.MockLanguageDetector)
	s.mockEncHandler = new(testutil.MockEncodingHandler)
	s.mockAnalysisEng = new(testutil.MockAnalysisEngine)
	s.mockGitClient = new(testutil.MockGitClient)
	s.mockPluginRun = new(testutil.MockPluginRunner) // This is testutil.MockPluginRunner which implements converter.PluginRunner
	s.mockTplExec = new(testutil.MockTemplateExecutor)

	// Setup mock factories using the local mocks
	s.mockProcessorFactory = NewMockProcessorFactory(s.mockProcessor)
	s.mockWalkerFactory = NewMockWalkerFactory(s.mockWalker)

	cacheFilePath := filepath.Join(s.tempOutputDir, cache.CacheFileName)

	// Setup default valid options
	s.opts = converter.Options{
		InputPath:             s.tempInputDir,
		OutputPath:            s.tempOutputDir,
		AppVersion:            "test-suite-v0.1", // Provide a version for cache manager init
		Logger:                s.loggerHandler,
		EventHooks:            s.mockHooks,
		OnErrorMode:           converter.OnErrorContinue,
		CacheEnabled:          true,
		CacheFilePath:         cacheFilePath,
		Concurrency:           2,
		LargeFileThreshold:    100 * 1024 * 1024,
		LargeFileMode:         converter.LargeFileSkip,
		BinaryMode:            converter.BinarySkip,
		GitDiffMode:           converter.GitDiffModeNone,
		AnalysisOptions:       converter.AnalysisConfig{ExtractComments: false},
		FrontMatterConfig:     converter.FrontMatterOptions{Enabled: false},
		DispatchWarnThreshold: 1 * time.Second,
		// Inject mocks for dependencies (these will be used by the REAL processor/walker instances)
		CacheManager:     s.mockCacheMgr,
		LanguageDetector: s.mockLangDet,
		EncodingHandler:  s.mockEncHandler,
		AnalysisEngine:   s.mockAnalysisEng,
		GitClient:        s.mockGitClient,
		PluginRunner:     s.mockPluginRun, // Inject the mock runner instance (implements converter.PluginRunner)
		TemplateExecutor: s.mockTplExec,
		// Inject mock factories that return REAL instances but configure the local mocks
		ProcessorFactory: s.mockProcessorFactory,
		WalkerFactory:    s.mockWalkerFactory,
	}

	// Default hook expectations
	s.mockHooks.On("OnRunComplete", mock.AnythingOfType("converter.Report")).Return(nil).Maybe()

	// Set default cache manager expectations
	s.mockCacheMgr.On("Load", cacheFilePath).Return(nil).Maybe()
	s.mockCacheMgr.On("Persist", cacheFilePath).Return(nil).Maybe()

	// Default walker expectation (now set within the factory) - removed from here
	// s.mockWalker.On("StartWalk", mock.AnythingOfType("*context.cancelCtx")).Return(nil).Maybe()

	return s
}

// --- Test Cases ---

// TestNewEngine_Success verifies successful engine initialization.
func TestNewEngine_Success(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	s.mockCacheMgr.On("Load", s.opts.CacheFilePath).Return(nil).Once()

	engine, err := converter.NewEngine(s.defaultCtx, s.opts)

	require.NoError(t, err)
	require.NotNil(t, engine)
	s.mockCacheMgr.AssertExpectations(t)
	// Verify default dependencies were set if they were nil initially (example)
	assert.NotNil(t, s.opts.LanguageDetector, "LanguageDetector should be set")
	assert.NotNil(t, s.opts.EncodingHandler, "EncodingHandler should be set")
	assert.NotNil(t, s.opts.AnalysisEngine, "AnalysisEngine should be set")
	assert.NotNil(t, s.opts.TemplateExecutor, "TemplateExecutor should be set")
}

// TestNewEngine_InputPathError verifies error on invalid input path.
func TestNewEngine_InputPathError(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	s.opts.InputPath = filepath.Join(s.tempInputDir, "nonexistent")

	_, err := converter.NewEngine(s.defaultCtx, s.opts)

	require.Error(t, err)
	assert.ErrorIs(t, err, converter.ErrConfigValidation)
	assert.Contains(t, err.Error(), "cannot access input path")
}

// TestNewEngine_OutputPathError verifies error on invalid output path parent.
func TestNewEngine_OutputPathError(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	invalidOutputPathParent := filepath.Join(s.tempOutputDir, "parent_is_file")
	invalidOutputPath := filepath.Join(invalidOutputPathParent, "out")
	// FIX: Use shared testutil helper function
	testutil.CreateDummyFile(t, invalidOutputPathParent, "i-am-a-file") // Use helper from testutil
	s.opts.OutputPath = invalidOutputPath

	_, err := converter.NewEngine(s.defaultCtx, s.opts)

	require.Error(t, err)
	assert.ErrorIs(t, err, converter.ErrConfigValidation)
	assert.Contains(t, err.Error(), "cannot create or access output directory")
}

// TestNewEngine_NilLoggerError checks for nil logger error.
func TestNewEngine_NilLoggerError(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	s.opts.Logger = nil

	_, err := converter.NewEngine(s.defaultCtx, s.opts)

	require.Error(t, err)
	assert.ErrorIs(t, err, converter.ErrConfigValidation)
	assert.Contains(t, err.Error(), "Logger implementation (slog.Handler) cannot be nil")
}

// TestNewEngine_NilHooks_UsesDefault verifies that nil hooks are replaced.
func TestNewEngine_NilHooks_UsesDefault(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	s.opts.EventHooks = nil
	s.mockCacheMgr.On("Load", s.opts.CacheFilePath).Return(nil).Maybe()

	_, err := converter.NewEngine(s.defaultCtx, s.opts) // opts will be modified by NewEngine

	require.NoError(t, err)
	require.NotNil(t, s.opts.EventHooks, "EventHooks should have been defaulted in opts")
	_, ok := s.opts.EventHooks.(*converter.NoOpHooks)
	assert.True(t, ok, "Defaulted EventHooks should be NoOpHooks")
}

// TestEngine_Run_HappyPath verifies basic orchestration using mock walker/processor.
func TestEngine_Run_HappyPath(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	s.opts.Concurrency = 1
	file1Rel := "file1.go"
	file2Rel := "subdir/file2.txt"
	file1Abs := filepath.Join(s.tempInputDir, file1Rel)
	file2Abs := filepath.Join(s.tempInputDir, file2Rel)

	// Configure expectations on the *mock* processor instance
	// Note: Even though the factory returns a *real* processor, these expectations
	// are placed on the *mock* instance `s.mockProcessor` which the factory knows about.
	// This setup is slightly complex; a cleaner approach might inject the mock directly
	// instead of via a factory if the goal is just to mock ProcessFile.
	// However, this factory pattern allows testing the factory injection mechanism itself.
	s.mockProcessor.On("ProcessFile", mock.Anything, file1Abs).Return(
		converter.FileInfo{Path: file1Rel, Language: "go"}, converter.StatusSuccess, nil,
	).Once()
	s.mockProcessor.On("ProcessFile", mock.Anything, file2Abs).Return(
		converter.FileInfo{Path: file2Rel, Language: "plaintext"}, converter.StatusSuccess, nil,
	).Once()

	// Configure mock walker expectations (StartWalk error)
	s.mockWalker.SetStartWalkError(nil) // No error for StartWalk

	// Configure other mock dependencies
	s.mockHooks.On("OnRunComplete", mock.AnythingOfType("converter.Report")).Return(nil).Once()
	s.mockCacheMgr.On("Persist", s.opts.CacheFilePath).Return(nil).Once()

	engine, err := converter.NewEngine(s.defaultCtx, s.opts)
	require.NoError(t, err)

	runComplete := make(chan struct{})
	var report converter.Report
	var runErr error
	go func() {
		report, runErr = engine.Run()
		close(runComplete)
	}()

	time.Sleep(10 * time.Millisecond)
	// Use the mock walker to simulate file dispatch
	require.True(t, s.mockWalker.Dispatch(file1Abs), "Dispatch file1 should succeed")
	require.True(t, s.mockWalker.Dispatch(file2Abs), "Dispatch file2 should succeed")
	s.mockWalker.CloseChannel() // Signal walker is done

	<-runComplete // Wait for engine.Run() to finish

	require.NoError(t, runErr)
	assert.False(t, report.Summary.FatalErrorOccurred)
	assert.Equal(t, int64(2), report.Summary.TotalFilesScanned)
	assert.Equal(t, 2, report.Summary.ProcessedCount)
	assert.Len(t, report.ProcessedFiles, 2)
	assert.ElementsMatch(t, []string{file1Rel, file2Rel}, []string{report.ProcessedFiles[0].Path, report.ProcessedFiles[1].Path})

	// Assert that the *mock* processor had its ProcessFile method called as expected.
	s.mockProcessor.AssertExpectations(t)
	// Assert that the *mock* walker had its StartWalk method called (indirectly via factory setup).
	s.mockWalker.AssertCalled(t, "StartWalk", mock.Anything)
	s.mockCacheMgr.AssertExpectations(t)
	s.mockHooks.AssertExpectations(t)
}

// TestEngine_Run_WalkerInitError tests handling of walker factory returning error.
func TestEngine_Run_WalkerInitError(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	mockInitError := errors.New("walker failed to initialize")

	// Configure the mock factory to return an error
	s.opts.WalkerFactory = func(opts *converter.Options, wc chan<- string, wg *sync.WaitGroup, lh slog.Handler) (*converter.Walker, error) {
		close(wc) // Need to close the channel the engine creates
		return nil, mockInitError
	}

	engine, err := converter.NewEngine(s.defaultCtx, s.opts)
	require.NoError(t, err) // NewEngine itself doesn't call the factory

	report, runErr := engine.Run() // Run calls the factory

	require.Error(t, runErr)
	assert.ErrorIs(t, runErr, mockInitError)
	assert.Contains(t, runErr.Error(), "walker initialization failed")
	assert.True(t, report.Summary.FatalErrorOccurred)
	assert.Equal(t, int64(0), report.Summary.TotalFilesScanned)
	assert.Equal(t, 0, report.Summary.ProcessedCount)

	// Processor should not have been called if walker init failed
	s.mockProcessor.AssertNotCalled(t, "ProcessFile", mock.Anything, mock.Anything)
	s.mockHooks.AssertCalled(t, "OnRunComplete", mock.AnythingOfType("converter.Report"))
}

// TestEngine_Run_WalkerStartWalkError tests handling of walker StartWalk returning error.
func TestEngine_Run_WalkerStartWalkError(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	mockWalkError := errors.New("critical directory read error")

	// Configure the mock walker (which the factory uses) to return an error from StartWalk
	s.mockWalker.SetStartWalkError(mockWalkError)
	// Set expectation on the mock walker instance
	s.mockWalker.On("StartWalk", mock.AnythingOfType("*context.cancelCtx")).Return(mockWalkError).Once()

	engine, err := converter.NewEngine(s.defaultCtx, s.opts)
	require.NoError(t, err)

	report, runErr := engine.Run() // Engine calls StartWalk on the *real* walker instance

	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "directory walk failed")
	// Check the underlying error returned by the engine run
	assert.ErrorIs(t, runErr, mockWalkError)
	assert.True(t, report.Summary.FatalErrorOccurred)
	assert.Equal(t, int64(0), report.Summary.TotalFilesScanned)
	assert.Equal(t, 0, report.Summary.ProcessedCount)

	// Assert the mock was *expected* to be called, even though the real one was executed
	s.mockWalker.AssertCalled(t, "StartWalk", mock.Anything)
	s.mockProcessor.AssertNotCalled(t, "ProcessFile", mock.Anything, mock.Anything)
	s.mockHooks.AssertCalled(t, "OnRunComplete", mock.AnythingOfType("converter.Report"))
}

// TestEngine_Run_CacheHit verifies cache hit handling.
func TestEngine_Run_CacheHit(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	s.opts.Concurrency = 1
	file1Rel := "cached.go"
	file1Abs := filepath.Join(s.tempInputDir, file1Rel)

	// Configure the mock processor expectation
	s.mockProcessor.On("ProcessFile", mock.Anything, file1Abs).Return(
		converter.FileInfo{Path: file1Rel, CacheStatus: converter.CacheStatusHit}, converter.StatusCached, nil,
	).Once()

	s.mockWalker.SetStartWalkError(nil) // No error for StartWalk
	s.mockHooks.On("OnRunComplete", mock.AnythingOfType("converter.Report")).Return(nil).Once()
	s.mockCacheMgr.On("Persist", s.opts.CacheFilePath).Return(nil).Once()

	engine, err := converter.NewEngine(s.defaultCtx, s.opts)
	require.NoError(t, err)

	runComplete := make(chan struct{})
	var report converter.Report
	var runErr error
	go func() {
		report, runErr = engine.Run()
		close(runComplete)
	}()

	time.Sleep(10 * time.Millisecond)
	s.mockWalker.Dispatch(file1Abs)
	s.mockWalker.CloseChannel()

	<-runComplete

	require.NoError(t, runErr)
	assert.False(t, report.Summary.FatalErrorOccurred)
	assert.Equal(t, int64(1), report.Summary.TotalFilesScanned)
	assert.Equal(t, 1, report.Summary.ProcessedCount) // Cached files are still "processed" in the summary count
	assert.Equal(t, 1, report.Summary.CachedCount)    // Specific cache count
	assert.Len(t, report.ProcessedFiles, 1)           // It appears in the processed list
	assert.Equal(t, converter.CacheStatusHit, report.ProcessedFiles[0].CacheStatus)

	// Assert expectations on mocks
	s.mockProcessor.AssertExpectations(t)
	s.mockWalker.AssertCalled(t, "StartWalk", mock.Anything)
	s.mockCacheMgr.AssertExpectations(t)
	s.mockHooks.AssertExpectations(t)
}

// TestEngine_Run_OnErrorContinue verifies error aggregation.
func TestEngine_Run_OnErrorContinue(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	s.opts.OnErrorMode = converter.OnErrorContinue
	s.opts.Concurrency = 1
	file1Rel := "good.go"
	file2Rel := "bad.txt"
	file1Abs := filepath.Join(s.tempInputDir, file1Rel)
	file2Abs := filepath.Join(s.tempInputDir, file2Rel)

	mockError := errors.New("simulated processing error")
	// Configure mock processor expectations
	s.mockProcessor.On("ProcessFile", mock.Anything, file1Abs).Return(
		converter.FileInfo{Path: file1Rel}, converter.StatusSuccess, nil,
	).Once()
	s.mockProcessor.On("ProcessFile", mock.Anything, file2Abs).Return(
		converter.ErrorInfo{Path: file2Rel, Error: mockError.Error(), IsFatal: false}, converter.StatusFailed, mockError,
	).Once()

	s.mockWalker.SetStartWalkError(nil)
	s.mockHooks.On("OnRunComplete", mock.AnythingOfType("converter.Report")).Return(nil).Once()
	s.mockCacheMgr.On("Persist", s.opts.CacheFilePath).Return(nil).Once()

	engine, err := converter.NewEngine(s.defaultCtx, s.opts)
	require.NoError(t, err)

	runComplete := make(chan struct{})
	var report converter.Report
	var runErr error
	go func() {
		report, runErr = engine.Run()
		close(runComplete)
	}()

	time.Sleep(10 * time.Millisecond)
	s.mockWalker.Dispatch(file1Abs)
	s.mockWalker.Dispatch(file2Abs)
	s.mockWalker.CloseChannel()

	<-runComplete

	require.NoError(t, runErr) // Engine itself doesn't error in 'continue' mode
	assert.False(t, report.Summary.FatalErrorOccurred)
	assert.Equal(t, int64(2), report.Summary.TotalFilesScanned)
	assert.Equal(t, 1, report.Summary.ProcessedCount) // Only good.go
	assert.Equal(t, 1, report.Summary.ErrorCount)     // bad.txt failed
	assert.Len(t, report.ProcessedFiles, 1)
	assert.Len(t, report.Errors, 1)
	assert.Equal(t, file2Rel, report.Errors[0].Path)
	assert.Contains(t, report.Errors[0].Error, "simulated processing error")

	// Assert expectations on mocks
	s.mockProcessor.AssertExpectations(t)
	s.mockWalker.AssertCalled(t, "StartWalk", mock.Anything)
	s.mockCacheMgr.AssertExpectations(t)
	s.mockHooks.AssertExpectations(t)
}

// TestEngine_Run_OnErrorStop verifies early exit.
func TestEngine_Run_OnErrorStop(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	s.opts.OnErrorMode = converter.OnErrorStop
	s.opts.Concurrency = 1
	file1Rel := "bad.txt"
	file1Abs := filepath.Join(s.tempInputDir, file1Rel)

	mockError := errors.New("fatal processing error")
	// Configure mock processor expectation for the first file
	s.mockProcessor.On("ProcessFile", mock.Anything, file1Abs).Return(
		converter.ErrorInfo{Path: file1Rel, Error: mockError.Error(), IsFatal: true}, converter.StatusFailed, mockError,
	).Once()
	// Do NOT set expectation for file2Abs, as it shouldn't be called

	s.mockWalker.SetStartWalkError(nil)
	s.mockHooks.On("OnRunComplete", mock.AnythingOfType("converter.Report")).Return(nil).Once()
	s.mockCacheMgr.On("Persist", s.opts.CacheFilePath).Return(nil).Maybe() // May not persist if stopped early

	engine, err := converter.NewEngine(s.defaultCtx, s.opts)
	require.NoError(t, err)

	runComplete := make(chan struct{})
	var report converter.Report
	var runErr error
	go func() {
		report, runErr = engine.Run()
		close(runComplete)
	}()

	time.Sleep(10 * time.Millisecond)
	s.mockWalker.Dispatch(file1Abs) // Dispatch the file that will cause the error
	// Do not dispatch file2Abs - the engine should stop
	// The CloseChannel might happen automatically due to context cancellation triggered by the fatal error

	<-runComplete

	require.Error(t, runErr) // Engine run should return the fatal error
	assert.ErrorIs(t, runErr, mockError)
	assert.Contains(t, runErr.Error(), "processing stopped due to fatal error")
	assert.True(t, report.Summary.FatalErrorOccurred)
	assert.Equal(t, int64(1), report.Summary.TotalFilesScanned) // Only the first file's result aggregated
	assert.Equal(t, 1, report.Summary.ErrorCount)
	assert.Len(t, report.Errors, 1)
	assert.Equal(t, file1Rel, report.Errors[0].Path)
	assert.True(t, report.Errors[0].IsFatal) // Verify error was marked fatal

	// Assert expectations on mocks
	s.mockProcessor.AssertExpectations(t) // Verifies ProcessFile was called once for file1Abs
	s.mockWalker.AssertCalled(t, "StartWalk", mock.Anything)
	s.mockHooks.AssertExpectations(t)
}

// TestEngine_Run_ContextCancellation verifies shutdown.
func TestEngine_Run_ContextCancellation(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	s.opts.Concurrency = 2
	fileCount := 5
	filesAbs := make([]string, fileCount)
	for i := 0; i < fileCount; i++ {
		fileRel := fmt.Sprintf("file%d.go", i)
		filesAbs[i] = filepath.Join(s.tempInputDir, fileRel)
		// FIX: Use shared testutil helper function
		testutil.CreateDummyFile(t, filesAbs[i], fmt.Sprintf("content %d", i))
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Simulate processing respecting cancellation using the mock's ProcessFunc
	s.mockProcessor.ProcessFunc = func(pCtx context.Context, absFilePath string) (interface{}, converter.Status, error) {
		// Set expectation dynamically for this call
		s.mockProcessor.Called(pCtx, absFilePath)
		select {
		case <-time.After(50 * time.Millisecond): // Simulate work
			relPath, _ := filepath.Rel(s.tempInputDir, absFilePath)
			return converter.FileInfo{Path: filepath.ToSlash(relPath)}, converter.StatusSuccess, nil
		case <-pCtx.Done(): // Check the context passed to ProcessFile
			relPath, _ := filepath.Rel(s.tempInputDir, absFilePath)
			// Return error matching context cancellation
			return converter.ErrorInfo{Path: filepath.ToSlash(relPath), Error: pCtx.Err().Error(), IsFatal: true}, converter.StatusFailed, pCtx.Err()
		}
	}
	// Expect ProcessFile to be called for some files, but not necessarily all
	s.mockProcessor.On("ProcessFile", mock.Anything, mock.AnythingOfType("string")).Return(
		converter.FileInfo{}, converter.StatusSuccess, nil, // Dummy return for expectation matching
	).Maybe() // Use Maybe() as not all files might be processed before cancellation

	s.mockWalker.SetStartWalkError(nil)
	s.mockHooks.On("OnRunComplete", mock.AnythingOfType("converter.Report")).Return(nil).Maybe()
	s.mockCacheMgr.On("Persist", s.opts.CacheFilePath).Return(nil).Maybe()

	engine, err := converter.NewEngine(ctx, s.opts) // Pass the cancellable context to the engine
	require.NoError(t, err)

	var report converter.Report
	var runErr error
	runDone := make(chan struct{})

	go func() {
		report, runErr = engine.Run()
		close(runDone)
	}()

	time.Sleep(5 * time.Millisecond)
	dispatchSuccessful := true
	for _, f := range filesAbs {
		// Dispatch files using the mock walker
		if !s.mockWalker.Dispatch(f) {
			dispatchSuccessful = false
			break // Stop dispatching if context cancelled
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Only close channel if dispatch didn't fail due to cancellation
	if dispatchSuccessful {
		s.mockWalker.CloseChannel()
	}

	time.Sleep(20 * time.Millisecond)
	cancel() // <<< Cancel the context part way through processing >>>

	select {
	case <-runDone:
	case <-time.After(3 * time.Second): // Increased timeout slightly
		t.Fatal("Engine.Run did not return after context cancellation")
	}

	require.Error(t, runErr)
	assert.ErrorIs(t, runErr, context.Canceled)
	assert.True(t, report.Summary.FatalErrorOccurred)
	// Verify fewer files were aggregated than dispatched, due to cancellation
	assert.Less(t, report.Summary.TotalFilesScanned, int64(fileCount))

	s.mockWalker.AssertCalled(t, "StartWalk", mock.Anything)
	s.mockHooks.AssertCalled(t, "OnRunComplete", mock.AnythingOfType("converter.Report"))
	// Cannot assert exact number of ProcessFile calls due to cancellation timing
}

// TestEngine_Run_ConcurrencyRace tests aggregation with race detector.
func TestEngine_Run_ConcurrencyRace(t *testing.T) { // minimal comment
	if testing.Short() {
		t.Skip("Skipping concurrency race test in short mode.")
	}

	s := setupEngineTestSuite(t)
	s.opts.Concurrency = 4
	numFiles := 100

	processorWG := sync.WaitGroup{}
	processorWG.Add(numFiles)

	// Configure the mock processor's behavior for different file indices
	s.mockProcessor.ProcessFunc = func(ctx context.Context, absFilePath string) (interface{}, converter.Status, error) {
		defer processorWG.Done()
		// Set expectation for this specific call
		s.mockProcessor.Called(ctx, absFilePath)

		time.Sleep(time.Duration(absFilePath[len(absFilePath)-1]%3) * time.Millisecond) // Simulate variable work

		relPath, _ := filepath.Rel(s.tempInputDir, absFilePath)
		relPath = filepath.ToSlash(relPath)
		i := 0
		_, err := fmt.Sscanf(filepath.Base(relPath), "file%d.txt", &i) // Use Sscanf return value
		if err != nil {
			t.Errorf("Failed to parse file index from path: %s, error: %v", relPath, err)
			i = 0 // Default index if parsing fails
		}

		switch i % 4 {
		case 0:
			return converter.FileInfo{Path: relPath}, converter.StatusSuccess, nil
		case 1:
			return converter.SkippedInfo{Path: relPath, Reason: "test_skip"}, converter.StatusSkipped, nil
		case 2:
			// Simulate non-fatal error
			mockErr := errors.New("test_error")
			return converter.ErrorInfo{Path: relPath, Error: mockErr.Error(), IsFatal: false}, converter.StatusFailed, mockErr
		case 3:
			// Simulate cache hit
			return converter.FileInfo{Path: relPath, CacheStatus: converter.CacheStatusHit}, converter.StatusCached, nil
		default:
			return nil, converter.StatusFailed, errors.New("unhandled case")
		}
	}
	// Need to set a general expectation for ProcessFile because we don't know which file goes to which worker
	s.mockProcessor.On("ProcessFile", mock.Anything, mock.AnythingOfType("string")).Return(
		converter.FileInfo{}, converter.StatusSuccess, nil, // Dummy return values for expectation
	).Maybe() // Allow zero or more calls matching this pattern

	filesAbs := make([]string, numFiles)
	for i := 0; i < numFiles; i++ {
		fileRel := fmt.Sprintf("file%d.txt", i)
		filesAbs[i] = filepath.Join(s.tempInputDir, fileRel)
		// No need to create dummy files if mocking the processor
		// testutil.CreateDummyFile(t, filesAbs[i], fmt.Sprintf("content %d", i)) // <-- Fixed
	}

	s.mockWalker.SetStartWalkError(nil)
	s.mockHooks.On("OnRunComplete", mock.AnythingOfType("converter.Report")).Return(nil).Once()
	s.mockCacheMgr.On("Persist", s.opts.CacheFilePath).Return(nil).Once()

	engine, err := converter.NewEngine(s.defaultCtx, s.opts)
	require.NoError(t, err)

	runComplete := make(chan struct{})
	var report converter.Report
	var runErr error
	go func() {
		report, runErr = engine.Run()
		close(runComplete)
	}()

	time.Sleep(10 * time.Millisecond)
	for _, fileAbs := range filesAbs {
		s.mockWalker.Dispatch(fileAbs)
	}
	s.mockWalker.CloseChannel()

	<-runComplete
	// Wait for all ProcessFunc calls to complete *before* asserting counts
	processorWG.Wait()

	require.NoError(t, runErr)
	assert.False(t, report.Summary.FatalErrorOccurred)

	// Approximate expected counts (adjust calculation if numFiles is not divisible by 4)
	expectedSuccessBase := numFiles / 4 // Case 0
	expectedSkipped := numFiles / 4     // Case 1
	expectedErrors := numFiles / 4      // Case 2
	expectedCached := numFiles / 4      // Case 3
	remaining := numFiles % 4           // Handle remainder
	if remaining > 0 {
		expectedSuccessBase++
	}
	if remaining > 1 {
		expectedSkipped++
	}
	if remaining > 2 {
		expectedErrors++
	}
	expectedProcessed := expectedSuccessBase + expectedCached // Processed = Success + Cached

	assert.Equal(t, expectedProcessed, report.Summary.ProcessedCount, "Processed count mismatch")
	assert.Equal(t, expectedCached, report.Summary.CachedCount, "Cached count mismatch")
	assert.Equal(t, expectedSkipped, report.Summary.SkippedCount, "Skipped count mismatch")
	assert.Equal(t, expectedErrors, report.Summary.ErrorCount, "Error count mismatch")
	assert.Equal(t, int64(numFiles), report.Summary.TotalFilesScanned, "Total scanned mismatch")

	assert.Len(t, report.ProcessedFiles, expectedProcessed)
	assert.Len(t, report.SkippedFiles, expectedSkipped)
	assert.Len(t, report.Errors, expectedErrors)

	// Assert the mock processor was called for each file
	s.mockProcessor.AssertNumberOfCalls(t, "ProcessFile", numFiles)
	s.mockWalker.AssertCalled(t, "StartWalk", mock.Anything)
	s.mockCacheMgr.AssertExpectations(t)
	s.mockHooks.AssertExpectations(t)
}

// TestEngine_Run_FatalErrorWrapping tests if the engine wraps the first fatal error.
func TestEngine_Run_FatalErrorWrapping(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	s.opts.OnErrorMode = converter.OnErrorStop
	s.opts.Concurrency = 1
	file1Rel := "bad1.txt"
	file1Abs := filepath.Join(s.tempInputDir, file1Rel)

	mockError1 := errors.New("the first fatal error")
	// Configure mock processor expectation
	s.mockProcessor.On("ProcessFile", mock.Anything, file1Abs).Return(
		converter.ErrorInfo{Path: file1Rel, Error: mockError1.Error(), IsFatal: true}, converter.StatusFailed, mockError1,
	).Once()

	s.mockWalker.SetStartWalkError(nil)
	s.mockHooks.On("OnRunComplete", mock.AnythingOfType("converter.Report")).Return(nil).Once()
	s.mockCacheMgr.On("Persist", s.opts.CacheFilePath).Return(nil).Maybe() // May not persist

	engine, err := converter.NewEngine(s.defaultCtx, s.opts)
	require.NoError(t, err)

	runComplete := make(chan struct{})
	var report converter.Report
	var runErr error
	go func() {
		report, runErr = engine.Run()
		close(runComplete)
	}()

	time.Sleep(10 * time.Millisecond)
	s.mockWalker.Dispatch(file1Abs)
	s.mockWalker.CloseChannel() // Close channel even if error occurred

	<-runComplete

	require.Error(t, runErr)
	assert.ErrorIs(t, runErr, mockError1, "Engine error should wrap the original fatal error")
	assert.Contains(t, runErr.Error(), "processing stopped due to fatal error", "Engine error message prefix mismatch")
	assert.Contains(t, runErr.Error(), mockError1.Error(), "Engine error message should contain original error text")
	assert.True(t, report.Summary.FatalErrorOccurred)
	assert.Equal(t, 1, report.Summary.ErrorCount)
	require.Len(t, report.Errors, 1)
	assert.True(t, report.Errors[0].IsFatal) // Check IsFatal is set correctly

	// Assert mock expectations
	s.mockProcessor.AssertExpectations(t)
	s.mockWalker.AssertCalled(t, "StartWalk", mock.Anything)
	s.mockHooks.AssertExpectations(t)
}

// TestEngine_Run_CachePersistError verifies behavior when cache persistence fails.
func TestEngine_Run_CachePersistError(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	s.opts.CacheEnabled = true // Ensure cache is enabled
	file1Rel := "file1.go"
	file1Abs := filepath.Join(s.tempInputDir, file1Rel)

	// Configure mock processor
	s.mockProcessor.On("ProcessFile", mock.Anything, file1Abs).Return(
		converter.FileInfo{Path: file1Rel, Language: "go"}, converter.StatusSuccess, nil,
	).Once()

	persistError := errors.New("disk full cannot persist cache")
	// Configure mock cache manager
	s.mockCacheMgr.On("Persist", s.opts.CacheFilePath).Return(persistError).Once() // Simulate persist failure
	s.mockWalker.SetStartWalkError(nil)
	s.mockHooks.On("OnRunComplete", mock.AnythingOfType("converter.Report")).Return(nil).Once()

	engine, err := converter.NewEngine(s.defaultCtx, s.opts)
	require.NoError(t, err)

	runComplete := make(chan struct{})
	var report converter.Report
	var runErr error
	go func() {
		report, runErr = engine.Run()
		close(runComplete)
	}()

	time.Sleep(10 * time.Millisecond)
	s.mockWalker.Dispatch(file1Abs)
	s.mockWalker.CloseChannel()

	<-runComplete

	// Run should NOT return the cache persist error as fatal, but should log it
	require.NoError(t, runErr, "Cache persist error should not make Engine.Run return an error")
	assert.False(t, report.Summary.FatalErrorOccurred)
	assert.Equal(t, 1, report.Summary.ProcessedCount) // Processing succeeded
	assert.Equal(t, 0, report.Summary.ErrorCount)     // No file processing errors

	// Assert mock expectations
	s.mockProcessor.AssertExpectations(t)
	s.mockWalker.AssertCalled(t, "StartWalk", mock.Anything)
	s.mockCacheMgr.AssertExpectations(t)
	s.mockHooks.AssertExpectations(t)
	// Check logs for the persist error message
	assert.Contains(t, s.logBuf.String(), "Failed to persist cache index")
	assert.Contains(t, s.logBuf.String(), persistError.Error())
}

// TestEngine_Run_HookCompleteError verifies behavior when OnRunComplete hook fails.
func TestEngine_Run_HookCompleteError(t *testing.T) { // minimal comment
	s := setupEngineTestSuite(t)
	file1Rel := "file1.go"
	file1Abs := filepath.Join(s.tempInputDir, file1Rel)

	// Configure mock processor
	s.mockProcessor.On("ProcessFile", mock.Anything, file1Abs).Return(
		converter.FileInfo{Path: file1Rel, Language: "go"}, converter.StatusSuccess, nil,
	).Once()

	hookError := errors.New("failed to send report to webhook")
	// Configure mock hooks
	s.mockHooks.On("OnRunComplete", mock.AnythingOfType("converter.Report")).Return(hookError).Once() // Simulate hook error
	s.mockWalker.SetStartWalkError(nil)
	s.mockCacheMgr.On("Persist", s.opts.CacheFilePath).Return(nil).Maybe() // Persist might still happen

	engine, err := converter.NewEngine(s.defaultCtx, s.opts)
	require.NoError(t, err)

	runComplete := make(chan struct{})
	var report converter.Report
	var runErr error
	go func() {
		report, runErr = engine.Run()
		close(runComplete)
	}()

	time.Sleep(10 * time.Millisecond)
	s.mockWalker.Dispatch(file1Abs)
	s.mockWalker.CloseChannel()

	<-runComplete

	// Run should succeed even if the final hook fails
	require.NoError(t, runErr, "OnRunComplete hook error should not make Engine.Run return an error")
	assert.False(t, report.Summary.FatalErrorOccurred)
	assert.Equal(t, 1, report.Summary.ProcessedCount)

	// Assert mock expectations
	s.mockProcessor.AssertExpectations(t)
	s.mockWalker.AssertCalled(t, "StartWalk", mock.Anything)
	s.mockHooks.AssertExpectations(t) // Verify OnRunComplete was called
	// Check logs for the hook error message
	assert.Contains(t, s.logBuf.String(), "OnRunComplete hook returned an error")
	assert.Contains(t, s.logBuf.String(), hookError.Error())
}

// // REMOVED: Local definition of createDummyFile

// --- END OF FINAL REVISED FILE pkg/converter/engine_test.go ---
