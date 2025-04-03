// --- START OF FINAL REVISED FILE pkg/converter/walker.go ---
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

	// Import the util package from its location within pkg/
	"github.com/stackvity/stack-converter/pkg/util"
)

// Walker is responsible for traversing the input directory, applying ignore rules,
// and dispatching eligible file paths to the worker pool.
// **MODIFIED:** Added ignoreMatcher, gitDiffMap, dispatchWarnDuration fields.
type Walker struct {
	opts                 *Options
	workerChan           chan<- string
	wg                   *sync.WaitGroup // WaitGroup pointer for signaling completion
	hooks                Hooks
	logger               *slog.Logger
	ignoreMatcher        *ignoreMatcher
	gitDiffMap           map[string]struct{}
	dispatchWarnDuration time.Duration // Configurable threshold
}

// NewWalker creates a new Walker instance.
// **MODIFIED:** Implemented ignore matcher and Git diff map initialization.
func NewWalker(opts *Options, workerChan chan<- string, wg *sync.WaitGroup, loggerHandler slog.Handler) (*Walker, error) {
	if loggerHandler == nil {
		loggerHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	logger := slog.New(loggerHandler).With(slog.String("component", "walker"))

	// --- Initialize Ignore Matcher ---
	ignoreMatcher, err := newIgnoreMatcher(opts.InputPath, opts.IgnorePatterns, logger)
	if err != nil {
		logger.Error("Failed to initialize ignore pattern matcher", slog.String("error", err.Error()))
		// Wrap config validation error for consistent error checking
		return nil, fmt.Errorf("%w: failed to initialize ignore patterns: %w", ErrConfigValidation, err)
	}
	logger.Debug("Ignore patterns loaded", slog.Int("count", ignoreMatcher.patternCount()))

	// --- Initialize Git Diff Map ---
	gitDiffMap := make(map[string]struct{})
	if opts.GitDiffMode == GitDiffModeDiffOnly || opts.GitDiffMode == GitDiffModeSince {
		// Assume opts.GitChangedFiles is populated earlier (e.g., during config loading)
		if opts.GitChangedFiles == nil {
			// This should ideally be caught during earlier config validation if mode requires it.
			logger.Warn("Git diff mode active but no changed files map provided via Options.GitChangedFiles. All files will be processed (ignoring Git diff).")
			// Proceed without filtering, but log the inconsistency.
		} else {
			logger.Debug("Git diff mode active, using provided filter map",
				slog.String("mode", string(opts.GitDiffMode)),
				slog.Int("files_in_diff", len(opts.GitChangedFiles)))
			gitDiffMap = opts.GitChangedFiles // Use the map provided in Options
		}
	}

	// --- Dispatch Warning Threshold ---
	// Use configured dispatch warning threshold, or default if not set/invalid
	dispatchWarnDuration := opts.DispatchWarnThreshold
	if dispatchWarnDuration <= 0 {
		dispatchWarnDuration = 1 * time.Second // Sensible default
		logger.Debug("DispatchWarnThreshold not set or invalid, using default", "threshold", dispatchWarnDuration)
	} else {
		logger.Debug("Using configured DispatchWarnThreshold", "threshold", dispatchWarnDuration)
	}

	// --- Return Walker Instance ---
	return &Walker{
		opts:                 opts,
		workerChan:           workerChan,
		wg:                   wg, // Store the waitgroup
		hooks:                opts.EventHooks,
		logger:               logger,
		ignoreMatcher:        ignoreMatcher,
		gitDiffMap:           gitDiffMap,
		dispatchWarnDuration: dispatchWarnDuration,
	}, nil
}

// StartWalk begins the directory traversal process.
// **MODIFIED:** Implemented closing workerChan in defer, improved error handling.
func (w *Walker) StartWalk(ctx context.Context) error {
	w.logger.Info("Starting directory walk", slog.String("path", w.opts.InputPath))

	// Ensure the worker channel is closed when the walk finishes or errors out
	defer func() {
		close(w.workerChan)
		w.logger.Debug("Walker finished, worker channel closed.")
	}()

	walkErr := filepath.WalkDir(w.opts.InputPath, w.walkFunc(ctx))

	if walkErr != nil {
		// Handle context cancellation explicitly - not a critical traversal error
		if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
			w.logger.Info("Directory walk cancelled", slog.String("reason", walkErr.Error()))
			return walkErr // Propagate context error
		}
		// Log other critical traversal errors
		w.logger.Error("Directory walk encountered an error during traversal", slog.String("path", w.opts.InputPath), slog.String("error", walkErr.Error()))
		// Wrap the error for context, potentially using a specific WalkerError type if needed
		return fmt.Errorf("directory walk failed: %w", walkErr)
	}

	w.logger.Info("Directory walk completed successfully.")
	return nil
}

// walkFunc returns the fs.WalkDirFunc used by filepath.WalkDir.
// **MODIFIED:** Implemented full logic including ignores, Git diff, and dispatch.
func (w *Walker) walkFunc(ctx context.Context) fs.WalkDirFunc {
	isGitDiffActive := w.opts.GitDiffMode == GitDiffModeDiffOnly || w.opts.GitDiffMode == GitDiffModeSince

	return func(path string, d fs.DirEntry, walkErr error) error {
		// 1. Handle initial access error passed by WalkDir
		if walkErr != nil {
			// Log the error but allow WalkDir to continue with other entries if possible.
			// Permission errors on the root input path are fatal.
			logLevel := slog.LevelWarn
			if path == w.opts.InputPath && errors.Is(walkErr, fs.ErrPermission) {
				logLevel = slog.LevelError
				w.logger.Log(ctx, logLevel, "Permission denied accessing input path, stopping walk.", slog.String("path", path), slog.String("error", walkErr.Error()))
				return fmt.Errorf("permission denied reading input directory %q: %w", path, walkErr) // Return fatal error
			}
			w.logger.Log(ctx, logLevel, "Error accessing path during walk (continuing)", slog.String("path", path), slog.String("error", walkErr.Error()))
			// Returning nil allows WalkDir to continue; returning error stops WalkDir.
			// SkipDir could also be used if error implies directory is inaccessible.
			if d != nil && d.IsDir() {
				return fs.SkipDir // Skip dir if entry info available and error occurred on dir
			}
			return nil // Continue walk for other files/dirs
		}

		// 2. Check for context cancellation early
		select {
		case <-ctx.Done():
			w.logger.Debug("Walk cancelled by context", slog.String("path", path))
			return ctx.Err() // Stop the walk
		default:
			// Continue if context is not cancelled
		}

		// 3. Skip Symbolic Links (as per proposal)
		if d.Type()&fs.ModeSymlink != 0 {
			w.logger.Debug("Skipping symbolic link", slog.String("path", path))
			return nil // Continue walk, skipping this symlink
		}

		// 4. Calculate Paths (Absolute & Relative)
		absPath, err := filepath.Abs(path)
		if err != nil {
			w.logger.Warn("Could not get absolute path, skipping", slog.String("path", path), slog.String("error", err.Error()))
			return nil // Skip this item
		}
		// Calculate relative path *from the absolute input path* for consistency
		relativePath, err := filepath.Rel(w.opts.InputPath, absPath)
		if err != nil {
			// This should be rare if absPath is derived correctly
			w.logger.Warn("Could not calculate relative path, skipping", slog.String("absolutePath", absPath), slog.String("inputPath", w.opts.InputPath), slog.String("error", err.Error()))
			return nil // Skip this item
		}

		// Normalize to use '/' separators for consistent matching and reporting
		relativePath = filepath.ToSlash(relativePath)

		// Skip the root input directory itself (".")
		if relativePath == "." {
			return nil
		}

		// 5. Trigger Discovery Hook
		// Use normalized relative path
		if hookErr := w.hooks.OnFileDiscovered(relativePath); hookErr != nil {
			// Log hook errors but don't stop the walk
			w.logger.Warn("Event hook OnFileDiscovered failed", slog.String("path", relativePath), slog.String("error", hookErr.Error()))
		}

		// 6. Apply Ignore Rules
		isDir := d.IsDir()
		if w.ignoreMatcher.Match(relativePath, isDir) {
			matchedPattern := w.ignoreMatcher.LastMatchPattern(relativePath, isDir) // Get the specific pattern
			logArgs := []any{slog.String("path", relativePath), slog.Bool("isDir", isDir), slog.String("pattern", matchedPattern)}
			w.logger.Debug("Path ignored", logArgs...)

			statusMsg := fmt.Sprintf("Ignored by pattern: %s", matchedPattern)
			if hookErr := w.hooks.OnFileStatusUpdate(relativePath, StatusSkipped, statusMsg, 0); hookErr != nil {
				w.logger.Warn("Event hook OnFileStatusUpdate (Ignored) failed", append(logArgs, slog.String("error", hookErr.Error()))...)
			}

			// If a directory is ignored, skip walking its contents
			if isDir {
				return fs.SkipDir
			}
			return nil // Skip this file
		}

		// 7. If it's a directory and not ignored, continue walking into it
		if isDir {
			return nil
		}

		// --- File Handling ---

		// 8. Apply Git Diff Filter (if active)
		if isGitDiffActive {
			// Use normalized relative path for lookup
			if _, found := w.gitDiffMap[relativePath]; !found {
				// File exists, wasn't ignored, but is NOT in the Git changed list
				logArgs := []any{slog.String("path", relativePath), slog.String("mode", string(w.opts.GitDiffMode))}
				w.logger.Debug("Path excluded by Git diff filter", logArgs...)

				statusMsg := fmt.Sprintf("Excluded by Git diff mode %s", w.opts.GitDiffMode)
				if hookErr := w.hooks.OnFileStatusUpdate(relativePath, StatusSkipped, statusMsg, 0); hookErr != nil {
					w.logger.Warn("Event hook OnFileStatusUpdate (Git Skipped) failed", append(logArgs, slog.String("error", hookErr.Error()))...)
				}
				return nil // Skip this file
			}
			w.logger.Debug("Path included by Git diff filter", slog.String("path", relativePath))
		}

		// 9. Dispatch Eligible File Path to Worker Pool
		logArgs := []any{slog.String("path", relativePath)}
		w.logger.Debug("Dispatching eligible file to worker channel", logArgs...)

		// Use timer to detect and log slow dispatch (workers might be busy)
		timer := time.NewTimer(w.dispatchWarnDuration)
		defer timer.Stop() // Ensure timer is cleaned up

		select {
		case w.workerChan <- absPath: // Sent successfully (send absolute path to worker)
			// Successfully dispatched
		case <-timer.C: // Timer expired, dispatch is blocked
			w.logger.Warn("Worker channel dispatch blocked, workers might be busy or pool too small", append(logArgs, slog.Duration("threshold", w.dispatchWarnDuration))...)
			// Block until send succeeds or context is cancelled
			select {
			case w.workerChan <- absPath:
				w.logger.Info("Worker channel dispatch succeeded after delay", logArgs...)
			case <-ctx.Done():
				w.logger.Debug("Dispatch cancelled by context after delay", logArgs...)
				return ctx.Err() // Stop walk due to cancellation
			}
		case <-ctx.Done(): // Context cancelled while initially trying to send
			w.logger.Debug("Dispatch cancelled by context", logArgs...)
			return ctx.Err() // Stop walk due to cancellation
		}

		return nil // File dispatched successfully
	}
}

// --- ignoreMatcher Implementation ---
// **MODIFIED:** Implemented all methods based on plan.

// ignoreMatcher handles matching paths against gitignore-style patterns.
type ignoreMatcher struct {
	patterns []ignorePattern
	basePath string // Absolute path to the input directory (walker's root)
	logger   *slog.Logger
}

// ignorePattern holds a parsed pattern and its context.
type ignorePattern struct {
	pattern     string // Processed pattern string (using '/' separators) for matching.
	origPattern string // Original pattern string for reporting/logging.
	negated     bool   // True if the pattern starts with '!'.
	isDirOnly   bool   // True if the pattern ends with '/'.
	isRooted    bool   // True if the pattern starts with '/' relative to its definition base.
	baseAbsPath string // Absolute path of the directory containing the defining ignore file OR the walker's input path for config patterns.
}

// newIgnoreMatcher creates an ignoreMatcher by loading patterns from file and config.
func newIgnoreMatcher(inputPath string, configPatterns []string, logger *slog.Logger) (*ignoreMatcher, error) {
	absInputPath, err := filepath.Abs(inputPath)
	if err != nil {
		return nil, fmt.Errorf("could not get absolute path for input directory '%s': %w", inputPath, err)
	}

	// Normalize input path for consistency
	absInputPath = filepath.Clean(absInputPath)

	matcher := &ignoreMatcher{
		patterns: make([]ignorePattern, 0),
		basePath: absInputPath, // Store the absolute path of the walker's root
		logger:   logger.With(slog.String("component", "ignoreMatcher")),
	}

	// --- Load from .stackconverterignore ---
	// Find ignore file by walking up from the input path
	ignoreFilePath, findErr := findIgnoreFile(absInputPath)
	if findErr != nil {
		matcher.logger.Warn("Error searching for .stackconverterignore file", slog.String("error", findErr.Error()))
		// Continue without file patterns if search fails, but log it.
	}

	if ignoreFilePath != "" {
		matcher.logger.Debug("Found ignore file", slog.String("path", ignoreFilePath))
		filePatterns, loadErr := loadPatternsFromFile(ignoreFilePath)
		if loadErr != nil {
			// Treat failure to load an *existing* ignore file as a config error
			return nil, fmt.Errorf("failed to load ignore file '%s': %w", ignoreFilePath, loadErr)
		}
		ignoreFileBaseDir := filepath.Dir(ignoreFilePath)
		matcher.addPatterns(filePatterns, ignoreFileBaseDir) // Patterns relative to ignore file location
		matcher.logger.Debug("Loaded patterns from ignore file", slog.Int("count", len(filePatterns)))
	} else {
		matcher.logger.Debug("No .stackconverterignore file found walking up from input path")
	}

	// --- Load from Config/Flags ---
	// These patterns are treated as relative to the inputPath (walker's root)
	if len(configPatterns) > 0 {
		matcher.logger.Debug("Adding ignore patterns from config/flags", slog.Int("count", len(configPatterns)))
		matcher.addPatterns(configPatterns, absInputPath)
	}

	matcher.logger.Debug("Total processed ignore patterns", slog.Int("count", len(matcher.patterns)))
	return matcher, nil
}

// findIgnoreFile walks up from startPath looking for .stackconverterignore.
func findIgnoreFile(absStartPath string) (string, error) {
	currentPath := filepath.Clean(absStartPath)
	for {
		potentialPath := filepath.Join(currentPath, ".stackconverterignore")
		fileInfo, err := os.Stat(potentialPath)
		if err == nil {
			// Found the file
			if fileInfo.IsDir() {
				// If it's a directory, ignore it and continue searching upwards
			} else {
				return potentialPath, nil // Found a file
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			// An error other than "not found" occurred (e.g., permissions)
			return "", fmt.Errorf("error checking for ignore file at '%s': %w", potentialPath, err)
		}

		// Move up one directory
		parent := filepath.Dir(currentPath)
		if parent == currentPath || parent == "" {
			// Reached root or couldn't move up, stop searching
			break
		}
		currentPath = parent
	}
	return "", nil // Not found
}

// loadPatternsFromFile reads an ignore file, trims lines, and skips blanks/comments.
func loadPatternsFromFile(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open ignore file '%s': %w", filePath, err)
	}
	defer file.Close()

	var patterns []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line != "" && !strings.HasPrefix(line, "#") {
			patterns = append(patterns, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading ignore file '%s': %w", filePath, err)
	}
	return patterns, nil
}

// addPatterns processes raw string patterns into ignorePattern structs and adds them.
func (m *ignoreMatcher) addPatterns(rawPatterns []string, baseAbsPath string) {
	// Ensure baseAbsPath is clean for relative path calculations later
	baseAbsPath = filepath.Clean(baseAbsPath)

	for _, rawPattern := range rawPatterns {
		p := ignorePattern{origPattern: rawPattern, baseAbsPath: baseAbsPath}
		trimmedPattern := rawPattern

		// Check for negation prefix '!'
		if strings.HasPrefix(trimmedPattern, "!") {
			p.negated = true
			trimmedPattern = trimmedPattern[1:]
		}

		// Trim leading/trailing whitespace
		trimmedPattern = strings.TrimSpace(trimmedPattern)

		// Handle comments potentially reintroduced after trimming negated lines
		if trimmedPattern == "" || strings.HasPrefix(trimmedPattern, "#") {
			continue
		}

		// Check if pattern is rooted relative to its base path '/'
		if strings.HasPrefix(trimmedPattern, "/") {
			p.isRooted = true
			trimmedPattern = strings.TrimPrefix(trimmedPattern, "/")
		}

		// Check if pattern explicitly targets only directories '/'
		if strings.HasSuffix(trimmedPattern, "/") {
			p.isDirOnly = true
			trimmedPattern = strings.TrimSuffix(trimmedPattern, "/")
		}

		// Normalize pattern to use '/' separators for matching
		p.pattern = filepath.ToSlash(trimmedPattern)

		// Skip empty patterns that might result from processing
		if p.pattern == "" {
			continue
		}

		m.patterns = append(m.patterns, p)
		m.logger.Log(context.TODO(), slog.LevelDebug-4, "Added processed pattern", // Use lower level for verbose pattern logging
			slog.String("original", p.origPattern),
			slog.String("processed", p.pattern),
			slog.String("base", p.baseAbsPath),
			slog.Bool("negated", p.negated),
			slog.Bool("dirOnly", p.isDirOnly),
			slog.Bool("rooted", p.isRooted),
		)
	}
}

// Match checks if a given path (relative to the Walker's input base path) matches any ignore patterns.
// It respects negation and precedence (last matching pattern wins).
func (m *ignoreMatcher) Match(relativePath string, isDir bool) bool {
	lastMatchResult := false // Default to not ignored
	matched := false         // Track if any pattern matched

	// Ensure relativePath uses '/' separators
	relativePath = filepath.ToSlash(relativePath)

	for _, p := range m.patterns {
		// Pass all necessary info to the utility function
		// Requires `m.basePath` (walker's absolute root) and `p.baseAbsPath` (where pattern was defined)
		if util.MatchesGitignore(p.pattern, p.baseAbsPath, m.basePath, relativePath, p.isRooted) {
			// Check directory-only constraint
			if p.isDirOnly && !isDir {
				continue // Pattern expects a directory, but path is not a directory
			}
			// A match occurred
			matched = true
			// Update the result based on negation (negated pattern means 'do not ignore')
			lastMatchResult = !p.negated
		}
	}

	if !matched {
		return false // No patterns matched, so it's not ignored
	}

	return lastMatchResult // Return the result of the *last* pattern that matched
}

// LastMatchPattern returns the original pattern string that determined the final match decision.
// Returns empty string if no pattern matched or if the last match was a negation.
func (m *ignoreMatcher) LastMatchPattern(relativePath string, isDir bool) string {
	lastPattern := ""
	matched := false
	lastMatchResult := false // Track the effective outcome (ignored=true, not ignored=false)

	relativePath = filepath.ToSlash(relativePath)

	for _, p := range m.patterns {
		if util.MatchesGitignore(p.pattern, p.baseAbsPath, m.basePath, relativePath, p.isRooted) {
			if p.isDirOnly && !isDir {
				continue
			}
			matched = true
			lastPattern = p.origPattern // Store the *original* pattern string
			lastMatchResult = !p.negated
		}
	}

	// Only return the pattern if it actually caused the item to be ignored
	if matched && lastMatchResult {
		return lastPattern
	}

	return "" // No pattern matched, or the last match was a negation (meaning 'keep')
}

// patternCount returns the number of processed patterns.
func (m *ignoreMatcher) patternCount() int {
	return len(m.patterns)
}

// --- END OF FINAL REVISED FILE pkg/converter/walker.go ---
