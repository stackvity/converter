// --- START OF FINAL REVISED FILE internal/cli/git/git_exec.go ---
//go:build !gogit

package git

import (
	"bufio"
	"bytes"
	"context" // Keep context for runGitCommand internal use
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter"
	libgit "github.com/stackvity/stack-converter/pkg/converter/git" // Use alias to avoid conflict with package name
)

// ExecGitClient implements the GitClient interface using os/exec.
type ExecGitClient struct {
	logger *slog.Logger
}

// NewExecGitClient creates a new ExecGitClient.
// Returns the interface type for consistency.
func NewExecGitClient(loggerHandler slog.Handler) converter.GitClient {
	if loggerHandler == nil {
		loggerHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	logger := slog.New(loggerHandler).With(slog.String("component", "gitClient"), slog.String("backend", "exec"))
	logger.Debug("Using 'exec' backend for Git operations.")
	return &ExecGitClient{logger: logger}
}

// IsGitAvailable checks if the git command is available in the system's PATH.
func (c *ExecGitClient) IsGitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// runGitCommand executes a git command and returns its stdout, stderr, and any error.
// This internal helper still uses context for timeout/cancellation of the command itself.
func (c *ExecGitClient) runGitCommand(ctx context.Context, repoPath string, args ...string) (string, string, error) {
	if ctx == nil {
		ctx = context.Background() // Should not happen if called correctly now, but keep defensively.
		c.logger.Warn("runGitCommand called with nil context, using background")
	}
	// Consider adding a sensible default timeout if the provided context doesn't have one
	// cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second) // Example: Add 30s timeout
	// defer cancel()
	// cmd := exec.CommandContext(cmdCtx, "git", args...)
	cmd := exec.CommandContext(ctx, "git", args...) // Use original ctx for now
	cmd.Dir = repoPath
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	stdoutStr := strings.TrimSpace(stdout.String())
	stderrStr := strings.TrimSpace(stderr.String())

	if runErr != nil {
		// Check if the error is due to context cancellation/deadline explicitly
		// This check remains useful even if the parent methods don't take context.
		if ctxErr := ctx.Err(); ctxErr != nil {
			// Return the context error directly for clarity
			detailedErr := fmt.Errorf("command 'git %s' context error in %s: %w. stderr: %s", strings.Join(args, " "), repoPath, ctxErr, stderrStr)
			return stdoutStr, stderrStr, detailedErr
		}
		// Otherwise, report the general execution error
		detailedErr := fmt.Errorf("command 'git %s' failed in %s: %w. stderr: %s", strings.Join(args, " "), repoPath, runErr, stderrStr)
		return stdoutStr, stderrStr, detailedErr
	}

	return stdoutStr, stderrStr, nil
}

// GetChangedFiles implements the GitClient interface using git commands.
// Removed the ctx context.Context parameter to match the interface.
func (c *ExecGitClient) GetChangedFiles(repoPath, mode string, ref string) ([]string, error) {
	logArgs := []any{slog.String("repo", repoPath), slog.String("mode", mode), slog.String("ref", ref)}
	c.logger.Debug("ExecGitClient: Getting changed files", logArgs...)

	info, err := os.Stat(repoPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Use helper from pkg/converter/git for consistent error wrapping
			return nil, libgit.Errorf("repository path does not exist: %s", repoPath)
		}
		return nil, libgit.Errorf("failed to access repository path %s: %w", repoPath, err)
	}
	if !info.IsDir() {
		return nil, libgit.Errorf("repository path is not a directory: %s", repoPath)
	}

	// Use a background context with a timeout for the internal git command execution.
	cmdCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second) // Add a 60-second timeout
	defer cancel()

	var gitArgs []string
	var stdout string
	var stderr string

	switch mode {
	case string(converter.GitDiffModeDiffOnly):
		gitArgs = []string{"status", "--porcelain=v1"}
		// Pass the internal context (cmdCtx) to runGitCommand.
		stdout, stderr, err = c.runGitCommand(cmdCtx, repoPath, gitArgs...)
		if err != nil {
			// Rely on the error returned by runGitCommand which checks its context.
			c.logger.Error("Failed to run git status", append(logArgs, slog.Any("error", err), slog.String("stderr", stderr))...)
			// Wrap the error using the helper from the git package
			return nil, libgit.Errorf("git status command failed: %w", err)
		}

		files := []string{}
		scanner := bufio.NewScanner(strings.NewReader(stdout))
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) > 3 {
				statusCode := line[:2]
				// Filter out untracked/ignored files from status output
				if !strings.HasPrefix(statusCode, "??") && !strings.HasPrefix(statusCode, "!!") {
					// Handle potentially renamed files (e.g., "R  orig -> new")
					// Fields handles spaces in filenames if they aren't quoted.
					// Porcelain v1 format: XY PATH or XY ORIG -> PATH
					// We are interested in the final path component.
					parts := strings.Fields(line[3:])
					if len(parts) > 0 {
						finalPath := parts[len(parts)-1] // Take the last part, works for simple paths and renames
						// Need to handle quotes if present (though less common with porcelain v1?)
						finalPath = strings.Trim(finalPath, `"`)
						files = append(files, filepath.ToSlash(finalPath))
					}
				}
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			c.logger.Error("Error scanning git status output", append(logArgs, slog.Any("error", scanErr))...)
			// Wrap the error using the helper from the git package
			return nil, libgit.Errorf("failed to parse git status output: %w", scanErr)
		}
		return files, nil

	case string(converter.GitDiffModeSince):
		if ref == "" {
			return nil, libgit.Errorf("git diff mode 'since' requires a non-empty reference")
		}
		gitArgs = []string{"diff", "--name-only", fmt.Sprintf("%s...HEAD", ref)}
		// Pass the internal context (cmdCtx) to runGitCommand.
		stdout, stderr, err = c.runGitCommand(cmdCtx, repoPath, gitArgs...)
		if err != nil {
			// Rely on the error returned by runGitCommand.
			if strings.Contains(stderr, "unknown revision") || strings.Contains(stderr, "bad revision") {
				specificErr := fmt.Errorf("invalid git reference '%s'", ref)
				c.logger.Error("Invalid git reference provided for --git-since", append(logArgs, slog.String("stderr", stderr), slog.Any("error", specificErr))...)
				// Wrap the specific error using the helper from the git package
				return nil, libgit.Errorf("invalid git reference '%s': %w", ref, specificErr)
			}
			c.logger.Error("Failed to run git diff since ref", append(logArgs, slog.Any("error", err), slog.String("stderr", stderr))...)
			// Wrap the generic error using the helper from the git package
			return nil, libgit.Errorf("git diff since ref '%s' failed: %w", ref, err)
		}
		files := strings.Fields(stdout)
		for i, f := range files {
			files[i] = filepath.ToSlash(f) // Normalize paths
		}
		return files, nil

	default:
		return nil, libgit.Errorf("unsupported git diff mode: %s", mode)
	}
}

// GetFileMetadata implements the GitClient interface using git log.
// Removed the ctx context.Context parameter to match the interface.
func (c *ExecGitClient) GetFileMetadata(repoPath, filePath string) (map[string]string, error) {
	logArgs := []any{slog.String("repo", repoPath), slog.String("file", filePath)}
	c.logger.Debug("ExecGitClient: Getting file metadata", logArgs...)

	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		c.logger.Warn("Could not get absolute path for file metadata", append(logArgs, slog.Any("error", err))...)
		// Do not return error, as metadata is optional
		return map[string]string{}, nil
	}
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		c.logger.Warn("Could not get absolute path for repository", append(logArgs, slog.Any("error", err))...)
		return map[string]string{}, nil
	}

	relPath, err := filepath.Rel(absRepoPath, absFilePath)
	if err != nil || strings.HasPrefix(filepath.Clean(relPath), "..") {
		c.logger.Debug("File path is outside the repository path, skipping git metadata", append(logArgs, slog.String("relPath", relPath))...)
		return map[string]string{}, nil // Not an error
	}
	relPath = filepath.ToSlash(relPath) // Ensure consistent path separators for git command

	// Use a background context with a timeout for the internal git command execution.
	cmdCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // Add a 30-second timeout
	defer cancel()

	// Format specifies: Hash, Author Name, Author Email, Commit Date ISO 8601
	format := "--pretty=format:%H%n%an%n%ae%n%cI"
	// Use '--' to separate options from paths, preventing issues with filenames starting with '-'
	// Use -1 to get only the most recent commit affecting the file.
	gitArgs := []string{"log", "-1", format, "--", relPath}

	// Pass the internal context (cmdCtx) to runGitCommand.
	stdout, stderr, err := c.runGitCommand(cmdCtx, absRepoPath, gitArgs...) // Use absRepoPath for cmd.Dir
	if err != nil {
		// Rely on the error returned by runGitCommand.
		// Adjust log level for non-critical errors like untracked files or empty repos.
		if strings.Contains(stderr, "does not have any commits yet") || strings.Contains(stderr, "fatal: bad object HEAD") || (stdout == "" && strings.Contains(stderr, "fatal: path")) {
			c.logger.Debug("No commit history or file not tracked", append(logArgs, slog.String("stderr", stderr))...)
			return map[string]string{}, nil // Return empty map, not an error
		}
		// Log other git log errors as warnings, as metadata is often optional
		c.logger.Warn("Failed to get git log for file", append(logArgs, slog.Any("error", err), slog.String("stderr", stderr))...)
		// Do not return the error itself, as metadata is optional
		return map[string]string{}, nil
	}

	if stdout == "" {
		c.logger.Debug("Git log returned empty output for file (likely untracked or no history)", logArgs...)
		return map[string]string{}, nil
	}

	lines := strings.SplitN(stdout, "\n", 4)
	if len(lines) < 4 {
		c.logger.Warn("Unexpected git log output format", append(logArgs, slog.String("output", stdout))...)
		return map[string]string{}, nil // Return empty map for malformed output
	}

	metadata := map[string]string{
		"commit":      lines[0],
		"author":      lines[1],
		"authorEmail": lines[2],
		"dateISO":     lines[3],
	}

	// Attempt to parse date for Unix timestamp
	commitTime, timeErr := time.Parse(time.RFC3339, lines[3])
	if timeErr == nil {
		metadata["dateUnix"] = fmt.Sprintf("%d", commitTime.Unix())
	} else {
		c.logger.Debug("Failed to parse commit date from git log", append(logArgs, slog.String("dateString", lines[3]), slog.Any("error", timeErr))...)
		// Continue without dateUnix if parsing fails
	}

	return metadata, nil
}

// --- END OF FINAL REVISED FILE internal/cli/git/git_exec.go ---
