// --- START OF MODIFIED FILE pkg/converter/walker_test.go ---
package converter_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter"

	// Use the actual util package. Tests will rely on its correct implementation.
	_ "github.com/stackvity/stack-converter/pkg/util" // Import for side effects if needed, or directly if used. Walker uses it internally.
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test Setup & Helpers ---

// MockHooks for walker tests (base implementation)
type MockWalkerHooks struct {
	DiscoveredPaths []string
	UpdatedStatuses map[string]string // path -> status:message string
	RunCompleted    bool
	mu              sync.Mutex
}

func (m *MockWalkerHooks) OnFileDiscovered(path string) error { // Minimal comment
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DiscoveredPaths = append(m.DiscoveredPaths, filepath.ToSlash(path)) // Normalize path
	return nil
}
func (m *MockWalkerHooks) OnFileStatusUpdate(path string, status converter.Status, message string, duration time.Duration) error { // Minimal comment
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.UpdatedStatuses == nil {
		m.UpdatedStatuses = make(map[string]string)
	}
	m.UpdatedStatuses[filepath.ToSlash(path)] = fmt.Sprintf("%s:%s", status, message) // Normalize path
	return nil
}
func (m *MockWalkerHooks) OnRunComplete(report converter.Report) error { // Minimal comment
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RunCompleted = true
	return nil
}

// Helper to create test directory structure
func createTestDirStructure(t *testing.T, rootDir string, structure map[string]string) { // Minimal comment
	t.Helper()
	for path, content := range structure {
		nativePath := filepath.FromSlash(path) // Use FromSlash for creating
		fullPath := filepath.Join(rootDir, nativePath)
		dir := filepath.Dir(fullPath)
		err := os.MkdirAll(dir, 0755)
		require.NoError(t, err, "Failed to create dir %s", dir)
		if strings.HasSuffix(path, "/") || content == "" {
			// Check if it already exists from MkdirAll above or previous iteration
			if _, statErr := os.Stat(fullPath); os.IsNotExist(statErr) {
				err = os.Mkdir(fullPath, 0755) // Use Mkdir if creating the final dir
				require.NoError(t, err, "Failed to create dir %s", fullPath)
			} else if statErr != nil {
				require.NoError(t, statErr, "Failed to stat existing dir %s", fullPath) // Ensure no other stat error
			}
		} else {
			err = os.WriteFile(fullPath, []byte(content), 0644)
			require.NoError(t, err, "Failed to write file %s", fullPath)
		}
	}
}

// Helper to collect dispatched files from the channel
func collectDispatchedFiles(ctx context.Context, workerChan <-chan string) ([]string, error) { // Minimal comment
	var dispatched []string
	for {
		select {
		case path, ok := <-workerChan:
			if !ok {
				return dispatched, nil
			}
			dispatched = append(dispatched, path)
		case <-ctx.Done():
			return dispatched, ctx.Err()
		case <-time.After(5 * time.Second): // Increased timeout slightly for CI robustness
			return dispatched, errors.New("timeout waiting for dispatched files")
		}
	}
}

// REMOVED: setupMockGitignoreMatcher function - Tests will use the actual implementation.

// Helper to run walker and collect relative paths
func runWalkerAndCollectRelative(t *testing.T, opts *converter.Options) ([]string, *MockWalkerHooks) { // Minimal comment
	t.Helper()
	workerChan := make(chan string, 100)
	var wg sync.WaitGroup
	logBuf := &strings.Builder{}
	logLevel := slog.LevelDebug // Use Debug level for test logs
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: logLevel}))
	handler := logger.Handler()
	// Inject logger into options for walker's internal use
	opts.Logger = handler
	// Ensure EventHooks is not nil
	if opts.EventHooks == nil {
		opts.EventHooks = &MockWalkerHooks{} // Use the base mock if none provided
	}
	// Ensure InputPath is absolute for accurate relative path calculations inside walker
	absInput, err := filepath.Abs(opts.InputPath)
	require.NoError(t, err)
	opts.InputPath = absInput
	walker, err := converter.NewWalker(opts, workerChan, &wg, handler)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	walkErr := walker.StartWalk(ctx)
	// Allow context cancellation errors
	if walkErr != nil && !errors.Is(walkErr, context.Canceled) && !errors.Is(walkErr, context.DeadlineExceeded) {
		require.NoError(t, walkErr, "Walker StartWalk failed unexpectedly. Logs:\n%s", logBuf.String())
	}
	dispatchedFiles, err := collectDispatchedFiles(ctx, workerChan)
	// Allow context cancellation errors during collection as well
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && err.Error() != "timeout waiting for dispatched files" {
		require.NoError(t, err, "Collecting dispatched files failed unexpectedly. Logs:\n%s", logBuf.String())
	}
	relativeDispatched := make([]string, len(dispatchedFiles))
	for i, absP := range dispatchedFiles {
		rel, err := filepath.Rel(opts.InputPath, absP)
		require.NoError(t, err, "Failed to get relative path for %s", absP)
		relativeDispatched[i] = filepath.ToSlash(rel)
	}
	sort.Strings(relativeDispatched)
	t.Logf("Walker Test Logs:\n%s", logBuf.String()) // Print logs for debugging test failures

	// Return the actual hooks implementation used (could be the base mock or a specialized one)
	// Type assertion needed to return the specific mock type for inspection
	mockHooks, ok := opts.EventHooks.(*MockWalkerHooks)
	if !ok {
		// Handle the case where a different hook type might have been injected
		// For this test suite, we expect MockWalkerHooks or a derivative.
		// If it was the HookWithError used in TestWalker_HookErrorHandling, cast to that.
		if withErrHook, okWithErr := opts.EventHooks.(*HookWithError); okWithErr {
			mockHooks = &withErrHook.MockWalkerHooks // Return the embedded base mock
		} else {
			// If it's neither, something is wrong with the test setup
			t.Fatalf("Unexpected type for EventHooks: %T", opts.EventHooks)
			return nil, nil // Should not be reached
		}
	}
	return relativeDispatched, mockHooks
}

// --- Test Cases ---

func TestWalker_BasicWalk(t *testing.T) { // Minimal comment
	// REMOVED: setupMockGitignoreMatcher() call
	rootDir := t.TempDir()
	createTestDirStructure(t, rootDir, map[string]string{
		"file1.go":         "package main",
		"subdir/file2.txt": "hello",
		"subdir/empty/":    "",
	})
	opts := &converter.Options{
		InputPath:      rootDir,
		IgnorePatterns: []string{},
		GitDiffMode:    converter.GitDiffModeNone,
	}
	relativeDispatched, mockHooks := runWalkerAndCollectRelative(t, opts)
	expectedFiles := []string{"file1.go", "subdir/file2.txt"}
	sort.Strings(expectedFiles) // Ensure expected is sorted for comparison
	assert.Equal(t, expectedFiles, relativeDispatched)
	// Discovery order isn't guaranteed, use ElementsMatch
	assert.ElementsMatch(t, []string{"file1.go", "subdir", "subdir/file2.txt", "subdir/empty"}, mockHooks.DiscoveredPaths)
	assert.Empty(t, mockHooks.UpdatedStatuses)
}

func TestWalker_IgnoreFile(t *testing.T) { // Minimal comment
	// REMOVED: setupMockGitignoreMatcher() call
	rootDir := t.TempDir()
	err := os.WriteFile(filepath.Join(rootDir, ".stackconverterignore"), []byte("*.log\n!important.log\nbuild/\n"), 0644)
	require.NoError(t, err)
	createTestDirStructure(t, rootDir, map[string]string{
		"file1.go":           "package main",
		"data.log":           "log data",
		"important.log":      "important log",
		"build/artifact.bin": "binary",
		"src/main.go":        "src main",
	})
	opts := &converter.Options{
		InputPath:      rootDir,
		IgnorePatterns: []string{},
		GitDiffMode:    converter.GitDiffModeNone,
	}
	relativeDispatched, mockHooks := runWalkerAndCollectRelative(t, opts)
	expectedFiles := []string{"file1.go", "important.log", "src/main.go"}
	sort.Strings(expectedFiles) // Ensure expected is sorted
	assert.Equal(t, expectedFiles, relativeDispatched)
	expectedDiscoveries := []string{
		".stackconverterignore",
		"file1.go", "data.log", "important.log", "build", "build/artifact.bin", "src", "src/main.go",
	}
	// Normalizing paths in mock hooks already
	assert.ElementsMatch(t, expectedDiscoveries, mockHooks.DiscoveredPaths)
	assert.Contains(t, mockHooks.UpdatedStatuses, "data.log")
	assert.Contains(t, mockHooks.UpdatedStatuses["data.log"], "skipped:Ignored by pattern: *.log")
	assert.Contains(t, mockHooks.UpdatedStatuses, "build")
	assert.Contains(t, mockHooks.UpdatedStatuses["build"], "skipped:Ignored by pattern: build/")
	assert.NotContains(t, mockHooks.UpdatedStatuses, "build/artifact.bin") // Because parent dir was skipped
}

func TestWalker_IgnoreFileNestedBase(t *testing.T) { // Minimal comment
	// REMOVED: setupMockGitignoreMatcher() call
	rootDir := t.TempDir()
	subDirName := "subdir"
	subDir := filepath.Join(rootDir, subDirName)
	subIgnoreFile := filepath.Join(subDir, ".stackconverterignore")
	require.NoError(t, os.Mkdir(subDir, 0755))
	err := os.WriteFile(subIgnoreFile, []byte("local.tmp\n*.log\n/root.log\n"), 0644) // Note: /root.log is relative to subdir!
	require.NoError(t, err)
	createTestDirStructure(t, rootDir, map[string]string{
		"root.log":                   "root log",         // Should NOT be ignored
		subDirName + "/file.go":      "go",               // Should NOT be ignored
		subDirName + "/local.tmp":    "temp",             // Should be ignored by subdir ignore
		subDirName + "/subsub/a.log": "subsub log",       // Should be ignored by subdir ignore
		subDirName + "/root.log":     "sub dir root log", // Should be ignored by subdir ignore's /root.log
	})
	opts := &converter.Options{
		InputPath:      rootDir, // Start walk from root
		IgnorePatterns: []string{},
		GitDiffMode:    converter.GitDiffModeNone,
	}
	relativeDispatched, mockHooks := runWalkerAndCollectRelative(t, opts)
	// Expect only root.log (from root) and subdir/file.go
	expectedFiles := []string{"root.log", "subdir/file.go"}
	sort.Strings(expectedFiles)
	assert.Equal(t, expectedFiles, relativeDispatched)
	// Check skips relative to rootDir
	assert.Contains(t, mockHooks.UpdatedStatuses, "subdir/local.tmp")
	assert.Contains(t, mockHooks.UpdatedStatuses["subdir/local.tmp"], "skipped:Ignored by pattern: local.tmp")
	assert.Contains(t, mockHooks.UpdatedStatuses, "subdir/subsub/a.log")
	assert.Contains(t, mockHooks.UpdatedStatuses["subdir/subsub/a.log"], "skipped:Ignored by pattern: *.log")
	assert.Contains(t, mockHooks.UpdatedStatuses, "subdir/root.log")
	assert.Contains(t, mockHooks.UpdatedStatuses["subdir/root.log"], "skipped:Ignored by pattern: /root.log")
	assert.NotContains(t, mockHooks.UpdatedStatuses, "root.log") // Root file should not be skipped
}

func TestWalker_ConfigIgnores(t *testing.T) { // Minimal comment
	// REMOVED: setupMockGitignoreMatcher() call
	rootDir := t.TempDir()
	createTestDirStructure(t, rootDir, map[string]string{
		"file1.go":           "package main",
		"temp/file.tmp":      "temp file",
		"node_modules/lib/a": "lib a",
		"main.log":           "log main",
	})
	opts := &converter.Options{
		InputPath:      rootDir,
		IgnorePatterns: []string{"*.log", "temp/", "node_modules/"}, // Ignored from config
		GitDiffMode:    converter.GitDiffModeNone,
	}
	relativeDispatched, mockHooks := runWalkerAndCollectRelative(t, opts)
	expectedFiles := []string{"file1.go"}
	sort.Strings(expectedFiles)
	assert.Equal(t, expectedFiles, relativeDispatched)
	assert.Contains(t, mockHooks.UpdatedStatuses, "main.log")
	assert.Contains(t, mockHooks.UpdatedStatuses["main.log"], "*.log")
	assert.Contains(t, mockHooks.UpdatedStatuses, "temp")
	assert.Contains(t, mockHooks.UpdatedStatuses["temp"], "temp/")
	assert.Contains(t, mockHooks.UpdatedStatuses, "node_modules")
	assert.Contains(t, mockHooks.UpdatedStatuses["node_modules"], "node_modules/")
}

func TestWalker_CombinedIgnores(t *testing.T) { // Minimal comment
	// REMOVED: setupMockGitignoreMatcher() call
	rootDir := t.TempDir()
	err := os.WriteFile(filepath.Join(rootDir, ".stackconverterignore"), []byte("*.log\n!keep.log\ndist/\n"), 0644)
	require.NoError(t, err)
	createTestDirStructure(t, rootDir, map[string]string{
		"keep.log":      "keep me", // Should be kept due to !keep.log
		"skip.log":      "skip me", // Should be skipped by *.log
		"dist/file.out": "output",  // Should be skipped by dist/
		"src/main.go":   "main",    // Should be kept
		"config.yaml":   "config",  // Should be skipped by config ignore
	})
	opts := &converter.Options{
		InputPath:      rootDir,
		IgnorePatterns: []string{"config.yaml", "!dist/important"}, // Config ignore + un-ignore (un-ignore has no effect here as dist/ takes precedence earlier)
		GitDiffMode:    converter.GitDiffModeNone,
	}
	relativeDispatched, mockHooks := runWalkerAndCollectRelative(t, opts)
	expectedFiles := []string{"keep.log", "src/main.go"}
	sort.Strings(expectedFiles)
	assert.Equal(t, expectedFiles, relativeDispatched)
	assert.Contains(t, mockHooks.UpdatedStatuses, "skip.log")
	assert.Contains(t, mockHooks.UpdatedStatuses["skip.log"], "*.log")
	assert.Contains(t, mockHooks.UpdatedStatuses, "dist")
	assert.Contains(t, mockHooks.UpdatedStatuses["dist"], "dist/")
	assert.Contains(t, mockHooks.UpdatedStatuses, "config.yaml")
	assert.Contains(t, mockHooks.UpdatedStatuses["config.yaml"], "config.yaml")
}

func TestWalker_GitDiffFilter(t *testing.T) { // Minimal comment
	// REMOVED: setupMockGitignoreMatcher() call
	rootDir := t.TempDir()
	createTestDirStructure(t, rootDir, map[string]string{
		"fileA.go":        "content a",
		"fileB.txt":       "content b",
		"subdir/fileC.go": "content c",
	})
	gitDiffMap := make(map[string]struct{})
	gitDiffMap["fileA.go"] = struct{}{}
	gitDiffMap["subdir/fileC.go"] = struct{}{} // Assume these are relative to rootDir, using '/'
	opts := &converter.Options{
		InputPath:       rootDir,
		IgnorePatterns:  []string{},
		GitDiffMode:     converter.GitDiffModeDiffOnly,
		GitChangedFiles: gitDiffMap, // Pass the prepared map
	}
	relativeDispatched, mockHooks := runWalkerAndCollectRelative(t, opts)
	expectedFiles := []string{"fileA.go", "subdir/fileC.go"}
	sort.Strings(expectedFiles)
	assert.Equal(t, expectedFiles, relativeDispatched)
	assert.Contains(t, mockHooks.UpdatedStatuses, "fileB.txt")
	assert.Contains(t, mockHooks.UpdatedStatuses["fileB.txt"], "skipped:Excluded by Git diff mode diffOnly")
}

func TestWalker_ContextCancellation(t *testing.T) { // Minimal comment
	// REMOVED: setupMockGitignoreMatcher() call
	rootDir := t.TempDir()
	structure := make(map[string]string)
	for i := 0; i < 100; i++ {
		structure[fmt.Sprintf("file%03d.txt", i)] = fmt.Sprintf("content %d", i)
	}
	createTestDirStructure(t, rootDir, structure)
	opts := &converter.Options{
		InputPath:      rootDir,
		IgnorePatterns: []string{},
		GitDiffMode:    converter.GitDiffModeNone,
	}
	workerChan := make(chan string, 5)
	var wg sync.WaitGroup
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	handler := logger.Handler()
	opts.Logger = handler
	opts.EventHooks = &MockWalkerHooks{}
	walker, err := converter.NewWalker(opts, workerChan, &wg, handler)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	walkErr := walker.StartWalk(ctx)
	require.Error(t, walkErr)
	assert.True(t, errors.Is(walkErr, context.DeadlineExceeded) || errors.Is(walkErr, context.Canceled), "Expected context cancellation error")
	collectCtx, collectCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer collectCancel()
	dispatchedFiles, _ := collectDispatchedFiles(collectCtx, workerChan)
	assert.Less(t, len(dispatchedFiles), 100, "Expected fewer than 100 files due to cancellation")
	assert.Contains(t, logBuf.String(), "Directory walk cancelled")
}

func TestWalker_PermissionError(t *testing.T) { // Minimal comment
	if runtime.GOOS == "windows" {
		t.Skip("Skipping permission denied test on Windows")
	}
	// REMOVED: setupMockGitignoreMatcher() call
	rootDir := t.TempDir()
	unreadableDirName := "unreadable_dir"
	unreadableDirPath := filepath.Join(rootDir, unreadableDirName)
	readableFile := "readable_file.txt"
	hiddenFile := filepath.Join(unreadableDirName, "hidden.txt")
	createTestDirStructure(t, rootDir, map[string]string{
		readableFile:                              "content",
		filepath.ToSlash(hiddenFile):              "secret",
		filepath.ToSlash(unreadableDirName + "/"): "",
	})
	err := os.Chmod(unreadableDirPath, 0000)
	require.NoError(t, err)
	defer os.Chmod(unreadableDirPath, 0755)
	opts := &converter.Options{
		InputPath:      rootDir,
		IgnorePatterns: []string{},
		GitDiffMode:    converter.GitDiffModeNone,
	}
	relativeDispatched, mockHooks := runWalkerAndCollectRelative(t, opts)
	expectedFiles := []string{readableFile}
	assert.Equal(t, expectedFiles, relativeDispatched)
	// Check logs via mock logger is difficult, check hook discoveries instead
	assert.Contains(t, mockHooks.DiscoveredPaths, filepath.ToSlash(unreadableDirName))
	assert.NotContains(t, mockHooks.DiscoveredPaths, filepath.ToSlash(hiddenFile))
}

// FIX: Define a specific hook implementation for testing errors
type HookWithError struct {
	MockWalkerHooks // Embed the base mock to inherit its methods
	errToReturn     error
	errPathSuffix   string
}

// Override OnFileDiscovered to return an error for specific paths
func (h *HookWithError) OnFileDiscovered(path string) error {
	// Call the embedded method first to record the discovery
	_ = h.MockWalkerHooks.OnFileDiscovered(path)
	// Return the predefined error if the path matches the suffix
	if h.errPathSuffix != "" && strings.HasSuffix(path, h.errPathSuffix) {
		return h.errToReturn
	}
	return nil
}

func TestWalker_HookErrorHandling(t *testing.T) { // Minimal comment
	// REMOVED: setupMockGitignoreMatcher() call
	rootDir := t.TempDir()
	createTestDirStructure(t, rootDir, map[string]string{
		"file1.go":  "package main",
		"file2.txt": "hello",
	})
	hookErr := errors.New("mock hook discovery error")
	// FIX: Instantiate the specialized hook implementation
	mockHooksWithErr := &HookWithError{
		errToReturn:   hookErr,
		errPathSuffix: "file1.go",
	}

	opts := &converter.Options{
		InputPath:      rootDir,
		IgnorePatterns: []string{},
		EventHooks:     mockHooksWithErr, // Pass the specialized mock hook implementation
		GitDiffMode:    converter.GitDiffModeNone,
	}
	logBuf := &strings.Builder{} // Capture logs
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	handler := logger.Handler()
	opts.Logger = handler // Inject logger

	// FIX: Retrieve the base mock from the specialized one for assertions
	relativeDispatched, baseMockHooks := runWalkerAndCollectRelative(t, opts)
	expectedFiles := []string{"file1.go", "file2.txt"}
	sort.Strings(expectedFiles)
	assert.Equal(t, expectedFiles, relativeDispatched) // Both files dispatched despite hook error

	// Assert calls on the base mock embedded within HookWithError
	assert.Contains(t, baseMockHooks.DiscoveredPaths, "file1.go")
	assert.Contains(t, baseMockHooks.DiscoveredPaths, "file2.txt")

	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Event hook OnFileDiscovered failed")
	assert.Contains(t, logOutput, "file1.go")
	assert.Contains(t, logOutput, hookErr.Error())
}

func TestWalker_SymlinkHandling(t *testing.T) { // Minimal comment
	if runtime.GOOS == "windows" {
		t.Skip("Skipping symlink test on Windows (requires special permissions or handling)")
	}
	// REMOVED: setupMockGitignoreMatcher() call
	rootDir := t.TempDir()
	targetFile := filepath.Join(rootDir, "target.txt")
	linkToFile := filepath.Join(rootDir, "link_to_file.txt")
	targetDir := filepath.Join(rootDir, "target_dir")
	linkToDir := filepath.Join(rootDir, "link_to_dir")
	require.NoError(t, os.WriteFile(targetFile, []byte("target content"), 0644))
	require.NoError(t, os.Mkdir(targetDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "file_in_dir.txt"), []byte("content"), 0644))
	require.NoError(t, os.Symlink(targetFile, linkToFile), "Failed to create file symlink")
	require.NoError(t, os.Symlink(targetDir, linkToDir), "Failed to create directory symlink")
	opts := &converter.Options{
		InputPath:      rootDir,
		IgnorePatterns: []string{},
		GitDiffMode:    converter.GitDiffModeNone,
	}
	relativeDispatched, mockHooks := runWalkerAndCollectRelative(t, opts)
	expectedFiles := []string{"target.txt", "target_dir/file_in_dir.txt"}
	sort.Strings(expectedFiles)
	assert.Equal(t, expectedFiles, relativeDispatched)
	expectedDiscoveries := []string{
		"target.txt", "target_dir", "target_dir/file_in_dir.txt",
		"link_to_file.txt", "link_to_dir",
	}
	assert.ElementsMatch(t, expectedDiscoveries, mockHooks.DiscoveredPaths)
	assert.NotContains(t, mockHooks.UpdatedStatuses, "link_to_file.txt")
	assert.NotContains(t, mockHooks.UpdatedStatuses, "link_to_dir")
}

func TestWalker_DispatchBlockingLog(t *testing.T) { // Minimal comment
	// REMOVED: setupMockGitignoreMatcher() call
	rootDir := t.TempDir()
	for i := 0; i < 10; i++ {
		createTestDirStructure(t, rootDir, map[string]string{fmt.Sprintf("file%d.go", i): "package main"})
	}
	warnThreshold := 50 * time.Millisecond // Use a shorter threshold for testing
	opts := &converter.Options{
		InputPath:             rootDir,
		IgnorePatterns:        []string{},
		EventHooks:            &MockWalkerHooks{},
		GitDiffMode:           converter.GitDiffModeNone,
		DispatchWarnThreshold: warnThreshold, // Set configurable threshold
	}
	workerChan := make(chan string, 2) // Small buffer
	var wg sync.WaitGroup
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	handler := logger.Handler()
	walker, err := converter.NewWalker(opts, workerChan, &wg, handler)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	var walkErr error
	var walkWg sync.WaitGroup
	walkWg.Add(1)
	go func() {
		defer walkWg.Done()
		walkErr = walker.StartWalk(ctx)
	}()
	// Wait slightly longer than the threshold
	time.Sleep(warnThreshold + 50*time.Millisecond)
	cancel() // Cancel the walk
	walkWg.Wait()
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "Worker channel dispatch blocked", "Expected blocking warning log")
	assert.Contains(t, logOutput, fmt.Sprintf("threshold=%s", warnThreshold)) // Verify threshold in log
	require.Error(t, walkErr)
	assert.True(t, errors.Is(walkErr, context.Canceled), "Expected walk to be cancelled")
}

// --- END OF MODIFIED FILE pkg/converter/walker_test.go ---
