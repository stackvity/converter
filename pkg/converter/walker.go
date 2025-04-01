// --- START OF MODIFIED FILE pkg/converter/walker.go ---
package converter

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	// FIX: Change import path from internal/util to pkg/util
	"github.com/stackvity/stack-converter/pkg/util" // Use the new location
)

// Walker is responsible for traversing the input directory, applying ignore rules,
// and dispatching eligible file paths to the worker pool.
type Walker struct {
	opts                 *Options
	workerChan           chan<- string
	wg                   *sync.WaitGroup
	hooks                Hooks
	logger               *slog.Logger
	ignoreMatcher        *ignoreMatcher
	gitDiffMap           map[string]struct{}
	dispatchWarnDuration time.Duration // Configurable threshold
}

// NewWalker creates a new Walker instance.
func NewWalker(opts *Options, workerChan chan<- string, wg *sync.WaitGroup, loggerHandler slog.Handler) (*Walker, error) { // Minimal comment
	logger := slog.New(loggerHandler).With(slog.String("component", "walker"))
	ignoreMatcher, err := newIgnoreMatcher(opts.InputPath, opts.IgnorePatterns, logger)
	if err != nil {
		logger.Error("Failed to initialize ignore pattern matcher", slog.String("error", err.Error()))
		return nil, fmt.Errorf("failed to initialize ignore patterns: %w", err)
	}
	logger.Debug("Ignore patterns loaded", slog.Int("count", ignoreMatcher.patternCount()))
	gitDiffMap := make(map[string]struct{})
	if opts.GitDiffMode == GitDiffModeDiffOnly || opts.GitDiffMode == GitDiffModeSince {
		// This assumes opts.GitChangedFiles is populated earlier (e.g., during config loading)
		if opts.GitChangedFiles == nil {
			logger.Warn("Git diff mode active but no changed files map provided via Options.GitChangedFiles")
		} else {
			logger.Debug("Git diff mode active, using provided filter map",
				slog.String("mode", string(opts.GitDiffMode)),
				slog.Int("files_in_diff", len(opts.GitChangedFiles)))
			gitDiffMap = opts.GitChangedFiles
		}
	}
	// Use configured dispatch warning threshold, or default if not set/invalid
	dispatchWarnDuration := opts.DispatchWarnThreshold
	if dispatchWarnDuration <= 0 {
		dispatchWarnDuration = 1 * time.Second // Default if not configured
	}
	return &Walker{
		opts:                 opts,
		workerChan:           workerChan,
		wg:                   wg,
		hooks:                opts.EventHooks,
		logger:               logger,
		ignoreMatcher:        ignoreMatcher,
		gitDiffMap:           gitDiffMap,
		dispatchWarnDuration: dispatchWarnDuration,
	}, nil
}

// StartWalk begins the directory traversal process.
func (w *Walker) StartWalk(ctx context.Context) error { // Minimal comment
	w.logger.Info("Starting directory walk", slog.String("path", w.opts.InputPath))
	walkErr := filepath.WalkDir(w.opts.InputPath, w.walkFunc(ctx))
	close(w.workerChan)
	w.logger.Debug("Worker channel closed")
	if walkErr != nil {
		if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
			w.logger.Info("Directory walk cancelled", slog.String("reason", walkErr.Error()))
			return walkErr
		}
		w.logger.Error("Directory walk encountered an error during traversal", slog.String("error", walkErr.Error()))
		return fmt.Errorf("directory walk failed: %w", walkErr)
	}
	w.logger.Info("Directory walk completed")
	return nil
}

// walkFunc returns the WalkDirFunc used by filepath.WalkDir.
func (w *Walker) walkFunc(ctx context.Context) fs.WalkDirFunc { // Minimal comment
	isGitDiffActive := w.opts.GitDiffMode == GitDiffModeDiffOnly || w.opts.GitDiffMode == GitDiffModeSince
	return func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			w.logger.Warn("Error accessing path during walk", slog.String("path", path), slog.String("error", err.Error()))
			if path == w.opts.InputPath && os.IsPermission(err) {
				return fmt.Errorf("permission denied reading input directory %q: %w", path, err)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		// Skip Symbolic Links
		if d.Type()&fs.ModeSymlink != 0 {
			w.logger.Debug("Skipping symbolic link", slog.String("path", path))
			return nil
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			w.logger.Warn("Could not get absolute path", slog.String("path", path), slog.String("error", err.Error()))
			return nil
		}
		relativePath, err := filepath.Rel(w.opts.InputPath, absPath)
		if err != nil {
			w.logger.Warn("Could not calculate relative path", slog.String("path", absPath), slog.String("input", w.opts.InputPath), slog.String("error", err.Error()))
			return nil
		}
		relativePath = filepath.ToSlash(relativePath)
		if relativePath == "." {
			return nil // Skip root
		}
		// Trigger Discovery Hook
		if hookErr := w.hooks.OnFileDiscovered(relativePath); hookErr != nil {
			w.logger.Warn("Event hook OnFileDiscovered failed", slog.String("path", relativePath), slog.String("error", hookErr.Error()))
		}
		isDir := d.IsDir()
		if w.ignoreMatcher.Match(relativePath, isDir) {
			matchedPattern := w.ignoreMatcher.LastMatchPattern(relativePath, isDir)
			w.logger.Debug("Path ignored", slog.String("path", relativePath), slog.Bool("isDir", isDir), slog.String("pattern", matchedPattern))
			statusMsg := fmt.Sprintf("Ignored by pattern: %s", matchedPattern)
			if hookErr := w.hooks.OnFileStatusUpdate(relativePath, StatusSkipped, statusMsg, 0); hookErr != nil {
				w.logger.Warn("Event hook OnFileStatusUpdate (Ignored) failed", slog.String("path", relativePath), slog.String("error", hookErr.Error()))
			}
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}
		if isDir {
			return nil // Continue walking
		}
		// Apply Git Diff Filter
		if isGitDiffActive {
			lookupPath := relativePath
			if _, found := w.gitDiffMap[lookupPath]; !found {
				w.logger.Debug("Path excluded by Git diff", slog.String("path", relativePath))
				statusMsg := fmt.Sprintf("Excluded by Git diff mode %s", w.opts.GitDiffMode)
				if hookErr := w.hooks.OnFileStatusUpdate(relativePath, StatusSkipped, statusMsg, 0); hookErr != nil {
					w.logger.Warn("Event hook OnFileStatusUpdate (Git Skipped) failed", slog.String("path", relativePath), slog.String("error", hookErr.Error()))
				}
				return nil
			}
			w.logger.Debug("Path included by Git diff", slog.String("path", relativePath))
		}
		// Dispatch eligible file path
		w.logger.Debug("Dispatching file to worker channel", slog.String("path", relativePath))
		timer := time.NewTimer(w.dispatchWarnDuration)
		defer timer.Stop() // Ensure timer is stopped if dispatch is quick
		select {
		case w.workerChan <- absPath: // Sent successfully
		case <-timer.C: // Timer expired, dispatch is blocked
			w.logger.Warn("Worker channel dispatch blocked, workers might be busy or pool too small", slog.String("path", relativePath), slog.Duration("threshold", w.dispatchWarnDuration))
			// Attempt to send again, blocking until success or cancellation
			select {
			case w.workerChan <- absPath:
			case <-ctx.Done():
				return ctx.Err()
			}
		case <-ctx.Done(): // Context cancelled while initially trying to send
			return ctx.Err()
		}
		return nil
	}
}

// --- ignoreMatcher ---

type ignoreMatcher struct {
	patterns []ignorePattern
	basePath string // Absolute path to the input directory
	logger   *slog.Logger
}

type ignorePattern struct {
	pattern     string // Cleaned pattern string for matching (using '/' separators)
	origPattern string // Original pattern string for reporting
	negated     bool
	isDirOnly   bool
	isRooted    bool   // Pattern started with '/' relative to its base
	baseAbsPath string // Absolute path of the dir containing the defining ignore file or the input path
}

// newIgnoreMatcher creates an ignoreMatcher by loading patterns.
func newIgnoreMatcher(inputPath string, configPatterns []string, logger *slog.Logger) (*ignoreMatcher, error) { // Minimal comment
	absInputPath, err := filepath.Abs(inputPath)
	if err != nil {
		return nil, fmt.Errorf("could not get absolute path for input: %w", err)
	}
	matcher := &ignoreMatcher{
		patterns: make([]ignorePattern, 0),
		basePath: absInputPath,
		logger:   logger.With(slog.String("component", "ignoreMatcher")),
	}
	ignoreFilePath, err := findIgnoreFile(absInputPath)
	if err != nil {
		matcher.logger.Warn("Error searching for .stackconverterignore", slog.String("error", err.Error()))
	}
	if ignoreFilePath != "" {
		matcher.logger.Debug("Loading ignore patterns from file", slog.String("path", ignoreFilePath))
		filePatterns, err := loadPatternsFromFile(ignoreFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to load ignore file %s: %w", ignoreFilePath, err)
		}
		ignoreFileBaseDir := filepath.Dir(ignoreFilePath)
		matcher.addPatterns(filePatterns, ignoreFileBaseDir)
		matcher.logger.Debug("Loaded patterns from ignore file", slog.Int("count", len(filePatterns)))
	} else {
		matcher.logger.Debug("No .stackconverterignore file found walking up from input path")
	}
	matcher.logger.Debug("Adding ignore patterns from config/flags", slog.Int("count", len(configPatterns)))
	matcher.addPatterns(configPatterns, absInputPath) // Patterns from config/flags are relative to inputPath
	matcher.logger.Debug("Total processed ignore patterns", slog.Int("count", len(matcher.patterns)))
	return matcher, nil
}

// findIgnoreFile walks up from startPath looking for .stackconverterignore.
func findIgnoreFile(absStartPath string) (string, error) { // Minimal comment
	currentPath := absStartPath
	for {
		potentialPath := filepath.Join(currentPath, ".stackconverterignore")
		if _, err := os.Stat(potentialPath); err == nil {
			return potentialPath, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("error checking for ignore file at %s: %w", potentialPath, err)
		}
		parent := filepath.Dir(currentPath)
		if parent == currentPath || parent == "" {
			break
		}
		currentPath = parent
	}
	return "", nil
}

// loadPatternsFromFile reads an ignore file and returns lines.
func loadPatternsFromFile(filePath string) ([]string, error) { // Minimal comment
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open ignore file %s: %w", filePath, err)
	}
	defer file.Close()
	var patterns []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			patterns = append(patterns, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read ignore file %s: %w", filePath, err)
	}
	return patterns, nil
}

// addPatterns processes raw string patterns into ignorePattern structs.
func (m *ignoreMatcher) addPatterns(rawPatterns []string, baseAbsPath string) { // Minimal comment
	for _, rawPattern := range rawPatterns {
		p := ignorePattern{origPattern: rawPattern, baseAbsPath: baseAbsPath}
		trimmedPattern := rawPattern
		if strings.HasPrefix(trimmedPattern, "!") {
			p.negated = true
			trimmedPattern = trimmedPattern[1:]
		}
		trimmedPattern = strings.TrimSpace(trimmedPattern)
		if strings.HasPrefix(trimmedPattern, "/") {
			p.isRooted = true
			trimmedPattern = strings.TrimPrefix(trimmedPattern, "/")
		}
		if strings.HasSuffix(trimmedPattern, "/") {
			p.isDirOnly = true
			trimmedPattern = strings.TrimSuffix(trimmedPattern, "/")
		}
		p.pattern = filepath.ToSlash(trimmedPattern)
		if p.pattern == "" {
			continue
		}
		m.patterns = append(m.patterns, p)
	}
}

// Match checks if a given path (relative to the Walker's input base path) matches any ignore patterns.
func (m *ignoreMatcher) Match(relativePath string, isDir bool) bool { // Minimal comment
	lastMatchResult := false
	for _, p := range m.patterns {
		// Pass all necessary info to the utility function
		// FIX: Ensure util points to the pkg/util package
		if util.MatchesGitignore(p.pattern, p.baseAbsPath, m.basePath, relativePath, p.isRooted) {
			if p.isDirOnly && !isDir {
				continue
			}
			lastMatchResult = !p.negated
		}
	}
	return lastMatchResult
}

// LastMatchPattern returns the original pattern string that determined the final match decision.
func (m *ignoreMatcher) LastMatchPattern(relativePath string, isDir bool) string { // Minimal comment
	lastPattern := ""
	matched := false
	lastMatchResult := false
	for _, p := range m.patterns {
		// FIX: Ensure util points to the pkg/util package
		if util.MatchesGitignore(p.pattern, p.baseAbsPath, m.basePath, relativePath, p.isRooted) {
			if p.isDirOnly && !isDir {
				continue
			}
			matched = true
			lastPattern = p.origPattern
			lastMatchResult = !p.negated
		}
	}
	if matched && lastMatchResult {
		return lastPattern
	}
	return ""
}

// patternCount returns the number of processed patterns.
func (m *ignoreMatcher) patternCount() int { // Minimal comment
	return len(m.patterns)
}

// --- END OF MODIFIED FILE pkg/converter/walker.go ---
