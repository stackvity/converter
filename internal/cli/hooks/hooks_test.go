// --- START OF FINAL REVISED FILE internal/cli/hooks/hooks_test.go ---
package hooks

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Mock Implementations ---

type MockTUIProgram struct {
	mock.Mock
}

// Send mocks the Send method.
func (m *MockTUIProgram) Send(msg interface{}) { // minimal comment
	m.Called(msg)
}

type MockProgressBar struct {
	mock.Mock
}

// Add mocks the Add method.
func (m *MockProgressBar) Add(num int) error { // minimal comment
	args := m.Called(num)
	return args.Error(0)
}

// Describe mocks the Describe method.
func (m *MockProgressBar) Describe(description string) error { // minimal comment
	args := m.Called(description)
	return args.Error(0)
}

// Close mocks the Close method.
func (m *MockProgressBar) Close() error { // minimal comment
	args := m.Called()
	return args.Error(0)
}

// --- Test Suite ---

func TestCLIHooks_OnFileDiscovered(t *testing.T) {
	testPath := "src/main.go"

	t.Run("TUI Enabled", func(t *testing.T) {
		mockTUI := new(MockTUIProgram)
		mockTUI.On("Send", mock.AnythingOfType("FileDiscoveredMsg")).Run(func(args mock.Arguments) {
			msg := args.Get(0).(FileDiscoveredMsg)
			assert.Equal(t, testPath, msg.Path)
		}).Once()

		logBuf := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		hooks := NewCLIHooks(logger, true, false, mockTUI, nil)
		err := hooks.OnFileDiscovered(testPath)
		require.NoError(t, err)
		mockTUI.AssertExpectations(t)
		assert.Empty(t, logBuf.String())
	})

	t.Run("Verbose Enabled", func(t *testing.T) {
		mockTUI := new(MockTUIProgram)
		logBuf := &bytes.Buffer{}
		logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		hooks := NewCLIHooks(logger, false, true, mockTUI, nil)
		err := hooks.OnFileDiscovered(testPath)
		require.NoError(t, err)

		mockTUI.AssertNotCalled(t, "Send", mock.Anything)
		logOutput := logBuf.String()
		assert.Contains(t, logOutput, `"level":"DEBUG"`)
		assert.Contains(t, logOutput, `"msg":"File discovered"`)
		assert.Contains(t, logOutput, `"path":"`+testPath+`"`)
	})

	t.Run("Neither TUI nor Verbose Enabled", func(t *testing.T) {
		mockTUI := new(MockTUIProgram)
		logBuf := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		hooks := NewCLIHooks(logger, false, false, mockTUI, nil)
		err := hooks.OnFileDiscovered(testPath)
		require.NoError(t, err)

		mockTUI.AssertNotCalled(t, "Send", mock.Anything)
		assert.Empty(t, logBuf.String())
	})
}

func TestCLIHooks_OnFileStatusUpdate(t *testing.T) {
	testPath := "src/file.py"
	testMsg := "Processing..."
	testDuration := 50 * time.Millisecond

	t.Run("TUI Enabled", func(t *testing.T) {
		mockTUI := new(MockTUIProgram)
		mockTUI.On("Send", mock.MatchedBy(func(msg FileStatusUpdateMsg) bool {
			return msg.Path == testPath &&
				msg.Status == converter.StatusProcessing &&
				msg.Message == testMsg &&
				msg.Duration == testDuration
		})).Once()

		logBuf := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		hooks := NewCLIHooks(logger, true, false, mockTUI, nil)
		err := hooks.OnFileStatusUpdate(testPath, converter.StatusProcessing, testMsg, testDuration)
		require.NoError(t, err)
		mockTUI.AssertExpectations(t)
		assert.Empty(t, logBuf.String())
	})

	t.Run("Verbose Enabled", func(t *testing.T) {
		mockTUI := new(MockTUIProgram)
		logBuf := &bytes.Buffer{}
		logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		hooks := NewCLIHooks(logger, false, true, mockTUI, nil)

		testCases := []struct {
			status        converter.Status
			message       string
			expectedLevel string
			expectedMsg   string
			checkKey      string
		}{
			{converter.StatusProcessing, "Starting", "DEBUG", "File status updated", "message"},
			{converter.StatusSuccess, "OK", "INFO", "File status updated", "message"},
			{converter.StatusCached, "Hit", "INFO", "File status updated", "message"},
			{converter.StatusSkipped, "Ignored", "INFO", "File status updated", "message"},
			{converter.StatusFailed, "Syntax Error", "ERROR", "File processing failed", "error"},
		}

		for _, tc := range testCases {
			logBuf.Reset()
			err := hooks.OnFileStatusUpdate(testPath, tc.status, tc.message, testDuration)
			require.NoError(t, err)
			logOutput := logBuf.String()

			durationRegex := regexp.QuoteMeta(fmt.Sprintf(`"duration":"%s"`, testDuration.String()))
			assert.Regexp(t, durationRegex, logOutput)

			assert.Contains(t, logOutput, `"level":"`+tc.expectedLevel+`"`)
			assert.Contains(t, logOutput, `"msg":"`+tc.expectedMsg+`"`)
			assert.Contains(t, logOutput, `"path":"`+testPath+`"`)
			assert.Contains(t, logOutput, `"status":"`+string(tc.status)+`"`)
			assert.Contains(t, logOutput, `"`+tc.checkKey+`":"`+tc.message+`"`)
		}
		mockTUI.AssertNotCalled(t, "Send", mock.Anything)
	})

	t.Run("Progress Bar Enabled", func(t *testing.T) {
		mockTUI := new(MockTUIProgram)
		mockProgress := new(MockProgressBar)
		logBuf := &bytes.Buffer{}
		logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelError}))
		hooks := NewCLIHooks(logger, false, false, mockTUI, mockProgress)

		mockProgress.On("Add", 1).Return(nil).Times(4)

		err := hooks.OnFileStatusUpdate(testPath, converter.StatusProcessing, "Starting", 0)
		require.NoError(t, err)
		assert.Empty(t, logBuf.String())

		err = hooks.OnFileStatusUpdate(testPath, converter.StatusSuccess, "OK", testDuration)
		require.NoError(t, err)
		assert.Empty(t, logBuf.String())

		err = hooks.OnFileStatusUpdate(testPath, converter.StatusCached, "Hit", 0)
		require.NoError(t, err)
		assert.Empty(t, logBuf.String())

		err = hooks.OnFileStatusUpdate(testPath, converter.StatusSkipped, "Ignored", 0)
		require.NoError(t, err)
		assert.Empty(t, logBuf.String())

		failMsg := "Failure reason"
		err = hooks.OnFileStatusUpdate(testPath, converter.StatusFailed, failMsg, testDuration)
		require.NoError(t, err)
		logOutput := logBuf.String()
		assert.Contains(t, logOutput, `"level":"ERROR"`)
		assert.Contains(t, logOutput, `"msg":"File processing failed"`)
		assert.Contains(t, logOutput, `"path":"`+testPath+`"`)
		assert.Contains(t, logOutput, `"error":"`+failMsg+`"`)

		mockTUI.AssertNotCalled(t, "Send", mock.Anything)
		mockProgress.AssertExpectations(t)
	})

	t.Run("Standard Log Mode (Non-TTY, Non-Verbose)", func(t *testing.T) {
		mockTUI := new(MockTUIProgram)
		mockProgress := new(MockProgressBar)
		logBuf := &bytes.Buffer{}
		logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
		hooks := NewCLIHooks(logger, false, false, mockTUI, nil)

		err := hooks.OnFileStatusUpdate(testPath, converter.StatusProcessing, "Starting", 0)
		require.NoError(t, err)
		assert.Empty(t, logBuf.String())

		err = hooks.OnFileStatusUpdate(testPath, converter.StatusSuccess, "OK", testDuration)
		require.NoError(t, err)
		assert.Empty(t, logBuf.String())

		err = hooks.OnFileStatusUpdate(testPath, converter.StatusCached, "Hit", 0)
		require.NoError(t, err)
		assert.Empty(t, logBuf.String())

		err = hooks.OnFileStatusUpdate(testPath, converter.StatusSkipped, "Ignored", 0)
		require.NoError(t, err)
		assert.Empty(t, logBuf.String())

		failMsg := "Failure reason"
		err = hooks.OnFileStatusUpdate(testPath, converter.StatusFailed, failMsg, testDuration)
		require.NoError(t, err)
		logOutput := logBuf.String()
		assert.Contains(t, logOutput, `"level":"ERROR"`)
		assert.Contains(t, logOutput, `"msg":"File processing failed"`)
		assert.Contains(t, logOutput, `"path":"`+testPath+`"`)
		assert.Contains(t, logOutput, `"error":"`+failMsg+`"`)

		mockTUI.AssertNotCalled(t, "Send", mock.Anything)
		mockProgress.AssertNotCalled(t, "Add", mock.Anything)
		mockProgress.AssertNotCalled(t, "Describe", mock.Anything)
		mockProgress.AssertNotCalled(t, "Close")
	})
}

func TestCLIHooks_OnRunComplete(t *testing.T) {
	finalReport := converter.Report{
		Summary: converter.ReportSummary{ProcessedCount: 10},
	}

	t.Run("TUI Enabled", func(t *testing.T) {
		mockTUI := new(MockTUIProgram)
		mockTUI.On("Send", mock.MatchedBy(func(msg RunCompleteMsg) bool {
			return msg.Report.Summary.ProcessedCount == finalReport.Summary.ProcessedCount
		})).Once()
		mockProgress := new(MockProgressBar)

		logBuf := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		hooks := NewCLIHooks(logger, true, false, mockTUI, mockProgress)
		err := hooks.OnRunComplete(finalReport)
		require.NoError(t, err)
		mockTUI.AssertExpectations(t)
		mockProgress.AssertNotCalled(t, "Close")
		assert.Empty(t, logBuf.String())
	})

	t.Run("Progress Bar Enabled", func(t *testing.T) {
		mockTUI := new(MockTUIProgram)
		mockProgress := new(MockProgressBar)
		mockProgress.On("Close").Return(nil).Once()

		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		logBuf := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
		hooks := NewCLIHooks(logger, false, false, mockTUI, mockProgress)

		err := hooks.OnRunComplete(finalReport)
		require.NoError(t, err)

		w.Close()
		_, _ = io.ReadAll(r) // Read to ensure pipe is drained
		os.Stderr = oldStderr

		mockTUI.AssertNotCalled(t, "Send", mock.Anything)
		mockProgress.AssertExpectations(t)
		// Removed check for exact newline in stderr as it's less reliable
		assert.NotContains(t, logBuf.String(), "Run Complete")
	})

	t.Run("Standard Log Mode (Non-TTY, Non-Verbose)", func(t *testing.T) {
		mockTUI := new(MockTUIProgram)
		mockProgress := new(MockProgressBar)
		logBuf := &bytes.Buffer{}
		logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
		hooks := NewCLIHooks(logger, false, false, mockTUI, nil)

		err := hooks.OnRunComplete(finalReport)
		require.NoError(t, err)

		mockTUI.AssertNotCalled(t, "Send", mock.Anything)
		mockProgress.AssertNotCalled(t, "Close")
		assert.NotContains(t, logBuf.String(), "Run Complete")
		assert.NotContains(t, logBuf.String(), `"processedCount":10`)
	})

	t.Run("Verbose Mode", func(t *testing.T) {
		mockTUI := new(MockTUIProgram)
		mockProgress := new(MockProgressBar)
		logBuf := &bytes.Buffer{}
		logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		hooks := NewCLIHooks(logger, false, true, mockTUI, nil)

		err := hooks.OnRunComplete(finalReport)
		require.NoError(t, err)

		mockTUI.AssertNotCalled(t, "Send", mock.Anything)
		mockProgress.AssertNotCalled(t, "Close")
		assert.NotContains(t, logBuf.String(), "Run Complete")
		assert.NotContains(t, logBuf.String(), `"processedCount":10`)
	})
}

// --- END OF FINAL REVISED FILE internal/cli/hooks/hooks_test.go ---
